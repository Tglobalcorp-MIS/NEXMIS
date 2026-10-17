package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

func toValidUTF8(s string) string {
	return strings.ReplaceAll(strings.ToValidUTF8(s, "�"), "\x00", "")
}

// ---- packages ----

type Package struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	Version       string    `json:"version"`
	Publisher     string    `json:"publisher"`
	FileName      string    `json:"file_name"`
	FileSize      int64     `json:"file_size"`
	SHA256        string    `json:"sha256"`
	Type          string    `json:"type"`
	InstallArgs   string    `json:"install_args"`
	UninstallArgs string    `json:"uninstall_args"`
	SuccessCodes  []int32   `json:"success_codes"`
	StoragePath   string    `json:"-"`
	CreatedByName *string   `json:"created_by_name"`
	CreatedAt     time.Time `json:"created_at"`
}

const packageCols = `p.id, p.name, p.version, p.publisher, p.file_name, p.file_size, p.sha256, p.type, p.install_args,
	p.uninstall_args, p.success_codes, p.storage_path, u.username, p.created_at`

func scanPackage(row pgx.Row) (*Package, error) {
	var p Package
	err := row.Scan(&p.ID, &p.Name, &p.Version, &p.Publisher, &p.FileName, &p.FileSize, &p.SHA256, &p.Type,
		&p.InstallArgs, &p.UninstallArgs, &p.SuccessCodes, &p.StoragePath, &p.CreatedByName, &p.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &p, nil
}

func (s *Store) ListPackages(ctx context.Context) ([]Package, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+packageCols+` FROM packages p LEFT JOIN users u ON u.id = p.created_by
		ORDER BY lower(p.name), p.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Package{}
	for rows.Next() {
		p, err := scanPackage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (s *Store) GetPackage(ctx context.Context, id uuid.UUID) (*Package, error) {
	return scanPackage(s.Pool.QueryRow(ctx, `SELECT `+packageCols+` FROM packages p LEFT JOIN users u ON u.id = p.created_by
		WHERE p.id = $1`, id))
}

func (s *Store) CreatePackage(ctx context.Context, p *Package, by uuid.UUID) error {
	if len(p.SuccessCodes) == 0 {
		p.SuccessCodes = []int32{0, 3010, 1641}
	}
	return mapErr(s.Pool.QueryRow(ctx, `INSERT INTO packages (id, name, version, publisher, file_name, file_size, sha256, type,
		install_args, uninstall_args, success_codes, storage_path, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING created_at`, p.ID, p.Name, p.Version, p.Publisher, p.FileName, p.FileSize, p.SHA256, p.Type, p.InstallArgs,
		p.UninstallArgs, p.SuccessCodes, p.StoragePath, by).Scan(&p.CreatedAt))
}

type PackagePatch struct {
	Name          *string  `json:"name"`
	Version       *string  `json:"version"`
	Publisher     *string  `json:"publisher"`
	InstallArgs   *string  `json:"install_args"`
	UninstallArgs *string  `json:"uninstall_args"`
	SuccessCodes  *[]int32 `json:"success_codes"`
}

func (s *Store) UpdatePackage(ctx context.Context, id uuid.UUID, p PackagePatch) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE packages SET name = COALESCE($2, name), version = COALESCE($3, version),
		publisher = COALESCE($4, publisher), install_args = COALESCE($5, install_args),
		uninstall_args = COALESCE($6, uninstall_args), success_codes = COALESCE($7, success_codes) WHERE id = $1`,
		id, p.Name, p.Version, p.Publisher, p.InstallArgs, p.UninstallArgs, p.SuccessCodes)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ErrInUse is returned when deleting a package referenced by deployments.
var ErrInUse = errors.New("in use")

func (s *Store) DeletePackage(ctx context.Context, id uuid.UUID) (string, error) {
	var path string
	err := s.Pool.QueryRow(ctx, `DELETE FROM packages WHERE id = $1 RETURNING storage_path`, id).Scan(&path)
	if err != nil {
		if strings.Contains(err.Error(), "23503") {
			return "", ErrInUse
		}
		return "", mapErr(err)
	}
	return path, nil
}

// ---- deployments ----

type Deployment struct {
	ID            uuid.UUID  `json:"id"`
	Name          string     `json:"name"`
	PackageID     uuid.UUID  `json:"package_id"`
	PackageName   string     `json:"package_name"`
	Target        Target     `json:"target"`
	Status        string     `json:"status"`
	ScheduledAt   *time.Time `json:"scheduled_at"`
	Total         int        `json:"total"`
	Succeeded     int        `json:"succeeded"`
	Failed        int        `json:"failed"`
	Pending       int        `json:"pending"`
	CreatedByName *string    `json:"created_by_name"`
	CreatedAt     time.Time  `json:"created_at"`
}

const deploymentSelect = `SELECT dp.id, dp.name, dp.package_id, p.name || CASE WHEN p.version <> '' THEN ' ' || p.version ELSE '' END,
	dp.target, dp.status, dp.scheduled_at,
	count(c.id), count(c.id) FILTER (WHERE c.status = 'succeeded'),
	count(c.id) FILTER (WHERE c.status IN ('failed','timeout','rejected','expired','canceled')),
	count(c.id) FILTER (WHERE c.status IN ('queued','sent','running')),
	u.username, dp.created_at
	FROM deployments dp JOIN packages p ON p.id = dp.package_id LEFT JOIN users u ON u.id = dp.created_by
	LEFT JOIN LATERAL (SELECT DISTINCT ON (device_id) id, status FROM commands
		WHERE deployment_id = dp.id ORDER BY device_id, created_at DESC) c ON true`

const deploymentGroup = ` GROUP BY dp.id, p.name, p.version, u.username`

func scanDeployment(row pgx.Row) (*Deployment, error) {
	var d Deployment
	var target []byte
	err := row.Scan(&d.ID, &d.Name, &d.PackageID, &d.PackageName, &target, &d.Status, &d.ScheduledAt,
		&d.Total, &d.Succeeded, &d.Failed, &d.Pending, &d.CreatedByName, &d.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	_ = json.Unmarshal(target, &d.Target)
	return &d, nil
}

func (s *Store) ListDeployments(ctx context.Context, p Page) ([]Deployment, int, error) {
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM deployments`).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit, offset := p.limitOffset()
	rows, err := s.Pool.Query(ctx, deploymentSelect+deploymentGroup+
		fmt.Sprintf(` ORDER BY dp.created_at DESC LIMIT %d OFFSET %d`, limit, offset))
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *d)
	}
	return out, total, rows.Err()
}

func (s *Store) GetDeployment(ctx context.Context, id uuid.UUID) (*Deployment, error) {
	return scanDeployment(s.Pool.QueryRow(ctx, deploymentSelect+` WHERE dp.id = $1`+deploymentGroup, id))
}

func (s *Store) CreateDeployment(ctx context.Context, name string, pkg uuid.UUID, t Target, status string, at *time.Time, by *uuid.UUID) (uuid.UUID, error) {
	tj, _ := json.Marshal(t)
	var id uuid.UUID
	err := s.Pool.QueryRow(ctx, `INSERT INTO deployments (name, package_id, target, status, scheduled_at, created_by)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, name, pkg, tj, status, at, by).Scan(&id)
	return id, mapErr(err)
}

func (s *Store) SetDeploymentStatus(ctx context.Context, q Querier, id uuid.UUID, status string) error {
	if q == nil {
		q = s.Pool
	}
	_, err := q.Exec(ctx, `UPDATE deployments SET status = $2 WHERE id = $1`, id, status)
	return err
}

// ClaimDeployment moves a scheduled deployment to running. It reports false
// when the deployment was already claimed or canceled. Call it in the same
// transaction that queues the deployment's commands, so a crash leaves the
// deployment scheduled rather than running with nothing queued.
func (s *Store) ClaimDeployment(ctx context.Context, tx pgx.Tx, id uuid.UUID) (bool, error) {
	tag, err := tx.Exec(ctx, `UPDATE deployments SET status = 'running' WHERE id = $1 AND status = 'scheduled'`, id)
	return tag.RowsAffected() == 1, err
}

// DueDeployments returns scheduled deployments whose time has come. They
// are claimed by ClaimDeployment.
func (s *Store) DueDeployments(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id FROM deployments
		WHERE status = 'scheduled' AND scheduled_at <= now() ORDER BY scheduled_at`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// CompleteFinishedDeployments flips running deployments with no pending
// commands to completed and returns their ids.
func (s *Store) CompleteFinishedDeployments(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.Pool.Query(ctx, `UPDATE deployments dp SET status = 'completed' WHERE status = 'running'
		AND NOT EXISTS (SELECT 1 FROM commands c WHERE c.deployment_id = dp.id AND c.status IN ('queued','sent','running'))
		AND dp.created_at < now() - interval '10 seconds'
		RETURNING id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

type DeploymentResult struct {
	DeviceID   uuid.UUID  `json:"device_id"`
	DeviceName string     `json:"device_name"`
	Status     string     `json:"status"`
	ExitCode   *int       `json:"exit_code"`
	FinishedAt *time.Time `json:"finished_at"`
	CommandID  uuid.UUID  `json:"command_id"`
	Stderr     string     `json:"stderr"`
}

func (s *Store) DeploymentResults(ctx context.Context, id uuid.UUID) ([]DeploymentResult, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT ON (c.device_id) c.device_id, d.name, c.status, c.exit_code, c.finished_at, c.id,
		left(c.stderr, 500) FROM commands c JOIN devices d ON d.id = c.device_id WHERE c.deployment_id = $1
		ORDER BY c.device_id, c.created_at DESC`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (DeploymentResult, error) {
		var x DeploymentResult
		err := r.Scan(&x.DeviceID, &x.DeviceName, &x.Status, &x.ExitCode, &x.FinishedAt, &x.CommandID, &x.Stderr)
		return x, err
	})
}

// FailedDeploymentDevices lists devices whose latest attempt failed.
func (s *Store) FailedDeploymentDevices(ctx context.Context, id uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.Pool.Query(ctx, `SELECT device_id FROM (SELECT DISTINCT ON (device_id) device_id, status FROM commands
		WHERE deployment_id = $1 ORDER BY device_id, created_at DESC) x
		WHERE status IN ('failed','timeout','rejected','expired','canceled')`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

func (s *Store) CancelDeploymentCommands(ctx context.Context, id uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `UPDATE commands SET status = 'canceled', finished_at = now()
		WHERE deployment_id = $1 AND status IN ('queued','sent')`, id)
	return err
}

// ---- schedules ----

type ScheduleAction struct {
	Type      string          `json:"type"` // command | deployment | refresh
	Command   json.RawMessage `json:"command,omitempty"`
	PackageID *uuid.UUID      `json:"package_id,omitempty"`
}

type Schedule struct {
	ID            uuid.UUID      `json:"id"`
	Name          string         `json:"name"`
	Cron          string         `json:"cron"`
	Enabled       bool           `json:"enabled"`
	Action        ScheduleAction `json:"action"`
	Target        Target         `json:"target"`
	LastRunAt     *time.Time     `json:"last_run_at"`
	NextRunAt     *time.Time     `json:"next_run_at"`
	CreatedBy     *uuid.UUID     `json:"-"`
	CreatedByName *string        `json:"created_by_name"`
	CreatedAt     time.Time      `json:"created_at"`
}

const scheduleCols = `s.id, s.name, s.cron, s.enabled, s.action, s.target, s.last_run_at, s.next_run_at, s.created_by, u.username, s.created_at`

func scanSchedule(row pgx.Row) (*Schedule, error) {
	var sc Schedule
	var action, target []byte
	err := row.Scan(&sc.ID, &sc.Name, &sc.Cron, &sc.Enabled, &action, &target, &sc.LastRunAt, &sc.NextRunAt,
		&sc.CreatedBy, &sc.CreatedByName, &sc.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	_ = json.Unmarshal(action, &sc.Action)
	_ = json.Unmarshal(target, &sc.Target)
	return &sc, nil
}

func (s *Store) ListSchedules(ctx context.Context) ([]Schedule, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+scheduleCols+` FROM schedules s LEFT JOIN users u ON u.id = s.created_by ORDER BY lower(s.name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sc)
	}
	return out, rows.Err()
}

func (s *Store) GetSchedule(ctx context.Context, id uuid.UUID) (*Schedule, error) {
	return scanSchedule(s.Pool.QueryRow(ctx, `SELECT `+scheduleCols+` FROM schedules s LEFT JOIN users u ON u.id = s.created_by
		WHERE s.id = $1`, id))
}

func (s *Store) CreateSchedule(ctx context.Context, sc *Schedule, by uuid.UUID) error {
	a, _ := json.Marshal(sc.Action)
	t, _ := json.Marshal(sc.Target)
	return mapErr(s.Pool.QueryRow(ctx, `INSERT INTO schedules (name, cron, action, target, enabled, next_run_at, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, created_at`, sc.Name, sc.Cron, a, t, sc.Enabled, sc.NextRunAt, by).
		Scan(&sc.ID, &sc.CreatedAt))
}

func (s *Store) SaveSchedule(ctx context.Context, sc *Schedule) error {
	a, _ := json.Marshal(sc.Action)
	t, _ := json.Marshal(sc.Target)
	tag, err := s.Pool.Exec(ctx, `UPDATE schedules SET name = $2, cron = $3, action = $4, target = $5, enabled = $6,
		next_run_at = $7 WHERE id = $1`, sc.ID, sc.Name, sc.Cron, a, t, sc.Enabled, sc.NextRunAt)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteSchedule(ctx context.Context, id uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM schedules WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ClaimDueSchedules atomically advances due schedules. next computes the
// following run time from a cron expression.
func (s *Store) ClaimDueSchedules(ctx context.Context, next func(cron string, from time.Time) (time.Time, error)) ([]Schedule, error) {
	var out []Schedule
	err := s.InTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+scheduleCols+` FROM schedules s LEFT JOIN users u ON u.id = s.created_by
			WHERE s.enabled AND s.next_run_at <= now() FOR UPDATE OF s SKIP LOCKED`)
		if err != nil {
			return err
		}
		for rows.Next() {
			sc, err := scanSchedule(rows)
			if err != nil {
				rows.Close()
				return err
			}
			out = append(out, *sc)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		now := time.Now()
		for _, sc := range out {
			var nextAt *time.Time
			if n, err := next(sc.Cron, now); err == nil {
				nextAt = &n
			}
			if _, err := tx.Exec(ctx, `UPDATE schedules SET last_run_at = now(), next_run_at = $2 WHERE id = $1`, sc.ID, nextAt); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

func (s *Store) TouchScheduleRun(ctx context.Context, id uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `UPDATE schedules SET last_run_at = now() WHERE id = $1`, id)
	return err
}

// ---- discovery ----

type DiscoveryScan struct {
	ID              uuid.UUID        `json:"id"`
	CIDRs           []string         `json:"cidrs"`
	Ports           []int32          `json:"ports"`
	Status          string           `json:"status"`
	Found           int              `json:"found"`
	RequestedByName string           `json:"requested_by_name"`
	ProbeDeviceID   *uuid.UUID       `json:"probe_device_id"`
	ProbeDeviceName *string          `json:"probe_device_name"`
	StartedAt       time.Time        `json:"started_at"`
	FinishedAt      *time.Time       `json:"finished_at"`
	Error           string           `json:"error"`
	Hosts           []DiscoveredHost `json:"hosts,omitempty"`
}

type DiscoveredHost struct {
	IP         string     `json:"ip"`
	MAC        string     `json:"mac"`
	Hostname   string     `json:"hostname"`
	OpenPorts  []int32    `json:"open_ports"`
	Vendor     string     `json:"vendor"`
	DeviceType string     `json:"device_type"`
	DeviceID   *uuid.UUID `json:"device_id"`
	RTTMillis  int        `json:"rtt_ms"`
}

const scanCols = `s.id, s.cidrs, s.ports, s.status, s.found, s.requested_by_name, s.probe_device_id, d.name, s.started_at,
	s.finished_at, s.error`

func scanScan(row pgx.Row) (*DiscoveryScan, error) {
	var x DiscoveryScan
	err := row.Scan(&x.ID, &x.CIDRs, &x.Ports, &x.Status, &x.Found, &x.RequestedByName, &x.ProbeDeviceID, &x.ProbeDeviceName,
		&x.StartedAt, &x.FinishedAt, &x.Error)
	if err != nil {
		return nil, mapErr(err)
	}
	return &x, nil
}

func (s *Store) CreateScan(ctx context.Context, cidrs []string, ports []int32, by uuid.UUID, byName string, probe *uuid.UUID) (*DiscoveryScan, error) {
	var id uuid.UUID
	if err := s.Pool.QueryRow(ctx, `INSERT INTO discovery_scans (cidrs, ports, status, requested_by, requested_by_name, probe_device_id)
		VALUES ($1,$2,'running',$3,$4,$5) RETURNING id`, cidrs, ports, by, byName, probe).Scan(&id); err != nil {
		return nil, mapErr(err)
	}
	return s.GetScan(ctx, id, false)
}

func (s *Store) GetScan(ctx context.Context, id uuid.UUID, withHosts bool) (*DiscoveryScan, error) {
	x, err := scanScan(s.Pool.QueryRow(ctx, `SELECT `+scanCols+` FROM discovery_scans s LEFT JOIN devices d ON d.id = s.probe_device_id
		WHERE s.id = $1`, id))
	if err != nil || !withHosts {
		return x, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT ip, mac, hostname, open_ports, vendor, device_type, device_id, rtt_ms
		FROM discovered_hosts WHERE scan_id = $1 ORDER BY inet(ip)`, id)
	if err != nil {
		return nil, err
	}
	x.Hosts, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (DiscoveredHost, error) {
		var h DiscoveredHost
		err := r.Scan(&h.IP, &h.MAC, &h.Hostname, &h.OpenPorts, &h.Vendor, &h.DeviceType, &h.DeviceID, &h.RTTMillis)
		return h, err
	})
	return x, err
}

func (s *Store) ListScans(ctx context.Context, p Page) ([]DiscoveryScan, int, error) {
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM discovery_scans`).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit, offset := p.limitOffset()
	rows, err := s.Pool.Query(ctx, `SELECT `+scanCols+` FROM discovery_scans s LEFT JOIN devices d ON d.id = s.probe_device_id
		ORDER BY s.started_at DESC`+fmt.Sprintf(` LIMIT %d OFFSET %d`, limit, offset))
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []DiscoveryScan{}
	for rows.Next() {
		x, err := scanScan(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *x)
	}
	return out, total, rows.Err()
}

// FinishScan stores scan results, linking each host to an existing device
// by MAC, IP or hostname.
func (s *Store) FinishScan(ctx context.Context, id uuid.UUID, hosts []protocol.DiscoveredHost, scanErr string) error {
	return s.InTx(ctx, func(tx pgx.Tx) error {
		for _, h := range hosts {
			mac := NormalizeMAC(h.MAC)
			var devID *uuid.UUID
			var found uuid.UUID
			err := tx.QueryRow(ctx, `SELECT id FROM devices WHERE ($1 <> '' AND $1 = ANY(mac_addresses)) OR $2 = ANY(ip_addresses)
				OR ($3 <> '' AND (lower(hostname) = lower($3) OR lower(name) = lower($3)))
				ORDER BY ($1 <> '' AND $1 = ANY(mac_addresses)) DESC LIMIT 1`, mac, h.IP, shortHost(h.Hostname)).Scan(&found)
			if err == nil {
				devID = &found
				_, _ = tx.Exec(ctx, `UPDATE devices SET sources = ARRAY(SELECT DISTINCT unnest(sources || ARRAY['discovery']))
					WHERE id = $1`, found)
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			ports := make([]int32, 0, len(h.OpenPorts))
			for _, p := range h.OpenPorts {
				ports = append(ports, int32(p))
			}
			if _, err := tx.Exec(ctx, `INSERT INTO discovered_hosts (scan_id, ip, mac, hostname, open_ports, vendor, device_type, rtt_ms, device_id)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (scan_id, ip) DO UPDATE SET mac = EXCLUDED.mac,
				hostname = EXCLUDED.hostname, open_ports = EXCLUDED.open_ports, vendor = EXCLUDED.vendor,
				device_type = EXCLUDED.device_type, rtt_ms = EXCLUDED.rtt_ms, device_id = EXCLUDED.device_id`,
				id, h.IP, mac, h.Hostname, ports, h.Vendor, h.DeviceType, h.RTTMillis, devID); err != nil {
				return err
			}
		}
		status := "completed"
		if scanErr != "" {
			status = "failed"
		}
		_, err := tx.Exec(ctx, `UPDATE discovery_scans SET status = $2, error = $3, finished_at = now(),
			found = (SELECT count(*) FROM discovered_hosts WHERE scan_id = $1) WHERE id = $1`, id, status, scanErr)
		return err
	})
}

func shortHost(h string) string {
	h, _, _ = strings.Cut(h, ".")
	return h
}

// ImportHosts registers discovered hosts that are not yet linked as devices.
func (s *Store) ImportHosts(ctx context.Context, scanID uuid.UUID, ips []string) (int, error) {
	n := 0
	err := s.InTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT ip, mac, hostname, vendor, device_type FROM discovered_hosts
			WHERE scan_id = $1 AND ip = ANY($2) AND device_id IS NULL`, scanID, ips)
		if err != nil {
			return err
		}
		type h struct{ ip, mac, host, vendor, typ string }
		var hs []h
		for rows.Next() {
			var x h
			if err := rows.Scan(&x.ip, &x.mac, &x.host, &x.vendor, &x.typ); err != nil {
				rows.Close()
				return err
			}
			hs = append(hs, x)
		}
		rows.Close()
		for _, x := range hs {
			name := shortHost(x.host)
			if name == "" {
				name = x.ip
			}
			typ := x.typ
			if typ == "" {
				typ = "unknown"
			}
			macs := []string{}
			if x.mac != "" {
				macs = append(macs, x.mac)
			}
			var id uuid.UUID
			if err := tx.QueryRow(ctx, `INSERT INTO devices (name, hostname, device_type, sources, ip_addresses, mac_addresses, manufacturer)
				VALUES ($1,$2,$3,ARRAY['discovery'],ARRAY[$4],$5,$6) RETURNING id`, name, x.host, typ, x.ip, macs, x.vendor).Scan(&id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE discovered_hosts SET device_id = $3 WHERE scan_id = $1 AND ip = $2`, scanID, x.ip, id); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

// ---- audit ----

type AuditEntry struct {
	ID         int64           `json:"id"`
	At         time.Time       `json:"at"`
	ActorID    *uuid.UUID      `json:"actor_id"`
	ActorName  string          `json:"actor_name"`
	ActorIP    string          `json:"actor_ip"`
	Action     string          `json:"action"`
	TargetType string          `json:"target_type"`
	TargetID   string          `json:"target_id"`
	TargetName string          `json:"target_name"`
	Details    json.RawMessage `json:"details"`
	Result     string          `json:"result"`
}

func (s *Store) InsertAudit(ctx context.Context, e AuditEntry) error {
	if len(e.Details) == 0 {
		e.Details = json.RawMessage(`{}`)
	}
	if e.Result == "" {
		e.Result = "success"
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO audit_log (actor_id, actor_name, actor_ip, action, target_type, target_id,
		target_name, details, result) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		e.ActorID, e.ActorName, e.ActorIP, e.Action, e.TargetType, e.TargetID, e.TargetName, []byte(e.Details), e.Result)
	return err
}

type AuditFilter struct {
	Actor    string
	Action   string
	TargetID string
	Q        string
	From     *time.Time
	To       *time.Time
}

func (f AuditFilter) where() *whereBuilder {
	w := &whereBuilder{}
	if f.Actor != "" {
		w.add("lower(actor_name) LIKE ?", likeEscape(strings.ToLower(f.Actor)))
	}
	if f.Action != "" {
		w.add("action LIKE ?", strings.ReplaceAll(f.Action, "*", "%"))
	}
	if f.TargetID != "" {
		w.add("target_id = ?", f.TargetID)
	}
	if f.Q != "" {
		p := likeEscape(strings.ToLower(f.Q))
		w.add("(lower(target_name) LIKE ? OR lower(details::text) LIKE ? OR lower(action) LIKE ?)", p, p, p)
	}
	if f.From != nil {
		w.add("at >= ?", *f.From)
	}
	if f.To != nil {
		w.add("at <= ?", *f.To)
	}
	return w
}

func (s *Store) ListAudit(ctx context.Context, f AuditFilter, p Page) ([]AuditEntry, int, error) {
	w := f.where()
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_log`+w.sql(), w.args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit, offset := p.limitOffset()
	rows, err := s.Pool.Query(ctx, `SELECT id, at, actor_id, actor_name, actor_ip, action, target_type, target_id, target_name,
		details, result FROM audit_log`+w.sql()+fmt.Sprintf(` ORDER BY at DESC, id DESC LIMIT %d OFFSET %d`, limit, offset), w.args...)
	if err != nil {
		return nil, 0, err
	}
	items, err := pgx.CollectRows(rows, scanAudit)
	return items, total, err
}

// EachAudit streams audit entries matching f (newest first) for export.
func (s *Store) EachAudit(ctx context.Context, f AuditFilter, fn func(AuditEntry) error) error {
	w := f.where()
	rows, err := s.Pool.Query(ctx, `SELECT id, at, actor_id, actor_name, actor_ip, action, target_type, target_id, target_name,
		details, result FROM audit_log`+w.sql()+` ORDER BY at DESC, id DESC LIMIT 1000000`, w.args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		e, err := scanAudit(rows)
		if err != nil {
			return err
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return rows.Err()
}

func scanAudit(r pgx.CollectableRow) (AuditEntry, error) {
	var e AuditEntry
	var details []byte
	err := r.Scan(&e.ID, &e.At, &e.ActorID, &e.ActorName, &e.ActorIP, &e.Action, &e.TargetType, &e.TargetID,
		&e.TargetName, &details, &e.Result)
	e.Details = json.RawMessage(details)
	return e, err
}

// PurgeAudit removes audit entries older than days (0 disables).
func (s *Store) PurgeAudit(ctx context.Context, days int) error {
	if days <= 0 {
		return nil
	}
	return s.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL nexmis.audit_purge = 'on'`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM audit_log WHERE at < now() - make_interval(days => $1)`, days)
		return err
	})
}

// ---- settings ----

// GetSetting decodes a setting into v. It returns false when unset.
func (s *Store) GetSetting(ctx context.Context, key string, v any) (bool, error) {
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(raw, v)
}

func (s *Store) PutSetting(ctx context.Context, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1,$2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, raw)
	return err
}

// ---- AD sync ----

// ADComputer is a computer object read from Active Directory.
type ADComputer struct {
	Name      string
	DNSName   string
	DN        string
	OS        string
	OSVersion string
	Enabled   bool
	LastLogon *time.Time
}

// UpsertADComputers merges AD computer objects into devices by hostname.
func (s *Store) UpsertADComputers(ctx context.Context, comps []ADComputer) (added, updated int, err error) {
	err = s.InTx(ctx, func(tx pgx.Tx) error {
		for _, c := range comps {
			var id uuid.UUID
			err := tx.QueryRow(ctx, `SELECT id FROM devices WHERE lower(hostname) = lower($1) OR lower(name) = lower($1)
				OR (ad_dn <> '' AND ad_dn = $2) ORDER BY agent_id IS NOT NULL DESC LIMIT 1`, c.Name, c.DN).Scan(&id)
			devType := "windows"
			if strings.Contains(strings.ToLower(c.OS), "server") {
				devType = "server"
			} else if c.OS != "" && !strings.Contains(strings.ToLower(c.OS), "windows") {
				devType = "other"
			}
			if errors.Is(err, pgx.ErrNoRows) {
				if _, err := tx.Exec(ctx, `INSERT INTO devices (name, hostname, device_type, sources, os_name, os_version,
					ad_dn, ad_enabled, ad_last_logon) VALUES ($1,$1,$2,ARRAY['ad'],$3,$4,$5,$6,$7)`,
					c.Name, devType, c.OS, c.OSVersion, c.DN, c.Enabled, c.LastLogon); err != nil {
					return err
				}
				added++
				continue
			}
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE devices SET ad_dn = $2, ad_enabled = $3, ad_last_logon = $4,
				sources = ARRAY(SELECT DISTINCT unnest(sources || ARRAY['ad'])),
				os_name = CASE WHEN agent_id IS NULL AND $5 <> '' THEN $5 ELSE os_name END,
				updated_at = now() WHERE id = $1`, id, c.DN, c.Enabled, c.LastLogon, c.OS); err != nil {
				return err
			}
			updated++
		}
		return nil
	})
	return added, updated, err
}
