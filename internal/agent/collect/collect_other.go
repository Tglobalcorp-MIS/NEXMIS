//go:build !windows

package collect

// Development implementation for non-Windows hosts. It reports real data
// where Linux exposes it cheaply (/proc, /sys, statfs) so that the agent can
// be run and integration-tested on Linux; Windows-only fields (Defender,
// BitLocker, event log...) are left empty. Software returns an empty list.

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

// Paths are variables so tests can point them at fixtures.
var (
	procRoot = "/proc"
	sysRoot  = "/sys"
	etcRoot  = "/etc"
)

// userHZ is the kernel's USER_HZ (clock ticks per second in /proc); it is
// 100 on every mainstream Linux architecture.
const userHZ = 100

type otherCollector struct {
	log *slog.Logger

	cpuMu       sync.Mutex
	cpuLastIdle uint64
	cpuLastTot  uint64
	cpuLastAt   time.Time

	batMu       sync.Mutex
	lastBattery *protocol.Battery
}

// New returns the development collector for non-Windows platforms.
func New() Collector {
	return &otherCollector{log: slog.Default().With("component", "collect")}
}

func (c *otherCollector) warn(source string, err error) {
	if err != nil {
		c.log.Debug("collect source failed", "source", source, "err", err)
	}
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ---- Identity ----

func (c *otherCollector) Identity(ctx context.Context) (Identity, error) {
	var id Identity
	for _, p := range []string{filepath.Join(etcRoot, "machine-id"), "/var/lib/dbus/machine-id"} {
		if v := readTrim(p); v != "" {
			id.MachineID = strings.ToLower(v)
			break
		}
	}
	host, _ := os.Hostname()
	if i := strings.IndexByte(host, '.'); i > 0 {
		id.Domain = host[i+1:]
		host = host[:i]
	}
	id.Hostname = host
	osr := osRelease()
	id.OS = osr["PRETTY_NAME"]
	if id.OS == "" {
		id.OS = osr["NAME"]
	}
	if id.OS == "" {
		id.OS = runtime.GOOS
	}
	id.OSVersion = osr["VERSION_ID"]
	if id.OSVersion == "" {
		id.OSVersion = kernelRelease()
	}
	if id.MachineID == "" {
		// Fall back to a stable value derived from the hostname so that a
		// dev box without machine-id can still enroll.
		id.MachineID = "dev-" + hex.EncodeToString([]byte(id.Hostname))
	}
	if id.Hostname == "" {
		return id, errors.New("collect: hostname unavailable")
	}
	return id, nil
}

func osRelease() map[string]string {
	out := map[string]string{}
	for _, p := range []string{filepath.Join(etcRoot, "os-release"), "/usr/lib/os-release"} {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			k, v, ok := strings.Cut(sc.Text(), "=")
			if !ok {
				continue
			}
			out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
		f.Close()
		break
	}
	return out
}

func kernelRelease() string {
	return readTrim(filepath.Join(procRoot, "sys/kernel/osrelease"))
}

// ---- CPU / memory / uptime ----

// readProcStat returns aggregate idle (idle+iowait) and total jiffies.
func readProcStat() (idle, total uint64, err error) {
	f, err := os.Open(filepath.Join(procRoot, "stat"))
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0, 0, errors.New("empty /proc/stat")
	}
	return parseProcStatCPU(sc.Text())
}

func parseProcStatCPU(line string) (idle, total uint64, err error) {
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, fmt.Errorf("unexpected /proc/stat line %q", line)
	}
	for i, f := range fields[1:] {
		if i >= 8 { // guest/guest_nice are already included in user/nice
			break
		}
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return 0, 0, err
		}
		total += v
		if i == 3 || i == 4 { // idle, iowait
			idle += v
		}
	}
	return idle, total, nil
}

func (c *otherCollector) cpuPercent(ctx context.Context) (float64, error) {
	c.cpuMu.Lock()
	defer c.cpuMu.Unlock()
	idle, tot, err := readProcStat()
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
		if idle, tot, err = readProcStat(); err != nil {
			return 0, err
		}
		now = time.Now()
	}
	pct := cpuPercentFromTimes(idle-c.cpuLastIdle, tot-c.cpuLastTot)
	c.cpuLastIdle, c.cpuLastTot, c.cpuLastAt = idle, tot, now
	return pct, nil
}

// memInfo returns total and available bytes from /proc/meminfo.
func memInfo() (total, avail uint64, err error) {
	f, err := os.Open(filepath.Join(procRoot, "meminfo"))
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	var free, buffers, cached uint64
	haveAvail := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fs := strings.Fields(v)
		if len(fs) == 0 {
			continue
		}
		n, _ := strconv.ParseUint(fs[0], 10, 64)
		n *= 1024
		switch k {
		case "MemTotal":
			total = n
		case "MemAvailable":
			avail, haveAvail = n, true
		case "MemFree":
			free = n
		case "Buffers":
			buffers = n
		case "Cached":
			cached = n
		}
	}
	if !haveAvail {
		avail = free + buffers + cached
	}
	if total == 0 {
		return 0, 0, errors.New("MemTotal missing")
	}
	if avail > total {
		avail = total
	}
	return total, avail, nil
}

func uptimeSec() uint64 {
	s := readTrim(filepath.Join(procRoot, "uptime"))
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil || v < 0 {
		return 0
	}
	return uint64(v)
}

// ---- network ----

func ipv4Addresses() []string {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if ip4 := ipn.IP.To4(); ip4 != nil && isUsableIPv4(ip4) && !seen[ip4.String()] {
				seen[ip4.String()] = true
				out = append(out, ip4.String())
			}
		}
	}
	sort.Strings(out)
	return out
}

func (c *otherCollector) Heartbeat(ctx context.Context) (protocol.Heartbeat, error) {
	hb := protocol.Heartbeat{Time: time.Now().UTC(), BatteryPct: -1}
	hb.UptimeSec = uptimeSec()
	cpu, err := c.cpuPercent(ctx)
	c.warn("/proc/stat", err)
	hb.CPUPercent = cpu
	if total, avail, err := memInfo(); err == nil {
		hb.MemTotal, hb.MemUsed = total, total-avail
	} else {
		c.warn("/proc/meminfo", err)
	}
	if u := os.Getenv("SUDO_USER"); u != "" {
		hb.LoggedInUser = u
	}
	hb.IPAddresses = ipv4Addresses()
	if b := readBattery(); b.Present {
		hb.OnBattery = !b.OnAC
		hb.BatteryPct = b.ChargePercent
	}
	return hb, nil
}

// ---- Hardware ----

func dmi(name string) string {
	return cleanString(readTrim(filepath.Join(sysRoot, "class/dmi/id", name)))
}

func (c *otherCollector) Hardware(ctx context.Context) (*protocol.Hardware, error) {
	hw := &protocol.Hardware{}
	hw.Manufacturer = dmi("sys_vendor")
	hw.Model = dmi("product_name")
	hw.SerialNumber = cleanSerial(dmi("product_serial")) // root only
	if hw.SerialNumber == "" {
		hw.SerialNumber = cleanSerial(dmi("chassis_serial"))
	}
	var chassis []uint64
	if v, err := strconv.ParseUint(dmi("chassis_type"), 10, 16); err == nil {
		chassis = append(chassis, v)
	}
	hw.ChassisType = chassisKind(chassis, hw.Manufacturer, hw.Model)
	hw.BIOSVendor = dmi("bios_vendor")
	hw.BIOSVersion = dmi("bios_version")
	if t, err := time.Parse("01/02/2006", dmi("bios_date")); err == nil {
		hw.BIOSDate = t.Format("2006-01-02")
	}

	osr := osRelease()
	hw.OSName = osr["PRETTY_NAME"]
	if hw.OSName == "" {
		hw.OSName = runtime.GOOS
	}
	hw.OSVersion = osr["VERSION_ID"]
	hw.OSBuild = kernelRelease()
	hw.OSEdition = osr["VARIANT"]
	hw.OSArch = goArchLabel(runtime.GOARCH)
	host, _ := os.Hostname()
	if i := strings.IndexByte(host, '.'); i > 0 {
		hw.Domain = host[i+1:]
	}

	hw.CPUs = cpuInfo()
	if total, _, err := memInfo(); err == nil {
		hw.MemoryTotal = total
	}
	hw.Disks = blockDisks()
	hw.Volumes = mountedVolumes()
	hw.NICs = nics()

	if _, err := os.Stat(filepath.Join(sysRoot, "class/tpm/tpm0")); err == nil {
		hw.TPMPresent = true
		if v := readTrim(filepath.Join(sysRoot, "class/tpm/tpm0/tpm_version_major")); v != "" {
			hw.TPMVersion = v + ".0"
		}
	}
	matches, _ := filepath.Glob(filepath.Join(sysRoot, "firmware/efi/efivars/SecureBoot-*"))
	if len(matches) > 0 {
		if b, err := os.ReadFile(matches[0]); err == nil && len(b) > 0 {
			hw.SecureBoot = b[len(b)-1] == 1
		}
	}
	return hw, nil
}

func goArchLabel(a string) string {
	switch a {
	case "amd64":
		return "x64"
	case "386":
		return "x86"
	}
	return a
}

func cpuInfo() []protocol.CPU {
	f, err := os.Open(filepath.Join(procRoot, "cpuinfo"))
	if err != nil {
		return nil
	}
	defer f.Close()
	type sock struct {
		name    string
		cores   int
		logical int
		mhz     float64
	}
	sockets := map[string]*sock{}
	var order []string
	cur := map[string]string{}
	flush := func() {
		if len(cur) == 0 {
			return
		}
		id := cur["physical id"]
		s := sockets[id]
		if s == nil {
			s = &sock{}
			sockets[id] = s
			order = append(order, id)
		}
		s.logical++
		if s.name == "" {
			s.name = cur["model name"]
			if s.name == "" {
				s.name = cur["Model"]
			}
		}
		if n, err := strconv.Atoi(cur["cpu cores"]); err == nil {
			s.cores = n
		}
		if m, err := strconv.ParseFloat(cur["cpu MHz"], 64); err == nil && m > s.mhz {
			s.mhz = m
		}
		cur = map[string]string{}
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if ok {
			cur[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	flush()
	maxMHz := 0
	if v, err := strconv.Atoi(readTrim(filepath.Join(sysRoot, "devices/system/cpu/cpu0/cpufreq/cpuinfo_max_freq"))); err == nil {
		maxMHz = v / 1000
	}
	var out []protocol.CPU
	for _, id := range order {
		s := sockets[id]
		cores := s.cores
		if cores == 0 {
			cores = s.logical
		}
		mhz := maxMHz
		if mhz == 0 {
			mhz = int(s.mhz)
		}
		out = append(out, protocol.CPU{Name: s.name, Cores: cores, LogicalProcs: s.logical, MaxClockMHz: mhz})
	}
	return out
}

func blockDisks() []protocol.Disk {
	entries, err := os.ReadDir(filepath.Join(sysRoot, "block"))
	if err != nil {
		return nil
	}
	var out []protocol.Disk
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "loop") || strings.HasPrefix(n, "ram") || strings.HasPrefix(n, "zram") ||
			strings.HasPrefix(n, "dm-") || strings.HasPrefix(n, "md") || strings.HasPrefix(n, "sr") || strings.HasPrefix(n, "nbd") {
			continue
		}
		base := filepath.Join(sysRoot, "block", n)
		sectors, _ := strconv.ParseUint(readTrim(filepath.Join(base, "size")), 10, 64)
		if sectors == 0 {
			continue
		}
		d := protocol.Disk{
			Model:        cleanString(readTrim(filepath.Join(base, "device/model"))),
			SerialNumber: cleanSerial(readTrim(filepath.Join(base, "device/serial"))),
			Size:         sectors * 512,
			MediaType:    "Unspecified",
			BusType:      "Unknown",
			Health:       "Unknown",
		}
		switch readTrim(filepath.Join(base, "queue/rotational")) {
		case "1":
			d.MediaType = "HDD"
		case "0":
			d.MediaType = "SSD"
		}
		link, _ := filepath.EvalSymlinks(base)
		switch {
		case strings.HasPrefix(n, "nvme"):
			d.BusType = "NVMe"
		case strings.Contains(link, "/usb"):
			d.BusType = "USB"
		case strings.HasPrefix(n, "vd") || strings.HasPrefix(n, "xvd"):
			d.BusType = "Virtual"
		case strings.Contains(link, "/ata"):
			d.BusType = "SATA"
		}
		out = append(out, d)
	}
	return out
}

var realFS = map[string]bool{
	"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true, "vfat": true,
	"exfat": true, "ntfs": true, "ntfs3": true, "zfs": true, "f2fs": true, "jfs": true,
	"reiserfs": true, "apfs": true, "hfs": true, "hfsplus": true, "fuseblk": true,
}

func mountedVolumes() []protocol.Volume {
	f, err := os.Open(filepath.Join(procRoot, "mounts"))
	if err != nil {
		return nil
	}
	defer f.Close()
	seenDev := map[string]bool{}
	var out []protocol.Volume
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) < 3 || !realFS[fs[2]] || seenDev[fs[0]] {
			continue
		}
		mount := unescapeMount(fs[1])
		size, free, ok := statfsSizes(mount)
		if !ok || size == 0 {
			continue
		}
		seenDev[fs[0]] = true
		out = append(out, protocol.Volume{Mount: mount, FileSystem: fs[2], Size: size, Free: free, Label: filepath.Base(fs[0])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mount < out[j].Mount })
	return out
}

// unescapeMount decodes the octal escapes (\040 etc.) used in /proc/mounts.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func defaultGateways() map[string][]string {
	out := map[string][]string{}
	f, err := os.Open(filepath.Join(procRoot, "net/route"))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Scan() // header
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) < 3 || fs[1] != "00000000" {
			continue
		}
		b, err := hex.DecodeString(fs[2])
		if err != nil || len(b) != 4 {
			continue
		}
		ip := net.IPv4(b[3], b[2], b[1], b[0]).String()
		out[fs[0]] = append(out[fs[0]], ip)
	}
	return out
}

func resolvConfDNS() []string {
	f, err := os.Open(filepath.Join(etcRoot, "resolv.conf"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) >= 2 && fs[0] == "nameserver" {
			out = append(out, fs[1])
		}
	}
	return out
}

func nics() []protocol.NIC {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	gws := defaultGateways()
	dns := resolvConfDNS()
	var out []protocol.NIC
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagLoopback != 0 || len(ifc.HardwareAddr) == 0 {
			continue
		}
		base := filepath.Join(sysRoot, "class/net", ifc.Name)
		n := protocol.NIC{
			Name:    ifc.Name,
			MAC:     strings.ToUpper(ifc.HardwareAddr.String()),
			Gateway: gws[ifc.Name],
			DNS:     dns,
			Type:    "other",
		}
		if _, err := os.Stat(filepath.Join(base, "wireless")); err == nil {
			n.Type = "wifi"
		} else if readTrim(filepath.Join(base, "type")) == "1" {
			if _, err := os.Stat(filepath.Join(base, "device")); err == nil {
				n.Type = "ethernet"
			}
		}
		if s, err := strconv.ParseInt(readTrim(filepath.Join(base, "speed")), 10, 64); err == nil && s > 0 {
			n.SpeedMbps = uint64(s)
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				n.IPs = append(n.IPs, ipn.IP.String())
			}
		}
		out = append(out, n)
	}
	return out
}

// ---- Software ----

func (c *otherCollector) Software(ctx context.Context) ([]protocol.Software, error) {
	return []protocol.Software{}, nil
}

// ---- Health ----

func (c *otherCollector) Health(ctx context.Context) (*protocol.Health, error) {
	h := &protocol.Health{Collected: time.Now().UTC()}
	if pct, err := c.cpuPercent(ctx); err == nil {
		h.CPUPercent = pct
	} else {
		c.warn("/proc/stat", err)
	}
	if total, avail, err := memInfo(); err == nil {
		h.MemPercent = round1(float64(total-avail) / float64(total) * 100)
	}
	h.UptimeSec = uptimeSec()
	h.LastBoot = h.Collected.Add(-time.Duration(h.UptimeSec) * time.Second).Truncate(time.Second)
	if _, err := os.Stat("/var/run/reboot-required"); err == nil {
		h.PendingReboot = true
		h.PendingRebootWhy = []string{"reboot-required"}
	}
	h.Volumes = mountedVolumes()
	h.DiskHealth = blockDisks()
	if procs, err := linuxTopProcesses(ctx, 5, 500*time.Millisecond); err == nil {
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

type linuxProc struct {
	name  string
	ticks uint64
	rss   uint64
}

func procSnapshot() (map[int]linuxProc, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, err
	}
	page := uint64(os.Getpagesize())
	out := make(map[int]linuxProc, len(entries))
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "stat"))
		if err != nil {
			continue
		}
		if p, ok := parseProcPidStat(string(b), page); ok {
			out[pid] = p
		}
	}
	return out, nil
}

// parseProcPidStat parses /proc/<pid>/stat. comm may contain spaces and
// parentheses, so fields are counted from the last ')'.
func parseProcPidStat(s string, pageSize uint64) (linuxProc, bool) {
	open := strings.IndexByte(s, '(')
	end := strings.LastIndexByte(s, ')')
	if open < 0 || end < open {
		return linuxProc{}, false
	}
	fs := strings.Fields(s[end+1:])
	// fs[0] is field 3 (state): utime=14, stime=15, rss=24.
	if len(fs) < 22 {
		return linuxProc{}, false
	}
	ut, _ := strconv.ParseUint(fs[11], 10, 64)
	st, _ := strconv.ParseUint(fs[12], 10, 64)
	rss, _ := strconv.ParseInt(fs[21], 10, 64)
	if rss < 0 {
		rss = 0
	}
	return linuxProc{name: s[open+1 : end], ticks: ut + st, rss: uint64(rss) * pageSize}, true
}

func linuxTopProcesses(ctx context.Context, n int, interval time.Duration) ([]protocol.Process, error) {
	a, err := procSnapshot()
	if err != nil {
		return nil, err
	}
	t0 := time.Now()
	select {
	case <-time.After(interval):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	b, err := procSnapshot()
	if err != nil {
		return nil, err
	}
	capTicks := time.Since(t0).Seconds() * userHZ * float64(runtime.NumCPU())
	var out []protocol.Process
	for pid, p := range b {
		prev, ok := a[pid]
		if !ok || p.name != prev.name {
			continue
		}
		d := float64(0)
		if p.ticks > prev.ticks {
			d = float64(p.ticks - prev.ticks)
		}
		pct := 0.0
		if capTicks > 0 {
			pct = round1(d / capTicks * 100)
		}
		out = append(out, protocol.Process{Name: p.name, PID: pid, CPU: pct, MemBytes: p.rss})
	}
	return topProcessesBy(out, n, func(x, y protocol.Process) bool {
		if x.CPU != y.CPU {
			return x.CPU > y.CPU
		}
		return x.MemBytes > y.MemBytes
	}), nil
}

// ---- Battery ----

func (c *otherCollector) Battery(ctx context.Context) (*protocol.Battery, error) {
	b := readBattery()
	c.batMu.Lock()
	c.lastBattery = b
	c.batMu.Unlock()
	return b, nil
}

func readBattery() *protocol.Battery {
	b := &protocol.Battery{Collected: time.Now().UTC(), OnAC: true}
	root := filepath.Join(sysRoot, "class/power_supply")
	entries, err := os.ReadDir(root)
	if err != nil {
		return b
	}
	acKnown, acOnline := false, false
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		typ := readTrim(filepath.Join(dir, "type"))
		switch typ {
		case "Mains", "USB":
			if v := readTrim(filepath.Join(dir, "online")); v != "" {
				acKnown = true
				acOnline = acOnline || v == "1"
			}
		case "Battery":
			if readTrim(filepath.Join(dir, "scope")) == "Device" || b.Present {
				continue // peripheral battery (mouse, ...) or already have one
			}
			if readTrim(filepath.Join(dir, "present")) == "0" {
				continue
			}
			fillLinuxBattery(b, dir)
		}
	}
	if acKnown {
		b.OnAC = acOnline
	}
	if !b.Present {
		return &protocol.Battery{Collected: b.Collected, OnAC: true}
	}
	return b
}

func fillLinuxBattery(b *protocol.Battery, dir string) {
	num := func(name string) uint64 {
		v, _ := strconv.ParseUint(readTrim(filepath.Join(dir, name)), 10, 64)
		return v
	}
	b.Present = true
	b.Name = readTrim(filepath.Join(dir, "model_name"))
	b.Manufacturer = readTrim(filepath.Join(dir, "manufacturer"))
	b.SerialNumber = strings.TrimSpace(readTrim(filepath.Join(dir, "serial_number")))
	b.Chemistry = readTrim(filepath.Join(dir, "technology"))
	// energy_* are µWh; charge_* are µAh and need voltage (µV).
	design, full := num("energy_full_design"), num("energy_full")
	if design == 0 {
		volt := num("voltage_min_design")
		if volt == 0 {
			volt = num("voltage_now")
		}
		design = num("charge_full_design") * volt / 1_000_000
		full = num("charge_full") * volt / 1_000_000
	}
	b.DesignCapacity = uint32(design / 1000) // -> mWh
	b.FullChargeCap = uint32(full / 1000)
	b.CycleCount = uint32(num("cycle_count"))
	b.HealthPercent = batteryHealthPercent(b.FullChargeCap, b.DesignCapacity)
	if v := readTrim(filepath.Join(dir, "capacity")); v != "" {
		b.ChargePercent, _ = strconv.Atoi(v)
	}
	switch readTrim(filepath.Join(dir, "status")) {
	case "Discharging":
		b.OnAC = false
	}
	// Runtime estimate: energy_now / power_now (µWh / µW = h).
	if pw, en := num("power_now"), num("energy_now"); pw > 0 && en > 0 && !b.OnAC {
		b.EstimatedRuntimeM = int(float64(en) / float64(pw) * 60)
	}
}
