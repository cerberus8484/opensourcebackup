//go:build integration

package catalog_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cerberus8484/opensourcebackup/internal/catalog"
)

// TestDispatchGuard_AgentConcurrency is the acceptance proof for Phase E1, slice
// E1.1: a per-agent concurrency guard admits jobs pending→running only while the
// agent is below its limit, atomically and race-safely against PostgreSQL.
//
// Invariant: for maxConcurrentJobsPerAgent = 1, no concurrent dispatch may ever
// start more than one active (running) job for the same agent.
func TestDispatchGuard_AgentConcurrency(t *testing.T) {
	db := newTestDB(t)
	truncateTables(t, db)
	sys, pol := createFixtureSystemAndPolicy(t, db)
	store := catalog.NewJobStore(db)
	guard := catalog.NewDispatchGuard(db, 1) // default E1.1 limit
	ctx := context.Background()

	mkPending := func(sysID uuid.UUID) *catalog.BackupJob {
		j := &catalog.BackupJob{SystemID: sysID, PolicyID: pol.ID, Status: "pending"}
		if err := store.Create(ctx, j); err != nil {
			t.Fatalf("create pending job: %v", err)
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
	runningForSystem := func(sysID uuid.UUID) int {
		var n int
		if err := db.Pool().QueryRow(ctx,
			`SELECT count(*) FROM backup_jobs WHERE system_id=$1 AND status='running'`,
			sysID,
		).Scan(&n); err != nil {
			t.Fatalf("count running: %v", err)
		}
		return n
	}

	// 1. No running job → dispatch allowed.
	t.Run("no running job allows dispatch", func(t *testing.T) {
		truncateTables(t, db)
		sys, pol = createFixtureSystemAndPolicy(t, db)
		j := mkPending(sys.ID)
		dec, err := guard.AdmitStart(ctx, j.ID)
		if err != nil {
			t.Fatalf("AdmitStart: %v", err)
		}
		if !dec.Allowed {
			t.Fatalf("decision = blocked (%s), want allowed", dec.Reason)
		}
		if s := statusOf(j.ID); s != "running" {
			t.Errorf("status = %q, want running", s)
		}
	})

	// 2. One running job → second job stays pending (blocked, not failed).
	t.Run("second job blocked while one runs", func(t *testing.T) {
		truncateTables(t, db)
		sys, pol = createFixtureSystemAndPolicy(t, db)
		a := mkPending(sys.ID)
		if dec, err := guard.AdmitStart(ctx, a.ID); err != nil || !dec.Allowed {
			t.Fatalf("admit first: dec=%+v err=%v", dec, err)
		}
		b := mkPending(sys.ID)
		dec, err := guard.AdmitStart(ctx, b.ID)
		if err != nil {
			t.Fatalf("AdmitStart second returned error (must be a decision, not error): %v", err)
		}
		if dec.Allowed {
			t.Fatalf("second job admitted, want blocked")
		}
		if dec.Reason != catalog.DispatchBlockedAgentConcurrency {
			t.Errorf("reason = %q, want %q", dec.Reason, catalog.DispatchBlockedAgentConcurrency)
		}
		if dec.RunningCount != 1 || dec.Limit != 1 {
			t.Errorf("running=%d limit=%d, want 1/1", dec.RunningCount, dec.Limit)
		}
		if s := statusOf(b.ID); s != "pending" {
			t.Errorf("blocked job status = %q, want pending (never failed)", s)
		}
	})

	// 3–5. A terminal job frees the slot for the next one.
	freeAndNext := func(t *testing.T, terminalize func(id uuid.UUID) error, wantStatus string) {
		t.Helper()
		truncateTables(t, db)
		sys, pol = createFixtureSystemAndPolicy(t, db)
		a := mkPending(sys.ID)
		if dec, err := guard.AdmitStart(ctx, a.ID); err != nil || !dec.Allowed {
			t.Fatalf("admit first: dec=%+v err=%v", dec, err)
		}
		if err := terminalize(a.ID); err != nil {
			t.Fatalf("terminalize: %v", err)
		}
		if s := statusOf(a.ID); s != wantStatus {
			t.Fatalf("first job status = %q, want %q", s, wantStatus)
		}
		b := mkPending(sys.ID)
		dec, err := guard.AdmitStart(ctx, b.ID)
		if err != nil {
			t.Fatalf("AdmitStart next: %v", err)
		}
		if !dec.Allowed {
			t.Fatalf("next job blocked (%s) after slot freed, want allowed", dec.Reason)
		}
	}

	t.Run("success frees slot", func(t *testing.T) {
		freeAndNext(t, func(id uuid.UUID) error { return store.CompleteJob(ctx, id, 1024) }, "success")
	})
	t.Run("failed frees slot", func(t *testing.T) {
		freeAndNext(t, func(id uuid.UUID) error { return store.FailJob(ctx, id, "restic exit 1") }, "failed")
	})
	t.Run("cancelled frees slot", func(t *testing.T) {
		freeAndNext(t, func(id uuid.UUID) error { return store.CancelJob(ctx, id, "operator stop") }, "cancelled")
	})

	// 6. Concurrent dispatch must not bypass the limit — the core invariant.
	t.Run("concurrent dispatch cannot exceed limit", func(t *testing.T) {
		truncateTables(t, db)
		sys, pol = createFixtureSystemAndPolicy(t, db)
		const n = 12
		jobs := make([]*catalog.BackupJob, n)
		for i := range jobs {
			jobs[i] = mkPending(sys.ID)
		}

		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			allowed int
			blocked int
			errs    []error
		)
		start := make(chan struct{})
		for i := range jobs {
			wg.Add(1)
			go func(id uuid.UUID) {
				defer wg.Done()
				<-start // release all goroutines at once
				dec, err := guard.AdmitStart(ctx, id)
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err != nil:
					errs = append(errs, err)
				case dec.Allowed:
					allowed++
				default:
					blocked++
				}
			}(jobs[i].ID)
		}
		close(start)
		wg.Wait()

		if len(errs) != 0 {
			t.Fatalf("unexpected errors from concurrent AdmitStart: %v", errs)
		}
		if allowed != 1 {
			t.Errorf("allowed = %d, want exactly 1", allowed)
		}
		if blocked != n-1 {
			t.Errorf("blocked = %d, want %d", blocked, n-1)
		}
		if got := runningForSystem(sys.ID); got != 1 {
			t.Errorf("running jobs in DB = %d, want exactly 1 (invariant violated)", got)
		}
	})

	// 7. Different agents run in parallel — the limit is per-agent, not global.
	t.Run("different agents run in parallel", func(t *testing.T) {
		truncateTables(t, db)
		sys, pol = createFixtureSystemAndPolicy(t, db)
		s2now := time.Now().UTC() // ONLINE so the E1.3a health gate admits it
		s2 := &catalog.System{Hostname: "fixture-host-2", RiskClass: "standard", LastSeen: &s2now}
		if err := catalog.NewSystemStore(db).Create(ctx, s2); err != nil {
			t.Fatalf("create second system: %v", err)
		}
		j1 := mkPending(sys.ID)
		j2 := mkPending(s2.ID)
		if dec, err := guard.AdmitStart(ctx, j1.ID); err != nil || !dec.Allowed {
			t.Fatalf("admit agent1: dec=%+v err=%v", dec, err)
		}
		if dec, err := guard.AdmitStart(ctx, j2.ID); err != nil || !dec.Allowed {
			t.Fatalf("admit agent2 blocked — limit must be per-agent: dec=%+v err=%v", dec, err)
		}
		if runningForSystem(sys.ID) != 1 || runningForSystem(s2.ID) != 1 {
			t.Errorf("both agents should have exactly one running job each")
		}
	})

	// ERROR semantics stay separate from BLOCKED.
	t.Run("unknown job is not found", func(t *testing.T) {
		if _, err := guard.AdmitStart(ctx, uuid.New()); !errors.Is(err, catalog.ErrNotFound) {
			t.Errorf("want ErrNotFound, got %v", err)
		}
	})
	t.Run("non-pending job is illegal transition", func(t *testing.T) {
		truncateTables(t, db)
		sys, pol = createFixtureSystemAndPolicy(t, db)
		a := mkPending(sys.ID)
		if dec, err := guard.AdmitStart(ctx, a.ID); err != nil || !dec.Allowed {
			t.Fatalf("admit: dec=%+v err=%v", dec, err)
		}
		// a is now running; admitting it again must be an ERROR, not a decision.
		if _, err := guard.AdmitStart(ctx, a.ID); !errors.Is(err, catalog.ErrIllegalTransition) {
			t.Errorf("want ErrIllegalTransition, got %v", err)
		}
	})
}
