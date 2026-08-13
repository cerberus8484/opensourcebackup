package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/cerberus8484/opensourcebackup/internal/catalog"
)

// listAgentJobs handles GET /v1/agent/jobs — returns pending jobs for the authenticated system only.
func (h *Handler) listAgentJobs(w http.ResponseWriter, r *http.Request) {
	systemID, ok := SystemIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authorization required")
		return
	}
	jobs, err := h.jobs.ListPendingBySystemID(r.Context(), systemID)
	if err != nil {
		h.log.Error("list agent jobs", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if jobs == nil {
		jobs = []catalog.BackupJob{}
	}
	writeJSON(w, http.StatusOK, jobs)
}

// startAgentJob handles PUT /v1/agent/jobs/{id}/start.
//
// When a dispatch guard is wired (E1.1) the pending→running transition is gated
// by it: a BLOCKED decision (e.g. the agent is at its concurrency limit) is NOT an
// error — the job is left pending and the agent retries on its next poll. Blocked
// starts return HTTP 429 with a structured reason so the agent can distinguish
// "not now" from a real failure; genuine errors keep their normal status codes.
func (h *Handler) startAgentJob(w http.ResponseWriter, r *http.Request) {
	job, ok := h.claimJob(w, r)
	if !ok {
		return
	}

	if h.dispatch == nil {
		// No guard configured — unguarded pending→running transition.
		if err := h.jobs.StartJob(r.Context(), job.ID); err != nil {
			writeError(w, httpStatusForError(err), safeErrorMessage(err))
			return
		}
		writeFreshJob(w, h, r, job.ID)
		return
	}

	decision, err := h.dispatch.AdmitStart(r.Context(), job.ID)
	if err != nil {
		writeError(w, httpStatusForError(err), safeErrorMessage(err))
		return
	}
	if !decision.Allowed {
		h.log.Info("job dispatch blocked",
			"job_id", job.ID,
			"agent_id", job.SystemID,
			"repository_id", decision.RepositoryID,
			"global_running", decision.GlobalRunning,
			"global_limit", decision.GlobalLimit,
			"repository_running", decision.RepoRunning,
			"repository_limit", decision.RepoLimit,
			"agent_running", decision.RunningCount,
			"agent_limit", decision.Limit,
			"health_state", decision.HealthState,
			"last_seen_age_sec", decision.LastSeenAgeSec,
			"reason", decision.Reason,
		)
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error":              "dispatch blocked",
			"reason":             decision.Reason,
			"status":             "pending",
			"global_running":     decision.GlobalRunning,
			"global_limit":       decision.GlobalLimit,
			"repository_running": decision.RepoRunning,
			"repository_limit":   decision.RepoLimit,
			"agent_running":      decision.RunningCount,
			"agent_limit":        decision.Limit,
			"health_state":       decision.HealthState,
		})
		return
	}
	// Allowed but the agent's health is degraded — surface a warning (E1.3a). DEGRADED
	// still dispatches; the operator sees the signal without the job being blocked.
	if decision.HealthState == string(catalog.HealthDegraded) {
		h.log.Warn("job dispatched to degraded agent",
			"job_id", job.ID,
			"agent_id", job.SystemID,
			"health_state", decision.HealthState,
			"last_seen_age_sec", decision.LastSeenAgeSec,
		)
	}
	writeFreshJob(w, h, r, job.ID)
}

// completeAgentJob handles PUT /v1/agent/jobs/{id}/complete.
func (h *Handler) completeAgentJob(w http.ResponseWriter, r *http.Request) {
	job, ok := h.claimJob(w, r)
	if !ok {
		return
	}
	var body struct {
		EngineSnapshotID string   `json:"engine_snapshot_id"`
		BytesUploaded    int64    `json:"bytes_uploaded"`
		Paths            []string `json:"paths"`
	}
	if err := decode(r, &body); err != nil {
		handleDecodeError(w, err)
		return
	}

	// Guarded running → success. If the job is no longer running (e.g. the reaper
	// already failed it, or a duplicate report arrived), this returns 409 rather
	// than resurrecting a terminal job.
	if err := h.jobs.CompleteJob(r.Context(), job.ID, body.BytesUploaded); err != nil {
		writeError(w, httpStatusForError(err), safeErrorMessage(err))
		return
	}
	// Pin a successful job to 100% so it never lingers at e.g. 98.7% (best-effort).
	if err := h.jobs.FinalizeProgress(r.Context(), job.ID); err != nil {
		h.log.Error("finalize progress", "error", err)
	}

	// Register snapshot only if policy has a repository
	policy, err := h.policies.GetByID(r.Context(), job.PolicyID)
	if err == nil && policy.RepositoryID != nil && body.EngineSnapshotID != "" {
		snap := &catalog.Snapshot{
			JobID:            job.ID,
			RepositoryID:     *policy.RepositoryID,
			EngineSnapshotID: body.EngineSnapshotID,
			Paths:            body.Paths,
			ChecksumStatus:   "unverified",
		}
		hostname, _ := SystemIDFromContext(r.Context())
		hostnameStr := hostname.String()
		snap.Hostname = &hostnameStr
		if err := h.snapshots.Create(r.Context(), snap); err != nil {
			h.log.Error("create snapshot", "error", err)
		}
	}

	writeFreshJob(w, h, r, job.ID)
}

// failAgentJob handles PUT /v1/agent/jobs/{id}/fail.
func (h *Handler) failAgentJob(w http.ResponseWriter, r *http.Request) {
	job, ok := h.claimJob(w, r)
	if !ok {
		return
	}
	var body struct {
		ErrorSummary string `json:"error_summary"`
	}
	if err := decode(r, &body); err != nil {
		handleDecodeError(w, err)
		return
	}
	if err := h.jobs.FailJob(r.Context(), job.ID, body.ErrorSummary); err != nil {
		writeError(w, httpStatusForError(err), safeErrorMessage(err))
		return
	}
	writeFreshJob(w, h, r, job.ID)
}

// progressAgentJob handles PUT /v1/agent/jobs/{id}/progress — live progress updates
// while a backup runs (B_JOB_PROGRESS). Aggregate counters only; no file paths.
func (h *Handler) progressAgentJob(w http.ResponseWriter, r *http.Request) {
	job, ok := h.claimJob(w, r)
	if !ok {
		return
	}
	var p catalog.JobProgress
	if err := decode(r, &p); err != nil {
		handleDecodeError(w, err)
		return
	}
	if err := h.jobs.UpdateProgress(r.Context(), job.ID, p); err != nil {
		writeError(w, httpStatusForError(err), safeErrorMessage(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// cancelStatusAgentJob handles GET /v1/agent/jobs/{id}/cancel-requested — the agent
// polls this while a backup runs to learn whether an operator requested a stop.
func (h *Handler) cancelStatusAgentJob(w http.ResponseWriter, r *http.Request) {
	job, ok := h.claimJob(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cancel_requested": job.CancelRequestedAt != nil,
		"reason":           job.CancelReason,
	})
}

// cancelledAgentJob handles PUT /v1/agent/jobs/{id}/cancelled — the agent reports
// that it stopped a running backup in response to a cancel request. The terminal
// status is "cancelled" — a deliberate stop is NOT a failure.
func (h *Handler) cancelledAgentJob(w http.ResponseWriter, r *http.Request) {
	job, ok := h.claimJob(w, r)
	if !ok {
		return
	}
	if err := h.jobs.CancelJob(r.Context(), job.ID, job.CancelReason); err != nil {
		writeError(w, httpStatusForError(err), safeErrorMessage(err))
		return
	}
	writeFreshJob(w, h, r, job.ID)
}

// writeFreshJob re-reads a job after a guarded transition and writes it as the
// response, so the agent always sees the authoritative post-transition state.
func writeFreshJob(w http.ResponseWriter, h *Handler, r *http.Request, id uuid.UUID) {
	fresh, err := h.jobs.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, httpStatusForError(err), safeErrorMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, fresh)
}

// claimJob fetches a job and verifies it belongs to the authenticated system.
func (h *Handler) claimJob(w http.ResponseWriter, r *http.Request) (*catalog.BackupJob, bool) {
	systemID, ok := SystemIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authorization required")
		return nil, false
	}
	jobID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job id")
		return nil, false
	}
	job, err := h.jobs.GetByID(r.Context(), jobID)
	if err != nil {
		writeError(w, httpStatusForError(err), safeErrorMessage(err))
		return nil, false
	}
	if job.SystemID != systemID {
		// Return 404, not 403 — don't reveal the job exists for other systems
		writeError(w, http.StatusNotFound, "catalog: not found")
		return nil, false
	}
	return job, true
}
