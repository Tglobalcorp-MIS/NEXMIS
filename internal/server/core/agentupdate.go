package core

import (
	"context"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

// Pushes of a new agent release are spread over time so that a fleet does
// not download at once: one device every pushSpreadStep, the whole fleet
// within pushSpreadMax. Agents add their own random delay on top.
const (
	pushSpreadStep = 2 * time.Second
	pushSpreadMax  = 10 * time.Minute
)

// AgentUpdateSettings are administrator overrides for agent self-update.
type AgentUpdateSettings struct {
	// AutoUpdate overrides the manifest's auto_update flag when set.
	AutoUpdate *bool `json:"auto_update,omitempty"`
}

// releaseState tracks the published release for change detection and the
// running push.
type releaseState struct {
	sync.Mutex
	init   bool
	fp     string
	cancel context.CancelFunc

	lastPush        time.Time
	lastPushDevices int
	lastPushReason  string
}

func (s *Service) AgentUpdateSettings(ctx context.Context) AgentUpdateSettings {
	s.mu.RLock()
	u := s.agentUpd
	s.mu.RUnlock()
	if u != nil {
		return *u
	}
	var v AgentUpdateSettings
	if _, err := s.Store.GetSetting(ctx, SettingAgentUpdate, &v); err != nil {
		s.Log.Error("load agent update settings", "err", err)
		return v // not cached: retried on the next call
	}
	s.mu.Lock()
	s.agentUpd = &v
	s.mu.Unlock()
	return v
}

// SetAgentUpdateSettings stores the overrides and pushes the resulting
// configuration to connected agents when the effective release changed.
func (s *Service) SetAgentUpdateSettings(ctx context.Context, v AgentUpdateSettings) error {
	if err := s.Store.PutSetting(ctx, SettingAgentUpdate, v); err != nil {
		return err
	}
	s.mu.Lock()
	s.agentUpd = &v
	s.mu.Unlock()
	s.CheckAgentRelease(ctx)
	return nil
}

// autoUpdate is the effective auto-update flag for a manifest.
func (s *Service) autoUpdate(ctx context.Context, m AgentManifest) bool {
	if v := s.AgentUpdateSettings(ctx).AutoUpdate; v != nil {
		return *v
	}
	return m.AutoUpdate
}

// releaseFingerprint identifies what agents are told to run: it changes
// with the version, any build's hash or the effective auto-update flag.
func releaseFingerprint(m AgentManifest, auto bool) string {
	if !auto || m.Version == "" || len(m.Builds) == 0 {
		return "off"
	}
	parts := make([]string, 0, len(m.Builds))
	for arch, b := range m.Builds {
		parts = append(parts, arch+"="+strings.ToLower(b.SHA256))
	}
	slices.Sort(parts)
	return m.Version + ";" + strings.Join(parts, ",")
}

// CheckAgentRelease compares the published release with the one seen last
// and pushes the configuration to connected agents when it changed (a new
// manifest.json, or auto-update switched on or off). The first call only
// records the current release: agents receive it when they connect. It
// reports whether a push was started.
func (s *Service) CheckAgentRelease(ctx context.Context) bool {
	m := s.agentManifest()
	fp := releaseFingerprint(m, s.autoUpdate(ctx, m))
	s.release.Lock()
	first, changed := !s.release.init, fp != s.release.fp
	s.release.init, s.release.fp = true, fp
	s.release.Unlock()
	if first || !changed {
		return false
	}
	if fp == "off" {
		// Agents with an update pending see it withdrawn.
		s.Log.Info("agent auto-update disabled or release withdrawn", "version", m.Version)
	} else {
		s.Log.Info("new agent release detected", "version", m.Version, "builds", len(m.Builds))
	}
	s.PushAgentUpdate("release_changed")
	return true
}

// PushAgentUpdate sends the current configuration, and with it the
// published agent version, to every connected agent, spread over time. A
// push still in progress is replaced. It returns the number of agents.
func (s *Service) PushAgentUpdate(reason string) int {
	conns := s.hub.snapshot()
	rand.Shuffle(len(conns), func(i, j int) { conns[i], conns[j] = conns[j], conns[i] })
	step := pushSpreadStep
	if n := time.Duration(len(conns)); n > 0 && n*step > pushSpreadMax {
		step = pushSpreadMax / n
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.release.Lock()
	if s.release.cancel != nil {
		s.release.cancel()
	}
	s.release.cancel = cancel
	s.release.lastPush, s.release.lastPushDevices, s.release.lastPushReason = time.Now(), len(conns), reason
	s.release.Unlock()
	s.Log.Info("pushing agent configuration", "reason", reason, "agents", len(conns),
		"over", (step * time.Duration(max(len(conns)-1, 0))).Round(time.Second))
	go func() {
		defer cancel()
		s.hub.pushConfigs(ctx, conns, step)
	}()
	return len(conns)
}

// AgentVersionStatus is one row of the version breakdown.
type AgentVersionStatus struct {
	Version string `json:"version"`
	Online  int    `json:"online"`
	Offline int    `json:"offline"`
	// State compares Version with the published one:
	// current | outdated | newer | unknown.
	State string `json:"state"`
}

// AgentUpdateSummary counts agent devices: online and up to date, online
// and waiting for the update, offline, and online with a version that
// cannot be compared (e.g. a development build).
type AgentUpdateSummary struct {
	Total    int `json:"total"`
	Current  int `json:"current"`
	Outdated int `json:"outdated"`
	Offline  int `json:"offline"`
	Unknown  int `json:"unknown"`
}

// AgentUpdateStatus is shown on the agent settings page.
type AgentUpdateStatus struct {
	Version            string               `json:"version"`
	Platforms          []string             `json:"platforms"`
	AutoUpdate         bool                 `json:"auto_update"`
	AutoUpdateSource   string               `json:"auto_update_source"` // manifest | settings
	ManifestAutoUpdate bool                 `json:"manifest_auto_update"`
	UpdateCheckSec     int                  `json:"update_check_sec"`
	ConnectedAgents    int                  `json:"connected_agents"`
	Summary            AgentUpdateSummary   `json:"summary"`
	Versions           []AgentVersionStatus `json:"versions"`
	LastPushAt         *time.Time           `json:"last_push_at"`
	LastPushAgents     int                  `json:"last_push_agents"`
	LastPushReason     string               `json:"last_push_reason,omitempty"`
}

// versionState classifies an agent version against the published one.
func versionState(v, latest string) string {
	_, okV := protocol.ParseVersion(v)
	_, okL := protocol.ParseVersion(latest)
	switch {
	case !okV || !okL:
		if v != "" && v == latest {
			return "current"
		}
		return "unknown"
	case protocol.NewerVersion(latest, v):
		return "outdated"
	case protocol.NewerVersion(v, latest):
		return "newer"
	}
	return "current"
}

func (s *Service) AgentUpdateStatus(ctx context.Context) (AgentUpdateStatus, error) {
	m := s.agentManifest()
	set := s.AgentUpdateSettings(ctx)
	st := AgentUpdateStatus{
		Version: m.Version, Platforms: []string{}, AutoUpdate: s.autoUpdate(ctx, m), AutoUpdateSource: "manifest",
		ManifestAutoUpdate: m.AutoUpdate, UpdateCheckSec: s.AgentConfig(ctx).UpdateCheckSec,
		ConnectedAgents: s.ConnectedAgents(), Versions: []AgentVersionStatus{},
	}
	if set.AutoUpdate != nil {
		st.AutoUpdateSource = "settings"
	}
	for arch := range m.Builds {
		st.Platforms = append(st.Platforms, arch)
	}
	slices.Sort(st.Platforms)
	counts, err := s.Store.AgentVersionCounts(ctx)
	if err != nil {
		return st, err
	}
	for _, c := range counts {
		row := AgentVersionStatus{Version: c.Version, Online: c.Online, Offline: c.Offline, State: versionState(c.Version, m.Version)}
		st.Versions = append(st.Versions, row)
		st.Summary.Total += c.Online + c.Offline
		st.Summary.Offline += c.Offline
		switch row.State {
		case "current", "newer":
			st.Summary.Current += c.Online
		case "outdated":
			st.Summary.Outdated += c.Online
		default:
			st.Summary.Unknown += c.Online
		}
	}
	s.release.Lock()
	if !s.release.lastPush.IsZero() {
		t := s.release.lastPush
		st.LastPushAt = &t
	}
	st.LastPushAgents, st.LastPushReason = s.release.lastPushDevices, s.release.lastPushReason
	s.release.Unlock()
	return st, nil
}
