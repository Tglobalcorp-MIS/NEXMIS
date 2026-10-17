package core

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
	"github.com/tglobalcorp-mis/nexmis/internal/server/events"
	"github.com/tglobalcorp-mis/nexmis/internal/server/secrets"
	"github.com/tglobalcorp-mis/nexmis/internal/server/store"
)

// A relay pairs one admin viewer with the user-session helper on the
// device and forwards frames between them. Media never touches the
// database; only session start/stop is persisted and audited.
type relay struct {
	id        uuid.UUID
	deviceID  uuid.UUID
	mode      string
	token     string
	requester uuid.UUID
	created   time.Time

	mu      sync.Mutex
	viewer  *websocket.Conn
	agent   *websocket.Conn
	status  string // waiting_consent | active | ended | denied
	reason  string
	done    chan struct{}
	endOnce sync.Once
}

type relayRegistry struct {
	mu     sync.Mutex
	relays map[uuid.UUID]*relay
}

func newRelayRegistry() *relayRegistry { return &relayRegistry{relays: map[uuid.UUID]*relay{}} }

func (rr *relayRegistry) add(r *relay) {
	rr.mu.Lock()
	rr.relays[r.id] = r
	rr.mu.Unlock()
}

func (rr *relayRegistry) get(id uuid.UUID) *relay {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return rr.relays[id]
}

func (rr *relayRegistry) remove(id uuid.UUID) {
	rr.mu.Lock()
	delete(rr.relays, id)
	rr.mu.Unlock()
}

func (rr *relayRegistry) endForDevice(deviceID uuid.UUID, reason string) {
	rr.mu.Lock()
	var list []*relay
	for _, r := range rr.relays {
		if r.deviceID == deviceID {
			list = append(list, r)
		}
	}
	rr.mu.Unlock()
	for _, r := range list {
		r.finish("ended", reason)
	}
}

// finish closes both sides once. The service-level bookkeeping is done by
// the caller (EndRemote) or by the watcher goroutine.
func (r *relay) finish(status, reason string) {
	r.endOnce.Do(func() {
		r.mu.Lock()
		r.status, r.reason = status, reason
		v, a := r.viewer, r.agent
		r.mu.Unlock()
		if v != nil {
			sendViewerStatus(v, status, reason)
			v.Close(websocket.StatusNormalClosure, reason)
		}
		if a != nil {
			a.Close(websocket.StatusNormalClosure, reason)
		}
		close(r.done)
	})
}

func sendViewerStatus(ws *websocket.Conn, status, reason string) {
	b, _ := json.Marshal(map[string]string{"t": "status", "status": status, "reason": reason})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = ws.Write(ctx, websocket.MessageText, b)
}

// RemoteRequestInput is the admin request body.
type RemoteRequestInput struct {
	Mode   string `json:"mode"`
	Reason string `json:"reason"`
}

// StartRemote creates a pending session and asks the agent for consent.
func (s *Service) StartRemote(ctx context.Context, actor Actor, deviceID uuid.UUID, in RemoteRequestInput) (*store.RemoteSession, error) {
	if in.Mode != protocol.RemoteView && in.Mode != protocol.RemoteControl {
		return nil, invalid("模式必須是 view 或 control")
	}
	if len(in.Reason) > 500 {
		return nil, invalid("原因過長")
	}
	d, err := s.Store.GetDevice(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	c := s.hub.get(deviceID)
	if c == nil {
		return nil, invalid("裝置目前離線，無法建立遠端連線")
	}
	cfg := s.configFor(ctx, d)
	if cfg.ConsentPolicy == protocol.ConsentDeny {
		return nil, invalid("此裝置所屬群組的政策禁止遠端連線")
	}
	if actor.ID == nil {
		return nil, invalid("需要登入使用者")
	}
	sess, err := s.Store.CreateRemoteSession(ctx, deviceID, in.Mode, *actor.ID, actor.Name, in.Reason)
	if err != nil {
		return nil, err
	}
	r := &relay{id: sess.ID, deviceID: deviceID, mode: in.Mode, token: secrets.RandomToken("nxr_", 32),
		requester: *actor.ID, created: time.Now(), status: "waiting_consent", done: make(chan struct{})}
	s.relays.add(r)

	req := protocol.RemoteRequest{
		SessionID: sess.ID.String(), Mode: in.Mode, Requester: actor.Name, Reason: in.Reason,
		RelayURL: "/api/agent/v1/remote/" + sess.ID.String(), RelayToken: r.token,
		Policy: cfg.ConsentPolicy, TimeoutSec: cfg.ConsentTimeout,
	}
	args, _ := json.Marshal(req)
	sc, err := protocol.SignCommand(s.signKey, protocol.Command{
		CommandID: sess.ID.String(), AgentID: c.agentID.String(), Kind: "remote", Args: args,
		TimeoutSec: cfg.ConsentTimeout, IssuedAt: time.Now(), ExpiresAt: time.Now().Add(2 * time.Minute), IssuedBy: actor.Name,
	})
	if err != nil {
		return nil, err
	}
	s.Audit(ctx, actor, "remote.request", "device", deviceID.String(), d.Name,
		map[string]any{"session_id": sess.ID, "mode": in.Mode, "reason": in.Reason}, true)
	if !s.hub.sendTo(deviceID, protocol.TRemoteRequest, sc) {
		s.EndRemoteStatus(ctx, sess.ID, "failed", "device_offline")
		return nil, invalid("裝置目前離線，無法建立遠端連線")
	}
	s.Bus.Publish(events.RemoteUpdated, sess)

	// Fail the session if neither consent nor the media channel arrives.
	go func() {
		wait := time.Duration(cfg.ConsentTimeout+45) * time.Second
		select {
		case <-r.done:
		case <-time.After(wait):
			r.mu.Lock()
			pending := r.status == "waiting_consent" || r.agent == nil
			r.mu.Unlock()
			if pending {
				s.EndRemoteStatus(context.Background(), sess.ID, "failed", "consent_timeout")
			}
		}
	}()
	return sess, nil
}

func (s *Service) onRemoteConsent(ctx context.Context, deviceID uuid.UUID, rc protocol.RemoteConsent) {
	id, err := uuid.Parse(rc.SessionID)
	if err != nil {
		return
	}
	r := s.relays.get(id)
	if r == nil || r.deviceID != deviceID {
		return
	}
	d, _ := s.Store.GetDevice(ctx, deviceID)
	name := ""
	if d != nil {
		name = d.Name
	}
	if !rc.Granted {
		s.Audit(ctx, SystemActor, "remote.denied", "device", deviceID.String(), name,
			map[string]any{"session_id": id, "reason": rc.Reason, "user": rc.User}, true)
		s.EndRemoteStatus(ctx, id, "denied", nonEmpty(rc.Reason, "denied"))
		return
	}
	s.Audit(ctx, SystemActor, "remote.consent", "device", deviceID.String(), name,
		map[string]any{"session_id": id, "user": rc.User}, true)
	if sess, err := s.Store.UpdateRemoteStatus(ctx, id, "pending", rc.User, ""); err == nil {
		s.Bus.Publish(events.RemoteUpdated, sess)
	}
}

// EndRemote stops a session on request of the admin, the user or the system.
func (s *Service) EndRemote(ctx context.Context, id, deviceID uuid.UUID, reason string) {
	if r := s.relays.get(id); r != nil && r.deviceID != deviceID {
		return
	}
	s.EndRemoteStatus(ctx, id, "ended", reason)
}

// EndRemoteStatus finalises a session with the given terminal status.
func (s *Service) EndRemoteStatus(ctx context.Context, id uuid.UUID, status, reason string) {
	r := s.relays.get(id)
	if r != nil {
		viewerStatus := status
		if status == "failed" {
			viewerStatus = "ended"
		}
		r.finish(viewerStatus, reason)
		s.relays.remove(id)
		s.hub.sendTo(r.deviceID, protocol.TRemoteStop, protocol.RemoteStop{SessionID: id.String(), By: "mis", Reason: reason})
	}
	sess, err := s.Store.UpdateRemoteStatus(context.WithoutCancel(ctx), id, status, "", reason)
	if err != nil {
		return
	}
	if status == "ended" || status == "failed" {
		s.Audit(ctx, SystemActor, "remote.end", "device", sess.DeviceID.String(), sess.DeviceName,
			map[string]any{"session_id": id, "reason": reason, "status": status}, true)
	}
	s.Bus.Publish(events.RemoteUpdated, sess)
}

// StopRemoteByUser is the admin "end session" action.
func (s *Service) StopRemoteByUser(ctx context.Context, actor Actor, id uuid.UUID) error {
	sess, err := s.Store.GetRemoteSession(ctx, id)
	if err != nil {
		return err
	}
	s.Audit(ctx, actor, "remote.stop", "device", sess.DeviceID.String(), sess.DeviceName, map[string]any{"session_id": id}, true)
	s.EndRemoteStatus(ctx, id, "ended", "mis_stopped")
	return nil
}

// viewer-to-agent messages allowed in view-only mode.
var viewOnlyAllowed = map[string]bool{"ack": true, "quality": true, "monitor": true}

// ServeRemoteViewer handles the admin browser side of a session.
func (s *Service) ServeRemoteViewer(w http.ResponseWriter, r *http.Request, actor Actor, isAdmin bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rl := s.relays.get(id)
	if rl == nil {
		http.Error(w, "session not active", http.StatusGone)
		return
	}
	if actor.ID == nil || (*actor.ID != rl.requester && !isAdmin) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(64 << 10)
	rl.mu.Lock()
	if rl.viewer != nil {
		rl.viewer.Close(websocket.StatusPolicyViolation, "replaced by another viewer")
	}
	rl.viewer = ws
	status, reason := rl.status, rl.reason
	rl.mu.Unlock()
	select {
	case <-rl.done:
		sendViewerStatus(ws, status, reason)
		ws.Close(websocket.StatusNormalClosure, "")
		return
	default:
	}
	sendViewerStatus(ws, status, "")
	if status == "active" {
		// A reconnecting viewer needs a fresh full frame.
		rl.forwardToAgent([]byte(`{"t":"refresh"}`))
	}

	ctx := r.Context()
	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			break
		}
		if typ != websocket.MessageText {
			continue
		}
		var m struct {
			T string `json:"t"`
		}
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if rl.mode == protocol.RemoteView && !viewOnlyAllowed[m.T] {
			continue
		}
		rl.forwardToAgent(data)
	}
	rl.mu.Lock()
	if rl.viewer == ws {
		rl.viewer = nil
	}
	rl.mu.Unlock()
	// The viewer closing the tab ends the session after a grace period so a
	// page reload can reattach.
	go func() {
		select {
		case <-rl.done:
		case <-time.After(20 * time.Second):
			rl.mu.Lock()
			gone := rl.viewer == nil
			rl.mu.Unlock()
			if gone {
				s.EndRemoteStatus(context.Background(), id, "ended", "viewer_closed")
			}
		}
	}()
}

func (rl *relay) forwardToAgent(data []byte) {
	rl.mu.Lock()
	a := rl.agent
	rl.mu.Unlock()
	if a == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = a.Write(ctx, websocket.MessageText, data)
}

// ServeRemoteAgent handles the helper's media channel.
func (s *Service) ServeRemoteAgent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rl := s.relays.get(id)
	tok := r.URL.Query().Get("token")
	if h := r.Header.Get("Authorization"); h != "" {
		tok = stringsTrimPrefix(h, "Bearer ")
	}
	if rl == nil || subtle.ConstantTimeCompare([]byte(tok), []byte(rl.token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(16 << 20)
	rl.mu.Lock()
	if rl.agent != nil {
		rl.mu.Unlock()
		ws.Close(websocket.StatusPolicyViolation, "already connected")
		return
	}
	rl.agent = ws
	rl.status = "active"
	viewer := rl.viewer
	rl.mu.Unlock()
	ctx := r.Context()
	if sess, err := s.Store.UpdateRemoteStatus(ctx, id, "active", "", ""); err == nil {
		s.Bus.Publish(events.RemoteUpdated, sess)
		s.Audit(ctx, SystemActor, "remote.start", "device", sess.DeviceID.String(), sess.DeviceName,
			map[string]any{"session_id": id, "mode": sess.Mode, "consent_user": sess.ConsentUser}, true)
	}
	if viewer != nil {
		sendViewerStatus(viewer, "active", "")
	}
	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			break
		}
		rl.mu.Lock()
		v := rl.viewer
		rl.mu.Unlock()
		if v == nil {
			continue
		}
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = v.Write(wctx, typ, data)
		cancel()
		if err != nil && !errors.Is(err, context.Canceled) {
			continue
		}
	}
	select {
	case <-rl.done:
	default:
		s.EndRemoteStatus(context.Background(), id, "ended", "agent_disconnected")
	}
}

func stringsTrimPrefix(s, p string) string {
	if len(s) >= len(p) && s[:len(p)] == p {
		return s[len(p):]
	}
	return s
}
