// Package sys wraps the operating-system operations the NEXMIS agent needs:
// install/data directories, secret protection, ACLs, script execution with
// process-tree tracking, power control, service control and package
// install/uninstall. The Windows build uses Win32 APIs directly (no cgo, so
// it cross-compiles for amd64 and arm64); other platforms get a best-effort
// development implementation so integration tests can run on Linux.
package sys

import "strings"

// protectedProcesses are image names that KillProcess refuses to terminate,
// because doing so bluescreens or destabilises Windows. Compared
// case-insensitively, with or without the ".exe" suffix.
var protectedProcesses = map[string]bool{
	"csrss":    true,
	"wininit":  true,
	"winlogon": true,
	"lsass":    true,
	"services": true,
	"smss":     true,
	"system":   true,
}

// normalizeImageName lower-cases an image name and strips a trailing ".exe".
func normalizeImageName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.TrimSuffix(n, ".exe")
	return n
}

// isProtectedPID reports whether a PID is one of the critical kernel/boot
// pseudo-processes (0 = System Idle, 4 = System) that must never be killed.
func isProtectedPID(pid int) bool {
	return pid == 0 || pid == 4
}

// isProtectedName reports whether an image name is on the protected list.
func isProtectedName(name string) bool {
	return protectedProcesses[normalizeImageName(name)]
}

// splitArgs splits a Windows-style argument string into fields, honouring
// double quotes. It is used to append caller-supplied extra arguments.
func splitArgs(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []string
	var cur strings.Builder
	inQuote := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
		case (r == ' ' || r == '\t') && !inQuote:
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

// splitCommandLine splits an uninstall command line into the executable and
// its remaining arguments, honouring a leading quoted path.
func splitCommandLine(s string) (exe string, args []string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if s[0] == '"' {
		if end := strings.IndexByte(s[1:], '"'); end >= 0 {
			exe = s[1 : 1+end]
			args = splitArgs(s[2+end:])
			return exe, args
		}
	}
	fields := splitArgs(s)
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], fields[1:]
}
