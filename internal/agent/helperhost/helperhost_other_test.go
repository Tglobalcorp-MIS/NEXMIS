//go:build !windows

package helperhost

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperipc"
)

// waitFor polls cond until true or the deadline passes.
func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestHostHelloSendReceive(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NEXMIS_AGENT_DATA", dir)

	h := New(nil, "/opt/nexmis/nexmis-agent")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Run(ctx)

	sock := filepath.Join(dir, "helper.sock")
	var conn net.Conn
	waitFor(t, 2*time.Second, func() bool {
		c, err := net.Dial("unix", sock)
		if err != nil {
			return false
		}
		conn = c
		return true
	})
	defer conn.Close()
	ipc := helperipc.NewConn(conn)

	// Helper announces itself.
	if err := ipc.Send(helperipc.THello, "", helperipc.Hello{
		User: `CORP\alice`, SessionID: 1, PID: 4321, Version: "test",
	}); err != nil {
		t.Fatalf("send hello: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool { return h.Online() })
	if got := h.User(); got != `CORP\alice` {
		t.Fatalf("User() = %q", got)
	}

	// Hello should be emitted on Events.
	select {
	case e := <-h.Events():
		if e.T != helperipc.THello {
			t.Fatalf("first event = %q, want hello", e.T)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no hello event")
	}

	// Host -> helper.
	if err := h.Send(helperipc.TNotify, "n1", helperipc.Notify{Title: "hi", Message: "there"}); err != nil {
		t.Fatalf("host send: %v", err)
	}
	got, err := ipc.Recv()
	// The host also pings every 15s; our notify should arrive well before that.
	for err == nil && got.T == helperipc.TPing {
		got, err = ipc.Recv()
	}
	if err != nil {
		t.Fatalf("helper recv: %v", err)
	}
	if got.T != helperipc.TNotify {
		t.Fatalf("helper got %q, want notify", got.T)
	}

	// Helper -> host.
	if err := ipc.Send(helperipc.TChatSend, "", helperipc.ChatSend{Body: "help me", User: `CORP\alice`}); err != nil {
		t.Fatalf("helper send: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		select {
		case e := <-h.Events():
			return e.T == helperipc.TChatSend
		default:
			return false
		}
	})
}

func TestHostPingRepliedByHelper(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NEXMIS_AGENT_DATA", dir)
	h := New(nil, "/opt/x")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Run(ctx)

	sock := filepath.Join(dir, "helper.sock")
	var conn net.Conn
	waitFor(t, 2*time.Second, func() bool {
		c, err := net.Dial("unix", sock)
		if err != nil {
			return false
		}
		conn = c
		return true
	})
	defer conn.Close()
	ipc := helperipc.NewConn(conn)
	_ = ipc.Send(helperipc.THello, "", helperipc.Hello{User: "u"})
	waitFor(t, 2*time.Second, func() bool { return h.Online() })

	// The helper pings; the host must pong.
	if err := ipc.Send(helperipc.TPing, "p1", nil); err != nil {
		t.Fatalf("ping: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		e, err := ipc.Recv()
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if e.T == helperipc.TPong {
			return
		}
	}
	t.Fatal("no pong from host")
}

func TestSendNoHelper(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NEXMIS_AGENT_DATA", dir)
	h := New(nil, "/opt/x")
	if err := h.Send(helperipc.TNotify, "", nil); err != ErrNoHelper {
		t.Fatalf("Send with no helper = %v, want ErrNoHelper", err)
	}
	if h.Online() {
		t.Fatal("Online should be false")
	}
	if h.User() != "" {
		t.Fatal("User should be empty")
	}
}
