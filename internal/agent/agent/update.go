package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/sys"
	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

// newerVersion reports whether candidate is a higher semantic version than
// current ("1.2.10" > "1.2.9"). Non-numeric versions (e.g. "dev") never
// trigger automatic updates.
func newerVersion(candidate, current string) bool {
	parse := func(v string) ([]int, bool) {
		v = strings.TrimPrefix(strings.TrimSpace(v), "v")
		if i := strings.IndexAny(v, "-+"); i >= 0 {
			v = v[:i]
		}
		parts := strings.Split(v, ".")
		out := make([]int, 3)
		if len(parts) == 0 || len(parts) > 3 {
			return nil, false
		}
		for i, p := range parts {
			n, err := strconv.Atoi(p)
			if err != nil {
				return nil, false
			}
			out[i] = n
		}
		return out, true
	}
	c, ok1 := parse(candidate)
	cur, ok2 := parse(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := range 3 {
		if c[i] != cur[i] {
			return c[i] > cur[i]
		}
	}
	return false
}

func (a *Agent) maybeSelfUpdate(c protocol.AgentConfig) {
	if c.LatestAgent == "" || !newerVersion(c.LatestAgent, a.opt.Version) {
		return
	}
	build, ok := c.AgentBuilds[a.platformKey()]
	if !ok {
		return
	}
	a.updateOnce.Do(func() {
		go func() {
			// Spread updates over a few minutes across the fleet.
			time.Sleep(jitter(2 * time.Minute))
			ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
			defer cancel()
			if err := a.selfUpdate(ctx, c.LatestAgent, build); err != nil {
				a.log.Error("self-update failed", "version", c.LatestAgent, "err", err)
			}
		}()
	})
}

func (a *Agent) selfUpdate(ctx context.Context, version string, b protocol.AgentBuild) error {
	if b.URL == "" || b.SHA256 == "" {
		return errors.New("update build is incomplete")
	}
	dest := filepath.Join(a.opt.DataDir, "update", "nexmis-agent-"+safeName(version)+filepath.Ext(a.opt.ExePath))
	if err := a.download(ctx, b.URL, dest, b.SHA256, b.Size); err != nil {
		return err
	}
	a.log.Info("installing agent update", "from", a.opt.Version, "to", version)
	if err := sys.ReplaceAndRestart(dest, a.opt.ServiceName); err != nil {
		return err
	}
	_ = os.Remove(dest)
	return nil
}
