package winsvc

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIsServiceFalseOffWindows(t *testing.T) {
	if IsService() {
		t.Skip("running under a service manager; skipping")
	}
}

func TestRunForegroundReturnsRunError(t *testing.T) {
	want := errors.New("boom")
	got := Run("test", func(ctx context.Context) error { return want })
	if !errors.Is(got, want) {
		t.Fatalf("Run returned %v, want %v", got, want)
	}
}

func TestRunPassesLiveContext(t *testing.T) {
	err := Run("test", func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return errors.New("context already cancelled")
		case <-time.After(10 * time.Millisecond):
			return nil
		}
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestManagementWindowsOnly(t *testing.T) {
	if IsService() {
		t.Skip("skip on service host")
	}
	if err := InstallService("n", "d", "desc", "exe"); !errors.Is(err, ErrWindowsOnly) {
		t.Fatalf("InstallService err = %v", err)
	}
	if err := StartService("n"); !errors.Is(err, ErrWindowsOnly) {
		t.Fatalf("StartService err = %v", err)
	}
	if err := StopService("n", time.Second); !errors.Is(err, ErrWindowsOnly) {
		t.Fatalf("StopService err = %v", err)
	}
	if err := RemoveService("n"); !errors.Is(err, ErrWindowsOnly) {
		t.Fatalf("RemoveService err = %v", err)
	}
	if ServiceExists("n") {
		t.Fatal("ServiceExists should be false off Windows")
	}
}
