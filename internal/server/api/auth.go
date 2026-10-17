package api

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
	"github.com/tglobalcorp-mis/nexmis/internal/server/ad"
	"github.com/tglobalcorp-mis/nexmis/internal/server/auth"
	"github.com/tglobalcorp-mis/nexmis/internal/server/core"
	"github.com/tglobalcorp-mis/nexmis/internal/server/secrets"
	"github.com/tglobalcorp-mis/nexmis/internal/server/store"
)

const (
	maxFailedLogins = 5
	lockDuration    = 15 * time.Minute
)

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.svc.Store.Pool.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": core.Version})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := core.ClientIP(r, s.svc.Cfg.TrustProxy)
	if !s.loginLimit.Allow(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "登入嘗試次數過多，請稍後再試")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && !s.originAllowed(r, origin) {
		writeError(w, http.StatusForbidden, "forbidden", "請求來源驗證失敗")
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	actor := core.Actor{Name: in.Username, IP: ip}
	ctx := r.Context()
	failed := func(reason string) {
		s.svc.Audit(ctx, actor, "auth.login", "user", "", in.Username, map[string]string{"reason": reason}, false)
		writeError(w, http.StatusUnauthorized, "unauthorized", "帳號或密碼錯誤")
	}
	if in.Username == "" || in.Password == "" {
		failed("empty")
		return
	}

	user, err := s.svc.Store.UserByUsername(ctx, in.Username)
	if errors.Is(err, store.ErrNotFound) {
		// AD users may sign in as DOMAIN\user or user@domain; the account
		// is stored under its sAMAccountName.
		if sam := ad.NormalizeUsername(in.Username); sam != in.Username {
			user, err = s.svc.Store.UserByUsername(ctx, sam)
		}
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, err)
		return
	}
	if user != nil && user.LockedUntil != nil && user.LockedUntil.After(time.Now()) {
		s.svc.Audit(ctx, actor, "auth.login", "user", user.ID.String(), user.Username, map[string]string{"reason": "locked"}, false)
		writeError(w, http.StatusUnauthorized, "unauthorized", "帳號已暫時鎖定，請 15 分鐘後再試")
		return
	}

	switch {
	case user != nil && user.Source == "local":
		if user.Disabled || !auth.VerifyPassword(user.PasswordHash, in.Password) {
			_ = s.svc.Store.RecordLoginFailure(ctx, user.ID, maxFailedLogins, lockDuration)
			failed("bad_password")
			return
		}
	default:
		// Try Active Directory for unknown users and AD-sourced users.
		if user == nil {
			auth.VerifyPassword("", in.Password) // equalise timing
		}
		adCfg, err := s.svc.ADSettings(ctx)
		if err != nil || !adCfg.Enabled {
			failed("unknown_user")
			return
		}
		info, err := adCfg.Authenticate(in.Username, in.Password)
		if err != nil {
			if user != nil {
				_ = s.svc.Store.RecordLoginFailure(ctx, user.ID, maxFailedLogins, lockDuration)
			}
			if !errors.Is(err, ad.ErrInvalidCredentials) {
				s.log.Warn("AD authentication error", "err", err)
				failed("ad_error")
				return
			}
			failed("bad_password")
			return
		}
		role := adCfg.RoleFor(info.Groups, store.RoleRank)
		if role == "" {
			s.svc.Audit(ctx, actor, "auth.login", "user", "", info.Username, map[string]string{"reason": "no_role_mapping"}, false)
			writeError(w, http.StatusForbidden, "forbidden", "您的 AD 帳號未被授權使用 NEXMIS，請聯絡系統管理員")
			return
		}
		if user, err = s.svc.Store.UpsertADUser(ctx, info.Username, info.DisplayName, info.Email, role); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				// A local account with the same name exists; refuse to merge.
				failed("name_conflict")
				return
			}
			s.fail(w, r, err)
			return
		}
		if user.Disabled {
			failed("disabled")
			return
		}
	}

	token := secrets.RandomToken("", 32)
	if err := s.svc.Store.CreateSession(ctx, secrets.HashToken(token), user.ID, s.svc.Cfg.SessionTTL, ip, r.UserAgent()); err != nil {
		s.fail(w, r, err)
		return
	}
	_ = s.svc.Store.RecordLoginSuccess(ctx, user.ID)
	uid := user.ID
	s.svc.Audit(ctx, core.Actor{ID: &uid, Name: user.Username, IP: ip}, "auth.login", "user", user.ID.String(), user.Username,
		map[string]string{"source": user.Source}, true)
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: r.TLS != nil || strings.HasPrefix(s.svc.Cfg.PublicURL, "https://"),
	})
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.svc.Store.DeleteSession(r.Context(), secrets.HashToken(c.Value))
	}
	u := userFrom(r)
	s.svc.Audit(r.Context(), s.actor(r), "auth.logout", "user", u.ID.String(), u.Username, nil, true)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, userFrom(r)) }

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if u.Source != "local" {
		writeError(w, http.StatusBadRequest, "bad_request", "AD 帳號請透過網域變更密碼")
		return
	}
	var in struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !auth.VerifyPassword(u.PasswordHash, in.Current) {
		s.svc.Audit(r.Context(), s.actor(r), "auth.password", "user", u.ID.String(), u.Username, nil, false)
		writeError(w, http.StatusBadRequest, "bad_request", "目前密碼不正確")
		return
	}
	if err := auth.CheckPasswordPolicy(in.New); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	hash, err := auth.HashPassword(in.New)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.svc.Store.UpdateUser(r.Context(), u.ID, store.UserPatch{PasswordHash: &hash}); err != nil {
		s.fail(w, r, err)
		return
	}
	// Sign out other sessions of this user.
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.svc.Store.DeleteUserSessions(r.Context(), u.ID, secrets.HashToken(c.Value))
	}
	s.svc.Audit(r.Context(), s.actor(r), "auth.password", "user", u.ID.String(), u.Username, nil, true)
	w.WriteHeader(http.StatusNoContent)
}

// ---- agent enrollment ----

func (s *Server) enroll(w http.ResponseWriter, r *http.Request) {
	ip := core.ClientIP(r, s.svc.Cfg.TrustProxy)
	if !s.enrollRate.Allow(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
		return
	}
	var in protocol.EnrollRequest
	if !decode(w, r, &in) {
		return
	}
	in.Hostname = strings.TrimSpace(in.Hostname)
	if in.Token == "" || in.MachineID == "" || in.Hostname == "" || len(in.PublicKey) != ed25519.PublicKeySize ||
		len(in.Hostname) > 255 || len(in.MachineID) > 128 {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid enrollment request")
		return
	}
	devID, agentID, err := s.svc.Store.Enroll(r.Context(), store.EnrollInput{
		TokenHash: secrets.HashToken(in.Token), MachineID: in.MachineID, Hostname: in.Hostname, Domain: in.Domain,
		OS: in.OS, OSVersion: in.OSVersion, AgentVersion: in.AgentVersion, PublicKey: in.PublicKey,
	})
	actor := core.Actor{Name: "agent:" + in.Hostname, IP: ip}
	if errors.Is(err, store.ErrInvalidToken) {
		s.svc.Audit(r.Context(), actor, "agent.enroll", "device", "", in.Hostname, map[string]string{"reason": "invalid_token"}, false)
		writeError(w, http.StatusForbidden, "forbidden", "invalid enrollment token")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// A re-enrolment replaces the agent key; drop any old connection.
	s.svc.DisconnectAgent(devID)
	s.svc.Audit(r.Context(), actor, "agent.enroll", "device", devID.String(), in.Hostname,
		map[string]string{"machine_id": in.MachineID, "agent_version": in.AgentVersion, "os": in.OS}, true)
	writeJSON(w, http.StatusOK, protocol.EnrollResponse{
		AgentID: agentID.String(), DeviceID: devID.String(), ServerSignKey: s.svc.ServerSignPublicKey(),
	})
}
