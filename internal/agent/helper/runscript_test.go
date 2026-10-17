package helper

import (
	"context"
	"strings"
	"testing"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperipc"
)

func TestRunScriptEcho(t *testing.T) {
	res := runScript(context.Background(), helperipc.RunScript{
		ID: "a", Shell: "cmd", Script: "echo nexmis-ok", TimeoutSec: 30,
	})
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d err=%q stderr=%q", res.ExitCode, res.Error, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "nexmis-ok") {
		t.Fatalf("stdout=%q", res.Stdout)
	}
}

func TestRunScriptNonZeroExit(t *testing.T) {
	res := runScript(context.Background(), helperipc.RunScript{
		ID: "b", Shell: "cmd", Script: "exit 7", TimeoutSec: 30,
	})
	if res.ExitCode != 7 {
		t.Fatalf("exit=%d (want 7) err=%q", res.ExitCode, res.Error)
	}
}

func TestRunScriptTimeout(t *testing.T) {
	// A 10s sleep with a 1s timeout must be killed.
	res := runScript(context.Background(), helperipc.RunScript{
		ID: "c", Shell: "cmd", Script: "sleep 10", TimeoutSec: 1,
	})
	if res.Error == "" {
		t.Fatalf("expected timeout error, got exit=%d", res.ExitCode)
	}
}

func TestCappedBuffer(t *testing.T) {
	b := &cappedBuffer{max: 8}
	n, _ := b.Write([]byte("1234"))
	if n != 4 {
		t.Fatalf("n=%d", n)
	}
	b.Write([]byte("56789")) // overflows the 8-byte cap
	if !b.truncated {
		t.Fatal("expected truncated")
	}
	if b.String() != "12345678" {
		t.Fatalf("buf=%q", b.String())
	}
}
