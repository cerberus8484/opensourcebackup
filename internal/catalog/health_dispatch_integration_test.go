//go:build integration

package catalog_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cerberus8484/opensourcebackup/internal/catalog"
)

// TestHealthAwareDispatch is the acceptance proof for E1.3a: the health gate sits
// between the operation-state gate and the concurrency gate. OFFLINE and UNKNOWN
// systems admit no new jobs; ONLINE and DEGRADED do. Operation-state priority is
// deterministic. E1.1 and E1.2 remain intact.
//
// Invariant: OFFLINE and UNKNOWN systems cannot start new jobs.
func TestHealthAwareDispatch(t *testing.T) {
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
		sys, pol = createFixtureSystemAndPolicy(t, db) // fixture is ONLINE (last_seen=now)
	}
	// setHealth stamps last_seen to a given age; nil age = never seen (UNKNOWN).
	setHealth := func(t *testing.T, age *time.Duration) {
		t.Helper()
		var ls *time.Time
		if age != nil {
			ts := time.Now().UTC().Add(-*age)
			ls = &ts
		}
		if _, err := db.Pool().Exec(ctx, `UPDATE systems SET last_seen=$1 WHERE id=$2`, ls, sys.ID); err != nil {
			t.Fatalf("set last_seen: %v", err)
		}
	}
	dur := func(d time.Duration) *time.Duration { return &d }
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

	// 1. ONLINE → dispatch allowed.
	t.Run("online allows dispatch", func(t *testing.T) {
		reset(t)
		setHealth(t, dur(10*time.Second))
		dec, err := guard.AdmitStart(ctx, mkPending(t).ID)
		if err != nil || !dec.Allowed {
			t.Fatalf("online must admit: dec=%+v err=%v", dec, err)
		}
		if dec.HealthState != string(catalog.HealthOnline) {
			t.Errorf("health = %q, want online", dec.HealthState)
		}
	})

	// 2. DEGRADED → dispatch still allowed (surfaced as warning by the caller).
	t.Run("degraded still allows dispatch", func(t *testing.T) {
		reset(t)
		setHealth(t, dur(5*time.Minute))
		dec, err := guard.AdmitStart(ctx, mkPending(t).ID)
		if err != nil || !dec.Allowed {
			t.Fatalf("degraded must still admit: dec=%+v err=%v", dec, err)
		}
		if dec.HealthState != string(catalog.HealthDegraded) {
			t.Errorf("health = %q, want degraded", dec.HealthState)
		}
	})

	// 3+5. OFFLINE → job stays pending (no start).
	t.Run("offline blocks (stays pending)", func(t *testing.T) {
		reset(t)
		setHealth(t, dur(20*time.Minute))
		j := mkPending(t)
		dec, err := guard.AdmitStart(ctx, j.ID)
		if err != nil {
			t.Fatalf("block must be a decision: %v", err)
		}
		if dec.Allowed || dec.Reason != catalog.DispatchBlockedSystemOffline {
			t.Fatalf("offline must block with system_offline: dec=%+v", dec)
		}
		if s := jstat(t, j.ID); s != "pending" {
			t.Errorf("status = %q, want pending", s)
		}
	})

	// 4+6. UNKNOWN → job stays pending (no start).
	t.Run("unknown blocks (stays pending)", func(t *testing.T) {
		reset(t)
		setHealth(t, nil) // never seen
		j := mkPending(t)
		dec, err := guard.AdmitStart(ctx, j.ID)
		if err != nil {
			t.Fatalf("block must be a decision: %v", err)
		}
		if dec.Allowed || dec.Reason != catalog.DispatchBlockedSystemHealthUnknown {
			t.Fatalf("unknown must block with system_health_unknown: dec=%+v", dec)
		}
		if s := jstat(t, j.ID); s != "pending" {
			t.Errorf("status = %q, want pending", s)
		}
	})

	// 7. OFFLINE → ONLINE re-enables the SAME pending job (no manual repair, no deadlock).
	t.Run("offline→online re-enables pending job", func(t *testing.T) {
		reset(t)
		setHealth(t, dur(20*time.Minute))
		j := mkPending(t)
		if dec, _ := guard.AdmitStart(ctx, j.ID); dec.Allowed {
			t.Fatal("should be blocked while offline")
		}
		setHealth(t, dur(5*time.Second)) // heartbeat came back → online
		dec, err := guard.AdmitStart(ctx, j.ID)
		if err != nil || !dec.Allowed {
			t.Fatalf("after heartbeat the pending job must start: dec=%+v err=%v", dec, err)
		}
		if s := jstat(t, j.ID); s != "running" {
			t.Errorf("status = %q, want running", s)
		}
	})

	// 8. Health change does not change operation state.
	t.Run("health change leaves operation state untouched", func(t *testing.T) {
		reset(t)
		setHealth(t, dur(20*time.Minute)) // now offline
		got, err := systems.GetByID(ctx, sys.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.OperationState != string(catalog.OperationActive) {
			t.Errorf("operation_state = %q, want active (health must not change it)", got.OperationState)
		}
	})

	// 10. Priority: MAINTENANCE + OFFLINE → operation-state reason wins (deterministic).
	t.Run("maintenance+offline reports system_maintenance", func(t *testing.T) {
		reset(t)
		if _, err := systems.SetOperationState(ctx, sys.ID, string(catalog.OperationMaintenance)); err != nil {
			t.Fatalf("set state: %v", err)
		}
		setHealth(t, dur(20*time.Minute)) // also offline
		dec, err := guard.AdmitStart(ctx, mkPending(t).ID)
		if err != nil {
			t.Fatalf("decision: %v", err)
		}
		if dec.Allowed || dec.Reason != catalog.DispatchBlockedSystemMaintenance {
			t.Errorf("reason = %q, want system_maintenance (operation state has priority)", dec.Reason)
		}
	})

	// 11. ACTIVE + OFFLINE → health reason (system_offline).
	t.Run("active+offline reports system_offline", func(t *testing.T) {
		reset(t) // active
		setHealth(t, dur(20*time.Minute))
		dec, _ := guard.AdmitStart(ctx, mkPending(t).ID)
		if dec.Allowed || dec.Reason != catalog.DispatchBlockedSystemOffline {
			t.Errorf("reason = %q, want system_offline", dec.Reason)
		}
	})

	// 12. ACTIVE + ONLINE + free slot → dispatch.
	t.Run("active+online+free slot dispatches", func(t *testing.T) {
		reset(t)
		setHealth(t, dur(10*time.Second))
		if dec, err := guard.AdmitStart(ctx, mkPending(t).ID); err != nil || !dec.Allowed {
			t.Fatalf("must admit: dec=%+v err=%v", dec, err)
		}
	})

	// 13. ACTIVE + ONLINE + occupied slot → agent_concurrency_limit (E1.1 still enforced
	//     downstream of the health gate).
	t.Run("active+online+occupied slot reports agent_concurrency_limit", func(t *testing.T) {
		reset(t)
		setHealth(t, dur(10*time.Second))
		if dec, err := guard.AdmitStart(ctx, mkPending(t).ID); err != nil || !dec.Allowed {
			t.Fatalf("first admit: dec=%+v err=%v", dec, err)
		}
		dec, err := guard.AdmitStart(ctx, mkPending(t).ID)
		if err != nil {
			t.Fatalf("decision: %v", err)
		}
		if dec.Allowed || dec.Reason != catalog.DispatchBlockedAgentConcurrency {
			t.Errorf("reason = %q, want agent_concurrency_limit", dec.Reason)
		}
	})
}
