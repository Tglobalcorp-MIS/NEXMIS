//go:build !windows

package collect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseProcStatCPU(t *testing.T) {
	idle, total, err := parseProcStatCPU("cpu  100 5 50 800 20 3 2 0 7 0")
	if err != nil {
		t.Fatal(err)
	}
	if idle != 820 || total != 980 {
		t.Errorf("idle=%d total=%d", idle, total)
	}
	if _, _, err := parseProcStatCPU("cpu0 1 2 3 4"); err == nil {
		t.Error("expected error for per-cpu line")
	}
}

func TestParseProcPidStat(t *testing.T) {
	line := "1234 (my (weird) proc) S 1 1234 1234 0 -1 4194560 100 0 0 0 250 50 0 0 20 0 1 0 100 123456 300 18446744073709551615"
	p, ok := parseProcPidStat(line, 4096)
	if !ok {
		t.Fatal("parse failed")
	}
	if p.name != "my (weird) proc" || p.ticks != 300 || p.rss != 300*4096 {
		t.Errorf("got %+v", p)
	}
	if _, ok := parseProcPidStat("garbage", 4096); ok {
		t.Error("garbage parsed")
	}
}

func TestUnescapeMount(t *testing.T) {
	if got := unescapeMount(`/mnt/my\040disk`); got != "/mnt/my disk" {
		t.Errorf("got %q", got)
	}
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadBatteryFixture(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"class/power_supply/AC/type":                  "Mains",
		"class/power_supply/AC/online":                "0",
		"class/power_supply/BAT0/type":                "Battery",
		"class/power_supply/BAT0/present":             "1",
		"class/power_supply/BAT0/status":              "Discharging",
		"class/power_supply/BAT0/capacity":            "81",
		"class/power_supply/BAT0/energy_full_design":  "57000000",
		"class/power_supply/BAT0/energy_full":         "42000000",
		"class/power_supply/BAT0/energy_now":          "34000000",
		"class/power_supply/BAT0/power_now":           "8500000",
		"class/power_supply/BAT0/cycle_count":         "321",
		"class/power_supply/BAT0/manufacturer":        "SMP",
		"class/power_supply/BAT0/model_name":          "5B10W13930",
		"class/power_supply/BAT0/technology":          "Li-poly",
		"class/power_supply/hidpp_battery_0/type":     "Battery",
		"class/power_supply/hidpp_battery_0/scope":    "Device",
		"class/power_supply/hidpp_battery_0/capacity": "5",
	})
	old := sysRoot
	sysRoot = root
	defer func() { sysRoot = old }()

	b := readBattery()
	if !b.Present || b.OnAC {
		t.Fatalf("present=%v onAC=%v", b.Present, b.OnAC)
	}
	if b.DesignCapacity != 57000 || b.FullChargeCap != 42000 || b.HealthPercent != 73.7 {
		t.Errorf("capacity %+v", b)
	}
	if b.ChargePercent != 81 || b.CycleCount != 321 || b.EstimatedRuntimeM != 240 {
		t.Errorf("charge/cycles/runtime %+v", b)
	}
	if b.Manufacturer != "SMP" || b.Chemistry != "Li-poly" {
		t.Errorf("strings %+v", b)
	}
}

func TestReadBatteryChargeBased(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"class/power_supply/ADP1/type":               "Mains",
		"class/power_supply/ADP1/online":             "1",
		"class/power_supply/BAT1/type":               "Battery",
		"class/power_supply/BAT1/charge_full_design": "5000000",
		"class/power_supply/BAT1/charge_full":        "2400000",
		"class/power_supply/BAT1/voltage_min_design": "11400000",
		"class/power_supply/BAT1/capacity":           "50",
	})
	old := sysRoot
	sysRoot = root
	defer func() { sysRoot = old }()
	b := readBattery()
	if !b.Present || !b.OnAC {
		t.Fatalf("%+v", b)
	}
	if b.DesignCapacity != 57000 || b.FullChargeCap != 27360 || b.HealthPercent != 48 {
		t.Errorf("%+v", b)
	}
}

func TestReadBatteryNone(t *testing.T) {
	old := sysRoot
	sysRoot = t.TempDir()
	defer func() { sysRoot = old }()
	b := readBattery()
	if b.Present || !b.OnAC {
		t.Errorf("%+v", b)
	}
}
