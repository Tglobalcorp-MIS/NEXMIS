package core

import (
	"context"
	"errors"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/server/auth"
	"github.com/tglobalcorp-mis/nexmis/internal/server/events"
	"github.com/tglobalcorp-mis/nexmis/internal/server/secrets"
	"github.com/tglobalcorp-mis/nexmis/internal/server/store"
)

// Bootstrap prepares state at server start: no agent is connected yet,
// stale remote sessions are closed, and the first admin is created.
func (s *Service) Bootstrap(ctx context.Context) error {
	if err := s.Store.MarkAllOffline(ctx); err != nil {
		return err
	}
	if err := s.Store.EndStaleRemoteSessions(ctx); err != nil {
		return err
	}
	n, err := s.Store.CountUsers(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	pw := s.Cfg.InitialAdminPassword
	generated := false
	if pw == "" {
		pw = secrets.RandomToken("", 12)
		generated = true
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	u := &store.User{Username: "admin", DisplayName: "系統管理員", Source: "local", Role: store.RoleAdmin, PasswordHash: hash}
	if err := s.Store.CreateUser(ctx, u); err != nil {
		return err
	}
	if generated {
		// Printed once so the installer can log in; it should be changed
		// immediately from the UI.
		s.Log.Warn("created initial admin account", "username", "admin", "password", pw)
	} else {
		s.Log.Info("created initial admin account from NEXMIS_INITIAL_ADMIN_PASSWORD", "username", "admin")
	}
	return nil
}

// RunJobs runs periodic maintenance until ctx is canceled.
func (s *Service) RunJobs(ctx context.Context) {
	fast := time.NewTicker(20 * time.Second)
	slow := time.NewTicker(time.Hour)
	defer fast.Stop()
	defer slow.Stop()
	s.hourly(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-fast.C:
			s.tick(ctx)
		case <-slow.C:
			s.hourly(ctx)
		}
	}
}

func (s *Service) tick(ctx context.Context) {
	due, err := s.Store.ClaimDueSchedules(ctx, func(expr string, from time.Time) (time.Time, error) {
		return s.NextRun(ctx, expr, from)
	})
	if err != nil {
		s.Log.Error("claim schedules", "err", err)
	}
	for _, sc := range due {
		if err := s.RunSchedule(ctx, SystemActor, sc); err != nil {
			s.Log.Error("run schedule", "schedule", sc.Name, "err", err)
		}
	}

	deps, err := s.Store.DueDeployments(ctx)
	if err != nil {
		s.Log.Error("due deployments", "err", err)
	}
	for _, id := range deps {
		d, err := s.Store.GetDeployment(ctx, id)
		if err != nil {
			continue
		}
		actor := Actor{Name: nonEmptyPtr(d.CreatedByName, "system")}
		if err := s.runDeployment(ctx, actor, id, nil); err != nil {
			s.Log.Error("run deployment", "id", id, "err", err)
		}
		if d, err := s.Store.GetDeployment(ctx, id); err == nil {
			s.Bus.Publish(events.DeploymentUpdated, d)
		}
	}
	done, err := s.Store.CompleteFinishedDeployments(ctx)
	if err != nil {
		s.Log.Error("complete deployments", "err", err)
	}
	for _, id := range done {
		if d, err := s.Store.GetDeployment(ctx, id); err == nil {
			s.Bus.Publish(events.DeploymentUpdated, d)
		}
	}

	timedOut, err := s.Store.TimeoutStaleCommands(ctx)
	if err != nil {
		s.Log.Error("timeout commands", "err", err)
	}
	for _, id := range timedOut {
		if c, err := s.Store.GetCommand(ctx, id); err == nil {
			s.Bus.Publish(events.CommandUpdated, c)
			s.onCommandFinished(ctx, c)
		}
	}
}

func (s *Service) hourly(ctx context.Context) {
	g := s.General(ctx)
	if err := s.Store.PurgeExpiredSessions(ctx); err != nil {
		s.Log.Error("purge sessions", "err", err)
	}
	if err := s.Store.PurgeTelemetry(ctx, g.DataRetentionDays); err != nil {
		s.Log.Error("purge telemetry", "err", err)
	}
	if err := s.Store.PurgeAudit(ctx, g.AuditRetentionDays); err != nil {
		s.Log.Error("purge audit", "err", err)
	}
	a, err := s.ADSettings(ctx)
	if err != nil || !a.Enabled {
		return
	}
	var last time.Time
	_, _ = s.Store.GetSetting(ctx, "ad_last_sync", &last)
	if time.Since(last) < time.Duration(a.SyncIntervalMin)*time.Minute {
		return
	}
	if _, _, _, err := s.SyncAD(ctx, SystemActor); err != nil {
		var ve *ValidationError
		if !errors.As(err, &ve) {
			s.Log.Error("AD sync", "err", err)
		}
	}
}

func nonEmptyPtr(p *string, def string) string {
	if p == nil || *p == "" {
		return def
	}
	return *p
}

// ResetAdminPassword sets the local "admin" password (recreating the
// account if it was deleted) and unlocks it. Used for break-glass recovery
// from the server console.
func (s *Service) ResetAdminPassword(ctx context.Context, pw string) error {
	if err := auth.CheckPasswordPolicy(pw); err != nil {
		return err
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	u, err := s.Store.UserByUsername(ctx, "admin")
	if errors.Is(err, store.ErrNotFound) {
		return s.Store.CreateUser(ctx, &store.User{Username: "admin", DisplayName: "系統管理員", Source: "local",
			Role: store.RoleAdmin, PasswordHash: hash})
	}
	if err != nil {
		return err
	}
	role, disabled := store.RoleAdmin, false
	if err := s.Store.UpdateUser(ctx, u.ID, store.UserPatch{PasswordHash: &hash, Role: &role, Disabled: &disabled}); err != nil {
		return err
	}
	if err := s.Store.RecordLoginSuccess(ctx, u.ID); err != nil {
		return err
	}
	s.Audit(ctx, Actor{Name: "console"}, "user.reset_admin", "user", u.ID.String(), "admin", nil, true)
	return nil
}
