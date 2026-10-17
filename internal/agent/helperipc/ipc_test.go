package helperipc

import (
	"net"
	"strings"
	"testing"
)

func TestConnRoundTrip(t *testing.T) {
	a, b := net.Pipe()
	ca, cb := NewConn(a), NewConn(b)
	defer ca.Close()
	defer cb.Close()
	big := strings.Repeat("x", 200000)
	go func() {
		_ = ca.Send(TRunScript, "1", RunScript{ID: "1", Shell: "powershell", Script: big})
	}()
	e, err := cb.Recv()
	if err != nil {
		t.Fatal(err)
	}
	var rs RunScript
	if err := e.Decode(&rs); err != nil || e.T != TRunScript || len(rs.Script) != len(big) {
		t.Fatalf("got %s %v %d", e.T, err, len(rs.Script))
	}
}
