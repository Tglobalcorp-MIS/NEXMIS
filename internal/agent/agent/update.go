package agent

import (
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/sys"
	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

const (
	// updateSpread is the longest random delay before an agent starts
	// downloading a new version, so a fleet does not download at once. The
	// server additionally spreads its pushes over up to 10 minutes.
	updateSpread = 5 * time.Minute
	// minUpdateCheck bounds how often an agent asks for its configuration,
	// whatever the server says.
	minUpdateCheck = time.Hour
)

// newerVersion reports whether candidate is a higher semantic version than
// current; see protocol.NewerVersion.
func newerVersion(candidate, current string) bool { return protocol.NewerVersion(candidate, current) }

// updateStartDelay is the random wait before the first update attempt.
func updateStartDelay() time.Duration {
	return 10*time.Second + rand.N(updateSpread-10*time.Second)
}

func (a *Agent) maybeSelfUpdate(c protocol.AgentConfig) {
	if c.LatestAgent == "" || !newerVersion(c.LatestAgent, a.opt.Version) {
		return
	}
	build, ok := c.AgentBuilds[a.platformKey()]
	if !ok {
		a.log.Warn("agent update has no build for this platform", "version", c.LatestAgent, "platform", a.platformKey())
		return
	}
	// One updater at a time. It keeps retrying with backoff (server down,
	// file locked, no network) until the update succeeds — which restarts
	// the service — or the agent stops.
	if !a.updating.CompareAndSwap(false, true) {
		return
	}
	wait := updateStartDelay()
	a.log.Info("agent update available", "from", a.opt.Version, "to", c.LatestAgent, "start_in", wait.Round(time.Second))
	go func() {
		defer a.updating.Store(false)
		for backoff := 5 * time.Minute; ; backoff = min(backoff*2, time.Hour) {
			select {
			case <-a.life.Done():
				return
			case <-time.After(wait):
			}
			// Use the latest config: its download links are fresh.
			cur := a.config()
			if cur.LatestAgent == "" || !newerVersion(cur.LatestAgent, a.opt.Version) {
				a.log.Info("agent update withdrawn by server", "version", c.LatestAgent)
				return
			}
			if b, ok := cur.AgentBuilds[a.platformKey()]; ok {
				c, build = cur, b
			}
			ctx, cancel := context.WithTimeout(a.life, time.Hour)
			err := a.selfUpdate(ctx, c.LatestAgent, build)
			cancel()
			if err == nil {
				return
			}
			wait = jitter(backoff)
			a.log.Error("self-update failed", "version", c.LatestAgent, "err", err, "retry_in", wait.Round(time.Second))
		}
	}()
}

func (a *Agent) selfUpdate(ctx context.Context, version string, b protocol.AgentBuild) error {
	if b.URL == "" || b.SHA256 == "" {
		return errors.New("update build is incomplete")
	}
	dest := filepath.Join(a.opt.DataDir, "update", "nexmis-agent-"+safeName(version)+filepath.Ext(a.opt.ExePath))
	a.log.Info("downloading agent update", "version", version, "size", b.Size)
	if err := a.download(ctx, b.URL, dest, b.SHA256, b.Size); err != nil {
		return err
	}
	a.log.Info("agent update verified", "version", version, "sha256", b.SHA256)
	a.log.Info("installing agent update", "from", a.opt.Version, "to", version)
	if err := sys.ReplaceBinary(dest); err != nil {
		return err
	}
	_ = os.Remove(dest)
	a.requestRestart(restartDelay)
	return nil
}

// restartDelay is how long the agent keeps running after replacing its
// executable, so the result of an "agent.update" command can be sent first.
const restartDelay = 3 * time.Second

// updateCheckInterval is the configured check interval, at least
// minUpdateCheck.
func updateCheckInterval(c protocol.AgentConfig) time.Duration {
	d := time.Duration(c.UpdateCheckSec) * time.Second
	if d <= 0 {
		d = time.Duration(protocol.DefaultAgentConfig().UpdateCheckSec) * time.Second
	}
	return max(d, minUpdateCheck)
}

// updateCheckLoop asks the server for the current configuration every
// UpdateCheckSec while connected. The reply goes through applyConfig and
// so through maybeSelfUpdate: a long-lived connection still picks up a new
// version if a push was missed (or auto-update was switched on later).
func (a *Agent) updateCheckLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(jitter(updateCheckInterval(a.config()))):
		}
		if !a.connected.Load() {
			continue // the handshake delivers a fresh config on reconnect
		}
		a.log.Info("checking for agent update", "version", a.opt.Version)
		if !a.send(protocol.TConfigRequest, protocol.ConfigRequest{Reason: "update_check", AgentVersion: a.opt.Version}) {
			a.log.Warn("update check not sent: send queue unavailable")
		}
	}
}
