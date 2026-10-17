//go:build !windows

package discovery

import (
	"bufio"
	"net/netip"
	"os"
	"strings"
)

// readARP parses the Linux neighbour table.
func readARP() map[netip.Addr]string {
	out := map[netip.Addr]string{}
	f, err := os.Open("/proc/net/arp")
	if err != nil {
		return out
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Scan() // header
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) < 4 || fields[3] == "00:00:00:00:00:00" {
			continue
		}
		if a, err := netip.ParseAddr(fields[0]); err == nil {
			out[a] = strings.ToLower(fields[3])
		}
	}
	return out
}
