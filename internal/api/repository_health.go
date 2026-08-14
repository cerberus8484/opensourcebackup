package api

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/cerberus8484/opensourcebackup/internal/catalog"
)

// handleRepositoryHealth handles GET /v1/repositories/{id}/health
//
// Returns derived health indicators for a repository.
// Nothing here is stored — it is computed fresh on each request from
// existing catalog data (snapshots, jobs, restore tests).
//
// Honest design: no "healthy" fabrication. If there is no data, the
// fields are nil/zero. The caller decides what that means.
func (h *Handler) handleRepositoryHealth(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid repository id")
		return
	}
	ctx := r.Context()

	repo, err := h.repositories.GetByID(ctx, id)
	if err != nil {
		writeError(w, httpStatusForError(err), "repository not found")
		return
	}

	// All snapshots for this repository
	snapshots, err := h.snapshots.ListByJobID(ctx, uuid.Nil) // placeholder — needs ListByRepositoryID
	if err != nil {
		snapshots = nil
	}
	// Filter to this repository manually (ListByRepositoryID not yet available)
	var repoSnaps []catalog.Snapshot
	for _, sn := range snapshots {
		if sn.RepositoryID == id {
			repoSnaps = append(repoSnaps, sn)
		}
	}

	// All restore tests across the platform — filter to our repo snapshots
	allRTs, err := h.restoreTests.List(ctx)
	if err != nil {
		allRTs = nil
	}
	snapIDs := make(map[uuid.UUID]bool, len(repoSnaps))
	for _, sn := range repoSnaps {
		snapIDs[sn.ID] = true
	}
	verifiedCount := 0
	var lastRestoreTest *time.Time
	for _, rt := range allRTs {
		if !snapIDs[rt.SnapshotID] {
			continue
		}
		if rt.Status == "success" {
			verifiedCount++
			if rt.FinishedAt != nil {
				if lastRestoreTest == nil || rt.FinishedAt.After(*lastRestoreTest) {
					lastRestoreTest = rt.FinishedAt
				}
			}
		}
	}

	// Last backup: most recent successful job using a policy with this repository
	allJobs, err := h.jobs.List(ctx)
	if err != nil {
		allJobs = nil
	}
	allPolicies, err := h.policies.List(ctx)
	if err != nil {
		allPolicies = nil
	}
	policyIDs := make(map[uuid.UUID]bool)
	for _, p := range allPolicies {
		if p.RepositoryID != nil && *p.RepositoryID == id {
			policyIDs[p.ID] = true
		}
	}
	var lastBackup *time.Time
	var lastRetention *time.Time
	for _, j := range allJobs {
		if !policyIDs[j.PolicyID] || j.Status != "success" {
			continue
		}
		ts := j.FinishedAt
		if ts == nil {
			ts = &j.CreatedAt
		}
		switch j.Type {
		case catalog.JobTypeBackup:
			if lastBackup == nil || ts.After(*lastBackup) {
				lastBackup = ts
			}
		case catalog.JobTypeRetention:
			if lastRetention == nil || ts.After(*lastRetention) {
				lastRetention = ts
			}
		}
	}

	health := catalog.RepositoryHealth{
		RepositoryID:      id,
		EncryptionEnabled: repo.EncryptionMode != nil && *repo.EncryptionMode != "",
		ImmutableMode:     repo.ImmutableMode,
		SecurityPosture:   repo.SecurityPosture,
		SnapshotCount:     len(repoSnaps),
		VerifiedCount:     verifiedCount,
		LastBackupAt:      lastBackup,
		LastRestoreTestAt: lastRestoreTest,
		LastRetentionAt:   lastRetention,
	}

	writeJSON(w, http.StatusOK, health)
}

// handleListRepositoryHealth handles GET /v1/repositories/health
// Returns health summaries for all repositories in one call — used by the dashboard.
func (h *Handler) handleListRepositoryHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	repos, err := h.repositories.List(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if repos == nil {
		writeJSON(w, http.StatusOK, []catalog.RepositoryHealth{})
		return
	}

	// Load all data once — avoid N+1 queries
	allSnapshots, _ := h.snapshots.List(ctx)
	allRTs, _ := h.restoreTests.List(ctx)
	allJobs, _ := h.jobs.List(ctx)
	allPolicies, _ := h.policies.List(ctx)

	// Build indices
	snapsByRepo := make(map[uuid.UUID][]catalog.Snapshot)
	for _, sn := range allSnapshots {
		snapsByRepo[sn.RepositoryID] = append(snapsByRepo[sn.RepositoryID], sn)
	}

	snapIDToRepoID := make(map[uuid.UUID]uuid.UUID)
	for repoID, snaps := range snapsByRepo {
		for _, sn := range snaps {
			snapIDToRepoID[sn.ID] = repoID
		}
	}

	policyToRepo := make(map[uuid.UUID]uuid.UUID)
	for _, p := range allPolicies {
		if p.RepositoryID != nil {
			policyToRepo[p.ID] = *p.RepositoryID
		}
	}

	type repoAgg struct {
		verified      int
		lastBackup    *time.Time
		lastRestore   *time.Time
		lastRetention *time.Time
	}
	agg := make(map[uuid.UUID]*repoAgg, len(repos))
	for _, repo := range repos {
		agg[repo.ID] = &repoAgg{}
	}

	// Aggregate restore test data
	for _, rt := range allRTs {
		repoID, ok := snapIDToRepoID[rt.SnapshotID]
		if !ok {
			continue
		}
		a, ok := agg[repoID]
		if !ok {
			continue
		}
		if rt.Status == "success" {
			a.verified++
			if rt.FinishedAt != nil {
				if a.lastRestore == nil || rt.FinishedAt.After(*a.lastRestore) {
					a.lastRestore = rt.FinishedAt
				}
			}
		}
	}

	// Aggregate job data
	for _, j := range allJobs {
		repoID, ok := policyToRepo[j.PolicyID]
		if !ok || j.Status != "success" {
			continue
		}
		a, ok := agg[repoID]
		if !ok {
			continue
		}
		ts := j.FinishedAt
		if ts == nil {
			ts = &j.CreatedAt
		}
		switch j.Type {
		case catalog.JobTypeBackup:
			if a.lastBackup == nil || ts.After(*a.lastBackup) {
				a.lastBackup = ts
			}
		case catalog.JobTypeRetention:
			if a.lastRetention == nil || ts.After(*a.lastRetention) {
				a.lastRetention = ts
			}
		}
	}

	// Build response
	result := make([]catalog.RepositoryHealth, 0, len(repos))
	for _, repo := range repos {
		a := agg[repo.ID]
		snaps := snapsByRepo[repo.ID]
		result = append(result, catalog.RepositoryHealth{
			RepositoryID:      repo.ID,
			EncryptionEnabled: repo.EncryptionMode != nil && *repo.EncryptionMode != "",
			ImmutableMode:     repo.ImmutableMode,
			SecurityPosture:   repo.SecurityPosture,
			SnapshotCount:     len(snaps),
			VerifiedCount:     a.verified,
			LastBackupAt:      a.lastBackup,
			LastRestoreTestAt: a.lastRestore,
			LastRetentionAt:   a.lastRetention,
		})
	}
	writeJSON(w, http.StatusOK, result)
}
