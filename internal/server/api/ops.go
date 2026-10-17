package api

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
	"github.com/tglobalcorp-mis/nexmis/internal/server/ad"
	"github.com/tglobalcorp-mis/nexmis/internal/server/auth"
	"github.com/tglobalcorp-mis/nexmis/internal/server/core"
	"github.com/tglobalcorp-mis/nexmis/internal/server/events"
	"github.com/tglobalcorp-mis/nexmis/internal/server/secrets"
	"github.com/tglobalcorp-mis/nexmis/internal/server/store"
)

// ---- live events ----

func (s *Server) eventsWS(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" && !s.originAllowed(r, origin) {
		writeError(w, http.StatusForbidden, "forbidden", "請求來源驗證失敗")
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionNoContextTakeover})
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ch, cancel := s.svc.Bus.Subscribe()
	defer cancel()
	// Chat and remote-session data are helpdesk-only over REST; keep the
	// live feed consistent with that.
	helpdeskEvents := store.RoleRank(userFrom(r).Role) >= store.RoleRank(store.RoleHelpdesk)
	ctx := ws.CloseRead(r.Context())
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if !helpdeskEvents && (ev.Type == events.ChatMessage || ev.Type == events.RemoteUpdated) {
				continue
			}
			b, _ := json.Marshal(ev)
			wctx, c := context.WithTimeout(ctx, 10*time.Second)
			err := ws.Write(wctx, websocket.MessageText, b)
			c()
			if err != nil {
				return
			}
		case <-ping.C:
			pctx, c := context.WithTimeout(ctx, 10*time.Second)
			err := ws.Ping(pctx)
			c()
			if err != nil {
				return
			}
		}
	}
}

// ---- discovery ----

func (s *Server) listScans(w http.ResponseWriter, r *http.Request) {
	items, total, err := s.svc.Store.ListScans(r.Context(), page(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeList(w, items, total)
}

func (s *Server) createScan(w http.ResponseWriter, r *http.Request) {
	var in core.ScanInput
	if !decode(w, r, &in) {
		return
	}
	scan, err := s.svc.StartScan(r.Context(), s.actor(r), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, scan)
}

func (s *Server) getScan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	scan, err := s.svc.Store.GetScan(r.Context(), id, true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if scan.Hosts == nil {
		scan.Hosts = []store.DiscoveredHost{}
	}
	writeJSON(w, http.StatusOK, scan)
}

func (s *Server) importHosts(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ScanID uuid.UUID `json:"scan_id"`
		IPs    []string  `json:"ips"`
	}
	if !decode(w, r, &in) {
		return
	}
	n, err := s.svc.Store.ImportHosts(r.Context(), in.ScanID, in.IPs)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.svc.Audit(r.Context(), s.actor(r), "discovery.import", "scan", in.ScanID.String(), "", map[string]any{"ips": in.IPs, "imported": n}, true)
	writeJSON(w, http.StatusOK, map[string]int{"imported": n})
}

// ---- packages ----

func (s *Server) listPackages(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.Store.ListPackages(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

var packageTypes = map[string]bool{"msi": true, "exe": true, "msix": true, "ps1": true}

func (s *Server) uploadPackage(w http.ResponseWriter, r *http.Request) {
	limit := s.svc.Cfg.MaxUploadMiB << 20
	r.Body = http.MaxBytesReader(w, r.Body, limit+1<<20)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "需要 multipart/form-data")
		return
	}
	id := uuid.New()
	dir := filepath.Join(s.svc.Cfg.DataDir, "packages", id.String())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		s.fail(w, r, err)
		return
	}
	success := false
	defer func() {
		if !success {
			os.RemoveAll(dir)
		}
	}()
	fields := map[string]string{}
	var fileName, sum string
	var size int64
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "上傳中斷或檔案過大")
			return
		}
		if part.FormName() == "file" {
			fileName = filepath.Base(strings.ReplaceAll(part.FileName(), `\`, "/"))
			if fileName == "" || fileName == "." || fileName == "/" {
				writeError(w, http.StatusBadRequest, "bad_request", "檔名無效")
				return
			}
			f, err := os.OpenFile(filepath.Join(dir, fileName), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			h := sha256.New()
			size, err = io.Copy(io.MultiWriter(f, h), io.LimitReader(part, limit+1))
			f.Close()
			if err != nil {
				writeError(w, http.StatusBadRequest, "bad_request", "上傳中斷")
				return
			}
			if size > limit {
				writeError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("檔案超過上限 %d MiB", s.svc.Cfg.MaxUploadMiB))
				return
			}
			sum = hex.EncodeToString(h.Sum(nil))
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(part, 64<<10))
		fields[part.FormName()] = strings.TrimSpace(string(b))
	}
	if fileName == "" || size == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "請選擇要上傳的檔案")
		return
	}
	typ := strings.ToLower(fields["type"])
	if typ == "" {
		typ = strings.TrimPrefix(strings.ToLower(filepath.Ext(fileName)), ".")
		if typ == "msixbundle" || typ == "appx" {
			typ = "msix"
		}
	}
	if !packageTypes[typ] {
		writeError(w, http.StatusBadRequest, "bad_request", "套件類型必須是 msi、exe、msix 或 ps1")
		return
	}
	name := fields["name"]
	if name == "" {
		name = strings.TrimSuffix(fileName, filepath.Ext(fileName))
	}
	p := &store.Package{
		ID: id, Name: name, Version: fields["version"], Publisher: fields["publisher"], FileName: fileName,
		FileSize: size, SHA256: sum, Type: typ, InstallArgs: fields["install_args"], UninstallArgs: fields["uninstall_args"],
		StoragePath: filepath.Join(dir, fileName),
	}
	if p.InstallArgs == "" && typ == "msi" {
		p.InstallArgs = "/qn /norestart"
	}
	if codes := fields["success_codes"]; codes != "" {
		for _, c := range strings.Split(codes, ",") {
			if v, err := strconv.Atoi(strings.TrimSpace(c)); err == nil {
				p.SuccessCodes = append(p.SuccessCodes, int32(v))
			}
		}
	}
	u := userFrom(r)
	if err := s.svc.Store.CreatePackage(r.Context(), p, u.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	success = true
	s.svc.Audit(r.Context(), s.actor(r), "package.upload", "package", id.String(), name,
		map[string]any{"file_name": fileName, "size": size, "sha256": sum, "type": typ}, true)
	p.CreatedByName = &u.Username
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) updatePackage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var p store.PackagePatch
	if !decode(w, r, &p) {
		return
	}
	if err := s.svc.Store.UpdatePackage(r.Context(), id, p); err != nil {
		s.fail(w, r, err)
		return
	}
	pkg, err := s.svc.Store.GetPackage(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.svc.Audit(r.Context(), s.actor(r), "package.update", "package", id.String(), pkg.Name, p, true)
	writeJSON(w, http.StatusOK, pkg)
}

func (s *Server) deletePackage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	pkg, err := s.svc.Store.GetPackage(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	path, err := s.svc.Store.DeletePackage(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if path != "" {
		os.RemoveAll(filepath.Dir(path))
	}
	s.svc.Audit(r.Context(), s.actor(r), "package.delete", "package", id.String(), pkg.Name, nil, true)
	w.WriteHeader(http.StatusNoContent)
}

// ---- deployments ----

func (s *Server) listDeployments(w http.ResponseWriter, r *http.Request) {
	items, total, err := s.svc.Store.ListDeployments(r.Context(), page(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeList(w, items, total)
}

func (s *Server) createDeployment(w http.ResponseWriter, r *http.Request) {
	var in core.DeploymentInput
	if !decode(w, r, &in) {
		return
	}
	d, err := s.svc.CreateDeployment(r.Context(), s.actor(r), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) getDeployment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	d, err := s.svc.Store.GetDeployment(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	results, err := s.svc.Store.DeploymentResults(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if results == nil {
		results = []store.DeploymentResult{}
	}
	writeJSON(w, http.StatusOK, struct {
		*store.Deployment
		Results []store.DeploymentResult `json:"results"`
	}{d, results})
}

func (s *Server) cancelDeployment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	d, err := s.svc.CancelDeployment(r.Context(), s.actor(r), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) retryDeployment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	d, err := s.svc.RetryDeployment(r.Context(), s.actor(r), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// ---- scripts ----

func (s *Server) listScripts(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.Store.ListScripts(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if items == nil {
		items = []store.Script{}
	}
	writeJSON(w, http.StatusOK, items)
}

func validScript(name, shell, body *string, timeout *int) string {
	if name != nil && strings.TrimSpace(*name) == "" {
		return "請輸入名稱"
	}
	if shell != nil && *shell != "powershell" && *shell != "cmd" {
		return "shell 必須是 powershell 或 cmd"
	}
	if body != nil && strings.TrimSpace(*body) == "" {
		return "指令內容不可為空"
	}
	if timeout != nil && (*timeout < 10 || *timeout > 3600) {
		return "逾時需介於 10 到 3600 秒"
	}
	return ""
}

func (s *Server) createScript(w http.ResponseWriter, r *http.Request) {
	var sc store.Script
	if !decode(w, r, &sc) {
		return
	}
	if sc.TimeoutSec == 0 {
		sc.TimeoutSec = 300
	}
	if sc.Shell == "" {
		sc.Shell = "powershell"
	}
	if msg := validScript(&sc.Name, &sc.Shell, &sc.Body, &sc.TimeoutSec); msg != "" {
		writeError(w, http.StatusBadRequest, "bad_request", msg)
		return
	}
	if err := s.svc.Store.CreateScript(r.Context(), &sc, userFrom(r).ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.svc.Audit(r.Context(), s.actor(r), "script.create", "script", sc.ID.String(), sc.Name, sc, true)
	writeJSON(w, http.StatusCreated, sc)
}

func (s *Server) updateScript(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var p store.ScriptPatch
	if !decode(w, r, &p) {
		return
	}
	if msg := validScript(p.Name, p.Shell, p.Body, p.TimeoutSec); msg != "" {
		writeError(w, http.StatusBadRequest, "bad_request", msg)
		return
	}
	if err := s.svc.Store.UpdateScript(r.Context(), id, p); err != nil {
		s.fail(w, r, err)
		return
	}
	s.svc.Audit(r.Context(), s.actor(r), "script.update", "script", id.String(), "", p, true)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteScript(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.svc.Store.DeleteScript(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.svc.Audit(r.Context(), s.actor(r), "script.delete", "script", id.String(), "", nil, true)
	w.WriteHeader(http.StatusNoContent)
}

// ---- schedules ----

func (s *Server) listSchedules(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.Store.ListSchedules(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) createSchedule(w http.ResponseWriter, r *http.Request) {
	var sc store.Schedule
	if !decode(w, r, &sc) {
		return
	}
	if err := s.svc.ValidateSchedule(r.Context(), &sc); err != nil {
		s.fail(w, r, err)
		return
	}
	u := userFrom(r)
	if err := s.svc.Store.CreateSchedule(r.Context(), &sc, u.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.svc.Audit(r.Context(), s.actor(r), "schedule.create", "schedule", sc.ID.String(), sc.Name, sc, true)
	out, err := s.svc.Store.GetSchedule(r.Context(), sc.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) updateSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	cur, err := s.svc.Store.GetSchedule(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Partial update: decode onto the current values.
	if !decode(w, r, cur) {
		return
	}
	cur.ID = id
	if err := s.svc.ValidateSchedule(r.Context(), cur); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.svc.Store.SaveSchedule(r.Context(), cur); err != nil {
		s.fail(w, r, err)
		return
	}
	s.svc.Audit(r.Context(), s.actor(r), "schedule.update", "schedule", id.String(), cur.Name, cur, true)
	out, err := s.svc.Store.GetSchedule(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.svc.Store.DeleteSchedule(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.svc.Audit(r.Context(), s.actor(r), "schedule.delete", "schedule", id.String(), "", nil, true)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) runSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	sc, err := s.svc.Store.GetSchedule(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.svc.RunSchedule(r.Context(), s.actor(r), *sc); err != nil {
		s.fail(w, r, err)
		return
	}
	_ = s.svc.Store.TouchScheduleRun(r.Context(), id)
	w.WriteHeader(http.StatusNoContent)
}

// ---- audit ----

func auditFilter(r *http.Request) store.AuditFilter {
	q := r.URL.Query()
	f := store.AuditFilter{Actor: q.Get("actor"), Action: q.Get("action"), TargetID: q.Get("target_id"), Q: q.Get("q")}
	if t, err := time.Parse(time.RFC3339, q.Get("from")); err == nil {
		f.From = &t
	}
	if t, err := time.Parse(time.RFC3339, q.Get("to")); err == nil {
		f.To = &t
	}
	return f
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	items, total, err := s.svc.Store.ListAudit(r.Context(), auditFilter(r), page(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeList(w, items, total)
}

func (s *Server) exportAudit(w http.ResponseWriter, r *http.Request) {
	loc := s.svc.Location(r.Context())
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="nexmis-audit-%s.csv"`, time.Now().In(loc).Format("20060102-150405")))
	w.Write([]byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM so Excel shows Chinese correctly
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"時間", "操作者", "來源 IP", "動作", "目標類型", "目標 ID", "目標名稱", "結果", "詳細內容"})
	err := s.svc.Store.EachAudit(r.Context(), auditFilter(r), func(e store.AuditEntry) error {
		return cw.Write([]string{e.At.In(loc).Format("2006-01-02 15:04:05"), e.ActorName, e.ActorIP, e.Action,
			e.TargetType, e.TargetID, csvSafe(e.TargetName), e.Result, csvSafe(string(e.Details))})
	})
	cw.Flush()
	if err != nil {
		s.log.Error("audit export", "err", err)
	}
	s.svc.Audit(r.Context(), s.actor(r), "audit.export", "audit", "", "", r.URL.Query(), err == nil)
}

// csvSafe neutralises spreadsheet formula injection.
func csvSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

// ---- settings ----

type adSettingsView struct {
	ad.Settings
	BindPasswordSet bool       `json:"bind_password_set"`
	LastSync        *time.Time `json:"last_sync"`
}

func (s *Server) getADSettings(w http.ResponseWriter, r *http.Request) {
	a, err := s.svc.ADSettings(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v := adSettingsView{Settings: a, BindPasswordSet: a.BindPassword != ""}
	v.BindPassword = ""
	var last time.Time
	if ok, _ := s.svc.Store.GetSetting(r.Context(), "ad_last_sync", &last); ok {
		v.LastSync = &last
	}
	if v.RoleMappings == nil {
		v.RoleMappings = []ad.RoleMapping{}
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) putADSettings(w http.ResponseWriter, r *http.Request) {
	var a ad.Settings
	if !decode(w, r, &a) {
		return
	}
	for _, m := range a.RoleMappings {
		if store.RoleRank(m.Role) == 0 || strings.TrimSpace(m.GroupDN) == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "群組對應需填寫群組 DN 與有效角色")
			return
		}
	}
	if a.Enabled && (a.URL == "" || a.BaseDN == "" || a.BindDN == "") {
		writeError(w, http.StatusBadRequest, "bad_request", "啟用 AD 整合需填寫伺服器位址、Base DN 與服務帳號")
		return
	}
	if err := s.svc.SetADSettings(r.Context(), a); err != nil {
		s.fail(w, r, err)
		return
	}
	logged := a
	logged.BindPassword = ""
	s.svc.Audit(r.Context(), s.actor(r), "settings.ad", "settings", "ad", "", logged, true)
	s.getADSettings(w, r)
}

func (s *Server) testAD(w http.ResponseWriter, r *http.Request) {
	var in ad.Settings
	// The UI may post unsaved settings to test; empty body tests saved ones.
	saved, err := s.svc.ADSettings(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if r.ContentLength != 0 {
		if !decode(w, r, &in) {
			return
		}
		if in.BindPassword == "" {
			in.BindPassword = saved.BindPassword
		}
	} else {
		in = saved
	}
	names, err := in.Test()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error(), "sample_computers": []string{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "連線成功", "sample_computers": names})
}

func (s *Server) syncAD(w http.ResponseWriter, r *http.Request) {
	added, updated, total, err := s.svc.SyncAD(r.Context(), s.actor(r))
	if err != nil {
		var ve *core.ValidationError
		if errors.As(err, &ve) {
			s.fail(w, r, err)
			return
		}
		writeError(w, http.StatusBadGateway, "ad_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"added": added, "updated": updated, "total": total})
}

func (s *Server) getAgentSettings(w http.ResponseWriter, r *http.Request) {
	c := s.svc.AgentConfig(r.Context())
	c.LatestAgent, c.AgentBuilds = "", nil
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) putAgentSettings(w http.ResponseWriter, r *http.Request) {
	c := s.svc.AgentConfig(r.Context())
	if !decode(w, r, &c) {
		return
	}
	if err := s.svc.SetAgentConfig(r.Context(), c); err != nil {
		s.fail(w, r, err)
		return
	}
	s.svc.Audit(r.Context(), s.actor(r), "settings.agent", "settings", "agent", "", c, true)
	s.getAgentSettings(w, r)
}

func (s *Server) getGeneralSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.General(r.Context()))
}

func (s *Server) putGeneralSettings(w http.ResponseWriter, r *http.Request) {
	g := s.svc.General(r.Context())
	if !decode(w, r, &g) {
		return
	}
	if err := s.svc.SetGeneral(r.Context(), g); err != nil {
		s.fail(w, r, err)
		return
	}
	s.svc.Audit(r.Context(), s.actor(r), "settings.general", "settings", "general", "", g, true)
	writeJSON(w, http.StatusOK, s.svc.General(r.Context()))
}

// ---- enrollment tokens ----

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.Store.ListEnrollmentTokens(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if items == nil {
		items = []store.EnrollmentToken{}
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) installCommand(token string) string {
	base := strings.TrimRight(s.svc.General(context.Background()).ServerURL, "/")
	return fmt.Sprintf(`powershell -NoProfile -ExecutionPolicy Bypass -Command "$ErrorActionPreference='Stop'; `+
		`[Net.ServicePointManager]::SecurityProtocol=[Net.SecurityProtocolType]::Tls12; `+
		`$p=Join-Path $env:TEMP 'nexmis-agent.exe'; `+
		`Invoke-WebRequest -UseBasicParsing -Uri '%s/api/agent/v1/installer' -Headers @{'X-NEXMIS-Enroll-Token'='%s'} -OutFile $p; `+
		`& $p install --server '%s' --token '%s'"`, base, token, base, token)
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var in store.EnrollmentToken
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "請輸入權杖名稱")
		return
	}
	if in.MaxUses != nil && *in.MaxUses <= 0 {
		in.MaxUses = nil
	}
	token := secrets.RandomToken("nxe_", 24)
	in.TokenPrefix = token[:8]
	u := userFrom(r)
	if err := s.svc.Store.CreateEnrollmentToken(r.Context(), &in, secrets.HashToken(token), u.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	in.CreatedByName = &u.Username
	if in.Tags == nil {
		in.Tags = []string{}
	}
	s.svc.Audit(r.Context(), s.actor(r), "enrollment_token.create", "enrollment_token", in.ID.String(), in.Name,
		map[string]any{"expires_at": in.ExpiresAt, "max_uses": in.MaxUses, "group_id": in.GroupID, "tags": in.Tags}, true)
	writeJSON(w, http.StatusCreated, struct {
		store.EnrollmentToken
		Token          string `json:"token"`
		InstallCommand string `json:"install_command"`
	}{in, token, s.installCommand(token)})
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.svc.Store.RevokeEnrollmentToken(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.svc.Audit(r.Context(), s.actor(r), "enrollment_token.revoke", "enrollment_token", id.String(), "", nil, true)
	w.WriteHeader(http.StatusNoContent)
}

// ---- users ----

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.Store.ListUsers(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		Role        string `json:"role"`
		Password    string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if in.Username == "" || strings.ContainsAny(in.Username, `\@ `) {
		writeError(w, http.StatusBadRequest, "bad_request", "帳號不可為空，且不可包含 \\、@ 或空白")
		return
	}
	if store.RoleRank(in.Role) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "無效的角色")
		return
	}
	if err := auth.CheckPasswordPolicy(in.Password); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	u := &store.User{Username: in.Username, DisplayName: in.DisplayName, Email: in.Email, Source: "local", Role: in.Role, PasswordHash: hash}
	if err := s.svc.Store.CreateUser(r.Context(), u); err != nil {
		s.fail(w, r, err)
		return
	}
	s.svc.Audit(r.Context(), s.actor(r), "user.create", "user", u.ID.String(), u.Username, map[string]string{"role": u.Role}, true)
	writeJSON(w, http.StatusCreated, u)
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var p store.UserPatch
	if !decode(w, r, &p) {
		return
	}
	target, err := s.svc.Store.UserByID(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if p.Role != nil && store.RoleRank(*p.Role) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "無效的角色")
		return
	}
	me := userFrom(r)
	if me.ID == id && ((p.Role != nil && *p.Role != store.RoleAdmin) || (p.Disabled != nil && *p.Disabled)) {
		writeError(w, http.StatusBadRequest, "bad_request", "不可降低自己的權限或停用自己")
		return
	}
	if p.Password != nil && *p.Password != "" {
		if target.Source != "local" {
			writeError(w, http.StatusBadRequest, "bad_request", "AD 帳號的密碼由網域管理")
			return
		}
		if err := auth.CheckPasswordPolicy(*p.Password); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		hash, err := auth.HashPassword(*p.Password)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		p.PasswordHash = &hash
	}
	if err := s.svc.Store.UpdateUser(r.Context(), id, p); err != nil {
		s.fail(w, r, err)
		return
	}
	// A password reset is how a compromised account is recovered, so it
	// must also end the sessions opened with the old password.
	if p.PasswordHash != nil {
		keep := ""
		if c, err := r.Cookie(sessionCookie); err == nil && me.ID == id {
			keep = secrets.HashToken(c.Value)
		}
		if err := s.svc.Store.DeleteUserSessions(r.Context(), id, keep); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	logged := p
	logged.Password, logged.PasswordHash = nil, nil
	s.svc.Audit(r.Context(), s.actor(r), "user.update", "user", id.String(), target.Username,
		map[string]any{"changes": logged, "password_changed": p.PasswordHash != nil}, true)
	u, err := s.svc.Store.UserByID(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if userFrom(r).ID == id {
		writeError(w, http.StatusBadRequest, "bad_request", "不可刪除自己")
		return
	}
	target, err := s.svc.Store.UserByID(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.svc.Store.DeleteUser(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.svc.Audit(r.Context(), s.actor(r), "user.delete", "user", id.String(), target.Username, nil, true)
	w.WriteHeader(http.StatusNoContent)
}

// ---- system ----

func (s *Server) systemInfo(w http.ResponseWriter, r *http.Request) {
	var dbVersion string
	_ = s.svc.Store.Pool.QueryRow(r.Context(), `SHOW server_version`).Scan(&dbVersion)
	m := s.svc.AgentManifest()
	_, _, installer := s.svc.AgentInstallerPath()
	writeJSON(w, http.StatusOK, map[string]any{
		"version": core.Version, "build_time": core.BuildTime, "go_version": runtime.Version(),
		"db_version": dbVersion, "agents_connected": s.svc.ConnectedAgents(),
		"uptime_sec": int64(s.svc.Uptime().Seconds()), "protocol": protocol.Version,
		"agent_latest_version": m.Version, "agent_installer_available": installer,
	})
}

func (s *Server) downloadAgent(w http.ResponseWriter, r *http.Request) {
	path, name, ok := s.svc.AgentInstallerPath()
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "伺服器尚未放置 Agent 安裝檔（請參考部署文件）")
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, path)
}

// publicInstaller lets unattended installs fetch the agent with a valid
// enrollment token (the token is not consumed).
func (s *Server) publicInstaller(w http.ResponseWriter, r *http.Request) {
	if !s.enrollRate.Allow(core.ClientIP(r, s.svc.Cfg.TrustProxy)) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
		return
	}
	// The token travels in a header, never the URL, so it does not end up in
	// proxy or access logs.
	tok := r.Header.Get("X-NEXMIS-Enroll-Token")
	if tok == "" {
		writeError(w, http.StatusForbidden, "forbidden", "invalid enrollment token")
		return
	}
	ok, err := s.svc.Store.EnrollmentTokenValid(r.Context(), secrets.HashToken(tok))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !ok {
		writeError(w, http.StatusForbidden, "forbidden", "invalid enrollment token")
		return
	}
	m := s.svc.AgentManifest()
	b, found := m.Builds["windows/"+nonEmpty(r.URL.Query().Get("arch"), "amd64")]
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "agent binary not published")
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="nexmis-agent.exe"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, filepath.Join(s.svc.AgentDir(), filepath.Base(b.File)))
}

func nonEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
