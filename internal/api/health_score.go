package api

import (
	"context"
	"net/http"
	"time"

	"github.com/cerberus8484/opensourcebackup/internal/catalog"
	"github.com/cerberus8484/opensourcebackup/internal/health"
)

// handleHealthScore handles GET /v1/health/score.
func (h *Handler) handleHealthScore(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, computeScore(r.Context(), h))
}

// computeScore loads catalog data and calls health.Calculate.
// Shared by handleHealthScore and handleHealthAlerts — single canonical implementation.
func computeScore(ctx context.Context, h *Handler) health.ScoreResult {
	now := time.Now()

	systems, _ := h.systems.List(ctx)
	jobs, _ := h.jobs.List(ctx)
	snapshots, _ := h.snapshots.List(ctx)
	rts, _ := h.restoreTests.List(ctx)
	repos, _ := h.repositories.List(ctx)
	policies, _ := h.policies.List(ctx)

	// ── Agent status ──────────────────────────────────────────────────────────
	online, idle, offline := 0, 0, 0
	for _, sys := range systems {
		switch agentStatusFromLastSeen(sys.LastSeen, now) {
		case "online":
			online++
		case "idle":
			idle++
		default:
			offline++
		}
	}

	// ── Jobs ──────────────────────────────────────────────────────────────────
	var successJobs, failedJobs, failedLast24h int
	var lastSuccessAt *time.Time
	cutoff24h := now.Add(-24 * time.Hour)
	for _, j := range jobs {
		if j.Type != catalog.JobTypeBackup {
			continue
		}
		switch j.Status {
		case "success":
			successJobs++
			if j.FinishedAt != nil && (lastSuccessAt == nil || j.FinishedAt.After(*lastSuccessAt)) {
				lastSuccessAt = j.FinishedAt
			}
		case "failed":
			failedJobs++
			if j.CreatedAt.After(cutoff24h) {
				failedLast24h++
			}
		}
	}

	// ── Restore coverage ──────────────────────────────────────────────────────
	var lastRestoreTestAt *time.Time
	for _, rt := range rts {
		if rt.Status == "success" && rt.FinishedAt != nil {
			if lastRestoreTestAt == nil || rt.FinishedAt.After(*lastRestoreTestAt) {
				lastRestoreTestAt = rt.FinishedAt
			}
		}
	}
	verifiedSnaps := countVerifiedSnapshots(snapshots, rts)

	// ── Repository security ───────────────────────────────────────────────────
	unprotected, unencrypted := 0, 0
	for _, repo := range repos {
		if repo.SecurityPosture.Immutability != catalog.SecurityProvenanceVerified {
			unprotected++
		}
		if repo.SecurityPosture.Encryption != catalog.SecurityProvenanceVerified {
			unencrypted++
		}
	}

	// ── Retention ─────────────────────────────────────────────────────────────
	policiesWithRetention := 0
	for _, p := range policies {
		if p.RetentionPlan.HasRules() {
			policiesWithRetention++
		}
	}

	return health.Calculate(health.Input{
		TotalSystems:          len(systems),
		OnlineAgents:          online,
		IdleAgents:            idle,
		OfflineAgents:         offline,
		TotalJobs:             successJobs + failedJobs,
		SuccessJobs:           successJobs,
		FailedJobs:            failedJobs,
		FailedLast24h:         failedLast24h,
		LastSuccessAt:         lastSuccessAt,
		TotalSnapshots:        len(snapshots),
		VerifiedSnapshots:     verifiedSnaps,
		LastRestoreTestAt:     lastRestoreTestAt,
		TotalRepos:            len(repos),
		UnprotectedRepos:      unprotected,
		UnencryptedRepos:      unencrypted,
		PoliciesWithRetention: policiesWithRetention,
		Now:                   now,
	})
}

// countVerifiedSnapshots counts snapshots with at least one successful restore test.
func countVerifiedSnapshots(snapshots []catalog.Snapshot, rts []catalog.RestoreTest) int {
	verified := make(map[string]bool)
	for _, rt := range rts {
		if rt.Status == "success" {
			verified[rt.SnapshotID.String()] = true
		}
	}
	count := 0
	for _, sn := range snapshots {
		if verified[sn.ID.String()] {
			count++
		}
	}
	return count
}

// agentStatusFromLastSeen classifies an agent as online/idle/offline.
func agentStatusFromLastSeen(lastSeen *time.Time, now time.Time) string {
	if lastSeen == nil {
		return "offline"
	}
	age := now.Sub(*lastSeen)
	switch {
	case age <= health.OnlineThreshold:
		return "online"
	case age <= health.IdleThreshold:
		return "idle"
	default:
		return "offline"
	}
}
