// Package core holds nexmis-server business logic shared by the admin API,
// the agent gateway and background jobs.
package core

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
	"github.com/tglobalcorp-mis/nexmis/internal/server/ad"
	"github.com/tglobalcorp-mis/nexmis/internal/server/config"
	"github.com/tglobalcorp-mis/nexmis/internal/server/events"
	"github.com/tglobalcorp-mis/nexmis/internal/server/secrets"
	"github.com/tglobalcorp-mis/nexmis/internal/server/store"
)

// Version is set at build time via -ldflags.
var (
	Version   = "dev"
	BuildTime = ""
)

// Setting keys.
const (
	SettingAD      = "ad"
	SettingAgent   = "agent"
	SettingGeneral = "general"
)

// GeneralSettings are administrator-tunable server settings.
type GeneralSettings struct {
	ServerURL           string  `json:"server_url"`
	Timezone            string  `json:"timezone"`
	DataRetentionDays   int     `json:"data_retention_days"`
	AuditRetentionDays  int     `json:"audit_retention_days"`
	BatteryAlertPercent float64 `json:"battery_alert_percent"`
	WarrantyAlertDays   int     `json:"warranty_alert_days"`
}

// Service is the central application object.
type Service struct {
	Cfg     *config.Config
	Store   *store.Store
	Bus     *events.Bus
	Log     *slog.Logger
	Box     *secrets.Box
	signKey ed25519.PrivateKey
	tokKey  []byte // HMAC key for file tokens, derived from the master key

	hub    *hub
	relays *relayRegistry

	started time.Time

	mu       sync.RWMutex
	agentCfg *protocol.AgentConfig
	general  *GeneralSettings
}

func New(cfg *config.Config, st *store.Store, log *slog.Logger) (*Service, error) {
	box, err := secrets.NewBox(cfg.MasterKey)
	if err != nil {
		return nil, err
	}
	sk, err := secrets.LoadSigningKey(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	s := &Service{
		Cfg:     cfg,
		Store:   st,
		Bus:     events.NewBus(),
		Log:     log,
		Box:     box,
		signKey: sk,
		tokKey:  deriveKey(cfg.MasterKey, "nexmis-file-token"),
		started: time.Now(),
	}
	s.hub = newHub(s)
	s.relays = newRelayRegistry()
	return s, nil
}

// ServerSignPublicKey is handed to agents at enrollment.
func (s *Service) ServerSignPublicKey() ed25519.PublicKey {
	return s.signKey.Public().(ed25519.PublicKey)
}

func (s *Service) Uptime() time.Duration { return time.Since(s.started) }

// ConnectedAgents returns the number of live agent connections.
func (s *Service) ConnectedAgents() int { return s.hub.count() }

// ---- settings ----

func (s *Service) AgentConfig(ctx context.Context) protocol.AgentConfig {
	s.mu.RLock()
	c := s.agentCfg
	s.mu.RUnlock()
	if c != nil {
		return *c
	}
	cfg := protocol.DefaultAgentConfig()
	if _, err := s.Store.GetSetting(ctx, SettingAgent, &cfg); err != nil {
		s.Log.Error("load agent settings", "err", err)
	}
	s.mu.Lock()
	s.agentCfg = &cfg
	s.mu.Unlock()
	return cfg
}

// SetAgentConfig saves the configuration and pushes it to connected agents.
func (s *Service) SetAgentConfig(ctx context.Context, c protocol.AgentConfig) error {
	clamp := func(v *int, lo, hi int) {
		if *v < lo {
			*v = lo
		}
		if *v > hi {
			*v = hi
		}
	}
	clamp(&c.HeartbeatSec, 15, 600)
	clamp(&c.HealthSec, 60, 86400)
	clamp(&c.BatterySec, 600, 7*86400)
	clamp(&c.HardwareSec, 3600, 7*86400)
	clamp(&c.SoftwareSec, 600, 7*86400)
	clamp(&c.ConsentTimeout, 10, 600)
	clamp(&c.RebootCountdown, 0, 3600)
	clamp(&c.RebootMaxDefer, 0, 10)
	switch c.ConsentPolicy {
	case protocol.ConsentAlwaysAsk, protocol.ConsentNotifyOnly, protocol.ConsentDeny:
	default:
		c.ConsentPolicy = protocol.ConsentAlwaysAsk
	}
	// Agent update fields are managed by the server, not the admin.
	c.LatestAgent, c.AgentBuilds = "", nil
	if err := s.Store.PutSetting(ctx, SettingAgent, c); err != nil {
		return err
	}
	s.mu.Lock()
	s.agentCfg = &c
	s.mu.Unlock()
	s.hub.broadcastConfig(ctx)
	return nil
}

func (s *Service) General(ctx context.Context) GeneralSettings {
	s.mu.RLock()
	g := s.general
	s.mu.RUnlock()
	if g != nil {
		return *g
	}
	def := GeneralSettings{
		ServerURL:           s.Cfg.PublicURL,
		Timezone:            s.Cfg.Timezone,
		DataRetentionDays:   90,
		AuditRetentionDays:  730,
		BatteryAlertPercent: 70,
		WarrantyAlertDays:   60,
	}
	if _, err := s.Store.GetSetting(ctx, SettingGeneral, &def); err != nil {
		s.Log.Error("load general settings", "err", err)
	}
	s.mu.Lock()
	s.general = &def
	s.mu.Unlock()
	return def
}

func (s *Service) SetGeneral(ctx context.Context, g GeneralSettings) error {
	if g.DataRetentionDays < 7 {
		g.DataRetentionDays = 7
	}
	if g.AuditRetentionDays != 0 && g.AuditRetentionDays < 180 {
		g.AuditRetentionDays = 180
	}
	if g.Timezone == "" {
		g.Timezone = s.Cfg.Timezone
	}
	if _, err := time.LoadLocation(g.Timezone); err != nil {
		g.Timezone = s.Cfg.Timezone
	}
	if err := s.Store.PutSetting(ctx, SettingGeneral, g); err != nil {
		return err
	}
	s.mu.Lock()
	s.general = &g
	s.mu.Unlock()
	return nil
}

// Location returns the configured time zone for schedules.
func (s *Service) Location(ctx context.Context) *time.Location {
	if loc, err := time.LoadLocation(s.General(ctx).Timezone); err == nil {
		return loc
	}
	return time.Local
}

// ADSettings returns the AD configuration with the bind password decrypted.
func (s *Service) ADSettings(ctx context.Context) (ad.Settings, error) {
	var a ad.Settings
	if _, err := s.Store.GetSetting(ctx, SettingAD, &a); err != nil {
		return a, err
	}
	if a.SyncIntervalMin == 0 {
		a.SyncIntervalMin = 360
	}
	pw, err := s.Box.Open(a.BindPassword)
	if err != nil {
		return a, err
	}
	a.BindPassword = pw
	return a, nil
}

// SetADSettings stores AD settings, keeping the existing password when the
// new one is empty.
func (s *Service) SetADSettings(ctx context.Context, a ad.Settings) error {
	if a.BindPassword == "" {
		old, err := s.ADSettings(ctx)
		if err == nil {
			a.BindPassword = old.BindPassword
		}
	}
	if a.SyncIntervalMin != 0 && a.SyncIntervalMin < 30 {
		a.SyncIntervalMin = 30
	}
	sealed, err := s.Box.Seal(a.BindPassword)
	if err != nil {
		return err
	}
	a.BindPassword = sealed
	return s.Store.PutSetting(ctx, SettingAD, a)
}

// ---- audit ----

// Actor identifies who performed an action.
type Actor struct {
	ID   *uuid.UUID
	Name string
	IP   string
}

// SystemActor is used for scheduled and automatic actions.
var SystemActor = Actor{Name: "system"}

// Audit writes an audit entry; failures are logged, never returned, so
// that auditing cannot be used to block operations silently — but every
// failure is visible in the server log.
func (s *Service) Audit(ctx context.Context, a Actor, action, targetType, targetID, targetName string, details any, ok bool) {
	var raw json.RawMessage
	if details != nil {
		raw, _ = json.Marshal(details)
	}
	result := "success"
	if !ok {
		result = "failure"
	}
	if err := s.Store.InsertAudit(context.WithoutCancel(ctx), store.AuditEntry{
		ActorID: a.ID, ActorName: a.Name, ActorIP: a.IP, Action: action, TargetType: targetType,
		TargetID: targetID, TargetName: targetName, Details: raw, Result: result,
	}); err != nil {
		s.Log.Error("audit write failed", "action", action, "err", err)
	}
}
