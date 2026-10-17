package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

// Device lifecycle states.
var LifecycleStates = []string{"procurement", "in_stock", "in_use", "repair", "idle", "retired"}

// Device types.
var DeviceTypes = []string{"windows", "server", "printer", "network", "storage", "other", "unknown"}

// Date is a calendar date serialised as YYYY-MM-DD.
type Date struct{ time.Time }

func (d Date) MarshalJSON() ([]byte, error) { return json.Marshal(d.Format("2006-01-02")) }

func (d *Date) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == "" {
		d.Time = time.Time{}
		return nil
	}
	t, err := time.Parse("2006-01-02", s[:min(len(s), 10)])
	if err != nil {
		return fmt.Errorf("invalid date %q", s)
	}
	d.Time = t
	return nil
}

type Device struct {
	ID            uuid.UUID        `json:"id"`
	Name          string           `json:"name"`
	Hostname      string           `json:"hostname"`
	Domain        string           `json:"domain"`
	DeviceType    string           `json:"device_type"`
	Sources       []string         `json:"sources"`
	HasAgent      bool             `json:"has_agent"`
	Online        bool             `json:"online"`
	LastSeenAt    *time.Time       `json:"last_seen_at"`
	OSName        string           `json:"os_name"`
	OSVersion     string           `json:"os_version"`
	OSEdition     string           `json:"os_edition"`
	Manufacturer  string           `json:"manufacturer"`
	Model         string           `json:"model"`
	SerialNumber  string           `json:"serial_number"`
	ChassisType   string           `json:"chassis_type"`
	IPAddresses   []string         `json:"ip_addresses"`
	MACAddresses  []string         `json:"mac_addresses"`
	LoggedInUser  string           `json:"logged_in_user"`
	GroupID       *uuid.UUID       `json:"group_id"`
	GroupName     *string          `json:"group_name"`
	Tags          []string         `json:"tags"`
	Lifecycle     string           `json:"lifecycle_state"`
	HealthStatus  string           `json:"health_status"`
	IssueCount    int              `json:"issue_count"`
	CPUPercent    *float32         `json:"cpu_percent"`
	MemPercent    *float32         `json:"mem_percent"`
	BatteryHealth *float32         `json:"battery_health"`
	AgentVersion  string           `json:"agent_version"`
	AssetTag      string           `json:"asset_tag"`
	OwnerName     string           `json:"owner_name"`
	Department    string           `json:"department"`
	Location      string           `json:"location"`
	PurchaseDate  *Date            `json:"purchase_date"`
	WarrantyEnd   *Date            `json:"warranty_end"`
	Notes         string           `json:"notes"`
	ADDN          string           `json:"ad_dn"`
	ADEnabled     *bool            `json:"ad_enabled"`
	ADLastLogon   *time.Time       `json:"ad_last_logon"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
	Issues        []protocol.Issue `json:"issues"`
	UptimeSec     *int64           `json:"uptime_sec"`
	PendingReboot bool             `json:"pending_reboot"`
	HelperOnline  bool             `json:"helper_online"`
	SessionState  string           `json:"session_state"`
	ConsentPolicy string           `json:"consent_policy"`
	EnrolledAt    *time.Time       `json:"enrolled_at"`
	FirstSeenAt   time.Time        `json:"first_seen_at"`

	AgentID        *uuid.UUID `json:"-"`
	AgentPublicKey []byte     `json:"-"`
	MachineID      *string    `json:"-"`
}

const deviceCols = `d.id, d.name, d.hostname, d.domain, d.device_type, d.sources, d.agent_id IS NOT NULL, d.online,
	d.last_seen_at, d.os_name, d.os_version, d.os_edition, d.manufacturer, d.model, d.serial_number, d.chassis_type,
	d.ip_addresses, d.mac_addresses, d.logged_in_user, d.group_id, g.name, d.tags, d.lifecycle_state, d.health_status,
	d.issue_count, d.cpu_percent, d.mem_percent, d.battery_health, d.agent_version, d.asset_tag, d.owner_name,
	d.department, d.location, d.purchase_date, d.warranty_end, d.notes, d.ad_dn, d.ad_enabled, d.ad_last_logon,
	d.created_at, d.updated_at, d.issues, d.uptime_sec, d.pending_reboot, d.helper_online, d.session_state,
	COALESCE(g.consent_policy, ''), d.enrolled_at, d.first_seen_at, d.agent_id, d.agent_public_key, d.machine_id`

const deviceFrom = ` FROM devices d LEFT JOIN device_groups g ON g.id = d.group_id`

func scanDevice(row pgx.Row) (*Device, error) {
	var d Device
	var purchase, warranty *time.Time
	var issues []byte
	err := row.Scan(&d.ID, &d.Name, &d.Hostname, &d.Domain, &d.DeviceType, &d.Sources, &d.HasAgent, &d.Online,
		&d.LastSeenAt, &d.OSName, &d.OSVersion, &d.OSEdition, &d.Manufacturer, &d.Model, &d.SerialNumber, &d.ChassisType,
		&d.IPAddresses, &d.MACAddresses, &d.LoggedInUser, &d.GroupID, &d.GroupName, &d.Tags, &d.Lifecycle, &d.HealthStatus,
		&d.IssueCount, &d.CPUPercent, &d.MemPercent, &d.BatteryHealth, &d.AgentVersion, &d.AssetTag, &d.OwnerName,
		&d.Department, &d.Location, &purchase, &warranty, &d.Notes, &d.ADDN, &d.ADEnabled, &d.ADLastLogon,
		&d.CreatedAt, &d.UpdatedAt, &issues, &d.UptimeSec, &d.PendingReboot, &d.HelperOnline, &d.SessionState,
		&d.ConsentPolicy, &d.EnrolledAt, &d.FirstSeenAt, &d.AgentID, &d.AgentPublicKey, &d.MachineID)
	if err != nil {
		return nil, mapErr(err)
	}
	if purchase != nil {
		d.PurchaseDate = &Date{*purchase}
	}
	if warranty != nil {
		d.WarrantyEnd = &Date{*warranty}
	}
	d.Issues = []protocol.Issue{}
	if len(issues) > 0 {
		_ = json.Unmarshal(issues, &d.Issues)
	}
	return &d, nil
}

// DeviceFilter holds list query options.
type DeviceFilter struct {
	Q         string
	Status    string // online | offline
	Type      string
	Lifecycle string
	Health    string
	GroupID   *uuid.UUID
	Tag       string
	HasAgent  *bool
	Sort      string
	IDs       []uuid.UUID
}

var deviceSorts = map[string]string{
	"name":       "lower(d.name)",
	"last_seen":  "d.last_seen_at NULLS FIRST",
	"-last_seen": "d.last_seen_at DESC NULLS LAST",
	"health":     "CASE d.health_status WHEN 'critical' THEN 0 WHEN 'warning' THEN 1 WHEN 'ok' THEN 2 ELSE 3 END, lower(d.name)",
	"battery":    "d.battery_health NULLS LAST",
	"type":       "d.device_type, lower(d.name)",
	"-created":   "d.created_at DESC",
}

func (f DeviceFilter) where() *whereBuilder {
	w := &whereBuilder{}
	if q := strings.TrimSpace(f.Q); q != "" {
		p := likeEscape(strings.ToLower(q))
		w.add(`(lower(d.name) LIKE ? OR lower(d.hostname) LIKE ? OR lower(d.serial_number) LIKE ?
			OR lower(d.logged_in_user) LIKE ? OR lower(d.asset_tag) LIKE ? OR lower(d.owner_name) LIKE ?
			OR lower(d.model) LIKE ? OR EXISTS (SELECT 1 FROM unnest(d.ip_addresses || d.mac_addresses) a WHERE lower(a) LIKE ?))`,
			p, p, p, p, p, p, p, p)
	}
	switch f.Status {
	case "online":
		w.add("d.online")
	case "offline":
		w.add("NOT d.online")
	}
	if f.Type != "" {
		w.add("d.device_type = ?", f.Type)
	}
	if f.Lifecycle != "" {
		w.add("d.lifecycle_state = ?", f.Lifecycle)
	}
	if f.Health != "" {
		w.add("d.health_status = ?", f.Health)
	}
	if f.GroupID != nil {
		w.add("d.group_id = ?", *f.GroupID)
	}
	if f.Tag != "" {
		w.add("? = ANY(d.tags)", f.Tag)
	}
	if f.HasAgent != nil {
		if *f.HasAgent {
			w.add("d.agent_id IS NOT NULL")
		} else {
			w.add("d.agent_id IS NULL")
		}
	}
	if f.IDs != nil {
		w.add("d.id = ANY(?)", f.IDs)
	}
	return w
}

func (s *Store) ListDevices(ctx context.Context, f DeviceFilter, p Page) ([]Device, int, error) {
	w := f.where()
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM devices d`+w.sql(), w.args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order, ok := deviceSorts[f.Sort]
	if !ok {
		order = "lower(d.name)"
	}
	limit, offset := p.limitOffset()
	rows, err := s.Pool.Query(ctx, `SELECT `+deviceCols+deviceFrom+w.sql()+` ORDER BY `+order+
		fmt.Sprintf(` LIMIT %d OFFSET %d`, limit, offset), w.args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *d)
	}
	return out, total, rows.Err()
}

func (s *Store) GetDevice(ctx context.Context, id uuid.UUID) (*Device, error) {
	return scanDevice(s.Pool.QueryRow(ctx, `SELECT `+deviceCols+deviceFrom+` WHERE d.id = $1`, id))
}

func (s *Store) DeviceByAgentID(ctx context.Context, agentID uuid.UUID) (*Device, error) {
	return scanDevice(s.Pool.QueryRow(ctx, `SELECT `+deviceCols+deviceFrom+` WHERE d.agent_id = $1`, agentID))
}

// NewManualDevice is the input for registering a non-agent device.
type NewManualDevice struct {
	Name         string   `json:"name"`
	DeviceType   string   `json:"device_type"`
	Hostname     string   `json:"hostname"`
	IPAddresses  []string `json:"ip_addresses"`
	MACAddresses []string `json:"mac_addresses"`
	Manufacturer string   `json:"manufacturer"`
	Model        string   `json:"model"`
	SerialNumber string   `json:"serial_number"`
	Source       string   `json:"-"`
}

func (s *Store) CreateManualDevice(ctx context.Context, in NewManualDevice) (uuid.UUID, error) {
	if in.Source == "" {
		in.Source = "manual"
	}
	if in.DeviceType == "" {
		in.DeviceType = "unknown"
	}
	var id uuid.UUID
	err := s.Pool.QueryRow(ctx, `INSERT INTO devices (name, hostname, device_type, sources, ip_addresses, mac_addresses,
		manufacturer, model, serial_number) VALUES ($1,$2,$3,ARRAY[$4],$5,$6,$7,$8,$9) RETURNING id`,
		in.Name, in.Hostname, in.DeviceType, in.Source, nonNil(in.IPAddresses), nonNil(normalizeMACs(in.MACAddresses)),
		in.Manufacturer, in.Model, in.SerialNumber).Scan(&id)
	return id, mapErr(err)
}

// DevicePatch carries optional device updates.
type DevicePatch struct {
	Name         *string   `json:"name"`
	DeviceType   *string   `json:"device_type"`
	GroupID      *string   `json:"group_id"` // "" clears
	Tags         *[]string `json:"tags"`
	AssetTag     *string   `json:"asset_tag"`
	OwnerName    *string   `json:"owner_name"`
	Department   *string   `json:"department"`
	Location     *string   `json:"location"`
	PurchaseDate *NullDate `json:"purchase_date"`
	WarrantyEnd  *NullDate `json:"warranty_end"`
	Notes        *string   `json:"notes"`
}

// NullDate distinguishes "absent" (nil pointer) from "null" (Valid=false).
type NullDate struct {
	Valid bool
	Time  time.Time
}

func (n *NullDate) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*n = NullDate{}
		return nil
	}
	var d Date
	if err := d.UnmarshalJSON(b); err != nil {
		return err
	}
	*n = NullDate{Valid: !d.IsZero(), Time: d.Time}
	return nil
}

func (s *Store) UpdateDevice(ctx context.Context, id uuid.UUID, p DevicePatch) error {
	sets := []string{"updated_at = now()"}
	args := []any{id}
	set := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if p.Name != nil {
		set("name", *p.Name)
	}
	if p.DeviceType != nil {
		set("device_type", *p.DeviceType)
	}
	if p.GroupID != nil {
		if *p.GroupID == "" {
			set("group_id", nil)
		} else {
			gid, err := uuid.Parse(*p.GroupID)
			if err != nil {
				return fmt.Errorf("invalid group_id")
			}
			set("group_id", gid)
		}
	}
	if p.Tags != nil {
		set("tags", cleanTags(*p.Tags))
	}
	if p.AssetTag != nil {
		set("asset_tag", *p.AssetTag)
	}
	if p.OwnerName != nil {
		set("owner_name", *p.OwnerName)
	}
	if p.Department != nil {
		set("department", *p.Department)
	}
	if p.Location != nil {
		set("location", *p.Location)
	}
	if p.Notes != nil {
		set("notes", *p.Notes)
	}
	if p.PurchaseDate != nil {
		if p.PurchaseDate.Valid {
			set("purchase_date", p.PurchaseDate.Time)
		} else {
			set("purchase_date", nil)
		}
	}
	if p.WarrantyEnd != nil {
		if p.WarrantyEnd.Valid {
			set("warranty_end", p.WarrantyEnd.Time)
		} else {
			set("warranty_end", nil)
		}
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE devices SET `+strings.Join(sets, ", ")+` WHERE id = $1`, args...)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AddTags / SetGroup support bulk actions from the device list.
func (s *Store) AddTags(ctx context.Context, ids []uuid.UUID, tags []string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE devices SET tags = ARRAY(SELECT DISTINCT unnest(tags || $2::text[])), updated_at = now()
		WHERE id = ANY($1)`, ids, cleanTags(tags))
	return err
}

func (s *Store) SetGroup(ctx context.Context, ids []uuid.UUID, groupID *uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `UPDATE devices SET group_id = $2, updated_at = now() WHERE id = ANY($1)`, ids, groupID)
	return err
}

func (s *Store) DeleteDevice(ctx context.Context, id uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM devices WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- lifecycle ----

type LifecycleEvent struct {
	ID        int64     `json:"id"`
	FromState string    `json:"from_state"`
	ToState   string    `json:"to_state"`
	Reason    string    `json:"reason"`
	ActorName string    `json:"actor_name"`
	At        time.Time `json:"at"`
}

func (s *Store) SetLifecycle(ctx context.Context, id uuid.UUID, state, reason, actor string) (string, error) {
	var from string
	err := s.InTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT lifecycle_state FROM devices WHERE id = $1 FOR UPDATE`, id).Scan(&from); err != nil {
			return mapErr(err)
		}
		if from == state {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE devices SET lifecycle_state = $2, updated_at = now() WHERE id = $1`, id, state); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO lifecycle_events (device_id, from_state, to_state, reason, actor_name)
			VALUES ($1,$2,$3,$4,$5)`, id, from, state, reason, actor)
		return err
	})
	return from, err
}

func (s *Store) LifecycleEvents(ctx context.Context, id uuid.UUID) ([]LifecycleEvent, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, from_state, to_state, reason, actor_name, at FROM lifecycle_events
		WHERE device_id = $1 ORDER BY at DESC LIMIT 200`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (LifecycleEvent, error) {
		var e LifecycleEvent
		err := r.Scan(&e.ID, &e.FromState, &e.ToState, &e.Reason, &e.ActorName, &e.At)
		return e, err
	})
}

// ---- groups ----

type Group struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	ConsentPolicy string    `json:"consent_policy"`
	DeviceCount   int       `json:"device_count"`
}

func (s *Store) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := s.Pool.Query(ctx, `SELECT g.id, g.name, g.description, g.consent_policy,
		(SELECT count(*) FROM devices d WHERE d.group_id = g.id) FROM device_groups g ORDER BY g.name`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Group, error) {
		var g Group
		err := r.Scan(&g.ID, &g.Name, &g.Description, &g.ConsentPolicy, &g.DeviceCount)
		return g, err
	})
}

func (s *Store) GetGroup(ctx context.Context, id uuid.UUID) (*Group, error) {
	var g Group
	err := s.Pool.QueryRow(ctx, `SELECT g.id, g.name, g.description, g.consent_policy,
		(SELECT count(*) FROM devices d WHERE d.group_id = g.id) FROM device_groups g WHERE g.id = $1`, id).
		Scan(&g.ID, &g.Name, &g.Description, &g.ConsentPolicy, &g.DeviceCount)
	if err != nil {
		return nil, mapErr(err)
	}
	return &g, nil
}

func (s *Store) CreateGroup(ctx context.Context, g *Group) error {
	if g.ConsentPolicy == "" {
		g.ConsentPolicy = protocol.ConsentAlwaysAsk
	}
	return mapErr(s.Pool.QueryRow(ctx, `INSERT INTO device_groups (name, description, consent_policy) VALUES ($1,$2,$3) RETURNING id`,
		g.Name, g.Description, g.ConsentPolicy).Scan(&g.ID))
}

type GroupPatch struct {
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	ConsentPolicy *string `json:"consent_policy"`
}

func (s *Store) UpdateGroup(ctx context.Context, id uuid.UUID, p GroupPatch) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE device_groups SET name = COALESCE($2, name),
		description = COALESCE($3, description), consent_policy = COALESCE($4, consent_policy) WHERE id = $1`,
		id, p.Name, p.Description, p.ConsentPolicy)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteGroup(ctx context.Context, id uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM device_groups WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- targets ----

// Target selects a set of devices.
type Target struct {
	All       bool        `json:"all"`
	DeviceIDs []uuid.UUID `json:"device_ids"`
	GroupIDs  []uuid.UUID `json:"group_ids"`
	Tags      []string    `json:"tags"`
}

func (t Target) Empty() bool {
	return !t.All && len(t.DeviceIDs) == 0 && len(t.GroupIDs) == 0 && len(t.Tags) == 0
}

// ResolveTarget returns the ids of matching devices that have an agent and
// are not retired.
func (s *Store) ResolveTarget(ctx context.Context, t Target) ([]uuid.UUID, error) {
	if t.Empty() {
		return nil, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT id FROM devices WHERE agent_id IS NOT NULL AND lifecycle_state <> 'retired'
		AND ($1 OR id = ANY($2) OR group_id = ANY($3) OR tags && $4::text[]) ORDER BY name`,
		t.All, nonNilUUIDs(t.DeviceIDs), nonNilUUIDs(t.GroupIDs), nonNil(t.Tags))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// ---- helpers ----

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nonNilUUIDs(s []uuid.UUID) []uuid.UUID {
	if s == nil {
		return []uuid.UUID{}
	}
	return s
}

func cleanTags(tags []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// NormalizeMAC converts a MAC address to lower-case colon form.
func NormalizeMAC(m string) string {
	m = strings.ToLower(strings.NewReplacer("-", "", ":", "", ".", "").Replace(strings.TrimSpace(m)))
	if len(m) != 12 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < 12; i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(m[i : i+2])
	}
	return b.String()
}

func normalizeMACs(in []string) []string {
	out := []string{}
	for _, m := range in {
		if n := NormalizeMAC(m); n != "" {
			out = append(out, n)
		}
	}
	return out
}
