package api_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
	"github.com/tglobalcorp-mis/nexmis/internal/server/api"
	"github.com/tglobalcorp-mis/nexmis/internal/server/config"
	"github.com/tglobalcorp-mis/nexmis/internal/server/core"
	"github.com/tglobalcorp-mis/nexmis/internal/server/store"
)

// These tests need PostgreSQL. Set NEXMIS_TEST_DATABASE_URL to a database
// that may be wiped, e.g. postgres://nexmis:nexmis@localhost/nexmis_test.

type env struct {
	t   *testing.T
	srv *httptest.Server
	cli *http.Client
	svc *core.Service
}

func setup(t *testing.T) *env {
	url := os.Getenv("NEXMIS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("NEXMIS_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(ctx, url, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.Pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{DataDir: t.TempDir(), MasterKey: make([]byte, 32), SessionTTL: time.Hour,
		InitialAdminPassword: "Admin-Pass-123", Timezone: "Asia/Taipei", MaxUploadMiB: 10}
	svc, err := core.New(cfg, st, log)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(api.New(svc, nil).Handler())
	t.Cleanup(srv.Close)
	cfg.PublicURL = srv.URL
	jar, _ := cookiejar.New(nil)
	return &env{t: t, srv: srv, cli: &http.Client{Jar: jar}, svc: svc}
}

func (e *env) do(method, path string, body any, out any) int {
	e.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-NEXMIS-CSRF", "1")
	resp, err := e.cli.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			e.t.Fatalf("%s %s: decode %q: %v", method, path, b, err)
		}
	}
	return resp.StatusCode
}

func (e *env) login() {
	e.t.Helper()
	if code := e.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "Admin-Pass-123"}, nil); code != 200 {
		e.t.Fatalf("login: %d", code)
	}
}

// fakeAgent speaks the agent protocol.
type fakeAgent struct {
	t       *testing.T
	agentID string
	devID   string
	priv    ed25519.PrivateKey
	signKey ed25519.PublicKey
	ws      *websocket.Conn
}

func (e *env) enroll(token, machineID, host string) *fakeAgent {
	e.t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	return e.enrollKey(token, machineID, host, pub, priv)
}

func (e *env) enrollKey(token, machineID, host string, pub ed25519.PublicKey, priv ed25519.PrivateKey) *fakeAgent {
	e.t.Helper()
	var resp protocol.EnrollResponse
	b, _ := json.Marshal(protocol.EnrollRequest{Token: token, MachineID: machineID, Hostname: host, OS: "Windows 11 Pro",
		AgentVersion: "1.0.0", PublicKey: pub})
	r, err := http.Post(e.srv.URL+"/api/agent/v1/enroll", "application/json", bytes.NewReader(b))
	if err != nil {
		e.t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		body, _ := io.ReadAll(r.Body)
		e.t.Fatalf("enroll: %d %s", r.StatusCode, body)
	}
	_ = json.NewDecoder(r.Body).Decode(&resp)
	return &fakeAgent{t: e.t, agentID: resp.AgentID, devID: resp.DeviceID, priv: priv, signKey: resp.ServerSignKey}
}

func (a *fakeAgent) connect(ctx context.Context, base string) {
	a.t.Helper()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/api/agent/v1/ws", nil)
	if err != nil {
		a.t.Fatal(err)
	}
	ws.SetReadLimit(1 << 24)
	a.ws = ws
	env := a.read(ctx)
	var ch protocol.AuthChallenge
	_ = env.Decode(&ch)
	now := time.Now().UTC()
	a.write(ctx, protocol.TAuthResponse, protocol.AuthResponse{AgentID: a.agentID, Timestamp: now,
		Signature: protocol.SignAuth(a.priv, ch.Nonce, a.agentID, now), AgentVersion: "1.0.0", Protocol: 1})
	if ok := a.read(ctx); ok.T != protocol.TAuthOK {
		a.t.Fatalf("expected auth.ok, got %s %s", ok.T, ok.P)
	}
}

// write sends a message and returns its envelope id.
func (a *fakeAgent) write(ctx context.Context, t string, p any) string {
	env, _ := protocol.NewEnvelope(t, uuid.NewString(), "", p)
	b, _ := json.Marshal(env)
	if err := a.ws.Write(ctx, websocket.MessageText, b); err != nil {
		a.t.Fatal(err)
	}
	return env.ID
}

func (a *fakeAgent) read(ctx context.Context) protocol.Envelope {
	a.t.Helper()
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, b, err := a.ws.Read(rctx)
	if err != nil {
		a.t.Fatal(err)
	}
	var env protocol.Envelope
	_ = json.Unmarshal(b, &env)
	return env
}

// readType skips messages until one of type t arrives.
func (a *fakeAgent) readType(ctx context.Context, t string) protocol.Envelope {
	a.t.Helper()
	for i := 0; i < 20; i++ {
		if env := a.read(ctx); env.T == t {
			return env
		}
	}
	a.t.Fatalf("no %s received", t)
	return protocol.Envelope{}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 50; i++ {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestEndToEnd(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	// Unauthenticated access is refused; CSRF header is required.
	if code := e.do("GET", "/api/v1/devices", nil, nil); code != 401 {
		t.Fatalf("expected 401, got %d", code)
	}
	if code := e.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "wrong"}, nil); code != 401 {
		t.Fatalf("expected 401 for bad password, got %d", code)
	}
	e.login()

	// Enrollment token.
	var tok struct {
		Token          string `json:"token"`
		InstallCommand string `json:"install_command"`
	}
	// The test server is plain HTTP: no one-line installer is offered.
	if code := e.do("POST", "/api/v1/enrollment-tokens", map[string]any{"name": "http"}, &tok); code != 201 || tok.InstallCommand != "" {
		t.Fatalf("create token over http: %d %q", code, tok.InstallCommand)
	}
	var general map[string]any
	e.do("GET", "/api/v1/settings/general", nil, &general)
	general["server_url"] = "https://mis.example.test"
	if code := e.do("PUT", "/api/v1/settings/general", general, nil); code >= 300 {
		t.Fatalf("set server url: %d", code)
	}
	if code := e.do("POST", "/api/v1/enrollment-tokens", map[string]any{"name": "test", "tags": []string{"總部"}}, &tok); code != 201 {
		t.Fatalf("create token: %d", code)
	}
	if !strings.Contains(tok.InstallCommand, tok.Token) {
		t.Fatal("install command must contain the token")
	}
	if strings.Contains(tok.InstallCommand, "token="+tok.Token) {
		t.Fatal("install command must not put the token in the URL")
	}
	// The installer endpoint takes the token from a header only. No agent
	// binary is published in tests, so a valid token yields 404.
	if r, _ := http.Get(e.srv.URL + "/api/agent/v1/installer?token=" + tok.Token); r.StatusCode != 403 {
		t.Fatalf("installer with token in URL: %d", r.StatusCode)
	}
	ireq, _ := http.NewRequest("GET", e.srv.URL+"/api/agent/v1/installer", nil)
	ireq.Header.Set("X-NEXMIS-Enroll-Token", tok.Token)
	if r, _ := http.DefaultClient.Do(ireq); r.StatusCode != 404 {
		t.Fatalf("installer with token header: %d", r.StatusCode)
	}

	// Invalid token is refused.
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	b, _ := json.Marshal(protocol.EnrollRequest{Token: "bogus", MachineID: "m", Hostname: "h", PublicKey: pub})
	r, _ := http.Post(e.srv.URL+"/api/agent/v1/enroll", "application/json", bytes.NewReader(b))
	if r.StatusCode != 403 {
		t.Fatalf("expected 403 for bogus token, got %d", r.StatusCode)
	}

	ag := e.enroll(tok.Token, "MACHINE-1", "PC-ACC-01")
	ag.connect(ctx, e.srv.URL)
	defer ag.ws.CloseNow()

	ag.write(ctx, protocol.THeartbeat, protocol.Heartbeat{CPUPercent: 12.5, MemTotal: 16 << 30, MemUsed: 8 << 30,
		LoggedInUser: `CORP\alice`, IPAddresses: []string{"10.0.1.23"}, AgentVersion: "1.0.0", HelperOnline: true})
	hw := &protocol.Hardware{Manufacturer: "Dell Inc.", Model: "Latitude 5440", SerialNumber: "SN123", ChassisType: "laptop",
		OSName: "Windows 11 Pro", OSBuild: "22631.4317", OSEdition: "Pro", MemoryTotal: 16 << 30,
		NICs: []protocol.NIC{{Name: "Ethernet", MAC: "00-11-22-33-44-55"}}}
	ag.write(ctx, protocol.TInventory, protocol.Inventory{Kind: "hardware", Hash: "h1", Collected: time.Now(), Hardware: hw})
	ag.write(ctx, protocol.TInventory, protocol.Inventory{Kind: "software", Hash: "s1", Collected: time.Now(),
		Software: []protocol.Software{{Name: "7-Zip", Version: "23.01"}, {Name: "Google Chrome", Version: "129.0"}}})
	ag.write(ctx, protocol.TBattery, protocol.Battery{Present: true, DesignCapacity: 50000, FullChargeCap: 30000, HealthPercent: 60})
	ag.write(ctx, protocol.THealth, protocol.Health{CPUPercent: 10, MemPercent: 50,
		Issues: []protocol.Issue{{Code: "disk_low", Severity: "warning", Message: "C: 剩餘 6%"}}})

	var dev store.Device
	waitFor(t, "device telemetry", func() bool {
		e.do("GET", "/api/v1/devices/"+ag.devID, nil, &dev)
		return dev.Online && dev.SerialNumber == "SN123" && dev.HealthStatus == "warning" && dev.BatteryHealth != nil
	})
	if dev.LoggedInUser != `CORP\alice` || len(dev.MACAddresses) != 1 || dev.MACAddresses[0] != "00:11:22:33:44:55" {
		t.Fatalf("unexpected device: %+v", dev)
	}
	if len(dev.Tags) != 1 || dev.Tags[0] != "總部" {
		t.Fatalf("token tags not applied: %v", dev.Tags)
	}

	// Software change detection.
	ag.write(ctx, protocol.TInventory, protocol.Inventory{Kind: "software", Hash: "s2", Collected: time.Now(),
		Software: []protocol.Software{{Name: "7-Zip", Version: "24.08"}, {Name: "Notepad++", Version: "8.6"}}})
	var changes []store.InventoryChange
	waitFor(t, "inventory changes", func() bool {
		e.do("GET", "/api/v1/devices/"+ag.devID+"/inventory/changes", nil, &changes)
		return len(changes) == 3
	})

	// Unchanged inventory with an unknown hash makes the server ask for it.
	ag.write(ctx, protocol.TInventory, protocol.Inventory{Kind: "hardware", Hash: "different", Unchanged: true})
	req := ag.readType(ctx, protocol.TInventoryRequest)
	var ir protocol.InventoryRequest
	_ = req.Decode(&ir)
	if ir.Kind != "hardware" {
		t.Fatalf("inventory request kind %q", ir.Kind)
	}

	// Command round trip with signature verification.
	var cmd store.Command
	if code := e.do("POST", "/api/v1/devices/"+ag.devID+"/commands",
		map[string]any{"kind": "powershell", "script": "Get-Date", "timeout_sec": 60}, &cmd); code != 201 {
		t.Fatalf("issue command: %d", code)
	}
	envc := ag.readType(ctx, protocol.TCommand)
	var sc protocol.SignedCommand
	_ = envc.Decode(&sc)
	c, err := protocol.VerifyCommand(ag.signKey, ag.agentID, sc, time.Now())
	if err != nil {
		t.Fatalf("verify command: %v", err)
	}
	if c.Kind != "powershell" || c.CommandID != cmd.ID.String() {
		t.Fatalf("unexpected command %+v", c)
	}
	ag.write(ctx, protocol.TCommandAck, protocol.CommandAck{CommandID: c.CommandID, Accepted: true})
	resID := ag.write(ctx, protocol.TCommandResult, protocol.CommandResult{CommandID: c.CommandID, Status: "succeeded",
		ExitCode: 0, Stdout: "2026年10月17日"})
	waitFor(t, "command result", func() bool {
		e.do("GET", "/api/v1/commands/"+cmd.ID.String(), nil, &cmd)
		return cmd.Status == "succeeded" && cmd.Stdout == "2026年10月17日"
	})
	// The stored result is acked so the agent can drop it from its outbox,
	// and a resent duplicate is acked again without changing the command.
	if ack := ag.readType(ctx, protocol.TCommandResultAck); ack.Ref != resID {
		t.Fatalf("result ack ref %q, want %q", ack.Ref, resID)
	}
	dupID := ag.write(ctx, protocol.TCommandResult, protocol.CommandResult{CommandID: c.CommandID, Status: "failed"})
	if ack := ag.readType(ctx, protocol.TCommandResultAck); ack.Ref != dupID {
		t.Fatalf("duplicate result ack ref %q, want %q", ack.Ref, dupID)
	}

	// Invalid command kinds are rejected.
	if code := e.do("POST", "/api/v1/devices/"+ag.devID+"/commands", map[string]any{"kind": "install"}, nil); code != 400 {
		t.Fatalf("expected 400 for install via generic API, got %d", code)
	}

	// Chat both ways.
	var msg store.ChatMessage
	if code := e.do("POST", "/api/v1/devices/"+ag.devID+"/messages", map[string]string{"body": "您好，請重新開機"}, &msg); code != 201 {
		t.Fatalf("send chat: %d", code)
	}
	envm := ag.readType(ctx, protocol.TChat)
	var chat protocol.Chat
	_ = envm.Decode(&chat)
	if chat.Body != "您好，請重新開機" {
		t.Fatalf("chat body %q", chat.Body)
	}
	ag.write(ctx, protocol.TChatAck, protocol.ChatRead{MessageIDs: []string{chat.MessageID}})
	ag.write(ctx, protocol.TChat, protocol.Chat{From: "user", Sender: `CORP\alice`, Body: "好的"})
	var msgs []store.ChatMessage
	waitFor(t, "chat messages", func() bool {
		e.do("GET", "/api/v1/devices/"+ag.devID+"/messages", nil, &msgs)
		return len(msgs) == 2 && msgs[0].DeliveredAt != nil && msgs[1].Body == "好的"
	})

	// Remote session: consent denied by the user.
	var sess store.RemoteSession
	if code := e.do("POST", "/api/v1/devices/"+ag.devID+"/remote", map[string]string{"mode": "control", "reason": "協助設定印表機"}, &sess); code != 201 {
		t.Fatalf("start remote: %d", code)
	}
	envr := ag.readType(ctx, protocol.TRemoteRequest)
	_ = envr.Decode(&sc)
	rc, err := protocol.VerifyCommand(ag.signKey, ag.agentID, sc, time.Now())
	if err != nil || rc.Kind != "remote" {
		t.Fatalf("remote request: %v %+v", err, rc)
	}
	ag.write(ctx, protocol.TRemoteConsent, protocol.RemoteConsent{SessionID: sess.ID.String(), Granted: false, Reason: "denied"})
	waitFor(t, "remote denied", func() bool {
		e.do("GET", "/api/v1/remote-sessions/"+sess.ID.String(), nil, &sess)
		return sess.Status == "denied"
	})

	// Remote session: granted, media relayed agent -> viewer.
	if code := e.do("POST", "/api/v1/devices/"+ag.devID+"/remote", map[string]string{"mode": "view"}, &sess); code != 201 {
		t.Fatalf("start remote: %d", code)
	}
	envr = ag.readType(ctx, protocol.TRemoteRequest)
	_ = envr.Decode(&sc)
	rc, _ = protocol.VerifyCommand(ag.signKey, ag.agentID, sc, time.Now())
	var rr protocol.RemoteRequest
	_ = json.Unmarshal(rc.Args, &rr)
	ag.write(ctx, protocol.TRemoteConsent, protocol.RemoteConsent{SessionID: sess.ID.String(), Granted: true, User: `CORP\alice`})

	wsBase := "ws" + strings.TrimPrefix(e.srv.URL, "http")
	hdr := http.Header{}
	u, _ := http.NewRequest("GET", e.srv.URL, nil)
	for _, ck := range e.cli.Jar.Cookies(u.URL) {
		hdr.Add("Cookie", ck.Name+"="+ck.Value)
	}
	viewer, _, err := websocket.Dial(ctx, wsBase+"/api/v1/remote-sessions/"+sess.ID.String()+"/ws", &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatalf("viewer dial: %v", err)
	}
	defer viewer.CloseNow()
	media, _, err := websocket.Dial(ctx, wsBase+rr.RelayURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + rr.RelayToken}}})
	if err != nil {
		t.Fatalf("media dial: %v", err)
	}
	defer media.CloseNow()
	frame := []byte{0x03, 0, 0, 0, 7}
	_ = media.Write(ctx, websocket.MessageBinary, frame)
	got := false
	for i := 0; i < 5 && !got; i++ {
		rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		typ, data, err := viewer.Read(rctx)
		cancel()
		if err != nil {
			t.Fatalf("viewer read: %v", err)
		}
		got = typ == websocket.MessageBinary && bytes.Equal(data, frame)
	}
	if !got {
		t.Fatal("frame not relayed to viewer")
	}
	// View-only: input is dropped, ack is forwarded.
	_ = viewer.Write(ctx, websocket.MessageText, []byte(`{"t":"mouse","a":"down","x":1,"y":1}`))
	_ = viewer.Write(ctx, websocket.MessageText, []byte(`{"t":"ack","seq":7}`))
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	_, data, err := media.Read(rctx)
	cancel()
	if err != nil || !strings.Contains(string(data), `"ack"`) {
		t.Fatalf("expected ack forwarded (input dropped), got %q %v", data, err)
	}
	if code := e.do("POST", "/api/v1/remote-sessions/"+sess.ID.String()+"/stop", nil, nil); code != 204 {
		t.Fatalf("stop: %d", code)
	}
	waitFor(t, "remote ended", func() bool {
		e.do("GET", "/api/v1/remote-sessions/"+sess.ID.String(), nil, &sess)
		return sess.Status == "ended"
	})

	// Dashboard and audit.
	var dash map[string]any
	if code := e.do("GET", "/api/v1/dashboard", nil, &dash); code != 200 {
		t.Fatalf("dashboard: %d", code)
	}
	var audit struct {
		Items []store.AuditEntry `json:"items"`
		Total int                `json:"total"`
	}
	e.do("GET", "/api/v1/audit?action=command.*", nil, &audit)
	if audit.Total == 0 {
		t.Fatal("expected command audit entries")
	}
	// Audit log is append-only.
	if _, err := e.svc.Store.Pool.Exec(ctx, `DELETE FROM audit_log`); err == nil {
		t.Fatal("audit log must reject deletes")
	}

	// Offline: disconnect, device goes offline, queued command delivered on reconnect.
	ag.ws.Close(websocket.StatusNormalClosure, "")
	waitFor(t, "offline", func() bool {
		e.do("GET", "/api/v1/devices/"+ag.devID, nil, &dev)
		return !dev.Online
	})
	e.do("POST", "/api/v1/devices/"+ag.devID+"/commands", map[string]any{"kind": "refresh"}, &cmd)
	ag.connect(ctx, e.srv.URL)
	envc = ag.readType(ctx, protocol.TCommand)
	_ = envc.Decode(&sc)
	c, _ = protocol.VerifyCommand(ag.signKey, ag.agentID, sc, time.Now())
	if c.CommandID != cmd.ID.String() {
		t.Fatalf("queued command not delivered: %s", c.CommandID)
	}

	// Re-enrollment of the same machine keeps the device and rotates the key.
	ag2 := e.enroll(tok.Token, "MACHINE-1", "PC-ACC-01")
	if ag2.devID != ag.devID || ag2.agentID == ag.agentID {
		t.Fatalf("re-enroll: dev %s/%s agent %s/%s", ag.devID, ag2.devID, ag.agentID, ag2.agentID)
	}
	fmt.Fprintln(io.Discard)
}

func TestRoles(t *testing.T) {
	e := setup(t)
	e.login()
	if code := e.do("POST", "/api/v1/users", map[string]string{"username": "viewer1", "role": "viewer", "password": "Viewer-Pass-1"}, nil); code != 201 {
		t.Fatalf("create user: %d", code)
	}
	if code := e.do("POST", "/api/v1/users", map[string]string{"username": "weak", "role": "viewer", "password": "short"}, nil); code != 400 {
		t.Fatalf("weak password accepted: %d", code)
	}
	jar, _ := cookiejar.New(nil)
	v := &env{t: t, srv: e.srv, cli: &http.Client{Jar: jar}, svc: e.svc}
	if code := v.do("POST", "/api/v1/auth/login", map[string]string{"username": "viewer1", "password": "Viewer-Pass-1"}, nil); code != 200 {
		t.Fatalf("viewer login: %d", code)
	}
	if code := v.do("GET", "/api/v1/devices", nil, nil); code != 200 {
		t.Fatalf("viewer list devices: %d", code)
	}
	if code := v.do("GET", "/api/v1/audit", nil, nil); code != 403 {
		t.Fatalf("viewer audit: %d", code)
	}
	if code := v.do("POST", "/api/v1/groups", map[string]string{"name": "x"}, nil); code != 403 {
		t.Fatalf("viewer create group: %d", code)
	}
	// Missing CSRF header is refused even for an admin.
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/groups", strings.NewReader(`{"name":"x"}`))
	resp, err := e.cli.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("missing CSRF header: %d", resp.StatusCode)
	}
	// An administrative password reset ends the user's existing sessions.
	var users []struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	e.do("GET", "/api/v1/users", nil, &users)
	for _, u := range users {
		if u.Username == "viewer1" {
			if code := e.do("PATCH", "/api/v1/users/"+u.ID, map[string]string{"password": "Viewer-Pass-2"}, nil); code != 200 {
				t.Fatalf("reset password: %d", code)
			}
		}
	}
	if code := v.do("GET", "/api/v1/devices", nil, nil); code != 401 {
		t.Fatalf("session survived password reset: %d", code)
	}
	// Account lockout after repeated failures.
	for i := 0; i < 5; i++ {
		v.do("POST", "/api/v1/auth/login", map[string]string{"username": "viewer1", "password": "nope"}, nil)
	}
	if code := v.do("POST", "/api/v1/auth/login", map[string]string{"username": "viewer1", "password": "Viewer-Pass-2"}, nil); code != 401 {
		t.Fatalf("expected locked account, got %d", code)
	}
}

func TestEnrollRetryWithSingleUseToken(t *testing.T) {
	e := setup(t)
	e.login()
	var tok struct {
		Token string `json:"token"`
	}
	e.do("POST", "/api/v1/enrollment-tokens", map[string]any{"name": "once", "max_uses": 1}, &tok)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	first := e.enrollKey(tok.Token, "M-RETRY", "PC-R", pub, priv)
	// The response was "lost": the agent retries with the same key.
	again := e.enrollKey(tok.Token, "M-RETRY", "PC-R", pub, priv)
	if again.devID != first.devID || again.agentID != first.agentID {
		t.Fatalf("retry got dev %s agent %s, want %s %s", again.devID, again.agentID, first.devID, first.agentID)
	}
	// A different key is a new enrollment and the token is used up.
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)
	b, _ := json.Marshal(protocol.EnrollRequest{Token: tok.Token, MachineID: "M-OTHER", Hostname: "PC-O", PublicKey: pub2})
	r, err := http.Post(e.srv.URL+"/api/agent/v1/enroll", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode == 200 {
		t.Fatal("exhausted single-use token accepted for another machine")
	}
}

func TestDeploymentScheduleDiscovery(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.login()
	var tok struct {
		Token string `json:"token"`
	}
	e.do("POST", "/api/v1/enrollment-tokens", map[string]any{"name": "t"}, &tok)
	ag := e.enroll(tok.Token, "M-2", "PC-02")
	ag.connect(ctx, e.srv.URL)
	defer ag.ws.CloseNow()

	// Upload a package (multipart).
	var body bytes.Buffer
	bnd := "XBOUNDARY"
	fmt.Fprintf(&body, "--%s\r\nContent-Disposition: form-data; name=\"name\"\r\n\r\n7-Zip\r\n", bnd)
	fmt.Fprintf(&body, "--%s\r\nContent-Disposition: form-data; name=\"version\"\r\n\r\n24.08\r\n", bnd)
	fmt.Fprintf(&body, "--%s\r\nContent-Disposition: form-data; name=\"file\"; filename=\"7z.msi\"\r\nContent-Type: application/octet-stream\r\n\r\nMSI-CONTENT\r\n--%s--\r\n", bnd, bnd)
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/packages", &body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+bnd)
	req.Header.Set("X-NEXMIS-CSRF", "1")
	resp, err := e.cli.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var pkg store.Package
	_ = json.NewDecoder(resp.Body).Decode(&pkg)
	resp.Body.Close()
	if resp.StatusCode != 201 || pkg.Type != "msi" || pkg.InstallArgs != "/qn /norestart" || pkg.FileSize != 11 {
		t.Fatalf("upload: %d %+v", resp.StatusCode, pkg)
	}

	// Deploy to the device; the agent receives a signed install with a download URL that works.
	var dep store.Deployment
	devUUID := uuid.MustParse(ag.devID)
	if code := e.do("POST", "/api/v1/deployments", map[string]any{"package_id": pkg.ID,
		"target": map[string]any{"device_ids": []uuid.UUID{devUUID}}}, &dep); code != 201 {
		t.Fatalf("create deployment: %d", code)
	}
	envc := ag.readType(ctx, protocol.TCommand)
	var sc protocol.SignedCommand
	_ = envc.Decode(&sc)
	c, err := protocol.VerifyCommand(ag.signKey, ag.agentID, sc, time.Now())
	if err != nil || c.Kind != "install" {
		t.Fatalf("install command: %v %+v", err, c)
	}
	var ia protocol.InstallArgs
	_ = json.Unmarshal(c.Args, &ia)
	dl, err := http.Get(e.srv.URL + ia.DownloadURL)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := io.ReadAll(dl.Body)
	dl.Body.Close()
	if dl.StatusCode != 200 || string(content) != "MSI-CONTENT" {
		t.Fatalf("download: %d %q", dl.StatusCode, content)
	}
	if bad, _ := http.Get(e.srv.URL + ia.DownloadURL + "x"); bad.StatusCode != 403 {
		t.Fatalf("tampered token accepted: %d", bad.StatusCode)
	}
	ag.write(ctx, protocol.TCommandResult, protocol.CommandResult{CommandID: c.CommandID, Status: "failed", ExitCode: 1603})
	waitFor(t, "deployment failure counted", func() bool {
		e.do("GET", "/api/v1/deployments/"+dep.ID.String(), nil, &dep)
		return dep.Failed == 1
	})
	// Retry re-queues the failed device.
	if code := e.do("POST", "/api/v1/deployments/"+dep.ID.String()+"/retry", nil, &dep); code != 200 {
		t.Fatalf("retry: %d", code)
	}
	envc = ag.readType(ctx, protocol.TCommand)
	_ = envc.Decode(&sc)
	c, _ = protocol.VerifyCommand(ag.signKey, ag.agentID, sc, time.Now())
	ag.write(ctx, protocol.TCommandResult, protocol.CommandResult{CommandID: c.CommandID, Status: "succeeded", ExitCode: 0})
	waitFor(t, "deployment success", func() bool {
		e.do("GET", "/api/v1/deployments/"+dep.ID.String(), nil, &dep)
		return dep.Succeeded == 1 && dep.Failed == 0
	})

	// Schedules: validation and run-now.
	if code := e.do("POST", "/api/v1/schedules", map[string]any{"name": "bad", "cron": "nope",
		"action": map[string]any{"type": "refresh"}, "target": map[string]any{"all": true}}, nil); code != 400 {
		t.Fatalf("bad cron accepted: %d", code)
	}
	var sch store.Schedule
	if code := e.do("POST", "/api/v1/schedules", map[string]any{"name": "每日盤點", "cron": "0 3 * * *", "enabled": true,
		"action": map[string]any{"type": "refresh"}, "target": map[string]any{"all": true}}, &sch); code != 201 {
		t.Fatalf("create schedule: %d", code)
	}
	if sch.NextRunAt == nil {
		t.Fatal("next_run_at not computed")
	}
	if code := e.do("POST", "/api/v1/schedules/"+sch.ID.String()+"/run", nil, nil); code != 204 {
		t.Fatalf("run schedule: %d", code)
	}
	envc = ag.readType(ctx, protocol.TCommand)
	_ = envc.Decode(&sc)
	c, _ = protocol.VerifyCommand(ag.signKey, ag.agentID, sc, time.Now())
	if c.Kind != "refresh" {
		t.Fatalf("schedule command kind %s", c.Kind)
	}

	// Discovery via the agent as probe.
	var scan store.DiscoveryScan
	if code := e.do("POST", "/api/v1/discovery/scans", map[string]any{"cidrs": []string{"10.9.0.0/30"}, "probe_device_id": ag.devID}, &scan); code != 201 {
		t.Fatalf("create scan: %d", code)
	}
	envc = ag.readType(ctx, protocol.TCommand)
	_ = envc.Decode(&sc)
	c, _ = protocol.VerifyCommand(ag.signKey, ag.agentID, sc, time.Now())
	if c.Kind != protocol.CmdScan {
		t.Fatalf("expected scan command, got %s", c.Kind)
	}
	hosts, _ := json.Marshal([]protocol.DiscoveredHost{{IP: "10.9.0.1", MAC: "00:11:32:aa:bb:cc", OpenPorts: []int{5000}, DeviceType: "storage", Vendor: "Synology"}})
	ag.write(ctx, protocol.TCommandResult, protocol.CommandResult{CommandID: c.CommandID, Status: "succeeded", Data: hosts})
	waitFor(t, "scan completed", func() bool {
		e.do("GET", "/api/v1/discovery/scans/"+scan.ID.String(), nil, &scan)
		return scan.Status == "completed" && len(scan.Hosts) == 1
	})
	var imp map[string]int
	e.do("POST", "/api/v1/discovery/hosts/import", map[string]any{"scan_id": scan.ID, "ips": []string{"10.9.0.1"}}, &imp)
	if imp["imported"] != 1 {
		t.Fatalf("import: %v", imp)
	}
	var list struct {
		Total int `json:"total"`
	}
	e.do("GET", "/api/v1/devices?type=storage", nil, &list)
	if list.Total != 1 {
		t.Fatalf("imported storage device not listed: %d", list.Total)
	}
}
