package api_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
	"github.com/tglobalcorp-mis/nexmis/internal/server/core"
)

func TestAgentUpdateRollout(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.login()

	var tok struct {
		Token string `json:"token"`
	}
	if code := e.do("POST", "/api/v1/enrollment-tokens", map[string]any{"name": "upd"}, &tok); code != 201 {
		t.Fatalf("create token: %d", code)
	}
	ag := e.enroll(tok.Token, "MACHINE-UPD", "PC-UPD-01")
	ag.connect(ctx, e.srv.URL)

	// Nothing published yet: no push.
	var st core.AgentUpdateStatus
	if code := e.do("GET", "/api/v1/settings/agent-update", nil, &st); code != 200 || st.Version != "" {
		t.Fatalf("status before publish: %d %+v", code, st)
	}
	if code := e.do("POST", "/api/v1/settings/agent-update/push", nil, nil); code != 409 {
		t.Fatalf("push without release: %d", code)
	}
	if e.svc.CheckAgentRelease(ctx) {
		t.Fatal("unchanged release must not push")
	}

	// Publishing a manifest pushes the new version to the connected agent.
	dir := e.svc.AgentDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"version":"1.0.1","auto_update":true,"builds":{"windows/amd64":{"file":"nexmis-agent.exe","sha256":"ab12","size":3}}}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if !e.svc.CheckAgentRelease(ctx) {
		t.Fatal("new release must push")
	}
	cfg := readConfig(t, ag, ctx)
	if b := cfg.AgentBuilds["windows/amd64"]; cfg.LatestAgent != "1.0.1" || b.SHA256 != "ab12" || b.URL == "" {
		t.Fatalf("pushed config: %+v", cfg)
	}
	if cfg.UpdateCheckSec != 6*3600 {
		t.Fatalf("update_check_sec = %d", cfg.UpdateCheckSec)
	}

	e.do("GET", "/api/v1/settings/agent-update", nil, &st)
	if st.Version != "1.0.1" || !st.AutoUpdate || st.AutoUpdateSource != "manifest" || st.Summary.Outdated != 1 || st.Summary.Total != 1 {
		t.Fatalf("status after publish: %+v", st)
	}

	// Periodic update check: the reply refers to the request.
	id := ag.write(ctx, protocol.TConfigRequest, protocol.ConfigRequest{Reason: "update_check", AgentVersion: "1.0.0"})
	env := ag.readType(ctx, protocol.TConfig)
	if env.Ref != id {
		t.Fatalf("config reply ref = %q, want %q", env.Ref, id)
	}

	// Switching auto-update off withdraws the version; push is refused.
	if code := e.do("PUT", "/api/v1/settings/agent-update", map[string]any{"auto_update": false}, &st); code != 200 || st.AutoUpdate || st.AutoUpdateSource != "settings" {
		t.Fatalf("disable auto-update: %d %+v", code, st)
	}
	if cfg := readConfig(t, ag, ctx); cfg.LatestAgent != "" {
		t.Fatalf("auto-update off still offers %q", cfg.LatestAgent)
	}
	if code := e.do("POST", "/api/v1/settings/agent-update/push", nil, nil); code != 409 {
		t.Fatalf("push with auto-update off: %d", code)
	}

	// Back to the manifest's setting, then a manual push.
	if code := e.do("PUT", "/api/v1/settings/agent-update", map[string]any{"auto_update": nil}, &st); code != 200 || !st.AutoUpdate || st.AutoUpdateSource != "manifest" {
		t.Fatalf("reset auto-update: %d %+v", code, st)
	}
	if cfg := readConfig(t, ag, ctx); cfg.LatestAgent != "1.0.1" {
		t.Fatalf("re-enabled config: %+v", cfg)
	}
	var pushed struct {
		Version string `json:"version"`
		Agents  int    `json:"agents"`
	}
	if code := e.do("POST", "/api/v1/settings/agent-update/push", nil, &pushed); code != 202 || pushed.Agents != 1 || pushed.Version != "1.0.1" {
		t.Fatalf("manual push: %d %+v", code, pushed)
	}
	if cfg := readConfig(t, ag, ctx); cfg.LatestAgent != "1.0.1" {
		t.Fatalf("manual push config: %+v", cfg)
	}
	var audit struct {
		Items []struct {
			Action string `json:"action"`
		} `json:"items"`
	}
	e.do("GET", "/api/v1/audit", nil, &audit)
	found := false
	for _, a := range audit.Items {
		found = found || a.Action == "agent.update.push"
	}
	if !found {
		t.Fatal("manual push is not audited")
	}
}

func readConfig(t *testing.T, ag *fakeAgent, ctx context.Context) protocol.AgentConfig {
	t.Helper()
	var cfg protocol.AgentConfig
	if err := ag.readType(ctx, protocol.TConfig).Decode(&cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}
