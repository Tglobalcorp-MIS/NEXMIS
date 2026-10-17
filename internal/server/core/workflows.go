package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/robfig/cron/v3"

	"github.com/tglobalcorp-mis/nexmis/internal/discovery"
	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
	"github.com/tglobalcorp-mis/nexmis/internal/server/events"
	"github.com/tglobalcorp-mis/nexmis/internal/server/store"
)

// ---- discovery ----

type ScanInput struct {
	CIDRs         []string   `json:"cidrs"`
	Ports         []int      `json:"ports"`
	ProbeDeviceID *uuid.UUID `json:"probe_device_id"`
}

// StartScan launches a scan from the server or, when ProbeDeviceID is set,
// from an agent on a remote network.
func (s *Service) StartScan(ctx context.Context, actor Actor, in ScanInput) (*store.DiscoveryScan, error) {
	addrs, err := discovery.ExpandCIDRs(in.CIDRs)
	if err != nil {
		return nil, invalid("%s", err.Error())
	}
	ports := in.Ports
	if len(ports) == 0 {
		ports = discovery.DefaultPorts
	}
	if len(ports) > 64 {
		return nil, invalid("埠數量上限為 64")
	}
	p32 := make([]int32, 0, len(ports))
	for _, p := range ports {
		if p <= 0 || p > 65535 {
			return nil, invalid("無效的埠號 %d", p)
		}
		p32 = append(p32, int32(p))
	}
	if in.ProbeDeviceID != nil && !s.IsOnline(*in.ProbeDeviceID) {
		return nil, invalid("探索代理裝置目前離線")
	}
	if actor.ID == nil {
		return nil, invalid("需要登入使用者")
	}
	scan, err := s.Store.CreateScan(ctx, in.CIDRs, p32, *actor.ID, actor.Name, in.ProbeDeviceID)
	if err != nil {
		return nil, err
	}
	s.Audit(ctx, actor, "discovery.scan", "scan", scan.ID.String(), strings.Join(in.CIDRs, ","),
		map[string]any{"cidrs": in.CIDRs, "ports": ports, "probe_device_id": in.ProbeDeviceID, "hosts": len(addrs)}, true)
	s.Bus.Publish(events.ScanUpdated, scan)

	if in.ProbeDeviceID != nil {
		args, _ := json.Marshal(protocol.ScanArgs{CIDRs: in.CIDRs, Ports: ports, ScanID: scan.ID.String()})
		cmd, err := s.Store.CreateCommand(ctx, nil, store.NewCommandRow{
			DeviceID: *in.ProbeDeviceID, Kind: protocol.CmdScan, Args: args, TimeoutSec: 1800, TTL: time.Hour,
			IssuedBy: actor.ID, IssuedByName: actor.Name,
		})
		if err != nil {
			s.finishScan(ctx, scan.ID, nil, err.Error())
			return scan, nil
		}
		s.deliverCommand(ctx, cmd)
		return scan, nil
	}
	go func() {
		sctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		hosts, err := discovery.Scan(sctx, addrs, discovery.Options{Ports: ports})
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		s.finishScan(sctx, scan.ID, hosts, msg)
	}()
	return scan, nil
}

func (s *Service) finishScan(ctx context.Context, id uuid.UUID, hosts []protocol.DiscoveredHost, errMsg string) {
	ctx = context.WithoutCancel(ctx)
	if err := s.Store.FinishScan(ctx, id, hosts, errMsg); err != nil {
		s.Log.Error("finish scan", "err", err)
	}
	if scan, err := s.Store.GetScan(ctx, id, false); err == nil {
		s.Bus.Publish(events.ScanUpdated, scan)
	}
}

// ---- packages & deployments ----

type DeploymentInput struct {
	Name        string       `json:"name"`
	PackageID   uuid.UUID    `json:"package_id"`
	Target      store.Target `json:"target"`
	ScheduledAt *time.Time   `json:"scheduled_at"`
}

func (s *Service) CreateDeployment(ctx context.Context, actor Actor, in DeploymentInput) (*store.Deployment, error) {
	pkg, err := s.Store.GetPackage(ctx, in.PackageID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, invalid("套件不存在")
		}
		return nil, err
	}
	if in.Target.Empty() {
		return nil, invalid("請選擇部署目標")
	}
	if strings.TrimSpace(in.Name) == "" {
		in.Name = "部署 " + pkg.Name + " " + pkg.Version
	}
	// Every deployment starts scheduled; an immediate one is claimed right
	// away below, and the job loop picks it up if that is interrupted.
	runNow := in.ScheduledAt == nil || !in.ScheduledAt.After(time.Now().Add(30*time.Second))
	if runNow {
		now := time.Now()
		in.ScheduledAt = &now
	}
	id, err := s.Store.CreateDeployment(ctx, in.Name, pkg.ID, in.Target, "scheduled", in.ScheduledAt, actor.ID)
	if err != nil {
		return nil, err
	}
	s.Audit(ctx, actor, "deployment.create", "deployment", id.String(), in.Name,
		map[string]any{"package": pkg.Name + " " + pkg.Version, "sha256": pkg.SHA256, "target": in.Target, "scheduled_at": in.ScheduledAt}, true)
	if runNow {
		if err := s.runDeployment(ctx, actor, id, nil, true); err != nil {
			return nil, err
		}
	}
	d, err := s.Store.GetDeployment(ctx, id)
	if err == nil {
		s.Bus.Publish(events.DeploymentUpdated, d)
	}
	return d, err
}

// runDeployment queues install commands for the deployment target, or for
// onlyDevices when retrying. With claim set it first moves a scheduled
// deployment to running; claiming and queueing happen in one transaction.
func (s *Service) runDeployment(ctx context.Context, actor Actor, id uuid.UUID, onlyDevices []uuid.UUID, claim bool) error {
	dep, err := s.Store.GetDeployment(ctx, id)
	if err != nil {
		return err
	}
	pkg, err := s.Store.GetPackage(ctx, dep.PackageID)
	if errors.Is(err, store.ErrNotFound) && claim {
		// The package was deleted while the deployment waited; it can never run.
		s.Log.Warn("deployment package no longer exists; canceling", "deployment", id)
		return s.Store.SetDeploymentStatus(ctx, nil, id, "canceled")
	}
	if err != nil {
		return err
	}
	devices := onlyDevices
	if devices == nil {
		if devices, err = s.Store.ResolveTarget(ctx, dep.Target); err != nil {
			return err
		}
	}
	codes := make([]int, 0, len(pkg.SuccessCodes))
	for _, c := range pkg.SuccessCodes {
		codes = append(codes, int(c))
	}
	args, _ := json.Marshal(protocol.InstallArgs{
		PackageID: pkg.ID.String(), FileName: pkg.FileName, SHA256: pkg.SHA256, Size: pkg.FileSize, Type: pkg.Type,
		Arguments: pkg.InstallArgs, SuccessCodes: codes, DeploymentID: id.String(),
	})
	var queued []*store.Command
	err = s.Store.InTx(ctx, func(tx pgx.Tx) error {
		queued = queued[:0]
		if claim {
			ok, err := s.Store.ClaimDeployment(ctx, tx, id)
			if err != nil || !ok {
				return err
			}
		}
		for _, devID := range devices {
			cmd, err := s.Store.CreateCommand(ctx, tx, store.NewCommandRow{
				DeviceID: devID, Kind: protocol.CmdInstall, Args: args, TimeoutSec: 3600, TTL: commandTTL(protocol.CmdInstall),
				IssuedBy: actor.ID, IssuedByName: actor.Name, DeploymentID: &id,
			})
			if err != nil {
				return fmt.Errorf("queue install for %s: %w", devID, err)
			}
			queued = append(queued, cmd)
		}
		status := "running"
		if len(devices) == 0 {
			status = "completed"
		}
		return s.Store.SetDeploymentStatus(ctx, tx, id, status)
	})
	if err != nil {
		return err
	}
	for _, cmd := range queued {
		s.Bus.Publish(events.CommandUpdated, cmd)
		s.deliverCommand(ctx, cmd)
	}
	return nil
}

func (s *Service) CancelDeployment(ctx context.Context, actor Actor, id uuid.UUID) (*store.Deployment, error) {
	if err := s.Store.CancelDeploymentCommands(ctx, id); err != nil {
		return nil, err
	}
	if err := s.Store.SetDeploymentStatus(ctx, nil, id, "canceled"); err != nil {
		return nil, err
	}
	d, err := s.Store.GetDeployment(ctx, id)
	if err != nil {
		return nil, err
	}
	s.Audit(ctx, actor, "deployment.cancel", "deployment", id.String(), d.Name, nil, true)
	s.Bus.Publish(events.DeploymentUpdated, d)
	return d, nil
}

func (s *Service) RetryDeployment(ctx context.Context, actor Actor, id uuid.UUID) (*store.Deployment, error) {
	failed, err := s.Store.FailedDeploymentDevices(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(failed) == 0 {
		return nil, invalid("沒有失敗的目標需要重試")
	}
	if err := s.runDeployment(ctx, actor, id, failed, false); err != nil {
		return nil, err
	}
	d, err := s.Store.GetDeployment(ctx, id)
	if err != nil {
		return nil, err
	}
	s.Audit(ctx, actor, "deployment.retry", "deployment", id.String(), d.Name, map[string]any{"devices": len(failed)}, true)
	s.Bus.Publish(events.DeploymentUpdated, d)
	return d, nil
}

// ---- schedules ----

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// NextRun computes the next run time of a cron expression in the server's
// time zone.
func (s *Service) NextRun(ctx context.Context, expr string, from time.Time) (time.Time, error) {
	sched, err := cronParser.Parse(expr)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(from.In(s.Location(ctx))), nil
}

// ValidateSchedule checks a schedule before saving and sets NextRunAt.
func (s *Service) ValidateSchedule(ctx context.Context, sc *store.Schedule) error {
	if strings.TrimSpace(sc.Name) == "" {
		return invalid("請輸入排程名稱")
	}
	next, err := s.NextRun(ctx, sc.Cron, time.Now())
	if err != nil {
		return invalid("Cron 運算式錯誤：%s", err.Error())
	}
	if sc.Target.Empty() {
		return invalid("請選擇排程目標")
	}
	switch sc.Action.Type {
	case "command":
		var nc NewCommand
		if err := json.Unmarshal(orEmpty(sc.Action.Command), &nc); err != nil {
			return invalid("指令格式錯誤")
		}
		if _, _, _, err := s.normalize(ctx, nc); err != nil {
			return err
		}
	case "deployment":
		if sc.Action.PackageID == nil {
			return invalid("請選擇要部署的套件")
		}
		if _, err := s.Store.GetPackage(ctx, *sc.Action.PackageID); err != nil {
			return invalid("套件不存在")
		}
	case "refresh":
	default:
		return invalid("不支援的排程動作")
	}
	if sc.Enabled {
		sc.NextRunAt = &next
	} else {
		sc.NextRunAt = nil
	}
	return nil
}

// RunSchedule executes a schedule's action now.
func (s *Service) RunSchedule(ctx context.Context, actor Actor, sc store.Schedule) error {
	s.Audit(ctx, actor, "schedule.run", "schedule", sc.ID.String(), sc.Name, map[string]any{"action": sc.Action.Type}, true)
	runAs := Actor{ID: sc.CreatedBy, Name: "排程：" + sc.Name}
	switch sc.Action.Type {
	case "command", "refresh":
		nc := NewCommand{Kind: protocol.CmdRefresh, TimeoutSec: 600}
		if sc.Action.Type == "command" {
			if err := json.Unmarshal(orEmpty(sc.Action.Command), &nc); err != nil {
				return err
			}
		}
		ids, err := s.Store.ResolveTarget(ctx, sc.Target)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := s.IssueCommand(ctx, runAs, id, nc, &sc.ID); err != nil {
				s.Log.Warn("schedule command", "schedule", sc.Name, "device", id, "err", err)
			}
		}
	case "deployment":
		// sc.CreatedBy may be nil once the creating user is deleted; the
		// deployment then runs under the schedule's name alone.
		if sc.Action.PackageID == nil {
			return errors.New("invalid deployment schedule")
		}
		_, err := s.CreateDeployment(ctx, runAs, DeploymentInput{
			Name:      sc.Name + " " + time.Now().In(s.Location(ctx)).Format("2006-01-02 15:04"),
			PackageID: *sc.Action.PackageID, Target: sc.Target,
		})
		return err
	}
	return nil
}

// ---- AD sync ----

// SyncAD imports computer objects from AD.
func (s *Service) SyncAD(ctx context.Context, actor Actor) (added, updated, total int, err error) {
	a, err := s.ADSettings(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	if !a.Enabled {
		return 0, 0, 0, invalid("尚未啟用 AD 整合")
	}
	comps, err := a.ListComputers(0)
	if err != nil {
		s.Audit(ctx, actor, "ad.sync", "ad", "", a.URL, map[string]any{"error": err.Error()}, false)
		return 0, 0, 0, err
	}
	rows := make([]store.ADComputer, 0, len(comps))
	for _, c := range comps {
		rows = append(rows, store.ADComputer{Name: c.Name, DNSName: c.DNSName, DN: c.DN, OS: c.OS,
			OSVersion: c.OSVersion, Enabled: c.Enabled, LastLogon: c.LastLogon})
	}
	added, updated, err = s.Store.UpsertADComputers(ctx, rows)
	if err != nil {
		return 0, 0, 0, err
	}
	s.Audit(ctx, actor, "ad.sync", "ad", "", a.URL, map[string]any{"added": added, "updated": updated, "total": len(comps)}, true)
	_ = s.Store.PutSetting(ctx, "ad_last_sync", time.Now())
	return added, updated, len(comps), nil
}
