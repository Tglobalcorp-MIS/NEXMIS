//go:build windows

package sys

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	modshell32          = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteExW = modshell32.NewProc("ShellExecuteExW")
)

// RegisterUninstall creates or refreshes the "Settings → Apps" entry.
func RegisterUninstall(e UninstallEntry) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, UninstallKey, registry.SET_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return err
	}
	defer k.Close()
	strs, dwords := UninstallValues(e)
	for name, v := range strs {
		if err := k.SetStringValue(name, v); err != nil {
			return err
		}
	}
	for name, v := range dwords {
		if err := k.SetDWordValue(name, v); err != nil {
			return err
		}
	}
	return nil
}

// UnregisterUninstall removes the "Settings → Apps" entry (no error when it
// does not exist).
func UnregisterUninstall() error {
	err := registry.DeleteKey(registry.LOCAL_MACHINE, UninstallKey)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	return err
}

// StopAgentProcesses terminates every nexmis-agent.exe started from dir
// (the service and the per-session --user-helper processes) except the
// calling process and its parent (the unelevated copy that asked for
// elevation), and returns how many it stopped.
func StopAgentProcesses(dir string) int {
	pids, err := pidsByName("nexmis-agent.exe")
	if err != nil {
		return 0
	}
	self, parent := uint32(os.Getpid()), uint32(os.Getppid())
	n := 0
	for _, pid := range pids {
		if pid == self || pid == parent {
			continue
		}
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid)
		if err != nil {
			continue
		}
		buf := make([]uint16, windows.MAX_LONG_PATH)
		size := uint32(len(buf))
		if windows.QueryFullProcessImageName(h, 0, &buf[0], &size) == nil &&
			strings.EqualFold(filepath.Dir(windows.UTF16ToString(buf[:size])), filepath.Clean(dir)) &&
			windows.TerminateProcess(h, 1) == nil {
			_, _ = windows.WaitForSingleObject(h, 5000)
			n++
		}
		windows.CloseHandle(h)
	}
	return n
}

// RemoveUserData deletes %LOCALAPPDATA%\NEXMIS (the helper's log) of every
// local profile and returns how many folders it removed.
func RemoveUserData() (int, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList`,
		registry.ENUMERATE_SUB_KEYS|registry.WOW64_64KEY)
	if err != nil {
		return 0, err
	}
	defer k.Close()
	sids, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, sid := range sids {
		pk, err := registry.OpenKey(k, sid, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		p, _, err := pk.GetStringValue("ProfileImagePath")
		pk.Close()
		if err != nil || p == "" {
			continue
		}
		if x, err := registry.ExpandString(p); err == nil {
			p = x
		}
		dir := filepath.Join(p, "AppData", "Local", "NEXMIS")
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			errs = append(errs, err)
			continue
		}
		n++
	}
	return n, errors.Join(errs...)
}

// RemoveMachineFiles deletes the data directory (configuration, identity,
// logs) and %ProgramData%\NEXMIS\install.log, then %ProgramData%\NEXMIS
// itself when nothing else is left in it.
func RemoveMachineFiles() error {
	data := DataDir()
	if err := os.RemoveAll(data); err != nil {
		return err
	}
	parent := filepath.Dir(data)
	if err := os.Remove(filepath.Join(parent, "install.log")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_ = os.Remove(parent) // only succeeds when empty
	return nil
}

// ScheduleRemoveInstallDir deletes dir, and its parent when empty, after
// the calling process (which may run from dir) has exited: a detached
// cmd.exe waits a few seconds, then removes them. As a fallback the
// remaining files are also marked for deletion at the next reboot.
func ScheduleRemoveInstallDir(dir string) error {
	dir = filepath.Clean(dir)
	parent := filepath.Dir(dir)
	sysdir := filepath.Join(os.Getenv("SystemRoot"), "System32")
	cmdExe := filepath.Join(sysdir, "cmd.exe")
	c := exec.Command(cmdExe)
	c.Dir = sysdir
	c.SysProcAttr = &windows.SysProcAttr{
		CmdLine: fmt.Sprintf(`"%s" /d /s /c "ping -n 4 127.0.0.1 >nul & rmdir /s /q "%s" & rmdir "%s""`,
			cmdExe, dir, parent),
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW | windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
	startErr := c.Start()
	if startErr == nil {
		_ = c.Process.Release()
	}

	// Reboot fallback: files first, then the (by then empty) directories.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() {
			_ = deleteAtReboot(filepath.Join(dir, e.Name()))
		}
	}
	_ = deleteAtReboot(dir)
	_ = deleteAtReboot(parent)
	return startErr
}

func deleteAtReboot(path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(p, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
}

// IsElevated reports whether the process runs with an elevated token.
func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

type shellExecuteInfo struct {
	cbSize         uint32
	fMask          uint32
	hwnd           uintptr
	lpVerb         *uint16
	lpFile         *uint16
	lpParameters   *uint16
	lpDirectory    *uint16
	nShow          int32
	hInstApp       uintptr
	lpIDList       uintptr
	lpClass        *uint16
	hkeyClass      uintptr
	dwHotKey       uint32
	hIconOrMonitor uintptr
	hProcess       windows.Handle
}

// RunElevated starts this executable again with args through a UAC prompt,
// waits for it and returns its exit code.
func RunElevated(args []string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 1, err
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(windows.ComposeCommandLine(args))
	const seeMaskNoCloseProcess = 0x40
	info := shellExecuteInfo{fMask: seeMaskNoCloseProcess, lpVerb: verb, lpFile: file, lpParameters: params, nShow: windows.SW_SHOWNORMAL}
	info.cbSize = uint32(unsafe.Sizeof(info))
	if r, _, err := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		return 1, err // includes the user declining the UAC prompt
	}
	if info.hProcess == 0 {
		return 1, errors.New("no process handle")
	}
	defer windows.CloseHandle(info.hProcess)
	if _, err := windows.WaitForSingleObject(info.hProcess, windows.INFINITE); err != nil {
		return 1, err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.hProcess, &code); err != nil {
		return 1, err
	}
	return int(code), nil
}

// ShowMessage shows a message box (used when started from Settings, where
// the console window closes as soon as the process exits).
func ShowMessage(title, text string, warning bool) {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString(title)
	var flags uint32 = windows.MB_OK | windows.MB_SETFOREGROUND | windows.MB_TOPMOST
	if warning {
		flags |= windows.MB_ICONWARNING
	} else {
		flags |= windows.MB_ICONINFORMATION
	}
	_, _ = windows.MessageBox(0, t, c, flags)
}
