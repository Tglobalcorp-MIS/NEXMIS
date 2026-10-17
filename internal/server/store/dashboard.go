package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type KeyCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type BatteryAlert struct {
	DeviceID      uuid.UUID `json:"device_id"`
	DeviceName    string    `json:"device_name"`
	HealthPercent float32   `json:"health_percent"`
}

type WarrantyAlert struct {
	DeviceID    uuid.UUID `json:"device_id"`
	DeviceName  string    `json:"device_name"`
	WarrantyEnd Date      `json:"warranty_end"`
}

type IssueCount struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Count    int    `json:"count"`
}

type Dashboard struct {
	Devices struct {
		Total        int `json:"total"`
		Online       int `json:"online"`
		Offline      int `json:"offline"`
		WithAgent    int `json:"with_agent"`
		WithoutAgent int `json:"without_agent"`
	} `json:"devices"`
	ByType      []KeyCount      `json:"by_type"`
	ByOSEdition []KeyCount      `json:"by_os_edition"`
	ByLifecycle []KeyCount      `json:"by_lifecycle"`
	Health      map[string]int  `json:"health"`
	Battery     []BatteryAlert  `json:"battery_alerts"`
	Warranty    []WarrantyAlert `json:"warranty_expiring"`
	TopIssues   []IssueCount    `json:"top_issues"`
	Recent      []Command       `json:"recent_commands"`
	UnreadChats int             `json:"unread_chats"`
}

func (s *Store) Dashboard(ctx context.Context, batteryThreshold float64, warrantyDays int) (*Dashboard, error) {
	d := &Dashboard{Health: map[string]int{"ok": 0, "warning": 0, "critical": 0, "unknown": 0}}
	active := ` WHERE lifecycle_state <> 'retired'`
	err := s.Pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE online), count(*) FILTER (WHERE NOT online AND agent_id IS NOT NULL),
		count(*) FILTER (WHERE agent_id IS NOT NULL), count(*) FILTER (WHERE agent_id IS NULL) FROM devices`+active).
		Scan(&d.Devices.Total, &d.Devices.Online, &d.Devices.Offline, &d.Devices.WithAgent, &d.Devices.WithoutAgent)
	if err != nil {
		return nil, err
	}
	keyCounts := func(q string) ([]KeyCount, error) {
		rows, err := s.Pool.Query(ctx, q)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (KeyCount, error) {
			var k KeyCount
			err := r.Scan(&k.Key, &k.Count)
			return k, err
		})
	}
	if d.ByType, err = keyCounts(`SELECT device_type, count(*) FROM devices` + active + ` GROUP BY 1 ORDER BY 2 DESC`); err != nil {
		return nil, err
	}
	if d.ByOSEdition, err = keyCounts(`SELECT os_name, count(*) FROM devices` + active +
		` AND os_name <> '' GROUP BY 1 ORDER BY 2 DESC LIMIT 12`); err != nil {
		return nil, err
	}
	if d.ByLifecycle, err = keyCounts(`SELECT lifecycle_state, count(*) FROM devices GROUP BY 1 ORDER BY 2 DESC`); err != nil {
		return nil, err
	}
	hc, err := keyCounts(`SELECT health_status, count(*) FROM devices` + active + ` AND agent_id IS NOT NULL GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	for _, k := range hc {
		d.Health[k.Key] = k.Count
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, name, battery_health FROM devices`+active+
		` AND battery_health IS NOT NULL AND battery_health < $1 ORDER BY battery_health LIMIT 20`, batteryThreshold)
	if err != nil {
		return nil, err
	}
	if d.Battery, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (BatteryAlert, error) {
		var b BatteryAlert
		err := r.Scan(&b.DeviceID, &b.DeviceName, &b.HealthPercent)
		return b, err
	}); err != nil {
		return nil, err
	}
	rows, err = s.Pool.Query(ctx, `SELECT id, name, warranty_end FROM devices`+active+
		` AND warranty_end IS NOT NULL AND warranty_end <= current_date + $1::int`+
		// Upcoming expirations first (soonest first), then already expired
		// (most recent first), so old expirations cannot crowd them out.
		` ORDER BY warranty_end < current_date, abs(warranty_end - current_date) LIMIT 20`, warrantyDays)
	if err != nil {
		return nil, err
	}
	if d.Warranty, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (WarrantyAlert, error) {
		var w WarrantyAlert
		err := r.Scan(&w.DeviceID, &w.DeviceName, &w.WarrantyEnd.Time)
		return w, err
	}); err != nil {
		return nil, err
	}
	rows, err = s.Pool.Query(ctx, `SELECT i->>'code', (array_agg(i->>'severity' ORDER BY CASE i->>'severity' WHEN 'critical' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END))[1], count(*) FROM devices, jsonb_array_elements(issues) i`+
		active+` GROUP BY 1 ORDER BY 3 DESC LIMIT 10`)
	if err != nil {
		return nil, err
	}
	if d.TopIssues, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (IssueCount, error) {
		var i IssueCount
		err := r.Scan(&i.Code, &i.Severity, &i.Count)
		return i, err
	}); err != nil {
		return nil, err
	}
	if d.Recent, _, err = s.ListCommands(ctx, CommandFilter{}, Page{PageSize: 10}); err != nil {
		return nil, err
	}
	if d.UnreadChats, err = s.UnreadChatCount(ctx); err != nil {
		return nil, err
	}
	return d, nil
}
