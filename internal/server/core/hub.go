package core

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
	"github.com/tglobalcorp-mis/nexmis/internal/server/events"
	"github.com/tglobalcorp-mis/nexmis/internal/server/store"
)

const (
	agentReadLimit    = 32 << 20
	agentSendBuffer   = 256
	agentPingInterval = 30 * time.Second
	agentPingTimeout  = 20 * time.Second
	handshakeTimeout  = 30 * time.Second
	agentWriteTimeout = 30 * time.Second
)

type agentConn struct {
	deviceID uuid.UUID
	agentID  uuid.UUID
	name     string
	addr     string
	ws       *websocket.Conn
	send     chan []byte
	ctx      context.Context
	cancel   context.CancelFunc
}

// enqueue queues a frame without blocking; a full queue means the agent is
// not keeping up and the connection is dropped (it will reconnect).
func (c *agentConn) enqueue(b []byte) bool {
	select {
	case c.send <- b:
		return true
	case <-c.ctx.Done():
		return false
	default:
		c.cancel()
		return false
	}
}

type hub struct {
	s     *Service
	mu    sync.RWMutex
	conns map[uuid.UUID]*agentConn
}

func newHub(s *Service) *hub { return &hub{s: s, conns: map[uuid.UUID]*agentConn{}} }

func (h *hub) count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns)
}

func (h *hub) get(deviceID uuid.UUID) *agentConn {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.conns[deviceID]
}

func (h *hub) register(c *agentConn) {
	h.mu.Lock()
	old := h.conns[c.deviceID]
	h.conns[c.deviceID] = c
	h.mu.Unlock()
	if old != nil {
		old.cancel()
	}
}

// unregister removes c if it is still the current connection and reports
// whether it was.
func (h *hub) unregister(c *agentConn) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conns[c.deviceID] == c {
		delete(h.conns, c.deviceID)
		return true
	}
	return false
}

// sendTo marshals and queues a message for a device. It reports false when
// the device is not connected.
func (h *hub) sendTo(deviceID uuid.UUID, typ string, payload any) bool {
	return h.sendRef(deviceID, typ, "", payload)
}

// sendRef is sendTo for a reply to the agent message with id ref.
func (h *hub) sendRef(deviceID uuid.UUID, typ, ref string, payload any) bool {
	c := h.get(deviceID)
	if c == nil {
		return false
	}
	env, err := protocol.NewEnvelope(typ, uuid.NewString(), ref, payload)
	if err != nil {
		return false
	}
	b, err := json.Marshal(env)
	if err != nil {
		return false
	}
	return c.enqueue(b)
}

func (h *hub) broadcastConfig(ctx context.Context) {
	h.mu.RLock()
	conns := make([]*agentConn, 0, len(h.conns))
	for _, c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.RUnlock()
	for _, c := range conns {
		d, err := h.s.Store.GetDevice(ctx, c.deviceID)
		if err != nil {
			continue
		}
		h.sendTo(c.deviceID, protocol.TConfig, h.s.configFor(ctx, d))
	}
}

// disconnect closes the connection of a device (e.g. when it is deleted).
func (h *hub) disconnect(deviceID uuid.UUID) {
	if c := h.get(deviceID); c != nil {
		c.cancel()
	}
}

// configFor returns the agent configuration with per-group overrides.
func (s *Service) configFor(ctx context.Context, d *store.Device) protocol.AgentConfig {
	cfg := s.AgentConfig(ctx)
	if d.ConsentPolicy != "" {
		cfg.ConsentPolicy = d.ConsentPolicy
	}
	s.applyAgentUpdate(&cfg, d)
	return cfg
}

// IsOnline reports whether a device has a live agent connection.
func (s *Service) IsOnline(deviceID uuid.UUID) bool { return s.hub.get(deviceID) != nil }

// DisconnectAgent drops the live connection of a device.
func (s *Service) DisconnectAgent(deviceID uuid.UUID) { s.hub.disconnect(deviceID) }

// ServeAgentWS handles the agent control channel.
func (s *Service) ServeAgentWS(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionNoContextTakeover})
	if err != nil {
		return
	}
	ws.SetReadLimit(agentReadLimit)
	addr := clientIP(r, s.Cfg.TrustProxy)
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()

	d, hello, err := s.handshake(ctx, ws)
	if err != nil {
		s.Log.Info("agent handshake failed", "addr", addr, "err", err)
		ws.Close(websocket.StatusPolicyViolation, "authentication failed")
		return
	}
	c := &agentConn{deviceID: d.ID, agentID: *d.AgentID, name: d.Name, addr: addr, ws: ws,
		send: make(chan []byte, agentSendBuffer)}
	c.ctx, c.cancel = context.WithCancel(ctx)
	s.hub.register(c)
	s.Log.Info("agent connected", "device", d.Name, "addr", addr, "version", hello.AgentVersion)

	if err := s.Store.SetOnline(ctx, d.ID, true); err != nil {
		s.Log.Error("set online", "err", err)
	}
	s.Bus.Publish(events.DeviceOnline, map[string]any{"device_id": d.ID, "name": d.Name})
	s.publishDevice(ctx, d.ID)

	ok, _ := protocol.NewEnvelope(protocol.TAuthOK, uuid.NewString(), "", protocol.AuthOK{
		DeviceID: d.ID.String(), Config: s.configFor(ctx, d)})
	okb, _ := json.Marshal(ok)
	c.enqueue(okb)

	go s.agentWriter(c)
	go s.flushPending(c)

	s.agentReader(c)

	c.cancel()
	ws.Close(websocket.StatusNormalClosure, "")
	if s.hub.unregister(c) {
		bg := context.Background()
		if err := s.Store.SetOnline(bg, d.ID, false); err != nil {
			s.Log.Error("set offline", "err", err)
		}
		s.Bus.Publish(events.DeviceOffline, map[string]any{"device_id": d.ID, "name": d.Name})
		s.publishDevice(bg, d.ID)
		s.relays.endForDevice(d.ID, "device_offline")
	}
	s.Log.Info("agent disconnected", "device", d.Name, "addr", addr)
}

func (s *Service) handshake(ctx context.Context, ws *websocket.Conn) (*store.Device, *protocol.AuthResponse, error) {
	hctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	if err := writeEnvelope(hctx, ws, protocol.TAuthChallenge, protocol.AuthChallenge{
		Nonce: nonce, ServerVersion: Version, Protocol: protocol.Version}); err != nil {
		return nil, nil, err
	}
	_, data, err := ws.Read(hctx)
	if err != nil {
		return nil, nil, err
	}
	var env protocol.Envelope
	if err := json.Unmarshal(data, &env); err != nil || env.T != protocol.TAuthResponse {
		return nil, nil, errors.New("expected auth.response")
	}
	var resp protocol.AuthResponse
	if err := env.Decode(&resp); err != nil {
		return nil, nil, err
	}
	fail := func(reason string, reenroll bool) error {
		_ = writeEnvelope(hctx, ws, protocol.TAuthFail, protocol.AuthFail{Reason: reason, Reenroll: reenroll})
		return errors.New(reason)
	}
	agentID, err := uuid.Parse(resp.AgentID)
	if err != nil {
		return nil, nil, fail("invalid agent id", true)
	}
	d, err := s.Store.DeviceByAgentID(ctx, agentID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, fail("unknown agent", true)
	}
	if err != nil {
		return nil, nil, fail("server error", false)
	}
	if err := protocol.VerifyAuth(d.AgentPublicKey, nonce, resp, time.Now()); err != nil {
		return nil, nil, fail(err.Error(), false)
	}
	return d, &resp, nil
}

func writeEnvelope(ctx context.Context, ws *websocket.Conn, typ string, payload any) error {
	env, err := protocol.NewEnvelope(typ, uuid.NewString(), "", payload)
	if err != nil {
		return err
	}
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return ws.Write(ctx, websocket.MessageText, b)
}

func (s *Service) agentWriter(c *agentConn) {
	t := time.NewTicker(agentPingInterval)
	defer t.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case b := <-c.send:
			wctx, cancel := context.WithTimeout(c.ctx, agentWriteTimeout)
			err := c.ws.Write(wctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				c.cancel()
				return
			}
		case <-t.C:
			pctx, cancel := context.WithTimeout(c.ctx, agentPingTimeout)
			err := c.ws.Ping(pctx)
			cancel()
			if err != nil {
				c.cancel()
				return
			}
		}
	}
}

// flushPending delivers queued commands and chat messages after (re)connect.
func (s *Service) flushPending(c *agentConn) {
	ctx := c.ctx
	cmds, err := s.Store.PendingCommands(ctx, c.deviceID)
	if err != nil {
		s.Log.Error("pending commands", "err", err)
	}
	for i := range cmds {
		s.deliverCommand(ctx, &cmds[i])
	}
	msgs, err := s.Store.UndeliveredChat(ctx, c.deviceID)
	if err != nil {
		s.Log.Error("undelivered chat", "err", err)
	}
	for _, m := range msgs {
		s.hub.sendTo(c.deviceID, protocol.TChat, chatToProto(m))
	}
}

func (s *Service) agentReader(c *agentConn) {
	for {
		_, data, err := c.ws.Read(c.ctx)
		if err != nil {
			return
		}
		var env protocol.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			s.Log.Warn("bad agent frame", "device", c.name, "err", err)
			continue
		}
		if err := s.handleAgentMessage(c, env); err != nil {
			s.Log.Warn("agent message failed", "device", c.name, "type", env.T, "err", err)
		}
	}
}

func (s *Service) handleAgentMessage(c *agentConn, env protocol.Envelope) error {
	ctx := c.ctx
	switch env.T {
	case protocol.THeartbeat:
		var hb protocol.Heartbeat
		if err := env.Decode(&hb); err != nil {
			return err
		}
		if err := s.Store.ApplyHeartbeat(ctx, c.deviceID, hb); err != nil {
			return err
		}
		s.publishDevice(ctx, c.deviceID)

	case protocol.TInventory:
		var inv protocol.Inventory
		if err := env.Decode(&inv); err != nil {
			return err
		}
		return s.handleInventory(ctx, c, inv)

	case protocol.THealth:
		var h protocol.Health
		if err := env.Decode(&h); err != nil {
			return err
		}
		if h.Collected.IsZero() {
			h.Collected = time.Now()
		}
		if _, err := s.Store.SaveHealth(ctx, c.deviceID, h); err != nil {
			return err
		}
		s.publishDevice(ctx, c.deviceID)

	case protocol.TBattery:
		var b protocol.Battery
		if err := env.Decode(&b); err != nil {
			return err
		}
		if b.Collected.IsZero() {
			b.Collected = time.Now()
		}
		if err := s.Store.SaveBattery(ctx, c.deviceID, b); err != nil {
			return err
		}
		s.publishDevice(ctx, c.deviceID)

	case protocol.TCommandAck:
		var a protocol.CommandAck
		if err := env.Decode(&a); err != nil {
			return err
		}
		cmd, err := s.Store.ApplyAck(ctx, c.deviceID, a)
		if err != nil {
			return err
		}
		s.Bus.Publish(events.CommandUpdated, cmd)
		if !a.Accepted {
			s.onCommandFinished(ctx, cmd)
		}

	case protocol.TCommandResult:
		var r protocol.CommandResult
		if err := env.Decode(&r); err != nil {
			return err
		}
		cmd, err := s.Store.ApplyResult(ctx, c.deviceID, r)
		if errors.Is(err, store.ErrNotFound) {
			// Duplicate after a lost ack, or a command finished meanwhile.
			s.hub.sendRef(c.deviceID, protocol.TCommandResultAck, env.ID, nil)
			return nil
		}
		if err != nil {
			return err
		}
		s.hub.sendRef(c.deviceID, protocol.TCommandResultAck, env.ID, nil)
		s.Bus.Publish(events.CommandUpdated, cmd)
		s.onCommandFinished(ctx, cmd)

	case protocol.TChat:
		var m protocol.Chat
		if err := env.Decode(&m); err != nil {
			return err
		}
		if m.Body == "" || len(m.Body) > 8000 {
			return errors.New("invalid chat body")
		}
		sender := m.Sender
		if sender == "" {
			sender = "使用者"
		}
		msg, err := s.Store.CreateChat(ctx, c.deviceID, "user", sender, m.Body)
		if err != nil {
			return err
		}
		s.Bus.Publish(events.ChatMessage, msg)

	case protocol.TChatAck, protocol.TChatRead:
		var r protocol.ChatRead
		if err := env.Decode(&r); err != nil {
			return err
		}
		ids := parseUUIDs(r.MessageIDs)
		var msgs []store.ChatMessage
		var err error
		if env.T == protocol.TChatAck {
			msgs, err = s.Store.MarkChatDelivered(ctx, c.deviceID, ids)
		} else {
			msgs, err = s.Store.MarkChatRead(ctx, c.deviceID, "mis", ids)
		}
		if err != nil {
			return err
		}
		for _, m := range msgs {
			s.Bus.Publish(events.ChatMessage, m)
		}

	case protocol.TRemoteConsent:
		var rc protocol.RemoteConsent
		if err := env.Decode(&rc); err != nil {
			return err
		}
		s.onRemoteConsent(ctx, c.deviceID, rc)

	case protocol.TRemoteStop:
		var rs protocol.RemoteStop
		if err := env.Decode(&rs); err != nil {
			return err
		}
		if id, err := uuid.Parse(rs.SessionID); err == nil {
			reason := "user_stopped"
			if rs.By == "error" {
				// The helper could not capture or stream (detail in the agent log).
				reason = "helper_error"
				s.Log.Warn("remote session failed on device", "session", id, "device", c.deviceID, "detail", rs.Reason)
			}
			s.EndRemote(ctx, id, c.deviceID, reason)
		}

	case protocol.TPong, protocol.TPing:
	default:
		// Unknown types are ignored for forward compatibility.
	}
	return nil
}

func (s *Service) handleInventory(ctx context.Context, c *agentConn, inv protocol.Inventory) error {
	if inv.Collected.IsZero() {
		inv.Collected = time.Now()
	}
	if inv.Unchanged {
		stored, err := s.Store.InventoryHash(ctx, c.deviceID, inv.Kind)
		if err != nil {
			return err
		}
		if stored == inv.Hash && stored != "" {
			return s.Store.TouchInventory(ctx, c.deviceID, inv.Kind, inv.Collected)
		}
		s.hub.sendTo(c.deviceID, protocol.TInventoryRequest, protocol.InventoryRequest{Kind: inv.Kind})
		return nil
	}
	var err error
	switch inv.Kind {
	case "hardware":
		err = s.Store.SaveHardware(ctx, c.deviceID, inv)
	case "software":
		err = s.Store.SaveSoftware(ctx, c.deviceID, inv)
	default:
		return errors.New("unknown inventory kind")
	}
	if err != nil {
		return err
	}
	s.publishDevice(ctx, c.deviceID)
	return nil
}

func (s *Service) publishDevice(ctx context.Context, id uuid.UUID) {
	d, err := s.Store.GetDevice(ctx, id)
	if err != nil {
		return
	}
	s.Bus.Publish(events.DeviceUpdated, d)
}

func parseUUIDs(in []string) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(in))
	for _, s := range in {
		if id, err := uuid.Parse(s); err == nil {
			out = append(out, id)
		}
	}
	return out
}

func nonEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// PushConfig re-sends the configuration to every connected agent (after a
// change to group consent policies, for example).
func (s *Service) PushConfig() { s.hub.broadcastConfig(context.Background()) }
