package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

// ---- enrollment tokens ----

type EnrollmentToken struct {
	ID            uuid.UUID  `json:"id"`
	Name          string     `json:"name"`
	TokenPrefix   string     `json:"token_prefix"`
	ExpiresAt     *time.Time `json:"expires_at"`
	MaxUses       *int       `json:"max_uses"`
	Uses          int        `json:"uses"`
	GroupID       *uuid.UUID `json:"group_id"`
	Tags          []string   `json:"tags"`
	Revoked       bool       `json:"revoked"`
	CreatedByName *string    `json:"created_by_name"`
	CreatedAt     time.Time  `json:"created_at"`
}

func (s *Store) ListEnrollmentTokens(ctx context.Context) ([]EnrollmentToken, error) {
	rows, err := s.Pool.Query(ctx, `SELECT t.id, t.name, t.token_prefix, t.expires_at, t.max_uses, t.uses, t.group_id,
		t.tags, t.revoked, u.username, t.created_at FROM enrollment_tokens t LEFT JOIN users u ON u.id = t.created_by
		ORDER BY t.created_at DESC`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (EnrollmentToken, error) {
		var t EnrollmentToken
		err := r.Scan(&t.ID, &t.Name, &t.TokenPrefix, &t.ExpiresAt, &t.MaxUses, &t.Uses, &t.GroupID, &t.Tags,
			&t.Revoked, &t.CreatedByName, &t.CreatedAt)
		return t, err
	})
}

func (s *Store) CreateEnrollmentToken(ctx context.Context, t *EnrollmentToken, tokenHash string, createdBy uuid.UUID) error {
	return mapErr(s.Pool.QueryRow(ctx, `INSERT INTO enrollment_tokens (name, token_hash, token_prefix, expires_at, max_uses,
		group_id, tags, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id, created_at`,
		t.Name, tokenHash, t.TokenPrefix, t.ExpiresAt, t.MaxUses, t.GroupID, cleanTags(t.Tags), createdBy).
		Scan(&t.ID, &t.CreatedAt))
}

func (s *Store) RevokeEnrollmentToken(ctx context.Context, id uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE enrollment_tokens SET revoked = true WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ErrInvalidToken means the enrollment token is unknown, revoked, expired
// or exhausted.
var ErrInvalidToken = errors.New("invalid enrollment token")

// EnrollInput is what the server learned from an enrollment request.
type EnrollInput struct {
	TokenHash    string
	MachineID    string
	Hostname     string
	Domain       string
	OS           string
	OSVersion    string
	AgentVersion string
	PublicKey    []byte
}

// Enroll validates the token and creates or re-keys the device. Devices are
// matched by machine id first, then by hostname against agent-less records
// (from AD sync or discovery) so those merge into a single device.
func (s *Store) Enroll(ctx context.Context, in EnrollInput) (deviceID, agentID uuid.UUID, err error) {
	agentID = uuid.New()
	err = s.InTx(ctx, func(tx pgx.Tx) error {
		// A retry after a lost response arrives with the key the server
		// already stored for this machine: answer it again without using up
		// the token (a single-use token would otherwise strand the agent).
		var tokenOK bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM enrollment_tokens WHERE token_hash = $1
			AND NOT revoked AND (expires_at IS NULL OR expires_at > now()))`, in.TokenHash).Scan(&tokenOK); err != nil {
			return err
		}
		if tokenOK && in.MachineID != "" {
			err := tx.QueryRow(ctx, `SELECT id, agent_id FROM devices WHERE machine_id = $1 AND agent_public_key = $2
				AND agent_id IS NOT NULL`, in.MachineID, in.PublicKey).Scan(&deviceID, &agentID)
			if err == nil {
				return nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		var tokID uuid.UUID
		var groupID *uuid.UUID
		var tags []string
		err := tx.QueryRow(ctx, `UPDATE enrollment_tokens SET uses = uses + 1
			WHERE token_hash = $1 AND NOT revoked AND (expires_at IS NULL OR expires_at > now())
			  AND (max_uses IS NULL OR uses < max_uses)
			RETURNING id, group_id, tags`, in.TokenHash).Scan(&tokID, &groupID, &tags)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidToken
		}
		if err != nil {
			return err
		}
		devType := "windows"
		if strings.Contains(strings.ToLower(in.OS), "server") {
			devType = "server"
		}
		err = tx.QueryRow(ctx, `SELECT id FROM devices WHERE machine_id = $1`, in.MachineID).Scan(&deviceID)
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `SELECT id FROM devices WHERE agent_id IS NULL AND machine_id IS NULL
				AND (lower(hostname) = lower($1) OR lower(name) = lower($1)) ORDER BY created_at LIMIT 1`, in.Hostname).Scan(&deviceID)
		}
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return tx.QueryRow(ctx, `INSERT INTO devices (name, hostname, domain, machine_id, agent_id, agent_public_key,
				agent_version, device_type, sources, os_name, os_version, enrolled_at, group_id, tags)
				VALUES ($1,$1,$2,$3,$4,$5,$6,$7,ARRAY['agent'],$8,$9,now(),$10,$11) RETURNING id`,
				in.Hostname, in.Domain, in.MachineID, agentID, in.PublicKey, in.AgentVersion, devType,
				in.OS, in.OSVersion, groupID, nonNil(tags)).Scan(&deviceID)
		case err != nil:
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE devices SET machine_id = $2, agent_id = $3, agent_public_key = $4, agent_version = $5,
			hostname = $6, domain = CASE WHEN $7 <> '' THEN $7 ELSE domain END,
			device_type = CASE WHEN device_type IN ('unknown','other') THEN $8 ELSE device_type END,
			sources = ARRAY(SELECT DISTINCT unnest(sources || ARRAY['agent'])),
			os_name = $9, os_version = $10, enrolled_at = now(),
			group_id = COALESCE(group_id, $11),
			tags = ARRAY(SELECT DISTINCT unnest(tags || $12::text[])),
			updated_at = now()
			WHERE id = $1`, deviceID, in.MachineID, agentID, in.PublicKey, in.AgentVersion, in.Hostname, in.Domain,
			devType, in.OS, in.OSVersion, groupID, nonNil(tags))
		return mapErr(err)
	})
	return deviceID, agentID, err
}

// ---- telemetry ----

func (s *Store) SetOnline(ctx context.Context, id uuid.UUID, online bool) error {
	_, err := s.Pool.Exec(ctx, `UPDATE devices SET online = $2, last_seen_at = now(),
		helper_online = CASE WHEN $2 THEN helper_online ELSE false END WHERE id = $1`, id, online)
	return err
}

// MarkAllOffline is called at server start: no agent is connected yet.
func (s *Store) MarkAllOffline(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `UPDATE devices SET online = false, helper_online = false WHERE online`)
	return err
}

func (s *Store) ApplyHeartbeat(ctx context.Context, id uuid.UUID, hb protocol.Heartbeat) error {
	var memPct *float64
	if hb.MemTotal > 0 {
		v := float64(hb.MemUsed) / float64(hb.MemTotal) * 100
		memPct = &v
	}
	ips := hb.IPAddresses
	_, err := s.Pool.Exec(ctx, `UPDATE devices SET online = true, last_seen_at = now(), cpu_percent = $2,
		mem_percent = COALESCE($3, mem_percent), uptime_sec = $4, logged_in_user = $5, session_state = $6,
		ip_addresses = CASE WHEN cardinality($7::text[]) > 0 THEN $7 ELSE ip_addresses END,
		agent_version = $8, helper_online = $9 WHERE id = $1`,
		id, hb.CPUPercent, memPct, int64(hb.UptimeSec), hb.LoggedInUser, hb.SessionState, nonNil(ips),
		hb.AgentVersion, hb.HelperOnline)
	return err
}

// InventoryHash returns the stored hash for a kind ("" when none).
func (s *Store) InventoryHash(ctx context.Context, id uuid.UUID, kind string) (string, error) {
	var h string
	err := s.Pool.QueryRow(ctx, `SELECT hash FROM device_inventory WHERE device_id = $1 AND kind = $2`, id, kind).Scan(&h)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return h, err
}

// TouchInventory records that an unchanged inventory was re-confirmed.
func (s *Store) TouchInventory(ctx context.Context, id uuid.UUID, kind string, at time.Time) error {
	_, err := s.Pool.Exec(ctx, `UPDATE device_inventory SET collected_at = $3 WHERE device_id = $1 AND kind = $2`, id, kind, at)
	return err
}

// SaveHardware stores a hardware inventory, refreshes the device summary
// and records notable changes.
func (s *Store) SaveHardware(ctx context.Context, id uuid.UUID, inv protocol.Inventory) error {
	hw := inv.Hardware
	if hw == nil {
		return errors.New("missing hardware")
	}
	data, err := json.Marshal(hw)
	if err != nil {
		return err
	}
	return s.InTx(ctx, func(tx pgx.Tx) error {
		var oldRaw []byte
		err := tx.QueryRow(ctx, `SELECT data FROM device_inventory WHERE device_id = $1 AND kind = 'hardware'`, id).Scan(&oldRaw)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if len(oldRaw) > 0 {
			var old protocol.Hardware
			if json.Unmarshal(oldRaw, &old) == nil {
				for _, c := range diffHardware(&old, hw) {
					if _, err := tx.Exec(ctx, `INSERT INTO inventory_changes (device_id, kind, change, item, old_value, new_value)
						VALUES ($1,'hardware',$2,$3,$4,$5)`, id, c.Change, c.Item, c.Old, c.New); err != nil {
						return err
					}
				}
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO device_inventory (device_id, kind, hash, data, collected_at)
			VALUES ($1,'hardware',$2,$3,$4) ON CONFLICT (device_id, kind) DO UPDATE
			SET hash = EXCLUDED.hash, data = EXCLUDED.data, collected_at = EXCLUDED.collected_at`,
			id, inv.Hash, data, inv.Collected); err != nil {
			return err
		}
		var macs []string
		for _, n := range hw.NICs {
			if m := NormalizeMAC(n.MAC); m != "" {
				macs = append(macs, m)
			}
		}
		sort.Strings(macs)
		devType := "windows"
		if strings.Contains(strings.ToLower(hw.OSName), "server") {
			devType = "server"
		}
		osVersion := hw.OSVersion
		if hw.OSBuild != "" {
			osVersion = hw.OSBuild
		}
		_, err = tx.Exec(ctx, `UPDATE devices SET manufacturer = $2, model = $3, serial_number = $4, chassis_type = $5,
			os_name = $6, os_version = $7, os_edition = $8,
			domain = CASE WHEN $9 <> '' THEN $9 ELSE domain END,
			mac_addresses = CASE WHEN cardinality($10::text[]) > 0 THEN $10 ELSE mac_addresses END,
			device_type = CASE WHEN device_type IN ('windows','server','unknown','other') THEN $11 ELSE device_type END,
			updated_at = now() WHERE id = $1`,
			id, hw.Manufacturer, hw.Model, hw.SerialNumber, hw.ChassisType, hw.OSName, osVersion, hw.OSEdition,
			hw.Domain, nonNil(macs), devType)
		return err
	})
}

// InventoryChange is one recorded difference between two inventories.
type InventoryChange struct {
	ID     int64     `json:"id"`
	Kind   string    `json:"kind"`
	Change string    `json:"change"`
	Item   string    `json:"item"`
	Old    string    `json:"old_value"`
	New    string    `json:"new_value"`
	At     time.Time `json:"at"`
}

func diffHardware(old, cur *protocol.Hardware) []InventoryChange {
	var out []InventoryChange
	cmp := func(item, a, b string) {
		if a != b {
			out = append(out, InventoryChange{Change: "changed", Item: item, Old: a, New: b})
		}
	}
	cmp("作業系統", strings.TrimSpace(old.OSName+" "+old.OSBuild), strings.TrimSpace(cur.OSName+" "+cur.OSBuild))
	cmp("BIOS 版本", old.BIOSVersion, cur.BIOSVersion)
	cmp("記憶體總量", fmtBytes(old.MemoryTotal), fmtBytes(cur.MemoryTotal))
	cmp("序號", old.SerialNumber, cur.SerialNumber)
	cmp("網域", old.Domain, cur.Domain)
	cpuNames := func(h *protocol.Hardware) string {
		var n []string
		for _, c := range h.CPUs {
			n = append(n, c.Name)
		}
		return strings.Join(n, ", ")
	}
	cmp("CPU", cpuNames(old), cpuNames(cur))
	diskSet := func(h *protocol.Hardware) map[string]string {
		m := map[string]string{}
		for _, d := range h.Disks {
			key := d.SerialNumber
			if key == "" {
				key = d.Model
			}
			m[key] = fmt.Sprintf("%s (%s)", d.Model, fmtBytes(d.Size))
		}
		return m
	}
	o, c := diskSet(old), diskSet(cur)
	for k, v := range c {
		if _, ok := o[k]; !ok {
			out = append(out, InventoryChange{Change: "added", Item: "磁碟", New: v})
		}
	}
	for k, v := range o {
		if _, ok := c[k]; !ok {
			out = append(out, InventoryChange{Change: "removed", Item: "磁碟", Old: v})
		}
	}
	return out
}

func fmtBytes(b uint64) string {
	const gib = 1 << 30
	if b == 0 {
		return ""
	}
	return fmt.Sprintf("%.1f GB", float64(b)/gib)
}

// swKey identifies an installed application. The same product can be
// installed for both architectures or per-machine and per-user at once, so
// the name alone is not unique.
type swKey struct{ name, arch, scope string }

func (k swKey) label() string {
	var tags []string
	if k.arch != "" {
		tags = append(tags, k.arch)
	}
	if k.scope == "user" {
		tags = append(tags, "使用者")
	}
	if len(tags) == 0 {
		return k.name
	}
	return k.name + " (" + strings.Join(tags, ", ") + ")"
}

// softwareChanges compares installed versions per application. A single
// version replaced by another is a change; otherwise (side-by-side
// versions) each version is reported as added or removed.
func softwareChanges(old, cur map[swKey][]string) []InventoryChange {
	var out []InventoryChange
	keys := map[swKey]bool{}
	for k := range old {
		keys[k] = true
	}
	for k := range cur {
		keys[k] = true
	}
	for k := range keys {
		o, c := multisetDiff(old[k], cur[k])
		switch {
		case len(o) == 1 && len(c) == 1 && len(old[k]) == 1 && len(cur[k]) == 1:
			out = append(out, InventoryChange{Change: "changed", Item: k.label(), Old: o[0], New: c[0]})
		default:
			for _, v := range c {
				out = append(out, InventoryChange{Change: "added", Item: k.label(), New: v})
			}
			for _, v := range o {
				out = append(out, InventoryChange{Change: "removed", Item: k.label(), Old: v})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Item != out[j].Item {
			return out[i].Item < out[j].Item
		}
		return out[i].Change < out[j].Change
	})
	return out
}

// multisetDiff returns the values only in a and only in b, counting
// duplicates.
func multisetDiff(a, b []string) (onlyA, onlyB []string) {
	n := map[string]int{}
	for _, v := range a {
		n[v]++
	}
	for _, v := range b {
		if n[v] > 0 {
			n[v]--
		} else {
			onlyB = append(onlyB, v)
		}
	}
	for _, v := range a {
		if n[v] > 0 {
			n[v]--
			onlyA = append(onlyA, v)
		}
	}
	return onlyA, onlyB
}

// SaveSoftware replaces the software list and records added/removed/updated
// applications. The very first inventory is not recorded as changes.
func (s *Store) SaveSoftware(ctx context.Context, id uuid.UUID, inv protocol.Inventory) error {
	data, err := json.Marshal(inv.Software)
	if err != nil {
		return err
	}
	return s.InTx(ctx, func(tx pgx.Tx) error {
		var hadPrevious bool
		if err := tx.QueryRow(ctx, `SELECT true FROM device_inventory WHERE device_id = $1 AND kind = 'software'`, id).
			Scan(&hadPrevious); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		old := map[swKey][]string{}
		rows, err := tx.Query(ctx, `SELECT name, arch, scope, version FROM device_software WHERE device_id = $1`, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var k swKey
			var v string
			if err := rows.Scan(&k.name, &k.arch, &k.scope, &v); err != nil {
				rows.Close()
				return err
			}
			old[k] = append(old[k], v)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		cur := map[swKey][]string{}
		for _, sw := range inv.Software {
			k := swKey{sw.Name, sw.Arch, sw.Scope}
			cur[k] = append(cur[k], sw.Version)
		}
		if hadPrevious {
			batch := &pgx.Batch{}
			for _, c := range softwareChanges(old, cur) {
				batch.Queue(`INSERT INTO inventory_changes (device_id, kind, change, item, old_value, new_value)
					VALUES ($1,'software',$2,$3,$4,$5)`, id, c.Change, c.Item, c.Old, c.New)
			}
			if batch.Len() > 0 {
				if err := tx.SendBatch(ctx, batch).Close(); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM device_software WHERE device_id = $1`, id); err != nil {
			return err
		}
		if len(inv.Software) > 0 {
			rowsIn := make([][]any, 0, len(inv.Software))
			for _, sw := range inv.Software {
				rowsIn = append(rowsIn, []any{id, sw.Name, sw.Version, sw.Publisher, sw.InstallDate, sw.Arch, sw.Scope, sw.UninstallID})
			}
			if _, err := tx.CopyFrom(ctx, pgx.Identifier{"device_software"},
				[]string{"device_id", "name", "version", "publisher", "install_date", "arch", "scope", "uninstall_id"},
				pgx.CopyFromRows(rowsIn)); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO device_inventory (device_id, kind, hash, data, collected_at)
			VALUES ($1,'software',$2,$3,$4) ON CONFLICT (device_id, kind) DO UPDATE
			SET hash = EXCLUDED.hash, data = EXCLUDED.data, collected_at = EXCLUDED.collected_at`,
			id, inv.Hash, data, inv.Collected)
		return err
	})
}

// HealthStatusOf derives an overall status from issues.
func HealthStatusOf(issues []protocol.Issue) string {
	status := "ok"
	for _, i := range issues {
		switch i.Severity {
		case protocol.SevCritical:
			return "critical"
		case protocol.SevWarning:
			status = "warning"
		}
	}
	return status
}

func (s *Store) SaveHealth(ctx context.Context, id uuid.UUID, h protocol.Health) (string, error) {
	data, err := json.Marshal(h)
	if err != nil {
		return "", err
	}
	if h.Issues == nil {
		h.Issues = []protocol.Issue{}
	}
	issues, _ := json.Marshal(h.Issues)
	status := HealthStatusOf(h.Issues)
	err = s.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO health_snapshots (device_id, collected_at, status, cpu_percent, mem_percent, data)
			VALUES ($1,$2,$3,$4,$5,$6)`, id, h.Collected, status, h.CPUPercent, h.MemPercent, data); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE devices SET health_status = $2, issues = $3, issue_count = $4,
			pending_reboot = $5, uptime_sec = $6, updated_at = now() WHERE id = $1`,
			id, status, issues, len(h.Issues), h.PendingReboot, int64(h.UptimeSec))
		return err
	})
	return status, err
}

func (s *Store) SaveBattery(ctx context.Context, id uuid.UUID, b protocol.Battery) error {
	if !b.Present {
		_, err := s.Pool.Exec(ctx, `UPDATE devices SET battery_health = NULL WHERE id = $1`, id)
		return err
	}
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	var health *float64
	if b.HealthPercent > 0 {
		health = &b.HealthPercent
	}
	return s.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO battery_snapshots (device_id, collected_at, design_capacity, full_charge_capacity,
			cycle_count, health_percent, data) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			id, b.Collected, int64(b.DesignCapacity), int64(b.FullChargeCap), int(b.CycleCount), health, data); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE devices SET battery_health = $2 WHERE id = $1`, id, health)
		return err
	})
}

// ---- reads for the device detail page ----

type InventorySummary struct {
	Hardware            *protocol.Hardware `json:"hardware"`
	HardwareCollectedAt *time.Time         `json:"hardware_collected_at"`
	SoftwareCollectedAt *time.Time         `json:"software_collected_at"`
	SoftwareCount       int                `json:"software_count"`
}

func (s *Store) Inventory(ctx context.Context, id uuid.UUID) (*InventorySummary, error) {
	out := &InventorySummary{}
	var raw []byte
	var at time.Time
	err := s.Pool.QueryRow(ctx, `SELECT data, collected_at FROM device_inventory WHERE device_id = $1 AND kind = 'hardware'`, id).Scan(&raw, &at)
	if err == nil {
		var hw protocol.Hardware
		if json.Unmarshal(raw, &hw) == nil {
			out.Hardware = &hw
			out.HardwareCollectedAt = &at
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	err = s.Pool.QueryRow(ctx, `SELECT collected_at FROM device_inventory WHERE device_id = $1 AND kind = 'software'`, id).Scan(&at)
	if err == nil {
		out.SoftwareCollectedAt = &at
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM device_software WHERE device_id = $1`, id).Scan(&out.SoftwareCount); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) Software(ctx context.Context, id uuid.UUID, q string, p Page) ([]protocol.Software, int, error) {
	w := &whereBuilder{}
	w.add("device_id = ?", id)
	if q = strings.TrimSpace(q); q != "" {
		pat := likeEscape(strings.ToLower(q))
		w.add("(lower(name) LIKE ? OR lower(publisher) LIKE ?)", pat, pat)
	}
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM device_software`+w.sql(), w.args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit, offset := p.limitOffset()
	rows, err := s.Pool.Query(ctx, `SELECT name, version, publisher, install_date, arch, scope, uninstall_id FROM device_software`+
		w.sql()+fmt.Sprintf(` ORDER BY lower(name) LIMIT %d OFFSET %d`, limit, offset), w.args...)
	if err != nil {
		return nil, 0, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (protocol.Software, error) {
		var sw protocol.Software
		err := r.Scan(&sw.Name, &sw.Version, &sw.Publisher, &sw.InstallDate, &sw.Arch, &sw.Scope, &sw.UninstallID)
		return sw, err
	})
	return items, total, err
}

func (s *Store) InventoryChanges(ctx context.Context, id uuid.UUID, limit int) ([]InventoryChange, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, kind, change, item, old_value, new_value, at FROM inventory_changes
		WHERE device_id = $1 ORDER BY at DESC, id DESC LIMIT $2`, id, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (InventoryChange, error) {
		var c InventoryChange
		err := r.Scan(&c.ID, &c.Kind, &c.Change, &c.Item, &c.Old, &c.New, &c.At)
		return c, err
	})
}

type HealthPoint struct {
	CollectedAt time.Time `json:"collected_at"`
	CPUPercent  *float32  `json:"cpu_percent"`
	MemPercent  *float32  `json:"mem_percent"`
	Status      string    `json:"status"`
}

type HealthView struct {
	Latest  *protocol.Health `json:"latest"`
	History []HealthPoint    `json:"history"`
}

func (s *Store) HealthHistory(ctx context.Context, id uuid.UUID, days int) (*HealthView, error) {
	out := &HealthView{History: []HealthPoint{}}
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT data FROM health_snapshots WHERE device_id = $1 ORDER BY collected_at DESC LIMIT 1`, id).Scan(&raw)
	if err == nil {
		var h protocol.Health
		if json.Unmarshal(raw, &h) == nil {
			out.Latest = &h
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT collected_at, cpu_percent, mem_percent, status FROM health_snapshots
		WHERE device_id = $1 AND collected_at > now() - make_interval(days => $2) ORDER BY collected_at`, id, days)
	if err != nil {
		return nil, err
	}
	out.History, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (HealthPoint, error) {
		var p HealthPoint
		err := r.Scan(&p.CollectedAt, &p.CPUPercent, &p.MemPercent, &p.Status)
		return p, err
	})
	return out, err
}

type BatteryPoint struct {
	CollectedAt   time.Time `json:"collected_at"`
	HealthPercent *float32  `json:"health_percent"`
	FullCharge    *int64    `json:"full_charge_capacity_mwh"`
	Design        *int64    `json:"design_capacity_mwh"`
	CycleCount    *int      `json:"cycle_count"`
}

type BatteryView struct {
	Latest  *protocol.Battery `json:"latest"`
	History []BatteryPoint    `json:"history"`
}

func (s *Store) BatteryHistory(ctx context.Context, id uuid.UUID, days int) (*BatteryView, error) {
	out := &BatteryView{History: []BatteryPoint{}}
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT data FROM battery_snapshots WHERE device_id = $1 ORDER BY collected_at DESC LIMIT 1`, id).Scan(&raw)
	if err == nil {
		var b protocol.Battery
		if json.Unmarshal(raw, &b) == nil {
			out.Latest = &b
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT collected_at, health_percent, full_charge_capacity, design_capacity, cycle_count
		FROM battery_snapshots WHERE device_id = $1 AND collected_at > now() - make_interval(days => $2) ORDER BY collected_at`, id, days)
	if err != nil {
		return nil, err
	}
	out.History, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (BatteryPoint, error) {
		var p BatteryPoint
		err := r.Scan(&p.CollectedAt, &p.HealthPercent, &p.FullCharge, &p.Design, &p.CycleCount)
		return p, err
	})
	return out, err
}

// PurgeTelemetry deletes time-series rows older than the retention period.
func (s *Store) PurgeTelemetry(ctx context.Context, retentionDays int) error {
	for _, q := range []string{
		`DELETE FROM health_snapshots WHERE collected_at < now() - make_interval(days => $1)`,
		`DELETE FROM inventory_changes WHERE at < now() - make_interval(days => $1 * 4)`,
		`DELETE FROM battery_snapshots WHERE collected_at < now() - make_interval(days => $1 * 4)`,
	} {
		if _, err := s.Pool.Exec(ctx, q, retentionDays); err != nil {
			return err
		}
	}
	return nil
}

// EnrollmentTokenValid reports whether a token could still be used.
func (s *Store) EnrollmentTokenValid(ctx context.Context, tokenHash string) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM enrollment_tokens WHERE token_hash = $1 AND NOT revoked
		AND (expires_at IS NULL OR expires_at > now()) AND (max_uses IS NULL OR uses < max_uses))`, tokenHash).Scan(&ok)
	return ok, err
}
