//go:build !windows

package sys

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// errWindowsOnly is returned by operations that only make sense on Windows.
var errWindowsOnly = errors.New("only supported on Windows")

// InstallDir returns the directory of the running executable.
func InstallDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// DataDir returns $NEXMIS_AGENT_DATA, or ./agent-data by default.
func DataDir() string {
	if d := os.Getenv("NEXMIS_AGENT_DATA"); d != "" {
		return d
	}
	return "agent-data"
}

// Protect is a no-op outside Windows (returns a copy).
func Protect(b []byte) ([]byte, error) {
	out := make([]byte, len(b))
	copy(out, b)
	return out, nil
}

// Unprotect is a no-op outside Windows (returns a copy).
func Unprotect(b []byte) ([]byte, error) {
	out := make([]byte, len(b))
	copy(out, b)
	return out, nil
}

// SecureDir creates a 0700 directory (no Windows ACLs off-platform).
func SecureDir(path string) error {
	return os.MkdirAll(path, 0o700)
}

// ScriptCommand builds (but does not start) a command running script in a
// POSIX shell. For shell=="powershell" it uses pwsh when available.
func ScriptCommand(ctx context.Context, shell, script string, env map[string]string) (*exec.Cmd, func(), error) {
	cleanup := func() {}
	var cmd *exec.Cmd
	if shell == "powershell" {
		if pwsh, err := exec.LookPath("pwsh"); err == nil {
			cmd = exec.CommandContext(ctx, pwsh, "-NoLogo", "-NonInteractive", "-NoProfile", "-Command", script)
		}
	}
	if cmd == nil {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", script)
	}
	cmd.Env = append(os.Environ(), flattenEnv(env)...)
	return cmd, cleanup, nil
}

func flattenEnv(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// StartTracked starts cmd in its own process group and returns a kill that
// terminates the whole group.
func StartTracked(cmd *exec.Cmd) (func(), error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	pid := cmd.Process.Pid
	kill := func() {
		// Negative pid signals the whole process group.
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = cmd.Process.Kill()
	}
	return kill, nil
}

// Reboot is Windows-only.
func Reboot(shutdown, force bool, reason string) error { return errWindowsOnly }

// Logoff is Windows-only.
func Logoff() error { return errWindowsOnly }

// SendSAS is Windows-only.
func SendSAS() error { return errWindowsOnly }

// RestartService is Windows-only.
func RestartService(ctx context.Context, name string) error { return errWindowsOnly }

// KillProcess terminates a process by PID or image name. The protected-process
// rules match the Windows build so tests exercise the same logic.
func KillProcess(pid int, name string) (int, error) {
	self := os.Getpid()
	if pid != 0 {
		if isProtectedPID(pid) || pid == self {
			return 0, errors.New("refusing to kill a protected process")
		}
		p, err := os.FindProcess(pid)
		if err != nil {
			return 0, err
		}
		if err := p.Kill(); err != nil {
			return 0, err
		}
		return 1, nil
	}
	if name == "" {
		return 0, errors.New("no pid or name given")
	}
	if isProtectedName(name) {
		return 0, errors.New("refusing to kill a protected process")
	}
	killed := 0
	for _, p := range listByName(name) {
		if p == self || isProtectedPID(p) {
			continue
		}
		if proc, err := os.FindProcess(p); err == nil {
			if proc.Kill() == nil {
				killed++
			}
		}
	}
	if killed == 0 {
		return 0, errors.New("no matching process found")
	}
	return killed, nil
}

// listByName returns PIDs on Linux whose /proc/<pid>/comm matches name.
func listByName(name string) []int {
	want := normalizeImageName(name)
	var out []int
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return out
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		comm, err := os.ReadFile(filepath.Join("/proc", e.Name(), "comm"))
		if err != nil {
			continue
		}
		if normalizeImageName(strings.TrimSpace(string(comm))) == want {
			out = append(out, pid)
		}
	}
	return out
}

// InstallCommand is Windows-only.
func InstallCommand(ctx context.Context, path, typ, args string) (*exec.Cmd, error) {
	return nil, errWindowsOnly
}

// UninstallCommand is Windows-only.
func UninstallCommand(ctx context.Context, uninstallID, args string) (*exec.Cmd, error) {
	return nil, errWindowsOnly
}

// ReplaceBinary is Windows-only.
func ReplaceBinary(newExe string) error { return errWindowsOnly }

// CleanupOldBinary is a no-op outside Windows.
func CleanupOldBinary() {}
