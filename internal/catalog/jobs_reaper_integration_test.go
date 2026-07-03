//go:build integration

package catalog_test

import (
	"context"
	"testing"
	"time"

	"github.com/cerberus8484/opensourcebackup/internal/catalog"
)

// TestJobStore_FailStaleJobs verifies the Stale-Job-Reaper logic: running jobs
// that have gone silent are auto-failed, while live and terminal jobs are left
// untouched. Timestamps are set via raw SQL for deterministic control.
func TestJobStore_FailStaleJobs(t *testing.T) {
	db := newTestDB(t)
	truncateTables(t, db)
	sys, pol := createFixtureSystemAndPolicy(t, db)
	store := catalog.NewJobStore(db)
	ctx := context.Background()

	// Helper: create a running job, then force its timestamps via raw SQL.
	mkJob := func(status string, startedAgo time.Duration, lastProgressAgo *time.Duration) *catalog.BackupJob {
		j := &catalog.BackupJob{SystemID: sys.ID, PolicyID: pol.ID, Status: status}
		if err := store.Create(ctx, j); err != nil {
			t.Fatalf("create job: %v", err)
		}
		started := time.Now().Add(-startedAgo)
		if lastProgressAgo == nil {
			_, err := db.Pool().Exec(ctx,
				`UPDATE backup_jobs SET started_at=$1, last_progress_at=NULL WHERE id=$2`,
				started, j.ID)
			if err != nil {
				t.Fatalf("set timestamps: %v", err)
			}
		} else {
			lp := time.Now().Add(-*lastProgressAgo)
			_, err := db.Pool().Exec(ctx,
				`UPDATE backup_jobs SET started_at=$1, last_progress_at=$2 WHERE id=$3`,
				started, lp, j.ID)
			if err != nil {
				t.Fatalf("set timestamps: %v", err)
			}
		}
		return j
	}

	dur := func(d time.Duration) *time.Duration { return &d }

	// A: running, started 13h ago, never reported progress → STALE (start grace 12h)
	jobA := mkJob("running", 13*time.Hour, nil)
	// B: running, started 1h ago, no progress yet → live (within start grace)
	jobB := mkJob("running", 1*time.Hour, nil)
	// C: running 13h, but reported progress 1m ago → live (recent heartbeat)
	jobC := mkJob("running", 13*time.Hour, dur(1*time.Minute))
	// D: running, reported progress 40m ago → STALE (progress grace 30m)
	jobD := mkJob("running", 2*time.Hour, dur(40*time.Minute))
	// E: success, started 13h ago → never reaped (terminal status)
	jobE := mkJob("success", 13*time.Hour, nil)
	// F: pending, created 7h ago → STALE (pending grace 6h) — offline agent
	jobF := mkPending(t, db, store, sys, pol, 7*time.Hour)
	// G: pending, created 1h ago → live (agent may still pick it up)
	jobG := mkPending(t, db, store, sys, pol, 1*time.Hour)

	n, err := store.FailStaleJobs(ctx, 30*time.Minute, 12*time.Hour, 6*time.Hour)
	if err != nil {
		t.Fatalf("FailStaleJobs: %v", err)
	}
	if n != 3 {
		t.Errorf("want 3 reaped, got %d", n)
	}

	wantStatus := func(j *catalog.BackupJob, want string) {
		got, err := store.GetByID(ctx, j.ID)
		if err != nil {
			t.Fatalf("get %s: %v", j.ID, err)
		}
		if got.Status != want {
			t.Errorf("job status: want %q, got %q", want, got.Status)
		}
		if want == "failed" {
			if got.FinishedAt == nil {
				t.Error("reaped job must have finished_at set")
			}
			if got.ErrorSummary == nil || *got.ErrorSummary == "" {
				t.Error("reaped job must have an error_summary")
			}
		}
	}

	wantStatus(jobA, "failed")  // stale by start grace
	wantStatus(jobB, "running") // still live
	wantStatus(jobC, "running") // recent heartbeat
	wantStatus(jobD, "failed")  // stale by progress grace
	wantStatus(jobE, "success") // terminal, untouched
	wantStatus(jobF, "failed")  // stale pending — offline agent never claimed it
	wantStatus(jobG, "pending") // recent pending — still claimable
}

// mkPending creates a pending job and back-dates its created_at via raw SQL so the
// pending-grace branch of the reaper can be tested deterministically.
func mkPending(t *testing.T, db *catalog.DB, store catalog.JobStore, sys *catalog.System, pol *catalog.BackupPolicy, createdAgo time.Duration) *catalog.BackupJob {
	t.Helper()
	ctx := context.Background()
	j := &catalog.BackupJob{SystemID: sys.ID, PolicyID: pol.ID, Status: "pending"}
	if err := store.Create(ctx, j); err != nil {
		t.Fatalf("create pending job: %v", err)
	}
	created := time.Now().Add(-createdAgo)
	if _, err := db.Pool().Exec(ctx,
		`UPDATE backup_jobs SET created_at=$1 WHERE id=$2`, created, j.ID); err != nil {
		t.Fatalf("set created_at: %v", err)
	}
	return j
}
