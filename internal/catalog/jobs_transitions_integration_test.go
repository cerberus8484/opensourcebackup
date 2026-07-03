//go:build integration

package catalog_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/cerberus8484/opensourcebackup/internal/catalog"
)

// TestJobStore_GuardedTransitions is the acceptance proof for B_STABILIZATION_CORE
// slice 1: job status changes go through atomic, guarded transitions. Legal edges
// succeed and set their side-effect fields; every illegal edge (especially a late
// report against an already-terminal job) is rejected with ErrIllegalTransition so
// a terminal job can never be resurrected.
func TestJobStore_GuardedTransitions(t *testing.T) {
	db := newTestDB(t)
	truncateTables(t, db)
	sys, pol := createFixtureSystemAndPolicy(t, db)
	store := catalog.NewJobStore(db)
	ctx := context.Background()

	// mkStatus creates a job and forces it into the given starting status.
	mkStatus := func(status string) *catalog.BackupJob {
		j := &catalog.BackupJob{SystemID: sys.ID, PolicyID: pol.ID, Status: "pending"}
		if err := store.Create(ctx, j); err != nil {
			t.Fatalf("create job: %v", err)
		}
		if status != "pending" {
			if _, err := db.Pool().Exec(ctx,
				`UPDATE backup_jobs SET status=$1 WHERE id=$2`, status, j.ID); err != nil {
				t.Fatalf("force status: %v", err)
			}
		}
		return j
	}

	statusOf := func(id uuid.UUID) string {
		got, err := store.GetByID(ctx, id)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		return got.Status
	}

	// ── Legal edges ──────────────────────────────────────────────────────────
	t.Run("pending→running start", func(t *testing.T) {
		j := mkStatus("pending")
		if err := store.StartJob(ctx, j.ID); err != nil {
			t.Fatalf("StartJob: %v", err)
		}
		if s := statusOf(j.ID); s != "running" {
			t.Errorf("status = %q, want running", s)
		}
	})

	t.Run("running→success complete sets bytes", func(t *testing.T) {
		j := mkStatus("running")
		if err := store.CompleteJob(ctx, j.ID, 4096); err != nil {
			t.Fatalf("CompleteJob: %v", err)
		}
		got, _ := store.GetByID(ctx, j.ID)
		if got.Status != "success" {
			t.Errorf("status = %q, want success", got.Status)
		}
		if got.FinishedAt == nil {
			t.Error("finished_at must be set")
		}
		if got.BytesUploaded == nil || *got.BytesUploaded != 4096 {
			t.Errorf("bytes_uploaded = %v, want 4096", got.BytesUploaded)
		}
	})

	t.Run("running→failed", func(t *testing.T) {
		j := mkStatus("running")
		if err := store.FailJob(ctx, j.ID, "restic exit 3"); err != nil {
			t.Fatalf("FailJob: %v", err)
		}
		got, _ := store.GetByID(ctx, j.ID)
		if got.Status != "failed" {
			t.Errorf("status = %q, want failed", got.Status)
		}
		if got.ErrorSummary == nil || *got.ErrorSummary != "restic exit 3" {
			t.Errorf("error_summary = %v, want 'restic exit 3'", got.ErrorSummary)
		}
	})

	t.Run("pending→failed (agent could not start)", func(t *testing.T) {
		j := mkStatus("pending")
		if err := store.FailJob(ctx, j.ID, "get policy failed"); err != nil {
			t.Fatalf("FailJob from pending: %v", err)
		}
		if s := statusOf(j.ID); s != "failed" {
			t.Errorf("status = %q, want failed", s)
		}
	})

	t.Run("running→cancelled sets reason", func(t *testing.T) {
		j := mkStatus("running")
		if err := store.CancelJob(ctx, j.ID, "Windows-Update"); err != nil {
			t.Fatalf("CancelJob: %v", err)
		}
		got, _ := store.GetByID(ctx, j.ID)
		if got.Status != "cancelled" {
			t.Errorf("status = %q, want cancelled", got.Status)
		}
		if got.ErrorSummary == nil || *got.ErrorSummary != "Windows-Update" {
			t.Errorf("error_summary = %v, want 'Windows-Update'", got.ErrorSummary)
		}
	})

	// ── Illegal edges — the whole point of the guard ─────────────────────────
	t.Run("cannot start a running job", func(t *testing.T) {
		j := mkStatus("running")
		if err := store.StartJob(ctx, j.ID); !errors.Is(err, catalog.ErrIllegalTransition) {
			t.Errorf("want ErrIllegalTransition, got %v", err)
		}
	})

	t.Run("cannot resurrect a failed job via complete", func(t *testing.T) {
		j := mkStatus("failed")
		if err := store.CompleteJob(ctx, j.ID, 100); !errors.Is(err, catalog.ErrIllegalTransition) {
			t.Errorf("want ErrIllegalTransition, got %v", err)
		}
		if s := statusOf(j.ID); s != "failed" {
			t.Errorf("status changed to %q — terminal job must stay failed", s)
		}
	})

	t.Run("cannot fail a succeeded job", func(t *testing.T) {
		j := mkStatus("success")
		if err := store.FailJob(ctx, j.ID, "late error"); !errors.Is(err, catalog.ErrIllegalTransition) {
			t.Errorf("want ErrIllegalTransition, got %v", err)
		}
	})

	t.Run("cannot cancel a cancelled job", func(t *testing.T) {
		j := mkStatus("cancelled")
		if err := store.CancelJob(ctx, j.ID, "again"); !errors.Is(err, catalog.ErrIllegalTransition) {
			t.Errorf("want ErrIllegalTransition, got %v", err)
		}
	})

	// ── Not found vs illegal transition ──────────────────────────────────────
	t.Run("unknown job is not found", func(t *testing.T) {
		if err := store.StartJob(ctx, uuid.New()); !errors.Is(err, catalog.ErrNotFound) {
			t.Errorf("want ErrNotFound, got %v", err)
		}
	})
}
