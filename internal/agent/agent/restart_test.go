package agent

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/config"
	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperipc"
)

type idleHost struct{}

func (idleHost) Run(ctx context.Context)              { <-ctx.Done() }
func (idleHost) Send(t, id string, payload any) error { return errors.New("no helper") }
func (idleHost) Events() <-chan helperipc.Envelope    { return nil }
func (idleHost) Online() bool                         { return false }
func (idleHost) User() string                         { return "" }

// TestRunReturnsRestartRequired checks that a restart request (made after a
// self-update replaced the executable) ends Run with ErrRestartRequired, which
// the service wrapper turns into a failure exit so the SCM restarts it.
func TestRunReturnsRestartRequired(t *testing.T) {
	cfg := &config.Config{ServerURL: "https://127.0.0.1:1", AgentID: "test", ServerSignKey: make([]byte, ed25519.PublicKeySize)}
	if _, err := cfg.NewKey(); err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{Version: "1.0.0", DataDir: t.TempDir()}, cfg,
		slog.New(slog.NewTextHandler(io.Discard, nil)), nil, idleHost{})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- a.Run(context.Background()) }()
	deadline := time.Now().Add(5 * time.Second)
	for a.stop.Load() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	a.requestRestart(0)

	select {
	case err := <-done:
		if !errors.Is(err, ErrRestartRequired) {
			t.Fatalf("Run = %v, want ErrRestartRequired", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after requestRestart")
	}
}

// TestRunStopsCleanly checks that a normal stop (service stop, Ctrl+C) still
// returns nil so the SCM does not treat it as a failure.
func TestRunStopsCleanly(t *testing.T) {
	cfg := &config.Config{ServerURL: "https://127.0.0.1:1", AgentID: "test", ServerSignKey: make([]byte, ed25519.PublicKeySize)}
	if _, err := cfg.NewKey(); err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{Version: "1.0.0", DataDir: t.TempDir()}, cfg,
		slog.New(slog.NewTextHandler(io.Discard, nil)), nil, idleHost{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop on cancel")
	}
}
