// Package discovery implements a low-impact network scanner used both by
// the server and by agents acting as discovery probes for networks the
// server cannot reach. It needs no raw-socket privileges: a host counts as
// alive when any probed TCP port accepts or actively refuses a connection.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

// DefaultPorts are probed when the caller does not specify any.
var DefaultPorts = []int{22, 80, 135, 139, 443, 445, 515, 631, 3389, 5000, 5001, 8080, 9100}

// MaxHosts bounds the total number of addresses in one scan.
const MaxHosts = 8192

// Options tunes a scan.
type Options struct {
	Ports       []int
	Timeout     time.Duration // per connection attempt
	Concurrency int           // simultaneous connection attempts
}

func (o *Options) defaults() {
	if len(o.Ports) == 0 {
		o.Ports = DefaultPorts
	}
	if o.Timeout <= 0 {
		o.Timeout = 700 * time.Millisecond
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 128
	}
}

// ExpandCIDRs validates and expands CIDRs or single addresses into host
// addresses (network and broadcast addresses excluded for IPv4 /0-/30).
func ExpandCIDRs(cidrs []string) ([]netip.Addr, error) {
	var out []netip.Addr
	seen := map[netip.Addr]bool{}
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		var p netip.Prefix
		if strings.Contains(c, "/") {
			var err error
			if p, err = netip.ParsePrefix(c); err != nil {
				return nil, fmt.Errorf("無效的網段 %q", c)
			}
		} else {
			a, err := netip.ParseAddr(c)
			if err != nil {
				return nil, fmt.Errorf("無效的位址 %q", c)
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		if !p.Addr().Is4() {
			return nil, fmt.Errorf("目前僅支援 IPv4 網段：%q", c)
		}
		p = p.Masked()
		if p.Bits() < 19 {
			return nil, fmt.Errorf("網段 %q 過大（最大 /19）", c)
		}
		first, last := p.Addr(), lastAddr(p)
		for a := first; a.IsValid() && p.Contains(a); a = a.Next() {
			if p.Bits() <= 30 && (a == first || a == last) {
				continue
			}
			if !seen[a] {
				seen[a] = true
				out = append(out, a)
			}
			if len(out) > MaxHosts {
				return nil, fmt.Errorf("掃描位址總數超過上限 %d", MaxHosts)
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("沒有可掃描的位址")
	}
	return out, nil
}

func lastAddr(p netip.Prefix) netip.Addr {
	b := p.Addr().As4()
	hostBits := 32 - p.Bits()
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	v |= (1 << hostBits) - 1
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// Scan probes all addresses and returns hosts that responded, enriched with
// reverse DNS, NetBIOS name, MAC (when on-link) and a device-type guess.
func Scan(ctx context.Context, addrs []netip.Addr, opt Options) ([]protocol.DiscoveredHost, error) {
	opt.defaults()
	type result struct {
		addr  netip.Addr
		ports []int
		rtt   time.Duration
	}
	var mu sync.Mutex
	results := map[netip.Addr]*result{}
	sem := make(chan struct{}, opt.Concurrency)
	var wg sync.WaitGroup
	dialer := net.Dialer{Timeout: opt.Timeout}

outer:
	for _, a := range addrs {
		for _, port := range opt.Ports {
			select {
			case <-ctx.Done():
				break outer
			case sem <- struct{}{}:
			}
			wg.Add(1)
			go func(a netip.Addr, port int) {
				defer wg.Done()
				defer func() { <-sem }()
				start := time.Now()
				conn, err := dialer.DialContext(ctx, "tcp4", netip.AddrPortFrom(a, uint16(port)).String())
				rtt := time.Since(start)
				open := err == nil
				if open {
					conn.Close()
				}
				alive := open || errors.Is(err, syscall.ECONNREFUSED) || isRefused(err)
				if !alive {
					return
				}
				mu.Lock()
				r := results[a]
				if r == nil {
					r = &result{addr: a, rtt: rtt}
					results[a] = r
				}
				if open {
					r.ports = append(r.ports, port)
				}
				if rtt < r.rtt {
					r.rtt = rtt
				}
				mu.Unlock()
			}(a, port)
		}
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	arp := readARP()
	hosts := make([]protocol.DiscoveredHost, 0, len(results))
	var hmu sync.Mutex
	var ewg sync.WaitGroup
	esem := make(chan struct{}, 32)
	for _, r := range results {
		sort.Ints(r.ports)
		h := protocol.DiscoveredHost{IP: r.addr.String(), OpenPorts: r.ports, RTTMillis: int(r.rtt.Milliseconds())}
		if mac, ok := arp[r.addr]; ok {
			h.MAC = mac
			h.Vendor = VendorOf(mac)
		}
		ewg.Add(1)
		esem <- struct{}{}
		go func(h protocol.DiscoveredHost) {
			defer ewg.Done()
			defer func() { <-esem }()
			lctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			if names, err := net.DefaultResolver.LookupAddr(lctx, h.IP); err == nil && len(names) > 0 {
				h.Hostname = strings.TrimSuffix(names[0], ".")
			}
			cancel()
			if h.Hostname == "" && contains(h.OpenPorts, 139, 445) {
				h.Hostname = NetBIOSName(h.IP, time.Second)
			}
			h.DeviceType = GuessType(h)
			hmu.Lock()
			hosts = append(hosts, h)
			hmu.Unlock()
		}(h)
	}
	ewg.Wait()
	sort.Slice(hosts, func(i, j int) bool {
		a, _ := netip.ParseAddr(hosts[i].IP)
		b, _ := netip.ParseAddr(hosts[j].IP)
		return a.Less(b)
	})
	return hosts, nil
}

func isRefused(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	// Windows reports WSAECONNREFUSED (10061) which does not always unwrap
	// to syscall.ECONNREFUSED.
	return strings.Contains(s, "refused") || strings.Contains(s, "10061")
}

func contains(list []int, vals ...int) bool {
	for _, v := range vals {
		for _, l := range list {
			if l == v {
				return true
			}
		}
	}
	return false
}

// GuessType infers a device type from open ports and vendor.
func GuessType(h protocol.DiscoveredHost) string {
	v := strings.ToLower(h.Vendor)
	p := h.OpenPorts
	switch {
	case contains(p, 9100, 515, 631) || matchVendor(v, "canon", "epson", "brother", "ricoh", "kyocera", "xerox", "lexmark", "konica", "sharp", "fuji"):
		return "printer"
	case matchVendor(v, "synology", "qnap", "netapp", "asustor", "buffalo"):
		return "storage"
	case contains(p, 5000, 5001) && !contains(p, 445, 3389):
		return "storage"
	case matchVendor(v, "cisco", "aruba", "juniper", "ubiquiti", "mikrotik", "tp-link", "zyxel", "fortinet", "netgear", "d-link", "ruckus", "h3c", "huawei"):
		return "network"
	case contains(p, 3389, 135) || (contains(p, 445) && contains(p, 139)):
		return "windows"
	case contains(p, 22) && !contains(p, 445):
		return "server"
	case len(p) == 0:
		return "unknown"
	}
	return "other"
}

func matchVendor(v string, names ...string) bool {
	for _, n := range names {
		if strings.Contains(v, n) {
			return true
		}
	}
	return false
}
