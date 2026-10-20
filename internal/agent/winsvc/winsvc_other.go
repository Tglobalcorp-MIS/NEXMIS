//go:build !windows

package winsvc

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// IsService always reports false off Windows.
func IsService() bool { return false }

// Run executes run in the foreground, cancelling its context on Ctrl+C or
// SIGTERM.
func Run(name string, run func(ctx context.Context) error) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx)
}

// InstallService is Windows-only.
func InstallService(name, displayName, description, exePath string, args ...string) error {
	return ErrWindowsOnly
}

// EnsureRecovery is Windows-only.
func EnsureRecovery(name string) error { return ErrWindowsOnly }

// StartService is Windows-only.
func StartService(name string) error { return ErrWindowsOnly }

// StopService is Windows-only.
func StopService(name string, timeout time.Duration) error { return ErrWindowsOnly }

// RemoveService is Windows-only.
func RemoveService(name string) error { return ErrWindowsOnly }

// ServiceExists always reports false off Windows.
func ServiceExists(name string) bool { return false }
