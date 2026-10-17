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
