package api

import (
	"net/http"

	"github.com/tglobalcorp-mis/nexmis/internal/server/core"
)

// ---- agent self-update ----

func (s *Server) getAgentUpdate(w http.ResponseWriter, r *http.Request) {
	st, err := s.svc.AgentUpdateStatus(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// putAgentUpdate sets or clears (null) the auto-update override.
func (s *Server) putAgentUpdate(w http.ResponseWriter, r *http.Request) {
	var in core.AgentUpdateSettings
	if !decode(w, r, &in) {
		return
	}
	if err := s.svc.SetAgentUpdateSettings(r.Context(), in); err != nil {
		s.fail(w, r, err)
		return
	}
	s.svc.Audit(r.Context(), s.actor(r), "settings.agent_update", "settings", "agent_update", "", in, true)
	s.getAgentUpdate(w, r)
}

// pushAgentUpdate re-sends the published version to every connected agent
// now (spread over a few minutes).
func (s *Server) pushAgentUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m := s.svc.AgentManifest()
	st, err := s.svc.AgentUpdateStatus(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if m.Version == "" || len(m.Builds) == 0 {
		writeError(w, http.StatusConflict, "conflict", "伺服器尚未發佈 Agent 版本")
		return
	}
	if !st.AutoUpdate {
		writeError(w, http.StatusConflict, "conflict", "自動更新已關閉；請先開啟，或改用裝置指令「更新 Agent」分批更新")
		return
	}
	n := s.svc.PushAgentUpdate("manual")
	s.svc.Audit(ctx, s.actor(r), "agent.update.push", "settings", "agent_update", m.Version,
		map[string]any{"version": m.Version, "agents": n, "outdated": st.Summary.Outdated}, true)
	writeJSON(w, http.StatusAccepted, map[string]any{"version": m.Version, "agents": n})
}
