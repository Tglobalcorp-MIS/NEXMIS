//go:build windows

package collect

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Win32 entry points that golang.org/x/sys/windows does not wrap.
var (
	modkernel32 = windows.NewLazySystemDLL("kernel32.dll")
	modwtsapi32 = windows.NewLazySystemDLL("wtsapi32.dll")

	procGetSystemTimes             = modkernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx       = modkernel32.NewProc("GlobalMemoryStatusEx")
	procGetTickCount64             = modkernel32.NewProc("GetTickCount64")
	procGetSystemPowerStatus       = modkernel32.NewProc("GetSystemPowerStatus")
	procWTSQuerySessionInformation = modwtsapi32.NewProc("WTSQuerySessionInformationW")
)

// ---- CPU / memory / uptime / power ----

// systemTimes returns idle and total (kernel+user; kernel includes idle)
// CPU time in 100ns units, summed across processors.
func systemTimes() (idle, total uint64, err error) {
	var idleFT, kernelFT, userFT windows.Filetime
	r, _, e := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idleFT)),
		uintptr(unsafe.Pointer(&kernelFT)),
		uintptr(unsafe.Pointer(&userFT)))
	if r == 0 {
		return 0, 0, fmt.Errorf("GetSystemTimes: %w", e)
	}
	ft := func(f windows.Filetime) uint64 { return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime) }
	return ft(idleFT), ft(kernelFT) + ft(userFT), nil
}

// memoryStatusEx mirrors MEMORYSTATUSEX (64 bytes).
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

func globalMemory() (total, avail uint64, err error) {
	var m memoryStatusEx
	m.Length = uint32(unsafe.Sizeof(m))
	r, _, e := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m)))
	if r == 0 {
		return 0, 0, fmt.Errorf("GlobalMemoryStatusEx: %w", e)
	}
	return m.TotalPhys, m.AvailPhys, nil
}

// uptimeSeconds uses GetTickCount64 (milliseconds since boot, including
// time spent in sleep/hibernate).
func uptimeSeconds() uint64 {
	r, _, _ := procGetTickCount64.Call()
	return uint64(r) / 1000
}

// systemPowerStatus mirrors SYSTEM_POWER_STATUS (12 bytes).
type systemPowerStatus struct {
	ACLineStatus        byte
	BatteryFlag         byte
	BatteryLifePercent  byte
	SystemStatusFlag    byte
	BatteryLifeTime     uint32
	BatteryFullLifeTime uint32
}

// powerStatus returns (onBattery, percent); percent is -1 without battery.
func powerStatus() (onBattery bool, pct int, err error) {
	var s systemPowerStatus
	r, _, e := procGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&s)))
	if r == 0 {
		return false, -1, fmt.Errorf("GetSystemPowerStatus: %w", e)
	}
	// BatteryFlag 128 = no system battery, 255 = unknown.
	if s.BatteryFlag&128 != 0 || s.BatteryFlag == 255 {
		return false, -1, nil
	}
	pct = -1
	if s.BatteryLifePercent <= 100 {
		pct = int(s.BatteryLifePercent)
	}
	return s.ACLineStatus == 0, pct, nil
}

// ---- WTS sessions ----

const (
	wtsUserName      = 5
	wtsDomainName    = 7
	wtsSessionInfoEx = 25

	wtsSessionStateLock   = 0
	wtsSessionStateUnlock = 1
)

func wtsQuery(session uint32, class uint32) (unsafe.Pointer, uint32, error) {
	var buf unsafe.Pointer
	var n uint32
	r, _, e := procWTSQuerySessionInformation.Call(
		0, // WTS_CURRENT_SERVER_HANDLE
		uintptr(session),
		uintptr(class),
		uintptr(unsafe.Pointer(&buf)),
		uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		return nil, 0, e
	}
	return buf, n, nil
}

func wtsQueryString(session uint32, class uint32) string {
	buf, _, err := wtsQuery(session, class)
	if err != nil || buf == nil {
		return ""
	}
	defer windows.WTSFreeMemory(uintptr(buf))
	return windows.UTF16PtrToString((*uint16)(buf))
}

// sessionUser returns "DOMAIN\user" for a session, or "".
func sessionUser(session uint32) string {
	user := wtsQueryString(session, wtsUserName)
	if user == "" {
		return ""
	}
	if dom := wtsQueryString(session, wtsDomainName); dom != "" {
		return dom + `\` + user
	}
	return user
}

// sessionInfoEx reads WTSINFOEXW level 1: SessionState and SessionFlags.
// Layout: DWORD Level; union (8-byte aligned because it contains
// LARGE_INTEGERs) { ULONG SessionId; WTS_CONNECTSTATE_CLASS SessionState;
// LONG SessionFlags; ... }.
func sessionInfoEx(session uint32) (state uint32, flags int32, ok bool) {
	buf, n, err := wtsQuery(session, wtsSessionInfoEx)
	if err != nil || buf == nil {
		return 0, 0, false
	}
	defer windows.WTSFreeMemory(uintptr(buf))
	if n < 20 {
		return 0, 0, false
	}
	level := *(*uint32)(buf)
	if level != 1 {
		return 0, 0, false
	}
	state = *(*uint32)(unsafe.Add(buf, 12))
	flags = *(*int32)(unsafe.Add(buf, 16))
	return state, flags, true
}

// interactiveSession finds the console session, or failing that the first
// active session with a user (e.g. RDP on Pro editions).
func interactiveSession() (session uint32, user string, found bool) {
	if id := windows.WTSGetActiveConsoleSessionId(); id != 0xFFFFFFFF {
		if u := sessionUser(id); u != "" {
			return id, u, true
		}
	}
	var infos *windows.WTS_SESSION_INFO
	var count uint32
	if err := windows.WTSEnumerateSessions(0, 0, 1, &infos, &count); err != nil {
		return 0, "", false
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(infos)))
	list := unsafe.Slice(infos, count)
	for _, s := range list {
		if s.State != windows.WTSActive || s.SessionID == 0 {
			continue
		}
		if u := sessionUser(s.SessionID); u != "" {
			return s.SessionID, u, true
		}
	}
	return 0, "", false
}

// consoleUserState returns the logged-on user and active|locked|none.
func consoleUserState() (string, string) {
	id, user, ok := interactiveSession()
	if !ok {
		return "", "none"
	}
	state, flags, ok := sessionInfoEx(id)
	if !ok {
		return user, "active"
	}
	// Note: on Windows 7 / 2008 R2 LOCK and UNLOCK are reversed (documented
	// defect); those systems are out of scope.
	if state == windows.WTSDisconnected || flags == wtsSessionStateLock {
		return user, "locked"
	}
	return user, "active"
}

// ---- network adapters ----

type adapterInfo struct {
	Name        string // description (hardware name)
	FriendlyNm  string
	MAC         string
	IPv4        []string
	IPs         []string
	Gateways    []string
	DNS         []string
	DHCP        bool
	SpeedMbps   uint64
	Type        string
	Up          bool
	IsLoopback  bool
	IsTunnel    bool
	HasHardware bool
}

const ipAdapterDHCPEnabled = 0x4

func adapters() ([]adapterInfo, error) {
	size := uint32(16 * 1024)
	var buf []byte
	for i := 0; i < 4; i++ {
		buf = make([]byte, size)
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC,
			windows.GAA_FLAG_INCLUDE_GATEWAYS|windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST,
			0, (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])), &size)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) || i == 3 {
			return nil, fmt.Errorf("GetAdaptersAddresses: %w", err)
		}
	}
	var out []adapterInfo
	for aa := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); aa != nil; aa = aa.Next {
		a := adapterInfo{
			Name:       windows.UTF16PtrToString(aa.Description),
			FriendlyNm: windows.UTF16PtrToString(aa.FriendlyName),
			DHCP:       aa.Flags&ipAdapterDHCPEnabled != 0,
			Up:         aa.OperStatus == windows.IfOperStatusUp,
			IsLoopback: aa.IfType == windows.IF_TYPE_SOFTWARE_LOOPBACK,
			IsTunnel:   aa.IfType == windows.IF_TYPE_TUNNEL,
		}
		switch aa.IfType {
		case windows.IF_TYPE_ETHERNET_CSMACD:
			a.Type = "ethernet"
		case windows.IF_TYPE_IEEE80211:
			a.Type = "wifi"
		default:
			a.Type = "other"
		}
		if aa.PhysicalAddressLength > 0 && aa.PhysicalAddressLength <= uint32(len(aa.PhysicalAddress)) {
			a.MAC = strings.ToUpper(net.HardwareAddr(aa.PhysicalAddress[:aa.PhysicalAddressLength]).String())
			a.HasHardware = true
		}
		// TransmitLinkSpeed is bits/s; ^uint64(0) means unknown.
		if aa.TransmitLinkSpeed != 0 && aa.TransmitLinkSpeed != ^uint64(0) {
			a.SpeedMbps = aa.TransmitLinkSpeed / 1_000_000
		}
		for u := aa.FirstUnicastAddress; u != nil; u = u.Next {
			ip := u.Address.IP()
			if ip == nil {
				continue
			}
			a.IPs = append(a.IPs, ip.String())
			if ip4 := ip.To4(); ip4 != nil && isUsableIPv4(ip4) {
				a.IPv4 = append(a.IPv4, ip4.String())
			}
		}
		for g := aa.FirstGatewayAddress; g != nil; g = g.Next {
			if ip := g.Address.IP(); ip != nil && !ip.IsUnspecified() {
				a.Gateways = append(a.Gateways, ip.String())
			}
		}
		for d := aa.FirstDnsServerAddress; d != nil; d = d.Next {
			if ip := d.Address.IP(); ip != nil {
				// Skip the IPv6 site-local placeholders fec0:0:0:ffff::1-3.
				if ip.To4() == nil && strings.HasPrefix(ip.String(), "fec0:0:0:ffff::") {
					continue
				}
				a.DNS = append(a.DNS, ip.String())
			}
		}
		out = append(out, a)
	}
	return out, nil
}

// ---- registry helpers ----

func regString(root registry.Key, path, name string) string {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return ""
	}
	defer k.Close()
	s, _, err := k.GetStringValue(name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

func regInt(root registry.Key, path, name string) (uint64, bool) {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return 0, false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue(name)
	if err != nil {
		return 0, false
	}
	return v, true
}

func regKeyExists(root registry.Key, path string) bool {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return false
	}
	k.Close()
	return true
}

// keyString reads a value of an open key as a string, accepting REG_SZ,
// REG_EXPAND_SZ and DWORD/QWORD.
func keyString(k registry.Key, name string) string {
	if s, _, err := k.GetStringValue(name); err == nil {
		return strings.TrimSpace(s)
	}
	if v, _, err := k.GetIntegerValue(name); err == nil {
		return fmt.Sprint(v)
	}
	return ""
}

func keyInt(k registry.Key, name string) (uint64, bool) {
	if v, _, err := k.GetIntegerValue(name); err == nil {
		return v, true
	}
	return 0, false
}

// ---- OS version (registry) ----

const regCurrentVersion = `SOFTWARE\Microsoft\Windows NT\CurrentVersion`

type osVersionInfo struct {
	Name           string // e.g. "Windows 11 Pro"
	Edition        string // e.g. "Pro"
	DisplayVersion string // e.g. "23H2"
	Build          int    // e.g. 22631
	UBR            uint64 // e.g. 3447
	Major, Minor   uint32
}

func (o osVersionInfo) BuildString() string {
	if o.Build == 0 {
		return ""
	}
	if o.UBR > 0 {
		return fmt.Sprintf("%d.%d", o.Build, o.UBR)
	}
	return fmt.Sprint(o.Build)
}

// KernelVersion returns "10.0.22631.3447".
func (o osVersionInfo) KernelVersion() string {
	if o.Build == 0 {
		return ""
	}
	return fmt.Sprintf("%d.%d.%s", o.Major, o.Minor, o.BuildString())
}

func readOSVersion() osVersionInfo {
	var o osVersionInfo
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, regCurrentVersion, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err == nil {
		defer k.Close()
		o.DisplayVersion = keyString(k, "DisplayVersion")
		if o.DisplayVersion == "" {
			o.DisplayVersion = keyString(k, "ReleaseId")
		}
		if b := keyString(k, "CurrentBuildNumber"); b != "" {
			fmt.Sscan(b, &o.Build)
		}
		if o.Build == 0 {
			if b := keyString(k, "CurrentBuild"); b != "" {
				fmt.Sscan(b, &o.Build)
			}
		}
		o.UBR, _ = keyInt(k, "UBR")
		if v, ok := keyInt(k, "CurrentMajorVersionNumber"); ok {
			o.Major = uint32(v)
		}
		if v, ok := keyInt(k, "CurrentMinorVersionNumber"); ok {
			o.Minor = uint32(v)
		}
		o.Edition = windowsEdition(keyString(k, "EditionID"))
		o.Name = keyString(k, "ProductName")
	}
	// RtlGetVersion is not subject to manifest-based version lies.
	if v := windows.RtlGetVersion(); v != nil {
		if o.Major == 0 {
			o.Major, o.Minor = v.MajorVersion, v.MinorVersion
		}
		if o.Build == 0 {
			o.Build = int(v.BuildNumber)
		}
	}
	o.Name = windowsOSName(o.Name, o.Build)
	return o
}

// osArch returns x64 | arm64 | x86 for the native OS architecture.
func osArch() string {
	a := regString(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`, "PROCESSOR_ARCHITECTURE")
	switch strings.ToUpper(a) {
	case "AMD64":
		return "x64"
	case "ARM64":
		return "arm64"
	case "X86":
		return "x86"
	case "":
		return ""
	}
	return strings.ToLower(a)
}

// ---- misc ----

func utf16Ptr(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return nil
	}
	return p
}
