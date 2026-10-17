// Package agent implements the NEXMIS agent service: enrollment, the
// persistent control channel to nexmis-server, telemetry scheduling,
// command execution and coordination with the user-session helper.
package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/collect"
	"github.com/tglobalcorp-mis/nexmis/internal/agent/config"
	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperhost"
	"github.com/tglobalcorp-mis/nexmis/internal/agent/tlsconf"
	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

// Capabilities advertised to the server.
var Capabilities = []string{"inventory", "health", "battery", "commands", "chat", "remote", "deploy", "discovery", "update"}

// Options configure an Agent.
type Options struct {
	Version     string
	ServiceName string
	DataDir     string
	ExePath     string
}

type Agent struct {
	opt  Options
	cfg  *config.Config
	log  *slog.Logger
	col  collect.Collector
	host helperhost.Host
	http *http.Client

	acfg     atomic.Pointer[protocol.AgentConfig]
	executed *executedLog
	outbox   *outbox
	// life is the Run context. Accepted commands run under it, not under a
	// connection's context, so a reconnect does not abort them.
	life context.Context

	connMu sync.Mutex
	conn   *websocket.Conn
	sendCh chan []byte

	connected atomic.Bool
	refresh   chan string // telemetry kinds to collect now ("all", "hardware", ...)

	invMu   sync.Mutex
	invHash map[string]string // kind -> last hash the server confirmed

	pendingMu  sync.Mutex
	pendingRun map[string]chan json.RawMessage // helper round-trips by id
	chatQueue  []protocol.Chat                 // MIS messages waiting for a helper
	remote     map[string]protocol.RemoteRequest

	cmdSem   chan struct{}
	updating atomic.Bool // a self-update loop is running
}

func New(opt Options, cfg *config.Config, log *slog.Logger, col collect.Collector, host helperhost.Host) (*Agent, error) {
	hc, err := tlsconf.HTTPClient(cfg.TLS, 0)
	if err != nil {
		return nil, err
	}
	a := &Agent{
		opt: opt, cfg: cfg, log: log, col: col, host: host, http: hc,
		executed:   newExecutedLog(opt.DataDir),
		outbox:     newOutbox(opt.DataDir),
		refresh:    make(chan string, 8),
		invHash:    map[string]string{},
		pendingRun: map[string]chan json.RawMessage{},
		remote:     map[string]protocol.RemoteRequest{},
		cmdSem:     make(chan struct{}, 4),
	}
	def := protocol.DefaultAgentConfig()
	a.acfg.Store(&def)
	return a, nil
}

func (a *Agent) config() protocol.AgentConfig { return *a.acfg.Load() }

// Run blocks until ctx is canceled.
func (a *Agent) Run(ctx context.Context) error {
	a.life = ctx
	if !a.cfg.Enrolled() {
		if err := a.enrollLoop(ctx); err != nil {
			return err
		}
	}
	go a.host.Run(ctx)
	go a.helperLoop(ctx)
	go a.telemetryLoop(ctx)

	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := a.session(ctx)
		if ctx.Err() != nil {
			break
		}
		if errors.Is(err, errReenroll) {
			a.log.Warn("server does not recognise this agent; re-enrolling")
			a.cfg.ResetIdentity()
			if err := a.cfg.Save(); err != nil {
				a.log.Error("save config", "err", err)
			}
			if err := a.enrollLoop(ctx); err != nil {
				return err
			}
			backoff = time.Second
			continue
		}
		if time.Since(start) > 2*time.Minute {
			backoff = time.Second // the connection was healthy for a while
		}
		wait := jitter(backoff)
		a.log.Warn("disconnected from server", "err", err, "retry_in", wait.Round(time.Second))
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
		backoff = min(backoff*2, 5*time.Minute)
	}
	return nil
}

// jitter spreads reconnects by ±20% so agents do not stampede a restarted
// server.
func jitter(d time.Duration) time.Duration {
	f := 0.8 + rand.Float64()*0.4
	return time.Duration(float64(d) * f)
}

var errReenroll = errors.New("re-enrollment required")

// ---- enrollment ----

func (a *Agent) enrollLoop(ctx context.Context) error {
	backoff := 5 * time.Second
	for {
		err := a.enroll(ctx)
		if err == nil {
			return nil
		}
		if errors.Is(err, errNoToken) {
			return err
		}
		a.log.Error("enrollment failed", "err", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(jitter(backoff)):
		}
		backoff = min(backoff*2, 10*time.Minute)
	}
}

var errNoToken = errors.New("agent is not enrolled and has no enrollment token; reinstall with --token")

func (a *Agent) enroll(ctx context.Context) error {
	if a.cfg.EnrollToken == "" {
		return errNoToken
	}
	id, err := a.col.Identity(ctx)
	if err != nil {
		return fmt.Errorf("identity: %w", err)
	}
	// Reuse the key of an earlier attempt whose response was lost: the
	// server recognises machine id + key as a retry and does not consume
	// another token use. The key is saved before the request for that.
	var pub ed25519.PublicKey
	if k := a.cfg.Key(); k != nil {
		pub = k.Public().(ed25519.PublicKey)
	} else {
		if pub, err = a.cfg.NewKey(); err != nil {
			return err
		}
		if err := a.cfg.Save(); err != nil {
			return err
		}
	}
	body, _ := json.Marshal(protocol.EnrollRequest{
		Token: a.cfg.EnrollToken, MachineID: id.MachineID, Hostname: id.Hostname, Domain: id.Domain,
		OS: id.OS, OSVersion: id.OSVersion, AgentVersion: a.opt.Version, PublicKey: pub,
	})
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(rctx, http.MethodPost, a.cfg.HTTPBase()+"/api/agent/v1/enroll", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %s: %s", resp.Status, bytes.TrimSpace(b))
	}
	var er protocol.EnrollResponse
	if err := json.Unmarshal(b, &er); err != nil {
		return err
	}
	if len(er.ServerSignKey) != ed25519.PublicKeySize || er.AgentID == "" {
		return errors.New("invalid enrollment response")
	}
	a.cfg.AgentID, a.cfg.DeviceID, a.cfg.ServerSignKey = er.AgentID, er.DeviceID, er.ServerSignKey
	// The token is a bearer secret; keep it only while it might still be
	// needed. A reinstall supplies a new one.
	a.cfg.EnrollToken = ""
	if err := a.cfg.Save(); err != nil {
		return err
	}
	a.log.Info("enrolled", "agent_id", er.AgentID, "device_id", er.DeviceID)
	return nil
}

// ---- control channel ----

func (a *Agent) session(ctx context.Context) error {
	tcfg, err := tlsconf.Config(a.cfg.TLS)
	if err != nil {
		return err
	}
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(dctx, a.cfg.WSURL("/api/agent/v1/ws"), &websocket.DialOptions{
		HTTPClient:      &http.Client{Transport: &http.Transport{TLSClientConfig: tcfg, Proxy: http.ProxyFromEnvironment}},
		CompressionMode: websocket.CompressionNoContextTakeover,
	})
	if err != nil {
		return err
	}
	defer ws.CloseNow()
	ws.SetReadLimit(16 << 20)

	if err := a.handshake(dctx, ws); err != nil {
		return err
	}
	sctx, scancel := context.WithCancel(ctx)
	defer scancel()

	sendCh := make(chan []byte, 512)
	a.connMu.Lock()
	a.conn, a.sendCh = ws, sendCh
	a.connMu.Unlock()
	a.connected.Store(true)
	a.log.Info("connected to server", "url", a.cfg.ServerURL)
	a.sendHelperStatus()
	defer func() {
		a.connected.Store(false)
		// Unacked results are resent on the next connection.
		a.outbox.ResetInFlight()
		a.connMu.Lock()
		a.conn, a.sendCh = nil, nil
		a.connMu.Unlock()
		a.sendHelperStatus()
	}()

	errc := make(chan error, 2)
	go func() { errc <- a.writer(sctx, ws, sendCh) }()
	go func() { errc <- a.reader(sctx, ws) }()

	// Flush queued results, then report fresh state.
	go a.flushOutbox()
	a.requestRefresh("connect")

	err = <-errc
	scancel()
	return err
}

func (a *Agent) handshake(ctx context.Context, ws *websocket.Conn) error {
	_, data, err := ws.Read(ctx)
	if err != nil {
		return err
	}
	var env protocol.Envelope
	if err := json.Unmarshal(data, &env); err != nil || env.T != protocol.TAuthChallenge {
		return errors.New("expected auth.challenge")
	}
	var ch protocol.AuthChallenge
	if err := env.Decode(&ch); err != nil {
		return err
	}
	now := time.Now().UTC()
	resp, _ := protocol.NewEnvelope(protocol.TAuthResponse, uuid.NewString(), env.ID, protocol.AuthResponse{
		AgentID: a.cfg.AgentID, Timestamp: now, Signature: protocol.SignAuth(a.cfg.Key(), ch.Nonce, a.cfg.AgentID, now),
		AgentVersion: a.opt.Version, Protocol: protocol.Version, Capabilities: Capabilities,
	})
	b, _ := json.Marshal(resp)
	if err := ws.Write(ctx, websocket.MessageText, b); err != nil {
		return err
	}
	_, data, err = ws.Read(ctx)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	switch env.T {
	case protocol.TAuthOK:
		var ok protocol.AuthOK
		if err := env.Decode(&ok); err != nil {
			return err
		}
		a.applyConfig(ok.Config)
		if ok.DeviceID != "" && ok.DeviceID != a.cfg.DeviceID {
			a.cfg.DeviceID = ok.DeviceID
			_ = a.cfg.Save()
		}
		return nil
	case protocol.TAuthFail:
		var f protocol.AuthFail
		_ = env.Decode(&f)
		if f.Reenroll && a.cfg.EnrollToken != "" {
			return errReenroll
		}
		return fmt.Errorf("authentication rejected: %s", f.Reason)
	}
	return fmt.Errorf("unexpected handshake message %q", env.T)
}

func (a *Agent) writer(ctx context.Context, ws *websocket.Conn, ch <-chan []byte) error {
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	misses := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case b := <-ch:
			wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err := ws.Write(wctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				return err
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			err := ws.Ping(pctx)
			cancel()
			if err != nil {
				misses++
				if misses >= 3 {
					return fmt.Errorf("server not responding: %w", err)
				}
				continue
			}
			misses = 0
		}
	}
}

func (a *Agent) reader(ctx context.Context, ws *websocket.Conn) error {
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return err
		}
		var env protocol.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			a.log.Warn("bad frame from server", "err", err)
			continue
		}
		a.handle(ctx, env)
	}
}

// send queues a message for the server. It returns false when offline or
// when the queue is full.
func (a *Agent) send(t string, payload any) bool {
	return a.sendID(t, uuid.NewString(), payload)
}

func (a *Agent) sendID(t, id string, payload any) bool {
	env, err := protocol.NewEnvelope(t, id, "", payload)
	if err != nil {
		return false
	}
	b, err := json.Marshal(env)
	if err != nil {
		return false
	}
	a.connMu.Lock()
	ch := a.sendCh
	a.connMu.Unlock()
	if ch == nil {
		return false
	}
	select {
	case ch <- b:
		return true
	default:
		return false
	}
}

// sendResult persists a command result and sends it when possible.
func (a *Agent) sendResult(r protocol.CommandResult) {
	a.outbox.Put(r)
	a.flushOutbox()
}

var flushMu sync.Mutex

// flushOutbox sends persisted results that are not already awaiting the
// server's ack. Each result is sent with its outbox entry name as envelope
// id; the entry is removed only when the server acks it (onResultAck), so
// a result is never lost to a dropped connection or a reboot.
func (a *Agent) flushOutbox() {
	flushMu.Lock()
	defer flushMu.Unlock()
	names, results := a.outbox.Pending()
	for i, r := range results {
		if a.outbox.InFlight(names[i]) {
			continue
		}
		if !a.connected.Load() || !a.sendID(protocol.TCommandResult, names[i], r) {
			return
		}
		a.outbox.SetInFlight(names[i])
	}
}

func (a *Agent) onResultAck(ref string) {
	if ref != "" {
		a.outbox.Done(ref)
	}
}

func (a *Agent) applyConfig(c protocol.AgentConfig) {
	def := protocol.DefaultAgentConfig()
	if c.HeartbeatSec <= 0 {
		c.HeartbeatSec = def.HeartbeatSec
	}
	if c.HealthSec <= 0 {
		c.HealthSec = def.HealthSec
	}
	if c.BatterySec <= 0 {
		c.BatterySec = def.BatterySec
	}
	if c.HardwareSec <= 0 {
		c.HardwareSec = def.HardwareSec
	}
	if c.SoftwareSec <= 0 {
		c.SoftwareSec = def.SoftwareSec
	}
	if c.ConsentPolicy == "" {
		c.ConsentPolicy = def.ConsentPolicy
	}
	if c.ConsentTimeout <= 0 {
		c.ConsentTimeout = def.ConsentTimeout
	}
	a.acfg.Store(&c)
	a.maybeSelfUpdate(c)
}

func (a *Agent) platformKey() string { return runtime.GOOS + "/" + runtime.GOARCH }
