package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExecutedLogRecover(t *testing.T) {
	dir := t.TempDir()
	l := newExecutedLog(dir)
	for _, id := range []string{"queued", "running", "finished"} {
		if !l.Claim(id) {
			t.Fatalf("claim %s", id)
		}
	}
	l.Mark("running", execStarted)
	l.Mark("finished", execStarted)
	l.Mark("finished", execDone)

	// Simulate a restart.
	l = newExecutedLog(dir)
	got := l.Recover()
	if len(got) != 1 || got[0] != "running" {
		t.Fatalf("interrupted = %v", got)
	}
	if !l.Claim("queued") {
		t.Error("a command that never started should run when re-delivered")
	}
	if l.Claim("running") || l.Claim("finished") {
		t.Error("started commands must not run twice")
	}
	if again := newExecutedLog(dir).Recover(); len(again) != 0 {
		t.Errorf("interrupted reported twice: %v", again)
	}
}

func TestExecutedLogLegacyFormat(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "executed.json"), []byte(`{"old":"2026-01-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	l := newExecutedLog(dir)
	if len(l.Recover()) != 0 || l.Claim("old") {
		t.Error("legacy entries should load as finished")
	}
}
