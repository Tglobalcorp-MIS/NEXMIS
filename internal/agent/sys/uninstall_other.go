//go:build !windows

package sys

import "os"

// RegisterUninstall is Windows-only.
func RegisterUninstall(e UninstallEntry) error { return errWindowsOnly }

// UnregisterUninstall is a no-op outside Windows.
func UnregisterUninstall() error { return nil }

// StopAgentProcesses is a no-op outside Windows.
func StopAgentProcesses(dir string) int { return 0 }

// RemoveUserData is a no-op outside Windows.
func RemoveUserData() (int, error) { return 0, nil }

// RemoveMachineFiles deletes the data directory.
func RemoveMachineFiles() error { return os.RemoveAll(DataDir()) }

// ScheduleRemoveInstallDir is Windows-only.
func ScheduleRemoveInstallDir(dir string) error { return errWindowsOnly }

// IsElevated reports whether the process runs as root.
func IsElevated() bool { return os.Geteuid() == 0 }

// RunElevated is Windows-only.
func RunElevated(args []string) (int, error) { return 1, errWindowsOnly }

// ShowMessage is a no-op outside Windows.
func ShowMessage(title, text string, warning bool) {}
