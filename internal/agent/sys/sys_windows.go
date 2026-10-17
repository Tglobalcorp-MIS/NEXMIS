//go:build windows

package sys

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

var (
	modsas         = windows.NewLazySystemDLL("sas.dll")
	modwtsapi32    = windows.NewLazySystemDLL("wtsapi32.dll")
	procSendSAS    = modsas.NewProc("SendSAS")
	procWTSLogoff  = modwtsapi32.NewProc("WTSLogoffSession")
	seShutdownName = "SeShutdownPrivilege"
)

// InstallDir returns %ProgramFiles%\NEXMIS\Agent.
func InstallDir() string {
	pf := os.Getenv("ProgramFiles")
	if pf == "" {
		pf = `C:\Program Files`
	}
	return filepath.Join(pf, "NEXMIS", "Agent")
}

// DataDir returns %ProgramData%\NEXMIS\Agent.
func DataDir() string {
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	return filepath.Join(pd, "NEXMIS", "Agent")
}

// Protect encrypts b with DPAPI at machine scope (CRYPTPROTECT_LOCAL_MACHINE).
func Protect(b []byte) ([]byte, error) {
	in := windows.DataBlob{Size: uint32(len(b))}
	if len(b) > 0 {
		in.Data = &b[0]
	}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_LOCAL_MACHINE, &out); err != nil {
		return nil, fmt.Errorf("CryptProtectData: %w", err)
	}
	defer localFree(out.Data)
	return blobBytes(out), nil
}

// Unprotect reverses Protect.
func Unprotect(b []byte) ([]byte, error) {
	in := windows.DataBlob{Size: uint32(len(b))}
	if len(b) > 0 {
		in.Data = &b[0]
	}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_LOCAL_MACHINE, &out); err != nil {
		return nil, fmt.Errorf("CryptUnprotectData: %w", err)
	}
	defer localFree(out.Data)
	return blobBytes(out), nil
}

func blobBytes(b windows.DataBlob) []byte {
	if b.Size == 0 || b.Data == nil {
		return []byte{}
	}
	src := unsafe.Slice(b.Data, b.Size)
	out := make([]byte, b.Size)
	copy(out, src)
	return out
}

func localFree(p *byte) {
	if p != nil {
		windows.LocalFree(windows.Handle(unsafe.Pointer(p)))
	}
}

// SecureDir creates path and sets a protected DACL granting full control only
// to SYSTEM and the Administrators group, with inheritance removed.
func SecureDir(path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	// D: DACL, P: protected (no inheritance), entries grant FA (full access),
	// OICI: object+container inherit, to SY (local system) and BA (admins).
	const sddl = "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return fmt.Errorf("parse sddl: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("get dacl: %w", err)
	}
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil)
	if err != nil {
		return fmt.Errorf("set security info: %w", err)
	}
	return nil
}

// ScriptCommand builds (but does not start) the command that runs script.
func ScriptCommand(ctx context.Context, shell, script string, env map[string]string) (*exec.Cmd, func(), error) {
	cleanup := func() {}
	var cmd *exec.Cmd
	switch strings.ToLower(shell) {
	case "powershell", "ps", "ps1", "":
		const prelude = "$ProgressPreference='SilentlyContinue'; [Console]::OutputEncoding=[Text.Encoding]::UTF8;\r\n"
		enc := encodeUTF16LEBase64(prelude + script)
		cmd = exec.CommandContext(ctx, "powershell.exe",
			"-NoLogo", "-NoProfile", "-NonInteractive",
			"-ExecutionPolicy", "Bypass", "-OutputFormat", "Text",
			"-EncodedCommand", enc)
	case "cmd":
		tmpDir := filepath.Join(DataDir(), "tmp")
		if err := os.MkdirAll(tmpDir, 0o755); err != nil {
			return nil, nil, err
		}
		f, err := os.CreateTemp(tmpDir, "nexmis-*.cmd")
		if err != nil {
			return nil, nil, err
		}
		body := "@echo off\r\nchcp 65001 >nul\r\n" + normalizeCRLF(script) + "\r\n"
		if _, err := f.WriteString(body); err != nil {
			f.Close()
			os.Remove(f.Name())
			return nil, nil, err
		}
		f.Close()
		name := f.Name()
		cleanup = func() { os.Remove(name) }
		cmd = exec.CommandContext(ctx, "cmd.exe", "/d", "/c", name)
	default:
		return nil, nil, fmt.Errorf("unknown shell %q", shell)
	}
	cmd.Env = append(os.Environ(), flattenEnv(env)...)
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd, cleanup, nil
}

func flattenEnv(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

func normalizeCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// encodeUTF16LEBase64 encodes s as UTF-16LE then base64 (PowerShell
// -EncodedCommand format).
func encodeUTF16LEBase64(s string) string {
	u := utf16.Encode([]rune(s))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		b[i*2] = byte(v)
		b[i*2+1] = byte(v >> 8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// StartTracked starts cmd inside a Job Object configured to kill the whole
// process tree when the job handle is closed (i.e. when kill runs).
func StartTracked(cmd *exec.Cmd) (func(), error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("CreateJobObject: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err = windows.SetInformationJobObject(job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("SetInformationJobObject: %w", err)
	}
	if err = cmd.Start(); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	// Assign the newly started process (and thus its children) to the job.
	// If it cannot be tracked, stop it now: an untracked process would
	// escape the timeout and outlive the reported failure.
	abort := func(err error) (func(), error) {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		windows.CloseHandle(job)
		return nil, err
	}
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return abort(fmt.Errorf("OpenProcess: %w", err))
	}
	if err = windows.AssignProcessToJobObject(job, ph); err != nil {
		windows.CloseHandle(ph)
		return abort(fmt.Errorf("AssignProcessToJobObject: %w", err))
	}
	windows.CloseHandle(ph)
	kill := func() {
		// Closing the job handle terminates every process in the job.
		windows.CloseHandle(job)
	}
	return kill, nil
}

// Reboot reboots or shuts the machine down after enabling SeShutdownPrivilege.
func Reboot(shutdown, force bool, reason string) error {
	if err := enablePrivilege(seShutdownName); err != nil {
		return err
	}
	var msg *uint16
	if reason != "" {
		msg = windows.StringToUTF16Ptr(reason)
	}
	rebootAfter := !shutdown
	code := uint32(windows.SHTDN_REASON_MAJOR_OPERATINGSYSTEM | windows.SHTDN_REASON_MINOR_MAINTENANCE | windows.SHTDN_REASON_FLAG_PLANNED)
	// timeout 0 performs the action immediately; the helper handles countdown.
	return windows.InitiateSystemShutdownEx(nil, msg, 0, force, rebootAfter, code)
}

func enablePrivilege(name string) error {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(),
		windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &token); err != nil {
		return fmt.Errorf("OpenProcessToken: %w", err)
	}
	defer token.Close()
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr(name), &luid); err != nil {
		return fmt.Errorf("LookupPrivilegeValue(%s): %w", name, err)
	}
	tp := windows.Tokenprivileges{PrivilegeCount: 1}
	tp.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}
	if err := windows.AdjustTokenPrivileges(token, false, &tp, 0, nil, nil); err != nil {
		return fmt.Errorf("AdjustTokenPrivileges(%s): %w", name, err)
	}
	// AdjustTokenPrivileges succeeds even if not all privileges were assigned.
	if err := windows.GetLastError(); err != nil && !errors.Is(err, windows.ERROR_SUCCESS) {
		return fmt.Errorf("privilege %s not held: %w", name, err)
	}
	return nil
}

// Logoff logs off the active console session.
func Logoff() error {
	session := windows.WTSGetActiveConsoleSessionId()
	if session == 0xFFFFFFFF {
		return errors.New("no active console session")
	}
	r, _, e := procWTSLogoff.Call(0, uintptr(session), 1 /* bWait */)
	if r == 0 {
		return fmt.Errorf("WTSLogoffSession: %w", e)
	}
	return nil
}

// SendSAS simulates the Secure Attention Sequence (Ctrl+Alt+Del). It must be
// called from the service process. If software SAS generation is disabled by
// policy it enables it (safe: only lets a service raise SAS) and retries once.
func SendSAS() error {
	if err := procSendSAS.Find(); err != nil {
		return fmt.Errorf("sas.dll SendSAS unavailable: %w", err)
	}
	call := func() error {
		// SendSAS(FALSE): generate SAS for the current (services) context.
		r, _, e := procSendSAS.Call(0)
		if r == 0 {
			if e != nil && !errors.Is(e, windows.ERROR_SUCCESS) {
				return e
			}
			return errors.New("SendSAS failed")
		}
		return nil
	}
	err := call()
	if err == nil {
		return nil
	}
	// SoftwareSASGeneration bit 2 (value 2) allows services to raise SAS.
	if perr := allowServiceSAS(); perr != nil {
		return fmt.Errorf("SendSAS failed and could not enable SoftwareSASGeneration: %v (original: %w)", perr, err)
	}
	if err2 := call(); err2 != nil {
		return fmt.Errorf("SendSAS failed after enabling SoftwareSASGeneration; check group policy 'Enable software Secure Attention Sequence': %w", err2)
	}
	return nil
}

func allowServiceSAS() error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System`,
		registry.SET_VALUE|registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return err
	}
	defer k.Close()
	// 1 = services only (the safe setting), 2 = services and ease of access, 3 = both.
	return k.SetDWordValue("SoftwareSASGeneration", 1)
}

// RestartService stops the named service (waiting for Stopped, stopping any
// running dependent services first) and starts it again, then restarts the
// dependents it stopped, via the SCM.
func RestartService(ctx context.Context, name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect SCM: %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("open service %q: %w", name, err)
	}
	defer s.Close()

	// Stop running dependents first. The list is ordered so that each
	// service comes before the services it depends on.
	deps, _ := s.ListDependentServices(svc.Active)
	var stopped []string
	for _, dep := range deps {
		if ds, err := m.OpenService(dep); err == nil {
			if stopAndWait(ctx, ds) == nil {
				stopped = append(stopped, dep)
			}
			ds.Close()
		}
	}
	if err := stopAndWait(ctx, s); err != nil {
		return err
	}
	if err := s.Start(); err != nil {
		return fmt.Errorf("start service %q: %w", name, err)
	}
	var failed []string
	for i := len(stopped) - 1; i >= 0; i-- {
		ds, err := m.OpenService(stopped[i])
		if err == nil {
			err = ds.Start()
			ds.Close()
		}
		if err != nil {
			failed = append(failed, stopped[i])
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("service %q restarted, but dependent services did not start: %s", name, strings.Join(failed, ", "))
	}
	return nil
}

func stopAndWait(ctx context.Context, s *mgr.Service) error {
	status, err := s.Query()
	if err != nil {
		return err
	}
	if status.State != svc.Stopped {
		if status.State != svc.StopPending {
			if status, err = s.Control(svc.Stop); err != nil {
				return fmt.Errorf("stop %q: %w", s.Name, err)
			}
		}
		deadline := time.Now().Add(30 * time.Second)
		for status.State != svc.Stopped {
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for %q to stop", s.Name)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(300 * time.Millisecond):
			}
			if status, err = s.Query(); err != nil {
				return err
			}
		}
	}
	return nil
}

// KillProcess terminates a process by PID or image name (case-insensitive,
// ".exe" optional), refusing critical system processes and itself. It returns
// the number of processes terminated.
func KillProcess(pid int, name string) (int, error) {
	self := os.Getpid()
	if pid != 0 {
		if isProtectedPID(pid) || pid == self {
			return 0, errors.New("refusing to kill a protected process")
		}
		if n := imageNameOfPID(uint32(pid)); n != "" && isProtectedName(n) {
			return 0, errors.New("refusing to kill a protected process")
		}
		if err := terminatePID(uint32(pid)); err != nil {
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
	pids, err := pidsByName(name)
	if err != nil {
		return 0, err
	}
	killed := 0
	for _, p := range pids {
		if int(p) == self || isProtectedPID(int(p)) {
			continue
		}
		if terminatePID(p) == nil {
			killed++
		}
	}
	if killed == 0 {
		return 0, errors.New("no matching process found")
	}
	return killed, nil
}

func terminatePID(pid uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return fmt.Errorf("OpenProcess(%d): %w", pid, err)
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		return fmt.Errorf("TerminateProcess(%d): %w", pid, err)
	}
	return nil
}

func imageNameOfPID(pid uint32) string {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	if err := windows.Process32First(snap, &e); err != nil {
		return ""
	}
	for {
		if e.ProcessID == pid {
			return windows.UTF16ToString(e.ExeFile[:])
		}
		if err := windows.Process32Next(snap, &e); err != nil {
			return ""
		}
	}
}

func pidsByName(name string) ([]uint32, error) {
	want := normalizeImageName(name)
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	if err := windows.Process32First(snap, &e); err != nil {
		return nil, err
	}
	var out []uint32
	for {
		if normalizeImageName(windows.UTF16ToString(e.ExeFile[:])) == want {
			out = append(out, e.ProcessID)
		}
		if err := windows.Process32Next(snap, &e); err != nil {
			break
		}
	}
	return out, nil
}

// InstallCommand builds the installation command for a package by type.
func InstallCommand(ctx context.Context, path, typ, args string) (*exec.Cmd, error) {
	logDir := filepath.Join(DataDir(), "logs")
	_ = os.MkdirAll(logDir, 0o755)
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	var cmd *exec.Cmd
	switch strings.ToLower(typ) {
	case "msi":
		logPath := filepath.Join(logDir, "install-"+base+".log")
		argv := []string{"/i", path}
		argv = append(argv, splitArgs(args)...)
		argv = append(argv, "/l*v", logPath)
		cmd = exec.CommandContext(ctx, "msiexec.exe", argv...)
	case "exe":
		cmd = exec.CommandContext(ctx, path, splitArgs(args)...)
	case "msix":
		// Provisioning installs for every user. Add-AppxPackage is not a
		// fallback: run as SYSTEM it would register the app for SYSTEM only.
		ps := fmt.Sprintf(
			`$ErrorActionPreference='Stop'; Add-AppxProvisionedPackage -Online -PackagePath %q -SkipLicense | Out-Null`,
			path)
		cmd = exec.CommandContext(ctx, "powershell.exe",
			"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
			"-Command", ps)
	case "ps1":
		cmd = exec.CommandContext(ctx, "powershell.exe",
			"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
			"-File", path)
		cmd.Args = append(cmd.Args, splitArgs(args)...)
	default:
		return nil, fmt.Errorf("unknown package type %q", typ)
	}
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd, nil
}

// UninstallCommand looks up the QuietUninstallString / UninstallString for an
// uninstall registry key name (searching 64- and 32-bit HKLM Uninstall hives)
// and builds the command. MsiExec entries are rewritten to a silent /x form.
func UninstallCommand(ctx context.Context, uninstallID, args string) (*exec.Cmd, error) {
	cmdline, err := lookupUninstallString(uninstallID)
	if err != nil {
		return nil, err
	}
	if g := msiProductGUID(cmdline); g != "" {
		argv := []string{"/x", g, "/qn", "/norestart"}
		argv = append(argv, splitArgs(args)...)
		cmd := exec.CommandContext(ctx, "msiexec.exe", argv...)
		cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
		return cmd, nil
	}
	exe, rest := splitCommandLine(cmdline)
	if exe == "" {
		return nil, fmt.Errorf("empty uninstall string for %q", uninstallID)
	}
	rest = append(rest, splitArgs(args)...)
	cmd := exec.CommandContext(ctx, exe, rest...)
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd, nil
}

func lookupUninstallString(id string) (string, error) {
	roots := []struct {
		path   string
		access uint32
	}{
		{`SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\` + id, registry.QUERY_VALUE | registry.WOW64_64KEY},
		{`SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\` + id, registry.QUERY_VALUE | registry.WOW64_32KEY},
	}
	type loc struct {
		root   registry.Key
		path   string
		access uint32
	}
	var locs []loc
	for _, r := range roots {
		locs = append(locs, loc{registry.LOCAL_MACHINE, r.path, r.access})
	}
	// Per-user installs live in each loaded user hive.
	if hku, err := registry.OpenKey(registry.USERS, "", registry.ENUMERATE_SUB_KEYS); err == nil {
		sids, _ := hku.ReadSubKeyNames(-1)
		hku.Close()
		for _, sid := range sids {
			if !strings.HasSuffix(sid, "_Classes") {
				locs = append(locs, loc{registry.USERS, sid + `\` + roots[0].path, registry.QUERY_VALUE})
			}
		}
	}
	for _, l := range locs {
		k, err := registry.OpenKey(l.root, l.path, l.access)
		if err != nil {
			continue
		}
		for _, v := range []string{"QuietUninstallString", "UninstallString"} {
			if s, _, err := k.GetStringValue(v); err == nil && s != "" {
				k.Close()
				return s, nil
			}
		}
		k.Close()
	}
	return "", fmt.Errorf("uninstall entry %q not found", id)
}

// msiProductGUID returns the {GUID} if cmdline is an MsiExec uninstall string.
func msiProductGUID(cmdline string) string {
	low := strings.ToLower(cmdline)
	if !strings.Contains(low, "msiexec") {
		return ""
	}
	start := strings.IndexByte(cmdline, '{')
	end := strings.IndexByte(cmdline, '}')
	if start >= 0 && end > start {
		return cmdline[start : end+1]
	}
	return ""
}

// ReplaceAndRestart performs an in-place self-update: it renames the running
// executable to ".old", moves newExe into its place, then schedules the
// service to restart via a detached helper process and exits so the service
// recovery/restart takes over.
func ReplaceAndRestart(newExe, serviceName string) error {
	cur, err := os.Executable()
	if err != nil {
		return err
	}
	cur, _ = filepath.Abs(cur)
	oldPath := cur + ".old"
	_ = os.Remove(oldPath)
	// A running exe can be renamed on Windows even while executing.
	if err := os.Rename(cur, oldPath); err != nil {
		return fmt.Errorf("rename current exe: %w", err)
	}
	if err := os.Rename(newExe, cur); err != nil {
		// Roll back so the service can still start.
		_ = os.Rename(oldPath, cur)
		return fmt.Errorf("move new exe into place: %w", err)
	}
	// Detached restarter: wait for this process to exit, then (re)start the
	// service. The service's SCM restart will pick up the new binary.
	restart := exec.Command("cmd.exe", "/c",
		fmt.Sprintf(`timeout /t 3 /nobreak >nul & sc stop %q >nul & timeout /t 2 /nobreak >nul & sc start %q >nul`, serviceName, serviceName))
	restart.SysProcAttr = &windows.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW | windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
	if err := restart.Start(); err != nil {
		return fmt.Errorf("spawn restarter: %w", err)
	}
	_ = restart.Process.Release()
	return nil
}

// CleanupOldBinary removes the ".old" file left by a previous self-update.
func CleanupOldBinary() {
	cur, err := os.Executable()
	if err != nil {
		return
	}
	cur, _ = filepath.Abs(cur)
	_ = os.Remove(cur + ".old")
}
