package collect

import (
	"strings"
	"testing"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

func bp(b bool) *bool { return &b }
func ip(i int) *int   { return &i }

func TestEvaluateHealth(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	day := uint64(24 * 3600)
	tp := func(d time.Duration) *time.Time { x := now.Add(-d); return &x }

	type want struct {
		code, sev string
		msg       string // substring, optional
	}
	tests := []struct {
		name string
		h    *protocol.Health
		b    *protocol.Battery
		want []want // exact set, in order
	}{
		{name: "nil inputs", want: nil},
		{name: "healthy", h: &protocol.Health{
			Collected: now, CPUPercent: 10, MemPercent: 40, UptimeSec: day,
			Volumes:           []protocol.Volume{{Mount: "C:", Size: 100 * gib, Free: 50 * gib}},
			DiskHealth:        []protocol.Disk{{Model: "SSD", Health: "Healthy"}},
			DefenderEnabled:   bp(true),
			DefenderRealtime:  bp(true),
			DefenderSigAgeDay: ip(1),
			FirewallEnabled:   bp(true),
			LastUpdateInstall: tp(10 * 24 * time.Hour),
			EventErrors24h:    3,
		}, b: &protocol.Battery{Present: true, HealthPercent: 95, CycleCount: 100}},
		{name: "disk low warning", h: &protocol.Health{Collected: now,
			Volumes: []protocol.Volume{{Mount: "C:", Size: 200 * gib, Free: gib * 124 / 10}}},
			want: []want{{IssueDiskLow, protocol.SevWarning, "C: 剩餘 6.2%（12.4 GB）"}}},
		{name: "disk low critical", h: &protocol.Health{Collected: now,
			Volumes: []protocol.Volume{{Mount: "D:", Size: 100 * gib, Free: 4 * gib}}},
			want: []want{{IssueDiskLow, protocol.SevCritical, "D: 剩餘 4.0%（4.0 GB）"}}},
		{name: "disk exactly 10 percent is fine", h: &protocol.Health{Collected: now,
			Volumes: []protocol.Volume{{Mount: "C:", Size: 100 * gib, Free: 10 * gib}, {Mount: "Z:", Size: 0, Free: 0}}}},
		{name: "disk unhealthy and warning", h: &protocol.Health{Collected: now,
			DiskHealth: []protocol.Disk{{Model: "WD Blue", Health: "Warning"}, {Model: "Samsung 980", Health: "Unhealthy"}, {Health: "Unknown"}}},
			want: []want{{IssueDiskUnhealthy, protocol.SevCritical, "Samsung 980"}, {IssueDiskUnhealthy, protocol.SevWarning, "WD Blue"}}},
		{name: "mem and cpu high", h: &protocol.Health{Collected: now, CPUPercent: 95.5, MemPercent: 91},
			want: []want{{IssueMemHigh, protocol.SevWarning, "91.0%"}, {IssueCPUHigh, protocol.SevWarning, "95.5%"}}},
		{name: "mem and cpu at threshold", h: &protocol.Health{Collected: now, CPUPercent: 90, MemPercent: 90}},
		{name: "pending reboot info", h: &protocol.Health{Collected: now, UptimeSec: 2 * day,
			PendingReboot: true, PendingRebootWhy: []string{"Windows Update", "Component Based Servicing"}},
			want: []want{{IssuePendingReboot, protocol.SevInfo, "Windows Update、Component Based Servicing"}}},
		{name: "pending reboot over 7 days", h: &protocol.Health{Collected: now, UptimeSec: 8 * day, PendingReboot: true},
			want: []want{{IssuePendingReboot, protocol.SevWarning, "8 天"}}},
		{name: "uptime long with pending reboot", h: &protocol.Health{Collected: now, UptimeSec: 31 * day, PendingReboot: true},
			want: []want{{IssuePendingReboot, protocol.SevWarning, ""}, {IssueUptimeLong, protocol.SevWarning, "31 天"}}},
		{name: "uptime 30 days exactly", h: &protocol.Health{Collected: now, UptimeSec: 30 * day}},
		{name: "defender off suppresses realtime and sig", h: &protocol.Health{Collected: now,
			DefenderEnabled: bp(false), DefenderRealtime: bp(false), DefenderSigAgeDay: ip(30)},
			want: []want{{IssueDefenderOff, protocol.SevCritical, ""}}},
		{name: "defender realtime off and sig old", h: &protocol.Health{Collected: now,
			DefenderEnabled: bp(true), DefenderRealtime: bp(false), DefenderSigAgeDay: ip(8)},
			want: []want{{IssueDefenderRealtimeOff, protocol.SevCritical, ""}, {IssueDefenderSigOld, protocol.SevWarning, "8 天"}}},
		{name: "defender unknown enabled but realtime off", h: &protocol.Health{Collected: now, DefenderRealtime: bp(false)},
			want: []want{{IssueDefenderRealtimeOff, protocol.SevCritical, ""}}},
		{name: "defender sig 7 days ok", h: &protocol.Health{Collected: now, DefenderEnabled: bp(true), DefenderSigAgeDay: ip(7)}},
		{name: "firewall off", h: &protocol.Health{Collected: now, FirewallEnabled: bp(false)},
			want: []want{{IssueFirewallOff, protocol.SevWarning, "防火牆"}}},
		{name: "firewall unknown", h: &protocol.Health{Collected: now}},
		{name: "updates stale", h: &protocol.Health{Collected: now, LastUpdateInstall: tp(46 * 24 * time.Hour)},
			want: []want{{IssueUpdatesStale, protocol.SevWarning, "46 天"}}},
		{name: "updates 44 days fine", h: &protocol.Health{Collected: now, LastUpdateInstall: tp(44 * 24 * time.Hour)}},
		{name: "event errors", h: &protocol.Health{Collected: now, EventErrors24h: 51},
			want: []want{{IssueEventErrors, protocol.SevWarning, "51"}}},
		{name: "event errors at threshold", h: &protocol.Health{Collected: now, EventErrors24h: 50}},
		{name: "battery degraded warning", b: &protocol.Battery{Present: true, HealthPercent: 65.4},
			want: []want{{IssueBatteryDegraded, protocol.SevWarning, "65.4%"}}},
		{name: "battery degraded critical with cycles", b: &protocol.Battery{Present: true, HealthPercent: 45, CycleCount: 900},
			want: []want{{IssueBatteryDegraded, protocol.SevCritical, "45.0%"}, {IssueBatteryCycles, protocol.SevInfo, "900"}}},
		{name: "battery unknown health", b: &protocol.Battery{Present: true, HealthPercent: 0, CycleCount: 800}},
		{name: "battery absent ignored", b: &protocol.Battery{Present: false, HealthPercent: 10, CycleCount: 2000}},
		{name: "ordering by severity", h: &protocol.Health{Collected: now, PendingReboot: true, UptimeSec: day,
			FirewallEnabled: bp(false), DefenderEnabled: bp(false)},
			want: []want{{IssueDefenderOff, protocol.SevCritical, ""}, {IssueFirewallOff, protocol.SevWarning, ""}, {IssuePendingReboot, protocol.SevInfo, ""}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateHealth(tt.h, tt.b)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d issues %+v, want %d %+v", len(got), got, len(tt.want), tt.want)
			}
			for i, w := range tt.want {
				g := got[i]
				if g.Code != w.code || g.Severity != w.sev {
					t.Errorf("issue %d = %s/%s, want %s/%s (%q)", i, g.Code, g.Severity, w.code, w.sev, g.Message)
				}
				if w.msg != "" && !strings.Contains(g.Message, w.msg) {
					t.Errorf("issue %d message %q does not contain %q", i, g.Message, w.msg)
				}
				if g.Message == "" {
					t.Errorf("issue %d has empty message", i)
				}
			}
		})
	}
}

func TestEvaluateHealthZeroCollectedUsesNow(t *testing.T) {
	old := time.Now().Add(-100 * 24 * time.Hour)
	got := EvaluateHealth(&protocol.Health{LastUpdateInstall: &old}, nil)
	if len(got) != 1 || got[0].Code != IssueUpdatesStale {
		t.Fatalf("got %+v", got)
	}
}
