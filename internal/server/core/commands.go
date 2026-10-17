package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
	"github.com/tglobalcorp-mis/nexmis/internal/server/events"
	"github.com/tglobalcorp-mis/nexmis/internal/server/store"
)

// NewCommand is the admin-facing command request (docs/api.md §3).
type NewCommand struct {
	Kind       string          `json:"kind"`
	Script     string          `json:"script"`
	TimeoutSec int             `json:"timeout_sec"`
	Args       json.RawMessage `json:"args"`
}

// ValidationError is returned for bad user input.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

// HelpdeskKinds are the command kinds the helpdesk role may issue.
var HelpdeskKinds = map[string]bool{protocol.CmdRefresh: true, protocol.CmdNotify: true, protocol.CmdLock: true}

// adminKinds are kinds that may be issued through the generic command API.
// install and discovery.scan are created by their own workflows only.
var adminKinds = map[string]bool{
	protocol.CmdPowerShell: true, protocol.CmdCmd: true, protocol.CmdReboot: true, protocol.CmdShutdown: true,
	protocol.CmdRefresh: true, protocol.CmdNotify: true, protocol.CmdLock: true, protocol.CmdLogoff: true,
	protocol.CmdServiceRestart: true, protocol.CmdProcessKill: true, protocol.CmdUninstall: true,
	protocol.CmdAgentUpdate: true,
}

const (
	defaultTimeout = 300
	maxTimeout     = 3600
	maxScriptBytes = 256 << 10
)

// normalize validates a command request and produces the stored args.
func (s *Service) normalize(ctx context.Context, nc NewCommand) (string, json.RawMessage, int, error) {
	kind := strings.TrimSpace(nc.Kind)
	if !adminKinds[kind] {
		return "", nil, 0, invalid("不支援的指令類型：%s", kind)
	}
	timeout := nc.TimeoutSec
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if timeout > maxTimeout {
		timeout = maxTimeout
	}
	var args any
	switch kind {
	case protocol.CmdPowerShell, protocol.CmdCmd:
		script := nc.Script
		var a protocol.ScriptArgs
		if len(nc.Args) > 0 {
			if err := json.Unmarshal(nc.Args, &a); err != nil {
				return "", nil, 0, invalid("參數格式錯誤")
			}
		}
		if script == "" {
			script = a.Script
		}
		if strings.TrimSpace(script) == "" {
			return "", nil, 0, invalid("指令內容不可為空")
		}
		if len(script) > maxScriptBytes {
			return "", nil, 0, invalid("指令內容過長（上限 256 KB）")
		}
		a.Script = script
		if a.RunAs != "" && a.RunAs != "system" && a.RunAs != "user" {
			return "", nil, 0, invalid("run_as 只能是 system 或 user")
		}
		args = a
	case protocol.CmdReboot, protocol.CmdShutdown:
		cfg := s.AgentConfig(ctx)
		a := protocol.RebootArgs{CountdownSec: cfg.RebootCountdown, AllowDefer: true}
		if len(nc.Args) > 0 {
			if err := json.Unmarshal(nc.Args, &a); err != nil {
				return "", nil, 0, invalid("參數格式錯誤")
			}
		}
		if a.CountdownSec < 0 || a.CountdownSec > 3600 {
			return "", nil, 0, invalid("倒數秒數需介於 0 到 3600")
		}
		a.Shutdown = kind == protocol.CmdShutdown
		if len(a.Message) > 500 {
			return "", nil, 0, invalid("訊息過長")
		}
		// A reboot runs for at most every countdown plus every deferral at
		// its longest, with some slack for the result to arrive.
		timeout = a.CountdownSec*(cfg.RebootMaxDefer+1) + cfg.RebootMaxDefer*protocol.MaxRebootDeferMinutes*60 + 600
		args = a
	case protocol.CmdAgentUpdate:
		// Staged rollout: update selected devices to the published version
		// while auto_update is off. Download links are minted per device
		// at delivery.
		m := s.agentManifest()
		if m.Version == "" || len(m.Builds) == 0 {
			return "", nil, 0, invalid("伺服器尚未發佈 Agent 版本")
		}
		args, _ = json.Marshal(protocol.AgentUpdateArgs{Version: m.Version})
	case protocol.CmdNotify:
		var a protocol.NotifyArgs
		if err := json.Unmarshal(orEmpty(nc.Args), &a); err != nil || strings.TrimSpace(a.Message) == "" {
			return "", nil, 0, invalid("通知內容不可為空")
		}
		if len(a.Message) > 2000 || len(a.Title) > 200 {
			return "", nil, 0, invalid("通知內容過長")
		}
		args = a
	case protocol.CmdServiceRestart:
		var a protocol.ServiceArgs
		if err := json.Unmarshal(orEmpty(nc.Args), &a); err != nil || strings.TrimSpace(a.Name) == "" {
			return "", nil, 0, invalid("請指定服務名稱")
		}
		args = a
	case protocol.CmdProcessKill:
		var a protocol.ProcessArgs
		if err := json.Unmarshal(orEmpty(nc.Args), &a); err != nil || (a.PID <= 0 && a.Name == "") {
			return "", nil, 0, invalid("請指定處理程序 PID 或名稱")
		}
		args = a
	case protocol.CmdUninstall:
		var a protocol.UninstallArgs
		if err := json.Unmarshal(orEmpty(nc.Args), &a); err != nil || a.UninstallID == "" {
			return "", nil, 0, invalid("請指定要解除安裝的軟體")
		}
		args = a
	default:
		args = map[string]any{}
	}
	raw, err := json.Marshal(args)
	return kind, raw, timeout, err
}

func orEmpty(b json.RawMessage) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage(`{}`)
	}
	return b
}

// IssueCommand validates and queues a command for a single device, sending
// it immediately when the device is online.
func (s *Service) IssueCommand(ctx context.Context, actor Actor, deviceID uuid.UUID, nc NewCommand, scheduleID *uuid.UUID) (*store.Command, error) {
	kind, args, timeout, err := s.normalize(ctx, nc)
	if err != nil {
		return nil, err
	}
	d, err := s.Store.GetDevice(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if !d.HasAgent {
		return nil, invalid("此裝置未安裝 Agent，無法派送指令")
	}
	cmd, err := s.Store.CreateCommand(ctx, nil, store.NewCommandRow{
		DeviceID: deviceID, Kind: kind, Args: args, TimeoutSec: timeout, TTL: commandTTL(kind),
		IssuedBy: actor.ID, IssuedByName: actor.Name, ScheduleID: scheduleID,
	})
	if err != nil {
		return nil, err
	}
	s.Audit(ctx, actor, "command.create", "device", deviceID.String(), d.Name, map[string]any{
		"command_id": cmd.ID, "kind": kind, "args": json.RawMessage(args), "timeout_sec": timeout}, true)
	s.Bus.Publish(events.CommandUpdated, cmd)
	s.deliverCommand(ctx, cmd)
	return cmd, nil
}

// commandTTL is how long a command may wait for an offline device.
func commandTTL(kind string) time.Duration {
	switch kind {
	case protocol.CmdReboot, protocol.CmdShutdown, protocol.CmdLock, protocol.CmdLogoff, protocol.CmdNotify:
		return time.Hour // stale power actions are dangerous
	case protocol.CmdInstall, protocol.CmdUninstall:
		return 7 * 24 * time.Hour
	}
	return 24 * time.Hour
}

// deliverCommand signs and sends a queued command if the device is online.
func (s *Service) deliverCommand(ctx context.Context, cmd *store.Command) bool {
	return s.sendCommand(ctx, cmd, nil)
}

// sendCommand is deliverCommand with an optional send function used in
// place of the hub's non-blocking send.
func (s *Service) sendCommand(ctx context.Context, cmd *store.Command, send func(string, any) bool) bool {
	c := s.hub.get(cmd.DeviceID)
	if c == nil {
		return false
	}
	args := cmd.Args
	if cmd.Kind == protocol.CmdInstall {
		// Download URLs are minted at send time so that queued deployments
		// never carry an expired link.
		var a protocol.InstallArgs
		if err := json.Unmarshal(args, &a); err == nil {
			pkgID, _ := uuid.Parse(a.PackageID)
			a.DownloadURL = s.fileURL(fileToken{Kind: "pkg", ID: pkgID, Device: cmd.DeviceID,
				Expires: time.Now().Add(time.Duration(cmd.TimeoutSec)*time.Second + 2*time.Hour).Unix()})
			args, _ = json.Marshal(a)
		}
	}
	if cmd.Kind == protocol.CmdAgentUpdate {
		var a protocol.AgentUpdateArgs
		if err := json.Unmarshal(args, &a); err == nil {
			a.Builds = s.agentBuilds(s.agentManifest(), cmd.DeviceID, 24*time.Hour)
			args, _ = json.Marshal(a)
		}
	}
	expires := cmd.ExpiresAt
	if time.Until(expires) < 10*time.Minute {
		expires = time.Now().Add(10 * time.Minute)
	}
	sc, err := protocol.SignCommand(s.signKey, protocol.Command{
		CommandID: cmd.ID.String(), AgentID: c.agentID.String(), Kind: cmd.Kind, Args: args,
		TimeoutSec: cmd.TimeoutSec, IssuedAt: time.Now(), ExpiresAt: expires, IssuedBy: cmd.IssuedByName,
	})
	if err != nil {
		s.Log.Error("sign command", "err", err)
		return false
	}
	if send == nil {
		send = func(typ string, p any) bool { return s.hub.sendTo(cmd.DeviceID, typ, p) }
	}
	if !send(protocol.TCommand, sc) {
		return false
	}
	if err := s.Store.MarkCommandSent(ctx, cmd.ID); err != nil {
		s.Log.Error("mark sent", "err", err)
		return true
	}
	if updated, err := s.Store.GetCommand(ctx, cmd.ID); err == nil {
		s.Bus.Publish(events.CommandUpdated, updated)
	}
	return true
}

// CancelCommand cancels a queued command.
func (s *Service) CancelCommand(ctx context.Context, actor Actor, id uuid.UUID) (*store.Command, error) {
	cmd, err := s.Store.CancelCommand(ctx, id)
	if err != nil {
		return nil, err
	}
	s.Audit(ctx, actor, "command.cancel", "device", cmd.DeviceID.String(), cmd.DeviceName, map[string]any{"command_id": id}, true)
	s.Bus.Publish(events.CommandUpdated, cmd)
	return cmd, nil
}

// IssueBatch queues a command for every device in the target.
func (s *Service) IssueBatch(ctx context.Context, actor Actor, t store.Target, nc NewCommand) ([]uuid.UUID, error) {
	if _, _, _, err := s.normalize(ctx, nc); err != nil {
		return nil, err
	}
	ids, err := s.Store.ResolveTarget(ctx, t)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, invalid("目標沒有符合的裝置（需已安裝 Agent 且未報廢）")
	}
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		cmd, err := s.IssueCommand(ctx, actor, id, nc, nil)
		if err != nil {
			s.Log.Warn("batch command", "device", id, "err", err)
			continue
		}
		out = append(out, cmd.ID)
	}
	return out, nil
}

// onCommandFinished runs follow-up logic for workflow commands.
func (s *Service) onCommandFinished(ctx context.Context, cmd *store.Command) {
	switch cmd.Kind {
	case protocol.CmdScan:
		var a protocol.ScanArgs
		if json.Unmarshal(cmd.Args, &a) != nil {
			return
		}
		scanID, err := uuid.Parse(a.ScanID)
		if err != nil {
			return
		}
		var hosts []protocol.DiscoveredHost
		if len(cmd.Data) > 0 {
			_ = json.Unmarshal(cmd.Data, &hosts)
		}
		errMsg := ""
		if cmd.Status != protocol.StatusSucceeded {
			errMsg = nonEmpty(cmd.Stderr, "探索代理執行失敗："+cmd.Status)
		}
		s.finishScan(ctx, scanID, hosts, errMsg)
	case protocol.CmdRefresh:
		// Nothing: telemetry arrives as regular messages.
	}
	if cmd.DeploymentID != nil {
		if d, err := s.Store.GetDeployment(ctx, *cmd.DeploymentID); err == nil {
			s.Bus.Publish(events.DeploymentUpdated, d)
		}
	}
}

// RequestRefresh asks an online agent to re-collect everything now.
func (s *Service) RequestRefresh(ctx context.Context, actor Actor, deviceID uuid.UUID) (*store.Command, error) {
	return s.IssueCommand(ctx, actor, deviceID, NewCommand{Kind: protocol.CmdRefresh, TimeoutSec: 600}, nil)
}

// ---- chat ----

func chatToProto(m store.ChatMessage) protocol.Chat {
	return protocol.Chat{MessageID: m.ID.String(), From: m.Direction, Sender: m.Sender, Body: m.Body, SentAt: m.CreatedAt}
}

// SendChat stores an MIS message and pushes it to the device.
func (s *Service) SendChat(ctx context.Context, actor Actor, deviceID uuid.UUID, body string) (*store.ChatMessage, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, invalid("訊息不可為空")
	}
	if len(body) > 8000 {
		return nil, invalid("訊息過長")
	}
	d, err := s.Store.GetDevice(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if !d.HasAgent {
		return nil, invalid("此裝置未安裝 Agent")
	}
	m, err := s.Store.CreateChat(ctx, deviceID, "mis", actor.Name, body)
	if err != nil {
		return nil, err
	}
	s.Audit(ctx, actor, "chat.send", "device", deviceID.String(), d.Name, map[string]any{"message_id": m.ID, "length": len(body)}, true)
	s.Bus.Publish(events.ChatMessage, m)
	s.hub.sendTo(deviceID, protocol.TChat, chatToProto(*m))
	return m, nil
}

// MarkUserChatRead marks the user's messages read and notifies the agent.
func (s *Service) MarkUserChatRead(ctx context.Context, deviceID uuid.UUID) error {
	msgs, err := s.Store.MarkChatRead(ctx, deviceID, "user", nil)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ID.String())
		s.Bus.Publish(events.ChatMessage, m)
	}
	if len(ids) > 0 {
		s.hub.sendTo(deviceID, protocol.TChatRead, protocol.ChatRead{MessageIDs: ids})
	}
	return nil
}

// DeleteDevice removes a device and disconnects its agent.
func (s *Service) DeleteDevice(ctx context.Context, actor Actor, id uuid.UUID) error {
	d, err := s.Store.GetDevice(ctx, id)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteDevice(ctx, id); err != nil {
		return err
	}
	s.hub.disconnect(id)
	s.Audit(ctx, actor, "device.delete", "device", id.String(), d.Name, map[string]any{
		"serial_number": d.SerialNumber, "hostname": d.Hostname}, true)
	return nil
}

// ErrOffline is returned when an operation needs an online agent.
var ErrOffline = errors.New("device offline")
