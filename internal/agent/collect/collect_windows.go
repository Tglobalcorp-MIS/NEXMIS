//go:build windows

package collect

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

// windowsCollector is the production Collector. Expensive sources (WMI,
// event log) are only touched by Hardware/Health/Battery; Heartbeat uses
// plain Win32 calls only.
type windowsCollector struct {
	log *slog.Logger

	cpuMu       sync.Mutex
	cpuLastIdle uint64
	cpuLastTot  uint64
	cpuLastAt   time.Time

	// lastBattery is the most recent Battery() result, reused by Health()
	// so that battery issues are reported without re-querying WMI.
	batMu       sync.Mutex
	lastBattery *protocol.Battery
}

// New returns the Windows collector.
func New() Collector {
	return &windowsCollector{log: slog.Default().With("component", "collect")}
}

// warn records a failed data source; the corresponding field stays empty.
func (c *windowsCollector) warn(source string, err error) {
	if err == nil {
		return
	}
	c.log.Warn("collect source failed", "source", source, "err", err)
}

// ---- Identity ----

func (c *windowsCollector) Identity(ctx context.Context) (Identity, error) {
	var id Identity
	id.MachineID = strings.ToLower(regString(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`, "MachineGuid"))
	id.Hostname = computerName()

	rows, err := wmiQuery(ctx, nsCIMV2, "Win32_ComputerSystem", "", []string{"Domain", "PartOfDomain"})
	if err == nil && len(rows) > 0 {
		if asBool(rows[0]["PartOfDomain"]) {
			id.Domain = asString(rows[0]["Domain"])
		}
	} else {
		c.warn("Win32_ComputerSystem", err)
		id.Domain = dnsDomain()
	}

	ov := readOSVersion()
	id.OS = ov.Name
	id.OSVersion = ov.KernelVersion()

	if id.MachineID == "" && id.Hostname == "" {
		return id, errors.New("collect: unable to determine machine identity")
	}
	return id, nil
}

func computerName() string {
	n := uint32(256)
	buf := make([]uint16, n)
	if err := windows.GetComputerNameEx(windows.ComputerNameDnsHostname, &buf[0], &n); err == nil && n > 0 {
		return windows.UTF16ToString(buf[:n])
	}
	h, _ := os.Hostname()
	return h
}

// dnsDomain returns the AD DNS domain when joined ("" otherwise).
func dnsDomain() string {
	n := uint32(256)
	buf := make([]uint16, n)
	if err := windows.GetComputerNameEx(windows.ComputerNameDnsDomain, &buf[0], &n); err == nil && n > 0 {
		return windows.UTF16ToString(buf[:n])
	}
	return ""
}

// ---- CPU sampling ----

// cpuPercent returns system CPU usage averaged since the previous call. On
// the first call (or when the last sample is too fresh) it takes a short
// 250 ms sample.
func (c *windowsCollector) cpuPercent(ctx context.Context) (float64, error) {
	c.cpuMu.Lock()
	defer c.cpuMu.Unlock()
	idle, tot, err := systemTimes()
	if err != nil {
		return 0, err
	}
	now := time.Now()
	if c.cpuLastAt.IsZero() || now.Sub(c.cpuLastAt) < 100*time.Millisecond || tot <= c.cpuLastTot {
		c.cpuLastIdle, c.cpuLastTot = idle, tot
		select {
		case <-time.After(250 * time.Millisecond):
		case <-ctx.Done():
			return 0, ctx.Err()
		}
		idle, tot, err = systemTimes()
		if err != nil {
			return 0, err
		}
		now = time.Now()
	}
	pct := cpuPercentFromTimes(idle-c.cpuLastIdle, tot-c.cpuLastTot)
	c.cpuLastIdle, c.cpuLastTot, c.cpuLastAt = idle, tot, now
	return pct, nil
}

// ---- Heartbeat ----

func (c *windowsCollector) Heartbeat(ctx context.Context) (protocol.Heartbeat, error) {
	hb := protocol.Heartbeat{Time: time.Now().UTC(), BatteryPct: -1}
	hb.UptimeSec = uptimeSeconds()

	cpu, err := c.cpuPercent(ctx)
	c.warn("GetSystemTimes", err)
	hb.CPUPercent = cpu

	if total, avail, err := globalMemory(); err == nil {
		hb.MemTotal = total
		hb.MemUsed = total - avail
	} else {
		c.warn("GlobalMemoryStatusEx", err)
	}

	hb.LoggedInUser, hb.SessionState = consoleUserState()

	if ads, err := adapters(); err == nil {
		seen := map[string]bool{}
		for _, a := range ads {
			if !a.Up || a.IsLoopback {
				continue
			}
			for _, ip := range a.IPv4 {
				if !seen[ip] {
					seen[ip] = true
					hb.IPAddresses = append(hb.IPAddresses, ip)
				}
			}
		}
		sort.Strings(hb.IPAddresses)
	} else {
		c.warn("GetAdaptersAddresses", err)
	}

	if onBatt, pct, err := powerStatus(); err == nil {
		hb.OnBattery, hb.BatteryPct = onBatt, pct
	} else {
		c.warn("GetSystemPowerStatus", err)
	}
	return hb, nil
}

// ---- helpers shared by the Windows files ----

func boolPtr(b bool) *bool { return &b }
func intPtr(i int) *int    { return &i }
