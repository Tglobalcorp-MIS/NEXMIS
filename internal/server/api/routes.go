package api

import (
	"net/http"

	"github.com/tglobalcorp-mis/nexmis/internal/server/store"
)

func (s *Server) routes() {
	const (
		admin    = store.RoleAdmin
		operator = store.RoleOperator
		helpdesk = store.RoleHelpdesk
		viewer   = store.RoleViewer
	)
	m := s.mux

	// Public
	m.HandleFunc("GET /healthz", s.healthz)
	m.HandleFunc("POST /api/v1/auth/login", s.login)

	// Agent endpoints (authenticated by enrollment token / Ed25519 / file token)
	m.HandleFunc("POST /api/agent/v1/enroll", s.enroll)
	m.HandleFunc("GET /api/agent/v1/ws", s.svc.ServeAgentWS)
	m.HandleFunc("GET /api/agent/v1/remote/{id}", s.svc.ServeRemoteAgent)
	m.HandleFunc("GET /api/agent/v1/files/{token}", s.svc.ServeAgentFile)
	m.HandleFunc("GET /api/agent/v1/installer", s.publicInstaller)

	// Auth
	s.handle("POST /api/v1/auth/logout", viewer, s.logout)
	s.handle("GET /api/v1/auth/me", viewer, s.me)
	s.handle("POST /api/v1/auth/password", viewer, s.changePassword)

	// Dashboard & events
	s.handle("GET /api/v1/dashboard", viewer, s.dashboard)
	s.handle("GET /api/v1/events/ws", viewer, s.eventsWS)

	// Devices
	s.handle("GET /api/v1/devices", viewer, s.listDevices)
	s.handle("POST /api/v1/devices", operator, s.createDevice)
	s.handle("POST /api/v1/devices/bulk", operator, s.bulkDevices)
	s.handle("GET /api/v1/devices/{id}", viewer, s.getDevice)
	s.handle("PATCH /api/v1/devices/{id}", operator, s.updateDevice)
	s.handle("DELETE /api/v1/devices/{id}", admin, s.deleteDevice)
	s.handle("GET /api/v1/devices/{id}/lifecycle", viewer, s.getLifecycle)
	s.handle("POST /api/v1/devices/{id}/lifecycle", operator, s.setLifecycle)
	s.handle("GET /api/v1/devices/{id}/inventory", viewer, s.getInventory)
	s.handle("GET /api/v1/devices/{id}/software", viewer, s.getSoftware)
	s.handle("GET /api/v1/devices/{id}/inventory/changes", viewer, s.getInventoryChanges)
	s.handle("GET /api/v1/devices/{id}/health", viewer, s.getHealth)
	s.handle("GET /api/v1/devices/{id}/battery", viewer, s.getBattery)
	s.handle("GET /api/v1/devices/{id}/commands", viewer, s.deviceCommands)
	s.handle("POST /api/v1/devices/{id}/commands", helpdesk, s.issueCommand)
	s.handle("GET /api/v1/devices/{id}/messages", helpdesk, s.listMessages)
	s.handle("POST /api/v1/devices/{id}/messages", helpdesk, s.sendMessage)
	s.handle("POST /api/v1/devices/{id}/messages/read", helpdesk, s.readMessages)
	s.handle("POST /api/v1/devices/{id}/remote", helpdesk, s.startRemote)
	s.handle("GET /api/v1/devices/{id}/remote-sessions", helpdesk, s.deviceRemoteSessions)

	// Commands
	s.handle("GET /api/v1/commands", viewer, s.listCommands)
	s.handle("POST /api/v1/commands/batch", operator, s.batchCommand)
	s.handle("GET /api/v1/commands/{id}", viewer, s.getCommand)
	s.handle("POST /api/v1/commands/{id}/cancel", operator, s.cancelCommand)

	// Remote sessions
	s.handle("GET /api/v1/remote-sessions", helpdesk, s.listRemoteSessions)
	s.handle("GET /api/v1/remote-sessions/{id}", helpdesk, s.getRemoteSession)
	s.handle("POST /api/v1/remote-sessions/{id}/stop", helpdesk, s.stopRemoteSession)
	s.handle("GET /api/v1/remote-sessions/{id}/ws", helpdesk, s.remoteViewerWS)

	// Groups
	s.handle("GET /api/v1/groups", viewer, s.listGroups)
	s.handle("POST /api/v1/groups", admin, s.createGroup)
	s.handle("PATCH /api/v1/groups/{id}", admin, s.updateGroup)
	s.handle("DELETE /api/v1/groups/{id}", admin, s.deleteGroup)

	// Discovery
	s.handle("GET /api/v1/discovery/scans", viewer, s.listScans)
	s.handle("POST /api/v1/discovery/scans", operator, s.createScan)
	s.handle("GET /api/v1/discovery/scans/{id}", viewer, s.getScan)
	s.handle("POST /api/v1/discovery/hosts/import", operator, s.importHosts)

	// Packages & deployments
	s.handle("GET /api/v1/packages", viewer, s.listPackages)
	s.handle("POST /api/v1/packages", operator, s.uploadPackage)
	s.handle("PATCH /api/v1/packages/{id}", operator, s.updatePackage)
	s.handle("DELETE /api/v1/packages/{id}", admin, s.deletePackage)
	s.handle("GET /api/v1/deployments", viewer, s.listDeployments)
	s.handle("POST /api/v1/deployments", operator, s.createDeployment)
	s.handle("GET /api/v1/deployments/{id}", viewer, s.getDeployment)
	s.handle("POST /api/v1/deployments/{id}/cancel", operator, s.cancelDeployment)
	s.handle("POST /api/v1/deployments/{id}/retry", operator, s.retryDeployment)

	// Script library & schedules
	s.handle("GET /api/v1/scripts", viewer, s.listScripts)
	s.handle("POST /api/v1/scripts", operator, s.createScript)
	s.handle("PATCH /api/v1/scripts/{id}", operator, s.updateScript)
	s.handle("DELETE /api/v1/scripts/{id}", operator, s.deleteScript)
	s.handle("GET /api/v1/schedules", viewer, s.listSchedules)
	s.handle("POST /api/v1/schedules", operator, s.createSchedule)
	s.handle("PATCH /api/v1/schedules/{id}", operator, s.updateSchedule)
	s.handle("DELETE /api/v1/schedules/{id}", operator, s.deleteSchedule)
	s.handle("POST /api/v1/schedules/{id}/run", operator, s.runSchedule)

	// Audit
	s.handle("GET /api/v1/audit", admin, s.listAudit)
	s.handle("GET /api/v1/audit/export", admin, s.exportAudit)

	// Settings & administration
	s.handle("GET /api/v1/settings/ad", admin, s.getADSettings)
	s.handle("PUT /api/v1/settings/ad", admin, s.putADSettings)
	s.handle("POST /api/v1/settings/ad/test", admin, s.testAD)
	s.handle("POST /api/v1/settings/ad/sync", admin, s.syncAD)
	s.handle("GET /api/v1/settings/agent", admin, s.getAgentSettings)
	s.handle("PUT /api/v1/settings/agent", admin, s.putAgentSettings)
	s.handle("GET /api/v1/settings/general", admin, s.getGeneralSettings)
	s.handle("PUT /api/v1/settings/general", admin, s.putGeneralSettings)
	s.handle("GET /api/v1/enrollment-tokens", admin, s.listTokens)
	s.handle("POST /api/v1/enrollment-tokens", admin, s.createToken)
	s.handle("DELETE /api/v1/enrollment-tokens/{id}", admin, s.revokeToken)
	s.handle("GET /api/v1/users", admin, s.listUsers)
	s.handle("POST /api/v1/users", admin, s.createUser)
	s.handle("PATCH /api/v1/users/{id}", admin, s.updateUser)
	s.handle("DELETE /api/v1/users/{id}", admin, s.deleteUser)
	s.handle("GET /api/v1/system/info", viewer, s.systemInfo)
	s.handle("GET /api/v1/agent/download", viewer, s.downloadAgent)

	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "API 不存在")
	})
	m.HandleFunc("/", s.serveUI)
}
