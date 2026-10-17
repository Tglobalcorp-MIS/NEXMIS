package core

import (
	"context"
	"errors"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/server/ad"
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
	minute := time.NewTicker(time.Minute)
	slow := time.NewTicker(time.Hour)
	defer fast.Stop()
	defer minute.Stop()
	defer slow.Stop()
	var adState adJobState
	s.hourly(ctx)
	s.adJobs(ctx, &adState)
	for {
		select {
		case <-ctx.Done():
			return
		case <-fast.C:
			s.tick(ctx)
		case <-minute.C:
			s.adJobs(ctx, &adState)
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
		if err := s.runDeployment(ctx, actor, id, nil, true); err != nil {
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
	expired, err := s.Store.ExpireCommands(ctx)
	if err != nil {
		s.Log.Error("expire commands", "err", err)
	}
	for _, id := range append(timedOut, expired...) {
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
}

// adRevalidateInterval is how often signed-in AD users are re-checked
// against the directory, so removal from a group or disabling the account
// takes effect without waiting for the session to expire.
const adRevalidateInterval = 15 * time.Minute

// adSyncRetry is the minimum gap between failed computer sync attempts;
// ad_last_sync only advances on success.
const adSyncRetry = 15 * time.Minute

type adJobState struct {
	lastSyncAttempt  time.Time
	lastRevalidation time.Time
}

// adJobs runs on a one-minute ticker so a configured sync interval shorter
// than an hour is honored.
func (s *Service) adJobs(ctx context.Context, st *adJobState) {
	a, err := s.ADSettings(ctx)
	if err != nil || !a.Enabled {
		return
	}
	if time.Since(st.lastRevalidation) >= adRevalidateInterval {
		st.lastRevalidation = time.Now()
		s.revalidateADUsers(ctx, a)
	}
	var last time.Time
	_, _ = s.Store.GetSetting(ctx, "ad_last_sync", &last)
	interval := time.Duration(a.SyncIntervalMin) * time.Minute
	if interval <= 0 || time.Since(last) < interval || time.Since(st.lastSyncAttempt) < min(adSyncRetry, interval) {
		return
	}
	st.lastSyncAttempt = time.Now()
	if _, _, _, err := s.SyncAD(ctx, SystemActor); err != nil {
		var ve *ValidationError
		if !errors.As(err, &ve) {
			s.Log.Error("AD sync", "err", err)
		}
	}
}

// revalidateADUsers signs out AD users that are gone, disabled, or no
// longer mapped to a role, and applies role changes. Directory errors leave
// sessions alone so an unreachable DC does not lock everyone out.
func (s *Service) revalidateADUsers(ctx context.Context, a ad.Settings) {
	users, err := s.Store.ADUsersWithSessions(ctx)
	if err != nil {
		s.Log.Error("list AD users", "err", err)
		return
	}
	for _, u := range users {
		info, err := a.Lookup(u.Username)
		role, reason := "", "not_found_or_disabled"
		switch {
		case errors.Is(err, ad.ErrUserNotFound):
		case err != nil:
			s.Log.Warn("AD revalidation skipped", "err", err)
			return
		default:
			role, reason = a.RoleFor(info.Groups, store.RoleRank), "no_role_mapping"
		}
		if role == "" {
			if err := s.Store.DeleteUserSessions(ctx, u.ID, ""); err != nil {
				s.Log.Error("revoke AD sessions", "user", u.Username, "err", err)
				continue
			}
			s.Audit(ctx, SystemActor, "auth.revoke", "user", u.ID.String(), u.Username, map[string]string{"reason": reason}, true)
			continue
		}
		if role != u.Role {
			if err := s.Store.UpdateUser(ctx, u.ID, store.UserPatch{Role: &role}); err != nil {
				s.Log.Error("update AD role", "user", u.Username, "err", err)
				continue
			}
			s.Audit(ctx, SystemActor, "user.role_sync", "user", u.ID.String(), u.Username,
				map[string]string{"from": u.Role, "to": role}, true)
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
	if err := s.Store.DeleteUserSessions(ctx, u.ID, ""); err != nil {
		return err
	}
	s.Audit(ctx, Actor{Name: "console"}, "user.reset_admin", "user", u.ID.String(), "admin", nil, true)
	return nil
}
