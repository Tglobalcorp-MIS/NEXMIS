//go:build windows

package collect

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

// ignoredStoppedServices are auto-start services that routinely stop on
// their own (trigger-started, update helpers, on-demand) and would otherwise
// be reported as "automatic but not running" on almost every machine.
// Compared case-insensitively; a trailing "_" matches per-user services
// such as "OneSyncSvc_1a2b3c".
var ignoredStoppedServices = []string{
	"gupdate", "gupdatem", "googleupdaterservice", "googleupdaterinternalservice",
	"edgeupdate", "edgeupdatem", "microsoftedgeelevationservice", "MozillaMaintenance",
	"sppsvc", "RemoteRegistry", "MapsBroker", "TrustedInstaller", "wuauserv",
	"WaaSMedicSvc", "UsoSvc", "BITS", "DoSvc", "TabletInputService", "CDPSvc",
	"ShellHWDetection", "sedsvc", "WbioSrvc", "tiledatamodelsvc", "wscsvc",
	"SysMain", "WSearch", "gpsvc", "AdobeARMservice", "AGSService", "AGMService",
	"ClickToRunSvc", "BrokerInfrastructure", "StateRepository", "Intel(R) TPM Provisioning Service",
	"stisvc", "WpnService", "camsvc", "NetTcpPortSharing", "IntelAudioService",
	"OneSyncSvc_", "CDPUserSvc_", "WpnUserService_", "cbdhsvc_", "PimIndexMaintenanceSvc_",
	"UnistoreSvc_", "UserDataSvc_", "MessagingService_", "BcastDVRUserService_",
	"DevicesFlowUserSvc_", "PrintWorkflowUserSvc_", "CaptureService_", "BluetoothUserService_",
}

func (c *windowsCollector) Health(ctx context.Context) (*protocol.Health, error) {
	h := &protocol.Health{Collected: time.Now().UTC()}

	if pct, err := c.cpuPercent(ctx); err == nil {
		h.CPUPercent = pct
	} else {
		c.warn("GetSystemTimes", err)
	}
	if total, avail, err := globalMemory(); err == nil && total > 0 {
		h.MemPercent = round1(float64(total-avail) / float64(total) * 100)
	} else {
		c.warn("GlobalMemoryStatusEx", err)
	}
	h.UptimeSec = uptimeSeconds()
	h.LastBoot = h.Collected.Add(-time.Duration(h.UptimeSec) * time.Second).Truncate(time.Second)

	h.PendingRebootWhy = pendingRebootReasons()
	h.PendingReboot = len(h.PendingRebootWhy) > 0

	h.Volumes = fixedVolumes()
	h.DiskHealth = c.physicalDisks(ctx)

	c.defenderStatus(ctx, h)
	h.FirewallEnabled = firewallEnabled()
	h.LastUpdateInstall = c.lastUpdateInstall(ctx)

	h.EventErrors24h, h.TopEvents = c.recentEventErrors(ctx)

	if svcs, err := stoppedAutoServices(); err == nil {
		h.StoppedServices = svcs
	} else {
		c.warn("services", err)
	}

	if procs, err := topProcesses(ctx, 5, 500*time.Millisecond); err == nil {
		h.TopProcesses = procs
	} else {
		c.warn("processes", err)
	}

	c.batMu.Lock()
	bat := c.lastBattery
	c.batMu.Unlock()
	h.Issues = EvaluateHealth(h, bat)
	return h, nil
}

// ---- pending reboot ----

func pendingRebootReasons() []string {
	var why []string
	if regKeyExists(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending`) {
		why = append(why, "Component Based Servicing")
	}
	if regKeyExists(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired`) {
		why = append(why, "Windows Update")
	}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager`, registry.QUERY_VALUE|registry.WOW64_64KEY); err == nil {
		if v, _, err := k.GetStringsValue("PendingFileRenameOperations"); err == nil && hasNonEmpty(v) {
			why = append(why, "PendingFileRenameOperations")
		}
		k.Close()
	}
	active := regString(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\ComputerName\ActiveComputerName`, "ComputerName")
	pending := regString(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\ComputerName\ComputerName`, "ComputerName")
	if active != "" && pending != "" && !strings.EqualFold(active, pending) {
		why = append(why, "ComputerName change")
	}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\Netlogon`, registry.QUERY_VALUE|registry.WOW64_64KEY); err == nil {
		if _, _, err := k.GetValue("JoinDomain", nil); err == nil {
			why = append(why, "Domain join")
		}
		k.Close()
	}
	return why
}

func hasNonEmpty(v []string) bool {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return true
		}
	}
	return false
}

// ---- Defender ----

func (c *windowsCollector) defenderStatus(ctx context.Context, h *protocol.Health) {
	rows, err := wmiQueryAll(ctx, nsDefender, "MSFT_MpComputerStatus", "",
		[]string{"AntivirusEnabled", "RealTimeProtectionEnabled", "AntivirusSignatureAge", "AMRunningMode", "AMServiceEnabled"})
	if err != nil || len(rows) == 0 {
		c.warn("MSFT_MpComputerStatus", err)
		return
	}
	r := rows[0]
	// When a third-party antivirus is registered, Defender runs in passive
	// mode (or is disabled by design); reporting it as "off" would be a
	// false critical, so leave the fields unknown.
	mode := strings.ToLower(asString(r["AMRunningMode"]))
	if strings.Contains(mode, "passive") || strings.Contains(mode, "edr") || c.thirdPartyAVActive(ctx) {
		return
	}
	if v, ok := r["AntivirusEnabled"]; ok {
		h.DefenderEnabled = boolPtr(asBool(v))
	}
	if v, ok := r["RealTimeProtectionEnabled"]; ok {
		h.DefenderRealtime = boolPtr(asBool(v))
	}
	if v, ok := r["AntivirusSignatureAge"]; ok {
		age := asUint64(v)
		// 65535 / 4294967295 mean "never updated"/unknown.
		if age < 65535 {
			h.DefenderSigAgeDay = intPtr(int(age))
		}
	}
}

// thirdPartyAVActive asks Security Center (client SKUs only) whether an
// enabled antivirus other than Defender is registered.
func (c *windowsCollector) thirdPartyAVActive(ctx context.Context) bool {
	rows, err := wmiQuery(ctx, nsSecCenter2, "AntiVirusProduct", "", []string{"displayName", "productState"})
	if err != nil {
		return false
	}
	for _, r := range rows {
		name := strings.ToLower(asString(r["displayName"]))
		if strings.Contains(name, "windows defender") || strings.Contains(name, "microsoft defender") {
			continue
		}
		state := asUint64(r["productState"])
		if (state>>12)&0xF == 1 { // "on"
			return true
		}
	}
	return false
}

// ---- Firewall ----

// firewallEnabled is false if any applicable profile is disabled. Group
// Policy settings take precedence over local ones. The domain profile is
// only considered on domain-joined machines.
func firewallEnabled() *bool {
	if !serviceRunning("mpssvc") {
		return boolPtr(false)
	}
	const local = `SYSTEM\CurrentControlSet\Services\SharedAccess\Parameters\FirewallPolicy\`
	const policy = `SOFTWARE\Policies\Microsoft\WindowsFirewall\`
	type prof struct{ local, policy string }
	profiles := []prof{{"StandardProfile", "PrivateProfile"}, {"PublicProfile", "PublicProfile"}}
	if dnsDomain() != "" {
		profiles = append(profiles, prof{"DomainProfile", "DomainProfile"})
	}
	known := false
	for _, p := range profiles {
		v, ok := regInt(registry.LOCAL_MACHINE, policy+p.policy, "EnableFirewall")
		if !ok {
			v, ok = regInt(registry.LOCAL_MACHINE, local+p.local, "EnableFirewall")
		}
		if !ok {
			continue
		}
		known = true
		if v == 0 {
			return boolPtr(false)
		}
	}
	if !known {
		return nil
	}
	return boolPtr(true)
}

func serviceRunning(name string) bool {
	m, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return true // unknown: do not claim it is stopped
	}
	defer windows.CloseServiceHandle(m)
	s, err := windows.OpenService(m, utf16Ptr(name), windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return true
	}
	defer windows.CloseServiceHandle(s)
	var st windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(s, &st); err != nil {
		return true
	}
	return st.CurrentState == windows.SERVICE_RUNNING
}

// ---- Updates ----

func (c *windowsCollector) lastUpdateInstall(ctx context.Context) *time.Time {
	rows, err := wmiQuery(ctx, nsCIMV2, "Win32_QuickFixEngineering", "", []string{"HotFixID", "InstalledOn"})
	if err != nil {
		c.warn("Win32_QuickFixEngineering", err)
		return nil
	}
	var latest time.Time
	now := time.Now().Add(48 * time.Hour)
	for _, r := range rows {
		t, ok := parseQFEDate(asString(r["InstalledOn"]))
		if !ok || t.After(now) {
			continue
		}
		if t.After(latest) {
			latest = t
		}
	}
	if latest.IsZero() {
		return nil
	}
	return &latest
}

// ---- Services ----

// serviceTriggerInfo mirrors SERVICE_TRIGGER_INFO.
type serviceTriggerInfo struct {
	TriggersCount uint32
	Triggers      uintptr
	Reserved      uintptr
}

func stoppedAutoServices() ([]string, error) {
	m, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_ENUMERATE_SERVICE|windows.SC_MANAGER_CONNECT)
	if err != nil {
		return nil, err
	}
	defer windows.CloseServiceHandle(m)

	var needed, count, resume uint32
	var buf []byte
	size := uint32(64 * 1024)
	for i := 0; i < 5; i++ {
		buf = make([]byte, size)
		err = windows.EnumServicesStatusEx(m, windows.SC_ENUM_PROCESS_INFO, windows.SERVICE_WIN32,
			windows.SERVICE_INACTIVE, &buf[0], size, &needed, &count, &resume, nil)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.ERROR_MORE_DATA) {
			return nil, err
		}
		size = needed + 4096
		resume = 0
	}
	if err != nil {
		return nil, err
	}
	entries := unsafe.Slice((*windows.ENUM_SERVICE_STATUS_PROCESS)(unsafe.Pointer(&buf[0])), count)
	var out []string
	cfgBuf := make([]byte, 8192)
	for _, e := range entries {
		name := windows.UTF16PtrToString(e.ServiceName)
		if e.ServiceStatusProcess.CurrentState != windows.SERVICE_STOPPED || isIgnoredService(name) {
			continue
		}
		s, err := windows.OpenService(m, e.ServiceName, windows.SERVICE_QUERY_CONFIG)
		if err != nil {
			continue
		}
		auto := false
		var n uint32
		cfg := (*windows.QUERY_SERVICE_CONFIG)(unsafe.Pointer(&cfgBuf[0]))
		if err := windows.QueryServiceConfig(s, cfg, uint32(len(cfgBuf)), &n); err == nil {
			auto = cfg.StartType == windows.SERVICE_AUTO_START
		}
		if auto {
			// Delayed-start and trigger-start services stop by design.
			var delayed windows.SERVICE_DELAYED_AUTO_START_INFO
			if err := windows.QueryServiceConfig2(s, windows.SERVICE_CONFIG_DELAYED_AUTO_START_INFO,
				(*byte)(unsafe.Pointer(&delayed)), uint32(unsafe.Sizeof(delayed)), &n); err == nil && delayed.IsDelayedAutoStartUp != 0 {
				auto = false
			}
		}
		if auto {
			tb := make([]byte, 4096)
			err := windows.QueryServiceConfig2(s, windows.SERVICE_CONFIG_TRIGGER_INFO, &tb[0], uint32(len(tb)), &n)
			switch {
			case err == nil:
				if ti := (*serviceTriggerInfo)(unsafe.Pointer(&tb[0])); ti.TriggersCount > 0 {
					auto = false
				}
			case errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER):
				auto = false // only a trigger list can need more than 4 KiB
			}
		}
		windows.CloseServiceHandle(s)
		if auto {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

func isIgnoredService(name string) bool {
	ln := strings.ToLower(name)
	for _, ig := range ignoredStoppedServices {
		li := strings.ToLower(ig)
		if strings.HasSuffix(li, "_") {
			if strings.HasPrefix(ln, li) {
				return true
			}
			continue
		}
		if ln == li {
			return true
		}
	}
	return false
}

// ---- Top processes ----

type procSample struct {
	name string
	cpu  int64 // 100ns units (user+kernel)
	ws   uint64
}

type procKey struct {
	pid     uintptr
	created int64
}

// processSnapshot reads every process in one NtQuerySystemInformation call
// (no per-process OpenProcess, so protected processes are included).
func processSnapshot() (map[procKey]procSample, error) {
	size := uint32(512 * 1024)
	var buf []uint64
	var err error
	for i := 0; i < 5; i++ {
		buf = make([]uint64, size/8+1)
		var ret uint32
		err = windows.NtQuerySystemInformation(windows.SystemProcessInformation, unsafe.Pointer(&buf[0]), uint32(len(buf)*8), &ret)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.STATUS_INFO_LENGTH_MISMATCH) {
			return nil, err
		}
		size = ret + 64*1024
	}
	if err != nil {
		return nil, err
	}
	base := unsafe.Pointer(&buf[0])
	limit := uintptr(len(buf) * 8)
	out := make(map[procKey]procSample, 256)
	for off := uintptr(0); off+unsafe.Sizeof(windows.SYSTEM_PROCESS_INFORMATION{}) <= limit; {
		p := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Add(base, off))
		if p.UniqueProcessID != 0 { // skip the Idle process
			name := ""
			if p.ImageName.Buffer != nil && p.ImageName.Length > 0 {
				name = windows.UTF16ToString(unsafe.Slice(p.ImageName.Buffer, p.ImageName.Length/2))
			}
			out[procKey{p.UniqueProcessID, p.CreateTime}] = procSample{
				name: name,
				cpu:  p.UserTime + p.KernelTime,
				ws:   uint64(p.WorkingSetSize),
			}
		}
		if p.NextEntryOffset == 0 {
			break
		}
		off += uintptr(p.NextEntryOffset)
	}
	return out, nil
}

// topProcesses samples twice, interval apart, and returns the n processes
// using the most CPU (percent of total machine capacity).
func topProcesses(ctx context.Context, n int, interval time.Duration) ([]protocol.Process, error) {
	a, err := processSnapshot()
	if err != nil {
		return nil, err
	}
	t0 := time.Now()
	select {
	case <-time.After(interval):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	b, err := processSnapshot()
	if err != nil {
		return nil, err
	}
	elapsed := time.Since(t0)
	ncpu := windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS)
	if ncpu == 0 {
		ncpu = 1
	}
	capacity := float64(elapsed.Nanoseconds()/100) * float64(ncpu)
	var procs []protocol.Process
	for k, s := range b {
		prev, ok := a[k]
		if !ok {
			continue
		}
		d := s.cpu - prev.cpu
		if d < 0 {
			d = 0
		}
		pct := 0.0
		if capacity > 0 {
			pct = round1(float64(d) / capacity * 100)
		}
		procs = append(procs, protocol.Process{Name: s.name, PID: int(k.pid), CPU: pct, MemBytes: s.ws})
	}
	return topProcessesBy(procs, n, func(x, y protocol.Process) bool {
		if x.CPU != y.CPU {
			return x.CPU > y.CPU
		}
		return x.MemBytes > y.MemBytes
	}), nil
}
