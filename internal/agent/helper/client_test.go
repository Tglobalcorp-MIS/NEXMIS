package helper

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperipc"
)

// pipePair returns the service side and the helper-side Conn over net.Pipe.
func pipePair() (service *helperipc.Conn, helperSide *helperipc.Conn) {
	a, b := net.Pipe()
	return helperipc.NewConn(a), helperipc.NewConn(b)
}

func TestRunClientSendsHelloAndPong(t *testing.T) {
	svc, hlp := pipePair()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go runClient(ctx, hlp, helperipc.Hello{User: `CORP\bob`, Version: "x"}, handlers{})

	// First message must be Hello.
	env, err := svc.Recv()
	if err != nil {
		t.Fatalf("recv hello: %v", err)
	}
	if env.T != helperipc.THello {
		t.Fatalf("first msg = %q", env.T)
	}
	var h helperipc.Hello
	_ = env.Decode(&h)
	if h.User != `CORP\bob` {
		t.Fatalf("hello user = %q", h.User)
	}

	// Service pings; helper must pong.
	if err := svc.Send(helperipc.TPing, "p1", nil); err != nil {
		t.Fatalf("ping: %v", err)
	}
	got, err := svc.Recv()
	for err == nil && got.T == helperipc.TPing {
		got, err = svc.Recv()
	}
	if err != nil {
		t.Fatalf("recv pong: %v", err)
	}
	if got.T != helperipc.TPong || got.ID != "p1" {
		t.Fatalf("expected pong id p1, got %q/%q", got.T, got.ID)
	}
}

func TestRunClientConsentResponse(t *testing.T) {
	svc, hlp := pipePair()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := handlers{
		Consent: func(ctx context.Context, req helperipc.ConsentRequest) helperipc.ConsentResult {
			return helperipc.ConsentResult{SessionID: req.SessionID, Granted: true, User: `CORP\bob`}
		},
	}
	go runClient(ctx, hlp, helperipc.Hello{}, h)
	// drain hello
	if _, err := svc.Recv(); err != nil {
		t.Fatalf("hello: %v", err)
	}

	if err := svc.Send(helperipc.TConsentRequest, "c1", helperipc.ConsentRequest{SessionID: "s1", Mode: "view"}); err != nil {
		t.Fatalf("send consent: %v", err)
	}
	env := recvSkipPing(t, svc)
	if env.T != helperipc.TConsentResult || env.ID != "c1" {
		t.Fatalf("consent result = %q/%q", env.T, env.ID)
	}
	var cr helperipc.ConsentResult
	_ = env.Decode(&cr)
	if !cr.Granted || cr.SessionID != "s1" {
		t.Fatalf("consent payload: %+v", cr)
	}
}

func TestRunClientRunScriptResponse(t *testing.T) {
	svc, hlp := pipePair()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := handlers{Run: runScript}
	go runClient(ctx, hlp, helperipc.Hello{}, h)
	if _, err := svc.Recv(); err != nil {
		t.Fatalf("hello: %v", err)
	}

	if err := svc.Send(helperipc.TRunScript, "r1", helperipc.RunScript{
		ID: "r1", Shell: "cmd", Script: "echo hello-nexmis", TimeoutSec: 30,
	}); err != nil {
		t.Fatalf("send run: %v", err)
	}
	env := recvSkipPing(t, svc)
	if env.T != helperipc.TRunResult {
		t.Fatalf("run result type = %q", env.T)
	}
	var rr helperipc.RunResult
	_ = env.Decode(&rr)
	if rr.ID != "r1" {
		t.Fatalf("run result id = %q", rr.ID)
	}
	if rr.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q err=%q", rr.ExitCode, rr.Stderr, rr.Error)
	}
}

func recvSkipPing(t *testing.T, c *helperipc.Conn) helperipc.Envelope {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		env, err := c.Recv()
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if env.T == helperipc.TPing || env.T == helperipc.TPong {
			continue
		}
		return env
	}
	t.Fatal("timed out waiting for message")
	return helperipc.Envelope{}
}
