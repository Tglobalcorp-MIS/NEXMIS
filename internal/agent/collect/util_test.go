package collect

import (
	"net"
	"testing"
	"time"
)

func TestAsConversions(t *testing.T) {
	if got := asUint64(int32(-1)); got != 4294967295 {
		t.Errorf("asUint64(int32(-1)) = %d", got)
	}
	if got := asUint64("17179869184"); got != 17179869184 {
		t.Errorf("asUint64(string) = %d", got)
	}
	if got := asUint64(nil); got != 0 {
		t.Errorf("asUint64(nil) = %d", got)
	}
	if got := asUint64(uint16(3)); got != 3 {
		t.Errorf("asUint64(uint16) = %d", got)
	}
	if got := asString("  Dell Inc. "); got != "Dell Inc." {
		t.Errorf("asString = %q", got)
	}
	if got := asString(int32(42)); got != "42" {
		t.Errorf("asString(int32) = %q", got)
	}
	if !asBool(true) || asBool(false) || !asBool(int32(1)) || asBool(nil) || !asBool("true") {
		t.Error("asBool")
	}
	if got := asInt("abc"); got != 0 {
		t.Errorf("asInt(bad) = %d", got)
	}
	got := asUintSlice([]any{int32(3), uint16(10), "x", int32(9)})
	if len(got) != 3 || got[0] != 3 || got[1] != 10 || got[2] != 9 {
		t.Errorf("asUintSlice = %v", got)
	}
}

func TestUTF16ArrayString(t *testing.T) {
	// "DELL U2720Q" followed by NUL padding, as WmiMonitorID returns it.
	var arr []any
	for _, r := range "DELL U2720Q" {
		arr = append(arr, int32(r))
	}
	arr = append(arr, int32(0), int32(0), int32(65))
	if got := utf16ArrayString(arr); got != "DELL U2720Q" {
		t.Errorf("got %q", got)
	}
	if got := utf16ArrayString(nil); got != "" {
		t.Errorf("nil -> %q", got)
	}
}

func TestParseCIMDatetime(t *testing.T) {
	tm, ok := parseCIMDatetime("20230512083000.000000+480")
	if !ok {
		t.Fatal("parse failed")
	}
	if want := time.Date(2023, 5, 12, 0, 30, 0, 0, time.UTC); !tm.Equal(want) {
		t.Errorf("got %v want %v", tm, want)
	}
	tm, ok = parseCIMDatetime("20200101000000.000000-060")
	if !ok || !tm.Equal(time.Date(2020, 1, 1, 1, 0, 0, 0, time.UTC)) {
		t.Errorf("negative offset: %v %v", tm, ok)
	}
	if _, ok := parseCIMDatetime("20200101"); ok {
		t.Error("short string should fail")
	}
	if _, ok := parseCIMDatetime(""); ok {
		t.Error("empty should fail")
	}
	if tm, ok := parseCIMDatetime("20190301000000"); !ok || tm.Year() != 2019 {
		t.Errorf("no fraction: %v %v", tm, ok)
	}
}

func TestParseQFEDate(t *testing.T) {
	cases := map[string]time.Time{
		"10/12/2023":       time.Date(2023, 10, 12, 0, 0, 0, 0, time.UTC),
		"3/7/2024":         time.Date(2024, 3, 7, 0, 0, 0, 0, time.UTC),
		"2024-03-07":       time.Date(2024, 3, 7, 0, 0, 0, 0, time.UTC),
		"2024/3/7":         time.Date(2024, 3, 7, 0, 0, 0, 0, time.UTC),
		"20240307":         time.Date(2024, 3, 7, 0, 0, 0, 0, time.UTC),
		"25/12/2023":       time.Date(2023, 12, 25, 0, 0, 0, 0, time.UTC),
		"7.3.2024":         time.Date(2024, 3, 7, 0, 0, 0, 0, time.UTC),
		"01d6a1b2c3d4e5f6": filetimeToTime(0x01d6a1b2c3d4e5f6),
	}
	for in, want := range cases {
		got, ok := parseQFEDate(in)
		if !ok || !got.Equal(want) {
			t.Errorf("parseQFEDate(%q) = %v,%v want %v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "garbage", "13/13/2023"} {
		if _, ok := parseQFEDate(bad); ok {
			t.Errorf("parseQFEDate(%q) should fail", bad)
		}
	}
	if y := filetimeToTime(0x01d6a1b2c3d4e5f6).Year(); y != 2020 {
		t.Errorf("filetime year = %d", y)
	}
}

func TestNormalizeInstallDate(t *testing.T) {
	for in, want := range map[string]string{
		"20231012":   "2023-10-12",
		"2023-10-12": "2023-10-12",
		"":           "",
		"20231399":   "",
		"whenever":   "whenever",
	} {
		if got := normalizeInstallDate(in); got != want {
			t.Errorf("normalizeInstallDate(%q) = %q want %q", in, got, want)
		}
	}
}

func TestChassisKind(t *testing.T) {
	cases := []struct {
		types       []uint64
		manu, model string
		want        string
	}{
		{[]uint64{10}, "LENOVO", "20XW", "laptop"},
		{[]uint64{31}, "HP", "Spectre", "laptop"},
		{[]uint64{3}, "Dell Inc.", "OptiPlex 7090", "desktop"},
		{[]uint64{35}, "Intel", "NUC", "desktop"},
		{[]uint64{23}, "Dell Inc.", "PowerEdge R740", "server"},
		{[]uint64{1, 2}, "Foo", "Bar", "other"},
		{nil, "", "", "other"},
		{[]uint64{3}, "VMware, Inc.", "VMware7,1", "vm"},
		{[]uint64{3}, "Microsoft Corporation", "Virtual Machine", "vm"},
		{[]uint64{1}, "innotek GmbH", "VirtualBox", "vm"},
		{[]uint64{1}, "QEMU", "Standard PC (Q35 + ICH9, 2009)", "vm"},
		{[]uint64{9}, "Microsoft Corporation", "Surface Laptop 5", "laptop"},
	}
	for _, c := range cases {
		if got := chassisKind(c.types, c.manu, c.model); got != c.want {
			t.Errorf("chassisKind(%v,%q,%q) = %q want %q", c.types, c.manu, c.model, got, c.want)
		}
	}
}

func TestWindowsOSName(t *testing.T) {
	cases := []struct {
		name  string
		build int
		want  string
	}{
		{"Windows 10 Pro", 22631, "Windows 11 Pro"},
		{"Windows 10 Home", 19045, "Windows 10 Home"},
		{"Windows 10 Enterprise", 22000, "Windows 11 Enterprise"},
		{"Windows Server 2022 Datacenter", 20348, "Windows Server 2022 Datacenter"},
		{"Windows Server 2025 Standard", 26100, "Windows Server 2025 Standard"},
		{"", 22631, "Windows"},
	}
	for _, c := range cases {
		if got := windowsOSName(c.name, c.build); got != c.want {
			t.Errorf("windowsOSName(%q,%d) = %q want %q", c.name, c.build, got, c.want)
		}
	}
}

func TestWindowsEdition(t *testing.T) {
	for in, want := range map[string]string{
		"Core": "Home", "Professional": "Pro", "Enterprise": "Enterprise",
		"Education": "Education", "ServerStandard": "Server Standard", "Weird": "Weird",
		"CoreSingleLanguage": "Home",
	} {
		if got := windowsEdition(in); got != want {
			t.Errorf("windowsEdition(%q) = %q want %q", in, got, want)
		}
	}
}

func TestDiskMappings(t *testing.T) {
	if diskMediaType(4) != "SSD" || diskMediaType(3) != "HDD" || diskMediaType(0) != "Unspecified" {
		t.Error("media type")
	}
	if diskBusType(17) != "NVMe" || diskBusType(11) != "SATA" || diskBusType(7) != "USB" || diskBusType(99) != "Unknown" {
		t.Error("bus type")
	}
	if diskHealth(0) != "Healthy" || diskHealth(1) != "Warning" || diskHealth(2) != "Unhealthy" || diskHealth(5) != "Unknown" {
		t.Error("health")
	}
	if diskDriveHealth("OK") != "Healthy" || diskDriveHealth("Pred Fail") != "Warning" ||
		diskDriveHealth("Error") != "Unhealthy" || diskDriveHealth("") != "Unknown" {
		t.Error("drive health")
	}
}

func TestBatteryHelpers(t *testing.T) {
	// 'L','I','O','N' little-endian.
	v := uint64('L') | uint64('I')<<8 | uint64('O')<<16 | uint64('N')<<24
	if got := batteryChemistry(v); got != "LION" {
		t.Errorf("chemistry = %q", got)
	}
	if got := batteryChemistry(uint64('L') | uint64('i')<<8 | uint64('P')<<16); got != "LiP" {
		t.Errorf("chemistry LiP = %q", got)
	}
	if got := batteryHealthPercent(42000, 57000); got != 73.7 {
		t.Errorf("health = %v", got)
	}
	if got := batteryHealthPercent(42000, 0); got != 0 {
		t.Errorf("health design 0 = %v", got)
	}
}

func TestMiscHelpers(t *testing.T) {
	if got := truncateRunes("繁體中文訊息", 3); got != "繁體中" {
		t.Errorf("truncate = %q", got)
	}
	if got := truncateRunes("abc", 10); got != "abc" {
		t.Errorf("truncate short = %q", got)
	}
	for ipStr, want := range map[string]bool{
		"192.168.1.10": true, "10.0.0.1": true, "127.0.0.1": false,
		"169.254.3.4": false, "0.0.0.0": false, "224.0.0.1": false,
	} {
		if got := isUsableIPv4(net.ParseIP(ipStr).To4()); got != want {
			t.Errorf("isUsableIPv4(%s) = %v", ipStr, got)
		}
		if got := isUsableIPv4(net.ParseIP(ipStr)); got != want { // 16-byte form
			t.Errorf("isUsableIPv4(%s) = %v", ipStr, got)
		}
	}
	if cleanSerial("To Be Filled By O.E.M.") != "" || cleanSerial(" ABC123 ") != "ABC123" {
		t.Error("cleanSerial")
	}
	if cpuPercentFromTimes(25, 100) != 75 || cpuPercentFromTimes(0, 0) != 0 || cpuPercentFromTimes(200, 100) != 0 {
		t.Error("cpuPercentFromTimes")
	}
	if unixDate(0) != "" || unixDate(1700000000) != "2023-11-14" {
		t.Errorf("unixDate = %q", unixDate(1700000000))
	}
	if !isVirtualMachine("Xen", "HVM domU") || isVirtualMachine("Dell Inc.", "Latitude 7440") {
		t.Error("isVirtualMachine")
	}
}
