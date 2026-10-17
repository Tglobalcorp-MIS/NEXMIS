package sys

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSplitArgs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"/qn /norestart", []string{"/qn", "/norestart"}},
		{`/i "C:\path with space\a.msi" /quiet`, []string{"/i", `C:\path with space\a.msi`, "/quiet"}},
		{`  one   two	three `, []string{"one", "two", "three"}},
	}
	for _, c := range cases {
		got := splitArgs(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("splitArgs(%q) = %v, want %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("splitArgs(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestSplitCommandLine(t *testing.T) {
	exe, args := splitCommandLine(`"C:\Program Files\App\unins.exe" /SILENT /x`)
	if exe != `C:\Program Files\App\unins.exe` {
		t.Fatalf("exe = %q", exe)
	}
	if len(args) != 2 || args[0] != "/SILENT" || args[1] != "/x" {
		t.Fatalf("args = %v", args)
	}
	exe, args = splitCommandLine(`MsiExec.exe /I{GUID}`)
	if exe != "MsiExec.exe" || len(args) != 1 || args[0] != "/I{GUID}" {
		t.Fatalf("exe=%q args=%v", exe, args)
	}
}

func TestNormalizeImageName(t *testing.T) {
	if normalizeImageName("LSASS.EXE") != "lsass" {
		t.Fatal("normalize LSASS.EXE")
	}
	if normalizeImageName("  Notepad  ") != "notepad" {
		t.Fatal("normalize trim")
	}
}

func TestProtectedProcess(t *testing.T) {
	for _, n := range []string{"csrss", "wininit.exe", "WINLOGON", "lsass", "services.exe", "smss"} {
		if !isProtectedName(n) {
			t.Fatalf("%q should be protected", n)
		}
	}
	if isProtectedName("notepad.exe") {
		t.Fatal("notepad should not be protected")
	}
	if !isProtectedPID(0) || !isProtectedPID(4) {
		t.Fatal("PID 0 and 4 must be protected")
	}
	if isProtectedPID(1234) {
		t.Fatal("1234 should not be protected")
	}
}

func TestKillProcessRejectsProtected(t *testing.T) {
	if _, err := KillProcess(4, ""); err == nil {
		t.Fatal("expected refusal to kill PID 4")
	}
	if _, err := KillProcess(0, "lsass.exe"); err == nil {
		t.Fatal("expected refusal to kill lsass by name")
	}
	if _, err := KillProcess(os.Getpid(), ""); err == nil {
		t.Fatal("expected refusal to kill self")
	}
}

func TestDataDirEnv(t *testing.T) {
	t.Setenv("NEXMIS_AGENT_DATA", filepath.Join(t.TempDir(), "d"))
	if DataDir() == "" {
		t.Fatal("DataDir empty")
	}
}

func TestSecureDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secure")
	if err := SecureDir(dir); err != nil {
		t.Fatalf("SecureDir: %v", err)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("dir not created: %v", err)
	}
}

func TestProtectRoundTripOther(t *testing.T) {
	// On non-Windows Protect/Unprotect are identity; verify the round trip.
	in := []byte("secret-bytes")
	p, err := Protect(in)
	if err != nil {
		t.Fatalf("Protect: %v", err)
	}
	out, err := Unprotect(p)
	if err != nil {
		t.Fatalf("Unprotect: %v", err)
	}
	if string(out) != string(in) {
		t.Fatalf("round trip mismatch: %q", out)
	}
}

func TestScriptCommandBuilds(t *testing.T) {
	cmd, cleanup, err := ScriptCommand(context.Background(), "cmd", "echo hi", map[string]string{"FOO": "bar"})
	if err != nil {
		t.Fatalf("ScriptCommand: %v", err)
	}
	defer cleanup()
	if cmd == nil || len(cmd.Args) == 0 {
		t.Fatal("expected a command")
	}
	found := false
	for _, e := range cmd.Env {
		if e == "FOO=bar" {
			found = true
		}
	}
	if !found {
		t.Fatal("env not applied")
	}
}
