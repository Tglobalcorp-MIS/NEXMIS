package agent

import (
	"testing"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

func TestNewerVersion(t *testing.T) {
	if !newerVersion("1.0.1", "1.0.0") || newerVersion("1.0.0", "1.0.0") || newerVersion("1.0.1", "dev") {
		t.Fatal("newerVersion must follow protocol.NewerVersion")
	}
}

func TestUpdateCheckInterval(t *testing.T) {
	cases := []struct {
		sec  int
		want time.Duration
	}{
		{0, 6 * time.Hour},        // older server: default
		{-5, 6 * time.Hour},       // nonsense: default
		{60, minUpdateCheck},      // never more often than hourly
		{2 * 3600, 2 * time.Hour}, // configured
		{7 * 86400, 7 * 24 * time.Hour},
	}
	for _, c := range cases {
		if got := updateCheckInterval(protocol.AgentConfig{UpdateCheckSec: c.sec}); got != c.want {
			t.Errorf("updateCheckInterval(%d) = %v, want %v", c.sec, got, c.want)
		}
	}
}

func TestUpdateStartDelay(t *testing.T) {
	for range 1000 {
		if d := updateStartDelay(); d < 10*time.Second || d >= updateSpread {
			t.Fatalf("updateStartDelay() = %v, want [10s, %v)", d, updateSpread)
		}
	}
}
