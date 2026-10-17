package collect

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// These tests call the real collector of the host platform (Linux in CI,
// Windows on a developer machine) and check that every method returns
// plausible data without panicking.

func TestCollectorIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id, err := New().Identity(ctx)
	if err != nil {
		t.Fatalf("Identity: %v", err)
	}
	if id.MachineID == "" || id.Hostname == "" || id.OS == "" {
		t.Errorf("incomplete identity: %+v", id)
	}
	t.Logf("identity: %+v", id)
}

func TestCollectorHeartbeat(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := New()
	for i := 0; i < 2; i++ { // second call uses the stored CPU sample
		hb, err := c.Heartbeat(ctx)
		if err != nil {
			t.Fatalf("Heartbeat: %v", err)
		}
		if hb.Time.IsZero() {
			t.Error("zero time")
		}
		if hb.CPUPercent < 0 || hb.CPUPercent > 100 {
			t.Errorf("cpu %v out of range", hb.CPUPercent)
		}
		if hb.MemTotal == 0 || hb.MemUsed > hb.MemTotal {
			t.Errorf("mem used %d total %d", hb.MemUsed, hb.MemTotal)
		}
		if hb.UptimeSec == 0 {
			t.Error("uptime 0")
		}
		if hb.BatteryPct < -1 || hb.BatteryPct > 100 {
			t.Errorf("battery pct %d", hb.BatteryPct)
		}
		switch hb.SessionState {
		case "", "active", "locked", "none":
		default:
			t.Errorf("session state %q", hb.SessionState)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func TestCollectorHardware(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	hw, err := New().Hardware(ctx)
	if err != nil {
		t.Fatalf("Hardware: %v", err)
	}
	if hw == nil {
		t.Fatal("nil hardware")
	}
	if hw.OSName == "" || hw.MemoryTotal == 0 {
		t.Errorf("missing OS/memory: %q %d", hw.OSName, hw.MemoryTotal)
	}
	switch hw.ChassisType {
	case "desktop", "laptop", "server", "vm", "other":
	default:
		t.Errorf("chassis %q", hw.ChassisType)
	}
	for _, v := range hw.Volumes {
		if v.Free > v.Size {
			t.Errorf("volume %s free %d > size %d", v.Mount, v.Free, v.Size)
		}
	}
	if _, err := json.Marshal(hw); err != nil {
		t.Errorf("marshal: %v", err)
	}
	t.Logf("hardware: os=%q arch=%s chassis=%s cpus=%d disks=%d vols=%d nics=%d",
		hw.OSName, hw.OSArch, hw.ChassisType, len(hw.CPUs), len(hw.Disks), len(hw.Volumes), len(hw.NICs))
}

func TestCollectorSoftware(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	sw, err := New().Software(ctx)
	if err != nil {
		t.Fatalf("Software: %v", err)
	}
	if sw == nil {
		t.Fatal("Software must return a non-nil slice")
	}
	seen := map[string]bool{}
	for _, s := range sw {
		if s.Name == "" {
			t.Errorf("empty name: %+v", s)
		}
		k := s.Name + "|" + s.Version + "|" + s.Arch
		if seen[k] {
			t.Errorf("duplicate %q", k)
		}
		seen[k] = true
	}
}

func TestCollectorHealthAndBattery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := New()
	b, err := c.Battery(ctx)
	if err != nil {
		t.Fatalf("Battery: %v", err)
	}
	if b == nil || b.Collected.IsZero() {
		t.Fatalf("bad battery %+v", b)
	}
	if !b.Present && (b.DesignCapacity != 0 || b.HealthPercent != 0) {
		t.Errorf("absent battery carries data: %+v", b)
	}
	if b.HealthPercent < 0 || b.ChargePercent < 0 || b.ChargePercent > 100 {
		t.Errorf("battery values out of range: %+v", b)
	}

	start := time.Now()
	h, err := c.Health(ctx)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if h == nil || h.Collected.IsZero() {
		t.Fatal("bad health")
	}
	if h.MemPercent <= 0 || h.MemPercent > 100 {
		t.Errorf("mem%% %v", h.MemPercent)
	}
	if h.CPUPercent < 0 || h.CPUPercent > 100 {
		t.Errorf("cpu%% %v", h.CPUPercent)
	}
	if h.LastBoot.After(h.Collected) {
		t.Error("last boot in the future")
	}
	if len(h.TopProcesses) > 5 {
		t.Errorf("%d top processes", len(h.TopProcesses))
	}
	for _, p := range h.TopProcesses {
		if p.CPU < 0 || p.CPU > 100.5 {
			t.Errorf("process %s cpu %v", p.Name, p.CPU)
		}
	}
	for _, is := range h.Issues {
		if is.Code == "" || is.Message == "" {
			t.Errorf("bad issue %+v", is)
		}
	}
	t.Logf("health took %v: cpu=%.1f mem=%.1f vols=%d procs=%d issues=%d",
		time.Since(start), h.CPUPercent, h.MemPercent, len(h.Volumes), len(h.TopProcesses), len(h.Issues))
}

func TestCollectorConcurrent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := New()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Heartbeat(ctx); err != nil {
				t.Errorf("Heartbeat: %v", err)
			}
			if _, err := c.Identity(ctx); err != nil {
				t.Errorf("Identity: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestCollectorCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := New()
	// Must return promptly and not panic.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = c.Heartbeat(ctx)
		_, _ = c.Health(ctx)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("collector ignored canceled context")
	}
}
