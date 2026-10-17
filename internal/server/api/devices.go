package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tglobalcorp-mis/nexmis/internal/server/core"
	"github.com/tglobalcorp-mis/nexmis/internal/server/store"
)

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	g := s.svc.General(r.Context())
	d, err := s.svc.Store.Dashboard(r.Context(), g.BatteryAlertPercent, g.WarrantyAlertDays)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.DeviceFilter{
		Q: q.Get("q"), Status: q.Get("status"), Type: q.Get("type"), Lifecycle: q.Get("lifecycle"),
		Health: q.Get("health"), GroupID: queryUUID(r, "group_id"), Tag: q.Get("tag"),
		HasAgent: queryBool(r, "has_agent"), Sort: q.Get("sort"),
	}
	items, total, err := s.svc.Store.ListDevices(r.Context(), f, page(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeList(w, items, total)
}

func (s *Server) getDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	d, err := s.svc.Store.GetDevice(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if d.ConsentPolicy == "" {
		d.ConsentPolicy = s.svc.AgentConfig(r.Context()).ConsentPolicy
	}
	writeJSON(w, http.StatusOK, d)
}

func validDeviceType(t string) bool { return slices.Contains(store.DeviceTypes, t) }

func (s *Server) createDevice(w http.ResponseWriter, r *http.Request) {
	// The body carries the device itself plus optional asset fields
	// (group, tags, asset tag, ...), which are applied as a patch.
	var raw json.RawMessage
	if !decode(w, r, &raw) {
		return
	}
	var in store.NewManualDevice
	var asset store.DevicePatch
	if json.Unmarshal(raw, &in) != nil || json.Unmarshal(raw, &asset) != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "請求格式錯誤")
		return
	}
	asset.Name, asset.DeviceType = nil, nil
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "請輸入裝置名稱")
		return
	}
	if in.DeviceType != "" && !validDeviceType(in.DeviceType) {
		writeError(w, http.StatusBadRequest, "bad_request", "無效的裝置類型")
		return
	}
	id, err := s.svc.Store.CreateManualDevice(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.svc.Store.UpdateDevice(r.Context(), id, asset); err != nil {
		s.fail(w, r, err)
		return
	}
	s.svc.Audit(r.Context(), s.actor(r), "device.create", "device", id.String(), in.Name, in, true)
	d, err := s.svc.Store.GetDevice(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) updateDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var p store.DevicePatch
	if !decode(w, r, &p) {
		return
	}
	if p.Name != nil && strings.TrimSpace(*p.Name) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "名稱不可為空")
		return
	}
	if p.DeviceType != nil && !validDeviceType(*p.DeviceType) {
		writeError(w, http.StatusBadRequest, "bad_request", "無效的裝置類型")
		return
	}
	if err := s.svc.Store.UpdateDevice(r.Context(), id, p); err != nil {
		s.fail(w, r, err)
		return
	}
	d, err := s.svc.Store.GetDevice(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.svc.Audit(r.Context(), s.actor(r), "device.update", "device", id.String(), d.Name, p, true)
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) bulkDevices(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DeviceIDs []uuid.UUID `json:"device_ids"`
		AddTags   []string    `json:"add_tags"`
		GroupID   *string     `json:"group_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.DeviceIDs) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "請選擇裝置")
		return
	}
	ctx := r.Context()
	if len(in.AddTags) > 0 {
		if err := s.svc.Store.AddTags(ctx, in.DeviceIDs, in.AddTags); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if in.GroupID != nil {
		var gid *uuid.UUID
		if *in.GroupID != "" {
			id, err := uuid.Parse(*in.GroupID)
			if err != nil {
				writeError(w, http.StatusBadRequest, "bad_request", "無效的群組")
				return
			}
			gid = &id
		}
		if err := s.svc.Store.SetGroup(ctx, in.DeviceIDs, gid); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if in.GroupID != nil {
		go s.svc.PushConfig()
	}
	s.svc.Audit(ctx, s.actor(r), "device.bulk_update", "device", "", "", in, true)
	writeJSON(w, http.StatusOK, map[string]int{"updated": len(in.DeviceIDs)})
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.svc.DeleteDevice(r.Context(), s.actor(r), id); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getLifecycle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	ev, err := s.svc.Store.LifecycleEvents(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if ev == nil {
		ev = []store.LifecycleEvent{}
	}
	writeJSON(w, http.StatusOK, ev)
}

func (s *Server) setLifecycle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		State  string `json:"state"`
		Reason string `json:"reason"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !slices.Contains(store.LifecycleStates, in.State) {
		writeError(w, http.StatusBadRequest, "bad_request", "無效的生命週期狀態")
		return
	}
	u := userFrom(r)
	from, err := s.svc.Store.SetLifecycle(r.Context(), id, in.State, in.Reason, u.Username)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d, err := s.svc.Store.GetDevice(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.svc.Audit(r.Context(), s.actor(r), "device.lifecycle", "device", id.String(), d.Name,
		map[string]string{"from": from, "to": in.State, "reason": in.Reason}, true)
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) getInventory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	inv, err := s.svc.Store.Inventory(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, inv)
}

func (s *Server) getSoftware(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	p := page(r)
	if p.PageSize == 0 {
		p.PageSize = 500
	}
	items, total, err := s.svc.Store.Software(r.Context(), id, r.URL.Query().Get("q"), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeList(w, items, total)
}

func (s *Server) getInventoryChanges(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	items, err := s.svc.Store.InventoryChanges(r.Context(), id, queryInt(r, "limit", 200, 1, 1000))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if items == nil {
		items = []store.InventoryChange{}
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) getHealth(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	v, err := s.svc.Store.HealthHistory(r.Context(), id, queryInt(r, "days", 7, 1, 90))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) getBattery(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	v, err := s.svc.Store.BatteryHistory(r.Context(), id, queryInt(r, "days", 180, 1, 1095))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// ---- commands ----

func (s *Server) deviceCommands(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	items, total, err := s.svc.Store.ListCommands(r.Context(), store.CommandFilter{DeviceID: &id}, page(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeList(w, items, total)
}

func (s *Server) issueCommand(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var nc core.NewCommand
	if !decode(w, r, &nc) {
		return
	}
	if store.RoleRank(userFrom(r).Role) < store.RoleRank(store.RoleOperator) && !core.HelpdeskKinds[nc.Kind] {
		writeError(w, http.StatusForbidden, "forbidden", "權限不足：此指令需要操作員以上權限")
		return
	}
	cmd, err := s.svc.IssueCommand(r.Context(), s.actor(r), id, nc, nil)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, cmd)
}

func (s *Server) listCommands(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, total, err := s.svc.Store.ListCommands(r.Context(), store.CommandFilter{
		DeviceID: queryUUID(r, "device_id"), DeploymentID: queryUUID(r, "deployment_id"),
		Status: q.Get("status"), Kind: q.Get("kind"),
	}, page(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeList(w, items, total)
}

func (s *Server) batchCommand(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Target  store.Target    `json:"target"`
		Command core.NewCommand `json:"command"`
	}
	if !decode(w, r, &in) {
		return
	}
	ids, err := s.svc.IssueBatch(r.Context(), s.actor(r), in.Target, in.Command)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"count": len(ids), "command_ids": ids})
}

func (s *Server) getCommand(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	c, err := s.svc.Store.GetCommand(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) cancelCommand(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	c, err := s.svc.CancelCommand(r.Context(), s.actor(r), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// ---- chat ----

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var before *time.Time
	if v := r.URL.Query().Get("before"); v != "" {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			before = &t
		}
	}
	items, err := s.svc.Store.ListChat(r.Context(), id, before, queryInt(r, "limit", 200, 1, 500))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if items == nil {
		items = []store.ChatMessage{}
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) sendMessage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if !decode(w, r, &in) {
		return
	}
	m, err := s.svc.SendChat(r.Context(), s.actor(r), id, in.Body)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) readMessages(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.svc.MarkUserChatRead(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- remote ----

func (s *Server) startRemote(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in core.RemoteRequestInput
	if !decode(w, r, &in) {
		return
	}
	sess, err := s.svc.StartRemote(r.Context(), s.actor(r), id, in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, sess)
}

func (s *Server) deviceRemoteSessions(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	items, total, err := s.svc.Store.ListRemoteSessions(r.Context(), &id, "", page(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeList(w, items, total)
}

func (s *Server) listRemoteSessions(w http.ResponseWriter, r *http.Request) {
	items, total, err := s.svc.Store.ListRemoteSessions(r.Context(), queryUUID(r, "device_id"), r.URL.Query().Get("status"), page(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeList(w, items, total)
}

func (s *Server) getRemoteSession(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	sess, err := s.svc.Store.GetRemoteSession(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) stopRemoteSession(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.svc.StopRemoteByUser(r.Context(), s.actor(r), id); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) remoteViewerWS(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" && !s.originAllowed(r, origin) {
		writeError(w, http.StatusForbidden, "forbidden", "請求來源驗證失敗")
		return
	}
	u := userFrom(r)
	s.svc.ServeRemoteViewer(w, r, s.actor(r), u.Role == store.RoleAdmin)
}

// ---- groups ----

func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) {
	g, err := s.svc.Store.ListGroups(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if g == nil {
		g = []store.Group{}
	}
	writeJSON(w, http.StatusOK, g)
}

func validConsent(p string) bool {
	return p == "always_ask" || p == "notify_only" || p == "deny"
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	var g store.Group
	if !decode(w, r, &g) {
		return
	}
	g.Name = strings.TrimSpace(g.Name)
	if g.Name == "" || (g.ConsentPolicy != "" && !validConsent(g.ConsentPolicy)) {
		writeError(w, http.StatusBadRequest, "bad_request", "請輸入群組名稱並選擇有效的同意政策")
		return
	}
	if err := s.svc.Store.CreateGroup(r.Context(), &g); err != nil {
		s.fail(w, r, err)
		return
	}
	s.svc.Audit(r.Context(), s.actor(r), "group.create", "group", g.ID.String(), g.Name, g, true)
	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) updateGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var p store.GroupPatch
	if !decode(w, r, &p) {
		return
	}
	if p.ConsentPolicy != nil && !validConsent(*p.ConsentPolicy) {
		writeError(w, http.StatusBadRequest, "bad_request", "無效的同意政策")
		return
	}
	if err := s.svc.Store.UpdateGroup(r.Context(), id, p); err != nil {
		s.fail(w, r, err)
		return
	}
	g, err := s.svc.Store.GetGroup(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.svc.Audit(r.Context(), s.actor(r), "group.update", "group", id.String(), g.Name, p, true)
	if p.ConsentPolicy != nil {
		go s.svc.PushConfig()
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	g, err := s.svc.Store.GetGroup(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.svc.Store.DeleteGroup(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.svc.Audit(r.Context(), s.actor(r), "group.delete", "group", id.String(), g.Name, nil, true)
	w.WriteHeader(http.StatusNoContent)
}
