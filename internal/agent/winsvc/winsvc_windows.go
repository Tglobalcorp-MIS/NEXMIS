//go:build windows

package winsvc

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// IsService reports whether the process was started by the SCM.
func IsService() bool {
	is, err := svc.IsWindowsService()
	return err == nil && is
}

// Run runs the service. Under the SCM it dispatches to the service control
// handler; otherwise it runs in the foreground and cancels on Ctrl+C/SIGTERM.
func Run(name string, run func(ctx context.Context) error) error {
	if !IsService() {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return run(ctx)
	}
	return svc.Run(name, &handler{run: run})
}

// handler adapts run to the svc.Handler interface.
type handler struct {
	run func(ctx context.Context) error
}

const acceptedControls = svc.AcceptStop | svc.AcceptShutdown

func (h *handler) Execute(args []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	const checkpointHint = 20000 // ms; see the 20s wait below
	s <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()

	s <- svc.Status{State: svc.Running, Accepts: acceptedControls}

	for {
		select {
		case err := <-done:
			if err != nil && ctx.Err() == nil {
				// run failed on its own; report a non-zero exit code.
				s <- svc.Status{State: svc.Stopped, Win32ExitCode: 1}
				return false, 1
			}
			s <- svc.Status{State: svc.Stopped}
			return false, 0
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				s <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				s <- svc.Status{State: svc.StopPending, WaitHint: checkpointHint}
				cancel()
				select {
				case <-done:
				case <-time.After(20 * time.Second):
				}
				s <- svc.Status{State: svc.Stopped}
				return false, 0
			}
		}
	}
}

// InstallService installs (or reconfigures) the agent as an auto-start
// (delayed), LocalSystem service with failure recovery.
func InstallService(name, displayName, description, exePath string, args ...string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect SCM: %w", err)
	}
	defer m.Disconnect()

	cfg := mgr.Config{
		DisplayName:      displayName,
		Description:      description,
		StartType:        mgr.StartAutomatic,
		DelayedAutoStart: true,
		ServiceStartName: "LocalSystem",
	}

	s, err := m.OpenService(name)
	if err == nil {
		// Already installed: update its configuration in place.
		defer s.Close()
		cfg.BinaryPathName = exePath
		if err := s.UpdateConfig(cfg); err != nil {
			return fmt.Errorf("update config: %w", err)
		}
		return setRecovery(s)
	}

	s, err = m.CreateService(name, exePath, cfg, args...)
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer s.Close()
	return setRecovery(s)
}

// setRecovery configures: restart after 1 minute, three times, reset after 1 day.
func setRecovery(s *mgr.Service) error {
	actions := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: time.Minute},
		{Type: mgr.ServiceRestart, Delay: time.Minute},
		{Type: mgr.ServiceRestart, Delay: time.Minute},
	}
	const resetPeriod = uint32(24 * 60 * 60) // seconds
	if err := s.SetRecoveryActions(actions, resetPeriod); err != nil {
		return fmt.Errorf("set recovery actions: %w", err)
	}
	return nil
}

// StartService starts the named service.
func StartService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect SCM: %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("open service: %w", err)
	}
	defer s.Close()
	return s.Start()
}

// StopService stops the named service and waits up to timeout for it to stop.
func StopService(name string, timeout time.Duration) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect SCM: %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("open service: %w", err)
	}
	defer s.Close()

	status, err := s.Control(svc.Stop)
	if err != nil {
		return fmt.Errorf("stop: %w", err)
	}
	deadline := time.Now().Add(timeout)
	for status.State != svc.Stopped {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for %q to stop", name)
		}
		time.Sleep(300 * time.Millisecond)
		if status, err = s.Query(); err != nil {
			return err
		}
	}
	return nil
}

// RemoveService deletes the named service from the SCM.
func RemoveService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect SCM: %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("open service: %w", err)
	}
	defer s.Close()
	return s.Delete()
}

// ServiceExists reports whether a service of that name is registered.
func ServiceExists(name string) bool {
	m, err := mgr.Connect()
	if err != nil {
		return false
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return false
	}
	s.Close()
	return true
}
