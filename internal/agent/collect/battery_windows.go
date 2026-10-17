//go:build windows

package collect

import (
	"context"
	"math"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

// win32BatteryRuntimeUnknown is reported by Win32_Battery.EstimatedRunTime
// while on AC power or when no estimate is available.
const win32BatteryRuntimeUnknown = 71582788

func (c *windowsCollector) Battery(ctx context.Context) (*protocol.Battery, error) {
	b := &protocol.Battery{Collected: time.Now().UTC()}

	onBattery, pct, perr := powerStatus()
	c.warn("GetSystemPowerStatus", perr)
	if perr == nil {
		b.OnAC = !onBattery
		if pct >= 0 {
			b.Present = true
			b.ChargePercent = pct
		}
	}

	// root\WMI battery classes (one instance per battery, keyed by
	// InstanceName). Multiple batteries are aggregated.
	type wmiBat struct {
		design, full, cycles, remaining uint64
		online                          bool
	}
	bats := map[string]*wmiBat{}
	get := func(inst string) *wmiBat {
		if bats[inst] == nil {
			bats[inst] = &wmiBat{}
		}
		return bats[inst]
	}

	if rows, err := wmiQuery(ctx, nsWMI, "BatteryStaticData", "",
		[]string{"InstanceName", "DeviceName", "ManufactureName", "SerialNumber", "Chemistry", "DesignedCapacity"}); err == nil {
		for _, r := range rows {
			get(asString(r["InstanceName"])).design = asUint64(r["DesignedCapacity"])
			if b.Name == "" {
				b.Name = asString(r["DeviceName"])
				b.Manufacturer = asString(r["ManufactureName"])
				b.SerialNumber = asString(r["SerialNumber"])
				b.Chemistry = batteryChemistry(asUint64(r["Chemistry"]))
			}
		}
	} else {
		c.warn("BatteryStaticData", err) // also fails on machines without a battery
	}
	if rows, err := wmiQuery(ctx, nsWMI, "BatteryFullChargedCapacity", "",
		[]string{"InstanceName", "FullChargedCapacity"}); err == nil {
		for _, r := range rows {
			get(asString(r["InstanceName"])).full = asUint64(r["FullChargedCapacity"])
		}
	} else {
		c.log.Debug("BatteryFullChargedCapacity unavailable", "err", err)
	}
	if rows, err := wmiQuery(ctx, nsWMI, "BatteryCycleCount", "",
		[]string{"InstanceName", "CycleCount"}); err == nil {
		for _, r := range rows {
			get(asString(r["InstanceName"])).cycles = asUint64(r["CycleCount"])
		}
	} else {
		c.log.Debug("BatteryCycleCount unavailable", "err", err)
	}
	statusRows := 0
	if rows, err := wmiQuery(ctx, nsWMI, "BatteryStatus", "",
		[]string{"InstanceName", "RemainingCapacity", "PowerOnline"}); err == nil {
		statusRows = len(rows)
		for _, r := range rows {
			wb := get(asString(r["InstanceName"]))
			wb.remaining = asUint64(r["RemainingCapacity"])
			wb.online = asBool(r["PowerOnline"])
		}
	} else {
		c.log.Debug("BatteryStatus unavailable", "err", err)
	}

	var design, full, remaining, cycles uint64
	anyOnline := false
	for _, wb := range bats {
		design += wb.design
		full += wb.full
		remaining += wb.remaining
		if wb.cycles > cycles {
			cycles = wb.cycles
		}
		anyOnline = anyOnline || wb.online
	}
	if len(bats) > 0 && (design > 0 || full > 0 || statusRows > 0) {
		b.Present = true
	}
	b.DesignCapacity = clampU32(design)
	b.FullChargeCap = clampU32(full)
	b.CycleCount = clampU32(cycles)
	b.HealthPercent = batteryHealthPercent(b.FullChargeCap, b.DesignCapacity)
	if statusRows > 0 {
		b.OnAC = anyOnline
	}

	// Win32_Battery: charge % and runtime estimate.
	if rows, err := wmiQuery(ctx, nsCIMV2, "Win32_Battery", "",
		[]string{"Name", "EstimatedChargeRemaining", "EstimatedRunTime"}); err == nil && len(rows) > 0 {
		r := rows[0]
		b.Present = true
		if b.Name == "" {
			b.Name = asString(r["Name"])
		}
		if v, ok := r["EstimatedChargeRemaining"]; ok {
			if p := asInt(v); p >= 0 && p <= 100 {
				b.ChargePercent = p
			}
		}
		if v, ok := r["EstimatedRunTime"]; ok {
			if rt := asUint64(v); rt > 0 && rt < win32BatteryRuntimeUnknown && !b.OnAC {
				b.EstimatedRuntimeM = int(rt)
			}
		}
	} else if err != nil {
		c.warn("Win32_Battery", err)
	}

	if b.ChargePercent == 0 && remaining > 0 && full > 0 {
		b.ChargePercent = int(math.Min(100, math.Round(float64(remaining)/float64(full)*100)))
	}

	if !b.Present {
		b = &protocol.Battery{Collected: b.Collected, Present: false, OnAC: true}
	}

	c.batMu.Lock()
	c.lastBattery = b
	c.batMu.Unlock()
	return b, nil
}

func clampU32(v uint64) uint32 {
	if v > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(v)
}
