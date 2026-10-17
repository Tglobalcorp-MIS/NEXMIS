package discovery

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

func TestExpandCIDRs(t *testing.T) {
	a, err := ExpandCIDRs([]string{"192.168.1.0/30", "192.168.1.1", "10.0.0.5"})
	if err != nil {
		t.Fatal(err)
	}
	// /30 -> .1 .2 (network/broadcast excluded); .1 deduped; plus 10.0.0.5
	if len(a) != 3 {
		t.Fatalf("got %v", a)
	}
	if _, err := ExpandCIDRs([]string{"10.0.0.0/8"}); err == nil {
		t.Fatal("expected too-large error")
	}
	if _, err := ExpandCIDRs([]string{"bogus"}); err == nil {
		t.Fatal("expected parse error")
	}
	if _, err := ExpandCIDRs([]string{"fe80::/120"}); err == nil {
		t.Fatal("expected ipv6 error")
	}
	b, _ := ExpandCIDRs([]string{"10.1.0.0/24"})
	if len(b) != 254 || b[0].String() != "10.1.0.1" || b[253].String() != "10.1.0.254" {
		t.Fatalf("unexpected /24 expansion: %d %v %v", len(b), b[0], b[len(b)-1])
	}
}

func TestScanLocalhost(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	hosts, err := Scan(context.Background(), []netip.Addr{netip.MustParseAddr("127.0.0.1")},
		Options{Ports: []int{port, 1}, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || len(hosts[0].OpenPorts) != 1 || hosts[0].OpenPorts[0] != port {
		t.Fatalf("got %+v", hosts)
	}
}

func TestGuessType(t *testing.T) {
	cases := []struct {
		h    protocol.DiscoveredHost
		want string
	}{
		{protocol.DiscoveredHost{OpenPorts: []int{80, 9100}}, "printer"},
		{protocol.DiscoveredHost{OpenPorts: []int{135, 445, 3389}}, "windows"},
		{protocol.DiscoveredHost{OpenPorts: []int{22, 443}, Vendor: "Ubiquiti"}, "network"},
		{protocol.DiscoveredHost{OpenPorts: []int{5000, 5001}}, "storage"},
		{protocol.DiscoveredHost{OpenPorts: []int{22}}, "server"},
		{protocol.DiscoveredHost{}, "unknown"},
	}
	for _, c := range cases {
		if got := GuessType(c.h); got != c.want {
			t.Errorf("%+v: got %s want %s", c.h, got, c.want)
		}
	}
}

func TestParseNBSTAT(t *testing.T) {
	b := make([]byte, 56)
	b[7] = 1 // one answer
	b = append(b, 2)
	name1 := []byte("WORKGROUP      ")
	b = append(b, name1...)
	b = append(b, 0x00, 0x84, 0x00) // group name
	name2 := []byte("PC-ACC-01      ")
	b = append(b, name2...)
	b = append(b, 0x00, 0x04, 0x00)
	if got := parseNBSTAT(b); got != "PC-ACC-01" {
		t.Fatalf("got %q", got)
	}
}

func TestVendorOf(t *testing.T) {
	if VendorOf("00-11-32-AA-BB-CC") != "Synology" {
		t.Fatal("expected Synology")
	}
}
