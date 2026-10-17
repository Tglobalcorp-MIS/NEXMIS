package collect

// Platform-independent helpers. Most of them are only called by the Windows
// implementation, but they live in an untagged file so that their behaviour
// is covered by the tests that run on Linux CI.

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
)

// ---- loose value conversion (WMI scripting VARIANTs) ----

// asString converts a loosely typed value (as returned by WMI through the
// scripting API) to a trimmed string.
func asString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case []byte:
		return strings.TrimSpace(string(x))
	case bool:
		return strconv.FormatBool(x)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	if u, ok := asUint64OK(v); ok {
		return strconv.FormatUint(u, 10)
	}
	if i, ok := v.(int64); ok {
		return strconv.FormatInt(i, 10)
	}
	return ""
}

// asUint64 converts a loosely typed value to uint64. WMI scripting returns
// CIM uint32 as VT_I4 and uint64 as a decimal string, so negative 32-bit
// values are reinterpreted as unsigned.
func asUint64(v any) uint64 {
	u, _ := asUint64OK(v)
	return u
}

func asUint64OK(v any) (uint64, bool) {
	switch x := v.(type) {
	case uint8:
		return uint64(x), true
	case uint16:
		return uint64(x), true
	case uint32:
		return uint64(x), true
	case uint64:
		return x, true
	case uint:
		return uint64(x), true
	case uintptr:
		return uint64(x), true
	case int8:
		return uint64(uint8(x)), true
	case int16:
		return uint64(uint16(x)), true
	case int32:
		return uint64(uint32(x)), true
	case int:
		if x < 0 {
			return uint64(uint32(int32(x))), true
		}
		return uint64(x), true
	case int64:
		if x < 0 {
			return 0, false
		}
		return uint64(x), true
	case float32:
		if x < 0 || math.IsNaN(float64(x)) {
			return 0, false
		}
		return uint64(x), true
	case float64:
		if x < 0 || math.IsNaN(x) {
			return 0, false
		}
		return uint64(x), true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, false
		}
		u, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return 0, false
		}
		return u, true
	}
	return 0, false
}

func asInt(v any) int {
	u := asUint64(v)
	if u > math.MaxInt32 {
		return math.MaxInt32
	}
	return int(u)
}

// asBool converts a loosely typed value to bool.
func asBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		b, _ := strconv.ParseBool(strings.TrimSpace(x))
		return b
	case nil:
		return false
	}
	return asUint64(v) != 0
}

// asUintSlice converts an array value ([]any of numbers) to []uint64.
func asUintSlice(v any) []uint64 {
	switch x := v.(type) {
	case []any:
		out := make([]uint64, 0, len(x))
		for _, e := range x {
			if u, ok := asUint64OK(e); ok {
				out = append(out, u)
			}
		}
		return out
	case []uint16:
		out := make([]uint64, len(x))
		for i, e := range x {
			out[i] = uint64(e)
		}
		return out
	case []uint8:
		out := make([]uint64, len(x))
		for i, e := range x {
			out[i] = uint64(e)
		}
		return out
	}
	return nil
}

// utf16ArrayString decodes a WMI uint16[] that holds UTF-16 code units
// (WmiMonitorID.UserFriendlyName etc.), stopping at the first NUL.
func utf16ArrayString(v any) string {
	vals := asUintSlice(v)
	units := make([]uint16, 0, len(vals))
	for _, u := range vals {
		if u == 0 {
			break
		}
		units = append(units, uint16(u))
	}
	return cleanString(string(utf16.Decode(units)))
}

// cleanString trims spaces and drops control characters.
func cleanString(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// parseCIMDatetime parses a CIM_DATETIME ("yyyymmddHHMMSS.mmmmmmsUUU",
// UUU = UTC offset in minutes).
func parseCIMDatetime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 14 {
		return time.Time{}, false
	}
	base := s
	offset := 0
	if len(s) >= 25 {
		base = s[:21]
		sign := s[21]
		if m, err := strconv.Atoi(s[22:25]); err == nil {
			switch sign {
			case '+':
				offset = m
			case '-':
				offset = -m
			}
		}
	} else if len(s) > 21 {
		base = s[:21]
	}
	base = strings.ReplaceAll(base, "*", "0")
	layout := "20060102150405"
	if len(base) >= 21 && base[14] == '.' {
		layout = "20060102150405.000000"
		base = base[:21]
	} else {
		base = base[:14]
	}
	t, err := time.Parse(layout, base)
	if err != nil || t.Year() < 1971 {
		return time.Time{}, false
	}
	return t.Add(-time.Duration(offset) * time.Minute).UTC(), true
}

// parseQFEDate parses Win32_QuickFixEngineering.InstalledOn, whose format
// depends on the OS locale and age: "M/D/YYYY", "YYYY-MM-DD", "D.M.YYYY",
// "YYYYMMDD" or a hex FILETIME ("01d6a1b2c3d4e5f6").
func parseQFEDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	// Hex FILETIME (16 hex digits).
	if len(s) == 16 && isHex(s) {
		ft, err := strconv.ParseUint(s, 16, 64)
		if err == nil && ft > 0 {
			return filetimeToTime(ft), true
		}
	}
	if len(s) == 8 && isDigits(s) {
		if t, err := time.Parse("20060102", s); err == nil {
			return t, true
		}
	}
	for _, layout := range []string{
		"1/2/2006", "01/02/2006", "2006-01-02", "2006/01/02", "2006/1/2",
		"2.1.2006", "02.01.2006", "1/2/2006 3:04:05 PM", "2006-01-02T15:04:05Z07:00",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	// Day-first slash dates (en-GB etc.) where the day is > 12.
	if parts := strings.Split(s, "/"); len(parts) == 3 {
		if t, err := time.Parse("2/1/2006", s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// filetimeToTime converts 100ns intervals since 1601-01-01 UTC.
func filetimeToTime(ft uint64) time.Time {
	const epochDiff = 116444736000000000 // 1601 -> 1970 in 100ns
	if ft < epochDiff {
		return time.Time{}
	}
	ns := (ft - epochDiff) * 100
	return time.Unix(0, int64(ns)).UTC()
}

func isHex(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return s != ""
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// normalizeInstallDate turns the Uninstall key's "YYYYMMDD" into
// "YYYY-MM-DD"; other formats are returned trimmed.
func normalizeInstallDate(s string) string {
	s = strings.TrimSpace(s)
	if len(s) == 8 && isDigits(s) {
		if t, err := time.Parse("20060102", s); err == nil {
			return t.Format("2006-01-02")
		}
		return ""
	}
	if t, ok := parseQFEDate(s); ok {
		return t.Format("2006-01-02")
	}
	return s
}

// ---- classification ----

// vmMarkers are substrings of Manufacturer/Model that identify virtual
// machines.
var vmMarkers = []string{
	"vmware", "virtualbox", "innotek", "virtual machine", "qemu", "kvm",
	"xen", "parallels", "bochs", "bhyve", "openstack", "amazon ec2",
	"google compute engine",
}

// isVirtualMachine reports whether the manufacturer/model strings describe a
// hypervisor guest (VMware, VirtualBox, Hyper-V "Virtual Machine", QEMU...).
func isVirtualMachine(manufacturer, model string) bool {
	s := strings.ToLower(manufacturer + " " + model)
	for _, m := range vmMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// chassisKind maps SMBIOS chassis types to desktop|laptop|server|vm|other.
func chassisKind(types []uint64, manufacturer, model string) string {
	if isVirtualMachine(manufacturer, model) {
		return "vm"
	}
	for _, t := range types {
		switch t {
		case 8, 9, 10, 11, 12, 14, 30, 31, 32:
			return "laptop"
		case 3, 4, 5, 6, 7, 13, 15, 16, 24, 34, 35, 36:
			return "desktop"
		case 17, 23, 25, 28, 29:
			return "server"
		}
	}
	return "other"
}

// windowsOSName fixes the registry ProductName, which still says
// "Windows 10" on Windows 11 (build >= 22000).
func windowsOSName(productName string, build int) string {
	name := strings.TrimSpace(productName)
	if name == "" {
		name = "Windows"
	}
	if build >= 22000 && strings.HasPrefix(name, "Windows 10") {
		name = "Windows 11" + strings.TrimPrefix(name, "Windows 10")
	}
	return name
}

// windowsEdition maps the registry EditionID to a friendly edition name.
func windowsEdition(editionID string) string {
	id := strings.TrimSpace(editionID)
	switch strings.ToLower(id) {
	case "core", "coresinglelanguage", "corecountryspecific":
		return "Home"
	case "coren":
		return "Home N"
	case "professional":
		return "Pro"
	case "professionaln":
		return "Pro N"
	case "professionalworkstation":
		return "Pro for Workstations"
	case "professionaleducation":
		return "Pro Education"
	case "enterprise":
		return "Enterprise"
	case "enterprisen":
		return "Enterprise N"
	case "enterprises":
		return "Enterprise LTSC"
	case "education":
		return "Education"
	case "iotenterprise", "iotenterprises":
		return "IoT Enterprise"
	case "serverstandard", "serverstandardeval":
		return "Server Standard"
	case "serverdatacenter", "serverdatacentereval":
		return "Server Datacenter"
	}
	return id
}

// diskMediaType maps MSFT_PhysicalDisk.MediaType.
func diskMediaType(v uint64) string {
	switch v {
	case 3:
		return "HDD"
	case 4:
		return "SSD"
	case 5:
		return "SCM"
	}
	return "Unspecified"
}

// diskBusType maps MSFT_PhysicalDisk.BusType.
func diskBusType(v uint64) string {
	names := map[uint64]string{
		1: "SCSI", 2: "ATAPI", 3: "ATA", 4: "1394", 5: "SSA", 6: "FibreChannel",
		7: "USB", 8: "RAID", 9: "iSCSI", 10: "SAS", 11: "SATA", 12: "SD",
		13: "MMC", 14: "Virtual", 15: "FileBackedVirtual", 16: "StorageSpaces",
		17: "NVMe", 18: "SCM", 19: "UFS",
	}
	if n, ok := names[v]; ok {
		return n
	}
	return "Unknown"
}

// diskHealth maps MSFT_PhysicalDisk.HealthStatus.
func diskHealth(v uint64) string {
	switch v {
	case 0:
		return "Healthy"
	case 1:
		return "Warning"
	case 2:
		return "Unhealthy"
	}
	return "Unknown"
}

// diskDriveHealth maps the Win32_DiskDrive.Status string.
func diskDriveHealth(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok":
		return "Healthy"
	case "pred fail", "degraded", "stressed":
		return "Warning"
	case "error", "nonrecover", "lost comm", "no contact":
		return "Unhealthy"
	}
	return "Unknown"
}

// batteryChemistry decodes BatteryStaticData.Chemistry, a 4-byte ASCII code
// stored little-endian in a uint32 (e.g. "LION", "LiP").
func batteryChemistry(v uint64) string {
	b := []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
	var sb strings.Builder
	for _, c := range b {
		if c >= 0x20 && c < 0x7f {
			sb.WriteByte(c)
		}
	}
	return strings.TrimSpace(sb.String())
}

// batteryHealthPercent returns full/design*100 rounded to one decimal, or 0
// when design is unknown.
func batteryHealthPercent(full, design uint32) float64 {
	if design == 0 || full == 0 {
		return 0
	}
	return math.Round(float64(full)/float64(design)*1000) / 10
}

// round1 rounds to one decimal place.
func round1(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return math.Round(f*10) / 10
}

// truncateRunes cuts s to at most n runes.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	i := 0
	for pos := range s {
		if i == n {
			return s[:pos]
		}
		i++
	}
	return s
}

// isUsableIPv4 filters loopback, APIPA (169.254/16) and unspecified addrs.
func isUsableIPv4(ip []byte) bool {
	if len(ip) == 16 {
		// IPv4-in-IPv6 form.
		for i := 0; i < 10; i++ {
			if ip[i] != 0 {
				return false
			}
		}
		if ip[10] != 0xff || ip[11] != 0xff {
			return false
		}
		ip = ip[12:]
	}
	if len(ip) != 4 {
		return false
	}
	switch {
	case ip[0] == 0, ip[0] == 127:
		return false
	case ip[0] == 169 && ip[1] == 254:
		return false
	case ip[0] >= 224:
		return false
	}
	return true
}

// topProcesses sorts by CPU (then memory) and keeps the first n.
func topProcessesBy[T any](items []T, n int, less func(a, b T) bool) []T {
	sort.SliceStable(items, func(i, j int) bool { return less(items[i], items[j]) })
	if len(items) > n {
		items = items[:n]
	}
	return items
}

// cpuPercentFromTimes computes busy% from deltas of idle/total ticks.
func cpuPercentFromTimes(idleDelta, totalDelta uint64) float64 {
	if totalDelta == 0 {
		return 0
	}
	if idleDelta > totalDelta {
		idleDelta = totalDelta
	}
	return round1(float64(totalDelta-idleDelta) / float64(totalDelta) * 100)
}

// cleanSerial drops OEM placeholder serials.
func cleanSerial(s string) string {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "", "default string", "to be filled by o.e.m.", "system serial number",
		"not specified", "none", "n/a", "0", "00000000", "123456789", "chassis serial number":
		return ""
	}
	return s
}

// unixDate formats a Unix timestamp (seconds) as YYYY-MM-DD in UTC.
func unixDate(sec int64) string {
	if sec <= 0 {
		return ""
	}
	return time.Unix(sec, 0).UTC().Format("2006-01-02")
}
