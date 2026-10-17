// Package api implements the HTTP surface of nexmis-server: the admin REST
// API, live event stream, agent endpoints and the embedded web UI.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tglobalcorp-mis/nexmis/internal/server/auth"
	"github.com/tglobalcorp-mis/nexmis/internal/server/core"
	"github.com/tglobalcorp-mis/nexmis/internal/server/secrets"
	"github.com/tglobalcorp-mis/nexmis/internal/server/store"
)

const sessionCookie = "nexmis_session"

type Server struct {
	svc        *core.Service
	log        *slog.Logger
	mux        *http.ServeMux
	ui         fs.FS
	loginLimit *auth.Throttle
	enrollRate *auth.Throttle
}

// New builds the HTTP handler. ui is the compiled web UI (may be empty).
func New(svc *core.Service, ui fs.FS) *Server {
	s := &Server{
		svc:        svc,
		log:        svc.Log,
		mux:        http.NewServeMux(),
		ui:         ui,
		loginLimit: auth.NewThrottle(20, 10*time.Minute),
		enrollRate: auth.NewThrottle(60, time.Minute),
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.recover(s.securityHeaders(s.logRequests(s.mux)))
}

type ctxKey int

const userKey ctxKey = 1

func userFrom(r *http.Request) *store.User {
	u, _ := r.Context().Value(userKey).(*store.User)
	return u
}

func (s *Server) actor(r *http.Request) core.Actor {
	u := userFrom(r)
	if u == nil {
		return core.Actor{Name: "anonymous", IP: core.ClientIP(r, s.svc.Cfg.TrustProxy)}
	}
	id := u.ID
	return core.Actor{ID: &id, Name: u.Username, IP: core.ClientIP(r, s.svc.Cfg.TrustProxy)}
}

// handle registers an authenticated admin API route with a minimum role.
func (s *Server) handle(pattern, role string, h http.HandlerFunc) {
	s.mux.Handle(pattern, s.requireRole(role, h))
}

func (s *Server) requireRole(role string, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := s.sessionUser(r)
		if u == nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "請先登入")
			return
		}
		if store.RoleRank(u.Role) < store.RoleRank(role) {
			writeError(w, http.StatusForbidden, "forbidden", "權限不足")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !s.checkCSRF(r) {
			writeError(w, http.StatusForbidden, "forbidden", "請求來源驗證失敗")
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
	})
}

func (s *Server) sessionUser(r *http.Request) *store.User {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	u, err := s.svc.Store.SessionUser(r.Context(), secrets.HashToken(c.Value), s.svc.Cfg.SessionTTL)
	if err != nil {
		return nil
	}
	return u
}

// checkCSRF requires the custom header (which cross-site forms cannot set
// without a CORS preflight we never grant) and, when present, a same-origin
// Origin header.
func (s *Server) checkCSRF(r *http.Request) bool {
	if r.Header.Get("X-NEXMIS-CSRF") != "1" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	return s.originAllowed(r, origin)
}

func (s *Server) originAllowed(r *http.Request, origin string) bool {
	o, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if strings.EqualFold(o.Host, r.Host) {
		return true
	}
	if pu, err := url.Parse(s.svc.Cfg.PublicURL); err == nil && strings.EqualFold(o.Host, pu.Host) {
		return true
	}
	for _, a := range s.svc.Cfg.AllowedOrigins {
		if strings.EqualFold(strings.TrimRight(origin, "/"), a) {
			return true
		}
	}
	return false
}

// ---- middleware ----

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController and websocket hijacking reach the
// underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			return
		}
		lvl := slog.LevelDebug
		if sw.status >= 500 {
			lvl = slog.LevelError
		} else if sw.status >= 400 {
			lvl = slog.LevelInfo
		}
		s.log.Log(r.Context(), lvl, "http", "method", r.Method, "path", r.URL.Path, "status", sw.status,
			"dur_ms", time.Since(start).Milliseconds(), "ip", core.ClientIP(r, s.svc.Cfg.TrustProxy))
	})
}

func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.log.Error("panic", "err", v, "path", r.URL.Path, "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "internal", "伺服器內部錯誤")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; "+
			"connect-src 'self' ws: wss:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		if r.TLS != nil || strings.HasPrefix(s.svc.Cfg.PublicURL, "https://") {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// ---- JSON helpers ----

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]apiError{"error": {Code: code, Message: msg}})
}

func writeList[T any](w http.ResponseWriter, items []T, total int) {
	if items == nil {
		items = []T{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

// fail maps a Go error to an API error response.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ve *core.ValidationError
	switch {
	case errors.As(err, &ve):
		writeError(w, http.StatusBadRequest, "bad_request", ve.Msg)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "資料不存在")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "資料重複或狀態衝突")
	case errors.Is(err, store.ErrInUse):
		writeError(w, http.StatusConflict, "conflict", "資料仍被使用中，無法刪除")
	case errors.Is(err, context.Canceled):
		// client went away
	default:
		s.log.Error("api error", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "伺服器內部錯誤")
	}
}

const maxJSONBody = 4 << 20

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "請求格式錯誤："+err.Error())
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "資料不存在")
		return uuid.Nil, false
	}
	return id, true
}

func page(r *http.Request) store.Page {
	q := r.URL.Query()
	p, _ := strconv.Atoi(q.Get("page"))
	ps, _ := strconv.Atoi(q.Get("page_size"))
	return store.Page{Page: p, PageSize: ps}
}

func queryUUID(r *http.Request, name string) *uuid.UUID {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return nil
	}
	return &id
}

func queryBool(r *http.Request, name string) *bool {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return nil
	}
	return &b
}

func queryInt(r *http.Request, name string, def, lo, hi int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil {
		return def
	}
	return max(lo, min(hi, v))
}

// ---- web UI ----

// serveUI serves the SPA, falling back to index.html for client routes.
func (s *Server) serveUI(w http.ResponseWriter, r *http.Request) {
	if s.ui == nil {
		http.Error(w, "web UI not built", http.StatusNotFound)
		return
	}
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if p == "" {
		p = "index.html"
	}
	if f, err := s.ui.Open(p); err == nil {
		st, _ := f.Stat()
		f.Close()
		if st != nil && !st.IsDir() {
			if strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFileFS(w, r, s.ui, p)
			return
		}
	}
	if strings.Contains(path.Base(p), ".") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFileFS(w, r, s.ui, "index.html")
}
