//go:build integration

package catalog_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/cerberus8484/opensourcebackup/internal/catalog"
)

// TestOperationState_Dispatch is the acceptance proof for E1.2: an admin-set,
// persistent, race-safe operation state per system that gates NEW dispatch,
// separate from the observed health state and without disturbing E1.1.
//
// Invariant: a non-ACTIVE system cannot start new jobs.
func TestOperationState_Dispatch(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	systems := catalog.NewSystemStore(db)
	jobs := catalog.NewJobStore(db)
	guard := catalog.NewDispatchGuard(db, 1)

	var sys *catalog.System
	var pol *catalog.BackupPolicy

	reset := func(t *testing.T) {
		t.Helper()
		truncateTables(t, db)
		sys, pol = createFixtureSystemAndPolicy(t, db)
	}
	setState := func(t *testing.T, state catalog.OperationState) {
		t.Helper()
		if _, err := systems.SetOperationState(ctx, sys.ID, string(state)); err != nil {
			t.Fatalf("SetOperationState(%s): %v", state, err)
		}
	}
	mkPending := func(t *testing.T) *catalog.BackupJob {
		t.Helper()
		j := &catalog.BackupJob{SystemID: sys.ID, PolicyID: pol.ID, Status: "pending"}
		if err := jobs.Create(ctx, j); err != nil {
			t.Fatalf("create job: %v", err)
		}
		return j
	}
	jstat := func(t *testing.T, id uuid.UUID) string {
		t.Helper()
		got, err := jobs.GetByID(ctx, id)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		return got.Status
	}

	// 1. Fixture systems default to ACTIVE (migration/DB default).
	t.Run("new system defaults to active", func(t *testing.T) {
		reset(t)
		got, err := systems.GetByID(ctx, sys.ID)
		if err != nil {
			t.Fatalf("get system: %v", err)
		}
		if got.OperationState != string(catalog.OperationActive) {
			t.Errorf("operation_state = %q, want active", got.OperationState)
		}
	})

	// 2. ACTIVE → dispatch allowed.
	t.Run("active allows dispatch", func(t *testing.T) {
		reset(t)
		j := mkPending(t)
		dec, err := guard.AdmitStart(ctx, j.ID)
		if err != nil || !dec.Allowed {
			t.Fatalf("active must admit: dec=%+v err=%v", dec, err)
		}
		if s := jstat(t, j.ID); s != "running" {
			t.Errorf("status = %q, want running", s)
		}
	})

	// 3–5. Non-ACTIVE states block a new job (stays pending, correct reason, no error).
	blockCases := []struct {
		state  catalog.OperationState
		reason string
	}{
		{catalog.OperationMaintenance, catalog.DispatchBlockedSystemMaintenance},
		{catalog.OperationDraining, catalog.DispatchBlockedSystemDraining},
		{catalog.OperationSuspended, catalog.DispatchBlockedSystemSuspended},
	}
	for _, bc := range blockCases {
		t.Run(string(bc.state)+" blocks new job (stays pending)", func(t *testing.T) {
			reset(t)
			setState(t, bc.state)
			j := mkPending(t)
			dec, err := guard.AdmitStart(ctx, j.ID)
			if err != nil {
				t.Fatalf("block must be a decision, not error: %v", err)
			}
			if dec.Allowed {
				t.Fatalf("%s must block dispatch", bc.state)
			}
			if dec.Reason != bc.reason {
				t.Errorf("reason = %q, want %q", dec.Reason, bc.reason)
			}
			if s := jstat(t, j.ID); s != "pending" {
				t.Errorf("blocked job status = %q, want pending (never failed)", s)
			}
		})
	}

	// 6–8. A running job is NOT disturbed when the system leaves ACTIVE.
	for _, target := range []catalog.OperationState{catalog.OperationMaintenance, catalog.OperationDraining, catalog.OperationSuspended} {
		t.Run("running job survives active→"+string(target), func(t *testing.T) {
			reset(t)
			j := mkPending(t)
			if dec, err := guard.AdmitStart(ctx, j.ID); err != nil || !dec.Allowed {
				t.Fatalf("admit: dec=%+v err=%v", dec, err)
			}
			setState(t, target) // switch while the job runs
			if s := jstat(t, j.ID); s != "running" {
				t.Errorf("running job status = %q after →%s, want running (no auto-cancel/drain)", s, target)
			}
		})
	}

	// 9. MAINTENANCE → ACTIVE re-enables a pending job.
	t.Run("maintenance→active re-enables pending job", func(t *testing.T) {
		reset(t)
		setState(t, catalog.OperationMaintenance)
		j := mkPending(t)
		if dec, _ := guard.AdmitStart(ctx, j.ID); dec.Allowed {
			t.Fatal("should be blocked under maintenance")
		}
		setState(t, catalog.OperationActive)
		dec, err := guard.AdmitStart(ctx, j.ID)
		if err != nil || !dec.Allowed {
			t.Fatalf("after →active the pending job must start: dec=%+v err=%v", dec, err)
		}
		if s := jstat(t, j.ID); s != "running" {
			t.Errorf("status = %q, want running", s)
		}
	})

	// 10. DRAINING → ACTIVE re-enables dispatch.
	t.Run("draining→active re-enables dispatch", func(t *testing.T) {
		reset(t)
		setState(t, catalog.OperationDraining)
		setState(t, catalog.OperationActive)
		j := mkPending(t)
		if dec, err := guard.AdmitStart(ctx, j.ID); err != nil || !dec.Allowed {
			t.Fatalf("dispatch must work again after →active: dec=%+v err=%v", dec, err)
		}
	})

	// 11. State persists (re-read through a fresh store instance).
	t.Run("operation state persists", func(t *testing.T) {
		reset(t)
		old, err := systems.SetOperationState(ctx, sys.ID, string(catalog.OperationSuspended))
		if err != nil {
			t.Fatalf("set: %v", err)
		}
		if old != string(catalog.OperationActive) {
			t.Errorf("old state = %q, want active", old)
		}
		fresh := catalog.NewSystemStore(db) // independent instance — proves it is in the DB
		got, err := fresh.GetByID(ctx, sys.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.OperationState != string(catalog.OperationSuspended) {
			t.Errorf("persisted state = %q, want suspended", got.OperationState)
		}
		if got.OperationStateChangedAt == nil {
			t.Error("operation_state_changed_at must be set")
		}
	})

	// 12. Operation state and health are independent: changing operation state must
	//     not touch last_seen (the health signal).
	t.Run("operation state change does not touch health signal", func(t *testing.T) {
		reset(t)
		before, err := systems.GetByID(ctx, sys.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if _, err := systems.SetOperationState(ctx, sys.ID, string(catalog.OperationMaintenance)); err != nil {
			t.Fatalf("set: %v", err)
		}
		after, err := systems.GetByID(ctx, sys.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		// last_seen (nil for a fresh fixture) is unchanged by an operation-state change.
		if (before.LastSeen == nil) != (after.LastSeen == nil) {
			t.Error("operation-state change altered last_seen — dimensions must be independent")
		}
	})

	// 13. E1.1 regression: with ACTIVE state, concurrent dispatch still admits exactly 1.
	t.Run("E1.1 concurrency invariant still holds under active", func(t *testing.T) {
		reset(t)
		const n = 10
		ids := make([]uuid.UUID, n)
		for i := range ids {
			ids[i] = mkPending(t).ID
		}
		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			allowed int
		)
		startCh := make(chan struct{})
		for _, id := range ids {
			wg.Add(1)
			go func(id uuid.UUID) {
				defer wg.Done()
				<-startCh
				dec, err := guard.AdmitStart(ctx, id)
				if err == nil && dec.Allowed {
					mu.Lock()
					allowed++
					mu.Unlock()
				}
			}(id)
		}
		close(startCh)
		wg.Wait()
		if allowed != 1 {
			t.Errorf("allowed = %d, want exactly 1 (E1.1 invariant regressed)", allowed)
		}
	})
}
