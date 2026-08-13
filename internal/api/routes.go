package api

import (
	"net/http"

	"github.com/cerberus8484/opensourcebackup/internal/audit"
	"github.com/cerberus8484/opensourcebackup/internal/auth"
	"github.com/cerberus8484/opensourcebackup/internal/security"
)

// RegisterRoutes attaches all v1 API routes to mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.health)

	// ── Authentication ─────────────────────────────────────────────────────
	authLimiter := security.NewIPRateLimiter(5.0/60, 5)
	authRate    := security.RateLimit(authLimiter)
	// Enrollment is more sensitive (joins an agent / mints tokens) — tighter limit.
	enrollLimiter := security.NewIPRateLimiter(2.0/60, 3) // 2/min sustained, burst 3
	enrollRate    := security.RateLimit(enrollLimiter)
	mux.Handle("POST /auth/login",  authRate(http.HandlerFunc(h.handleRBACLogin)))
	mux.Handle("POST /auth/logout", http.HandlerFunc(h.handleRBACLogout))
	mux.Handle("GET /auth/me",      http.HandlerFunc(h.handleAuthMe))
	mux.Handle("GET /auth/status",  http.HandlerFunc(h.handleAuthStatus)) // backward compat

	// ── User management — admin only ────────────────────────────────────────
	mux.HandleFunc("GET /v1/users",               requireRoleFn(auth.RoleAdmin, h.handleListUsers))
	mux.HandleFunc("POST /v1/users",              requireRoleFn(auth.RoleAdmin, h.handleCreateUser))
	mux.HandleFunc("PUT /v1/users/{id}/role",     requireRoleFn(auth.RoleAdmin, h.handleUpdateUserRole))
	mux.HandleFunc("DELETE /v1/users/{id}",       requireRoleFn(auth.RoleAdmin, h.handleDeleteUser))

	// Agent binary downloads
	mux.HandleFunc("GET /downloads/agent", h.listDownloads)
	mux.HandleFunc("GET /downloads/agent/{version}/{platform}", h.downloadAgent)

	// Agent install scripts (served from scripts/ directory)
	mux.HandleFunc("GET /scripts/install-agent.sh", h.serveInstallScript)
	mux.HandleFunc("GET /scripts/install-agent-freebsd.sh", h.serveInstallScript)
	mux.HandleFunc("GET /scripts/install-agent.ps1", h.serveInstallScript)
	// Local (all-in-one) installer script
	mux.HandleFunc("GET /scripts/install-local.ps1", h.serveInstallScript)

	// Enrollment — operator+ required (generates token that lets an agent join).
	// Rate-limited to blunt token-minting abuse.
	mux.Handle("POST /v1/systems/{id}/enrollment-token",
		enrollRate(requireRoleFn(auth.RoleOperator, h.createEnrollmentToken)))

	// Agent enrollment (unauthenticated — presents one-time token).
	// Rate-limited to blunt brute-forcing of enrollment tokens.
	mux.Handle("POST /v1/agent/enroll", enrollRate(http.HandlerFunc(h.enrollAgent)))

	// Agent-only routes (protected by AgentAuth middleware)
	agentMux := http.NewServeMux()
	agentMux.HandleFunc("PUT /v1/agent/heartbeat", h.handleAgentHeartbeat)
	agentMux.HandleFunc("GET /v1/agent/jobs", h.listAgentJobs)
	// Retention
	agentMux.HandleFunc("GET  /v1/agent/retention/jobs", h.handleListAgentRetentionJobs)
	agentMux.HandleFunc("POST /v1/agent/retention/validate", h.handleRetentionValidate)
	agentMux.HandleFunc("PUT  /v1/agent/retention/jobs/{id}/complete", h.handleCompleteRetentionJob)
	agentMux.HandleFunc("PUT  /v1/agent/retention/jobs/{id}/fail", h.handleFailRetentionJob)
	agentMux.HandleFunc("PUT /v1/agent/jobs/{id}/start", h.startAgentJob)
	agentMux.HandleFunc("PUT /v1/agent/jobs/{id}/complete", h.completeAgentJob)
	agentMux.HandleFunc("PUT /v1/agent/jobs/{id}/fail", h.failAgentJob)
	agentMux.HandleFunc("PUT /v1/agent/jobs/{id}/progress", h.progressAgentJob)
	agentMux.HandleFunc("GET /v1/agent/jobs/{id}/cancel-requested", h.cancelStatusAgentJob)
	agentMux.HandleFunc("PUT /v1/agent/jobs/{id}/cancelled", h.cancelledAgentJob)
	// Snapshot read for restore tests (agent needs engine_snapshot_id)
	agentMux.HandleFunc("GET /v1/agent/snapshots/{id}", h.getSnapshot)
	// Restore tests
	agentMux.HandleFunc("GET /v1/agent/restore-tests", h.listAgentRestoreTests)
	agentMux.HandleFunc("PUT /v1/agent/restore-tests/{id}/complete", h.completeAgentRestoreTest)
	agentMux.HandleFunc("PUT /v1/agent/restore-tests/{id}/fail", h.failAgentRestoreTest)
	mux.Handle("/v1/agent/", AgentAuth(h.agentTokens)(agentMux))

	mux.HandleFunc("GET /v1/systems",         h.listSystems)
	mux.HandleFunc("POST /v1/systems",        requireRoleFn(auth.RoleOperator, h.createSystem))
	mux.HandleFunc("GET /v1/systems/{id}",    h.getSystem)
	mux.HandleFunc("PUT /v1/systems/{id}",    requireRoleFn(auth.RoleOperator, h.updateSystem))
	mux.HandleFunc("DELETE /v1/systems/{id}", requireRoleFn(auth.RoleAdmin, h.deleteSystem))
	mux.HandleFunc("PUT /v1/systems/{id}/operation-state", requireRoleFn(auth.RoleOperator, h.setOperationState))

	mux.HandleFunc("GET /v1/repositories",             h.listRepositories)
	mux.HandleFunc("POST /v1/repositories",            requireRoleFn(auth.RoleOperator, h.createRepository))
	mux.HandleFunc("GET /v1/repositories/health",      h.handleListRepositoryHealth)
	mux.HandleFunc("GET /v1/repositories/{id}",        h.getRepository)
	mux.HandleFunc("GET /v1/repositories/{id}/health", h.handleRepositoryHealth)
	mux.HandleFunc("PUT /v1/repositories/{id}",        requireRoleFn(auth.RoleAdmin, h.updateRepository))    // admin: immutable_mode
	mux.HandleFunc("DELETE /v1/repositories/{id}",     requireRoleFn(auth.RoleAdmin, h.deleteRepository))

	mux.HandleFunc("GET /v1/policies",         h.listPolicies)
	mux.HandleFunc("POST /v1/policies",        requireRoleFn(auth.RoleOperator, h.createPolicy))
	mux.HandleFunc("GET /v1/policies/{id}",    h.getPolicy)
	mux.HandleFunc("PUT /v1/policies/{id}",    requireRoleFn(auth.RoleOperator, h.updatePolicy))
	mux.HandleFunc("DELETE /v1/policies/{id}", requireRoleFn(auth.RoleAdmin, h.deletePolicy))

	mux.HandleFunc("GET /v1/jobs",              h.listJobs)
	mux.HandleFunc("POST /v1/jobs",             requireRoleFn(auth.RoleOperator, h.createJob))
	mux.HandleFunc("POST /v1/jobs/{id}/cancel", requireRoleFn(auth.RoleOperator, h.cancelJob))
	mux.HandleFunc("GET /v1/jobs/{id}",         h.getJob)
	mux.HandleFunc("PUT /v1/jobs/{id}",         requireRoleFn(auth.RoleOperator, h.updateJob))
	mux.HandleFunc("DELETE /v1/jobs/{id}",      requireRoleFn(auth.RoleAdmin, h.deleteJob))

	mux.HandleFunc("GET /v1/snapshots",          h.listSnapshots)
	mux.HandleFunc("POST /v1/snapshots",         requireRoleFn(auth.RoleOperator, h.createSnapshot))
	mux.HandleFunc("GET /v1/snapshots/{id}",     h.getSnapshot)
	mux.HandleFunc("DELETE /v1/snapshots/{id}",  requireRoleFn(auth.RoleAdmin, h.deleteSnapshot))

	mux.HandleFunc("GET /v1/restore-tests", h.listRestoreTests)
	mux.HandleFunc("POST /v1/restore-tests", h.createRestoreTest)
	mux.HandleFunc("GET /v1/restore-tests/{id}", h.getRestoreTest)
	mux.HandleFunc("PUT /v1/restore-tests/{id}/start", h.startRestoreTest)
	mux.HandleFunc("PUT /v1/restore-tests/{id}/complete", h.completeRestoreTest)
	mux.HandleFunc("PUT /v1/restore-tests/{id}/fail", h.failRestoreTest)
	mux.HandleFunc("DELETE /v1/restore-tests/{id}", h.deleteRestoreTest)

	// ── Health score + alerts + activity ─────────────────────────────────
	mux.HandleFunc("GET /v1/health/score",    h.handleHealthScore)
	mux.HandleFunc("GET /v1/health/alerts",   h.handleHealthAlerts)
	mux.HandleFunc("GET /v1/health/activity", h.handleHealthActivity)

	// ── Audit log — admin only (contains hashed IPs, user-agents, actions) ──
	mux.HandleFunc("GET /v1/audit", requireRoleFn(auth.RoleAdmin, h.handleAuditLog))

	// ── Notification channels ─────────────────────────────────────────────
	mux.HandleFunc("GET /v1/notifications",            h.handleListNotifications)
	mux.HandleFunc("POST /v1/notifications",           requireRoleFn(auth.RoleOperator, h.handleCreateNotification))
	mux.HandleFunc("DELETE /v1/notifications/{id}",    requireRoleFn(auth.RoleOperator, h.handleDeleteNotification))
	mux.HandleFunc("POST /v1/notifications/{id}/test", requireRoleFn(auth.RoleOperator, h.handleTestNotification))

	// ── GDPR — Art. 17 (erasure) + Art. 20 (portability) — admin only ────
	mux.HandleFunc("GET /v1/gdpr/systems/{id}/export",    requireRoleFn(auth.RoleAdmin, h.handleGDPRExport))
	mux.HandleFunc("DELETE /v1/gdpr/systems/{id}/purge",  requireRoleFn(auth.RoleAdmin, h.handleGDPRPurge))

	// Web UI — served from WEB_UI_DIR (default: web/dist).
	// All unknown paths fall through to index.html for React Router.
	webDir := h.webUIDir()
	if webDir != "" {
		mux.Handle("/ui/", http.StripPrefix("/ui", spaHandler(webDir)))
		mux.Handle("/", http.RedirectHandler("/ui/", http.StatusFound))
	}
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// unused import guards
var _ = auth.HashToken
var _ = audit.NoopStore{}
var _ = security.ClientIP
