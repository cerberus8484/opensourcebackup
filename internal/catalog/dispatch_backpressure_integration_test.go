//go:build integration

package catalog_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cerberus8484/opensourcebackup/internal/catalog"
)

// TestDispatchBackpressure is the acceptance proof for E1.4: global and per-repository
// concurrency guards compose in front of the E1.1 per-agent guard, race-safely against
// PostgreSQL, with deterministic block-reason priority.
//
// Invariants (limit=L): running_global ≤ L_global, running_per_repo ≤ L_repo,
// running_per_agent ≤ L_agent — even under concurrent dispatch.
func TestDispatchBackpressure(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	systems := catalog.NewSystemStore(db)
	repos := catalog.NewRepositoryStore(db)
	policies := catalog.NewPolicyStore(db)
	jobs := catalog.NewJobStore(db)

	mkSystem := func(t *testing.T, name string) uuid.UUID {
		t.Helper()
		now := time.Now().UTC() // ONLINE + ACTIVE (default) → passes op/health gates
		s := &catalog.System{Hostname: name, RiskClass: "standard", LastSeen: &now}
		if err := systems.Create(ctx, s); err != nil {
			t.Fatalf("create system: %v", err)
		}
		return s.ID
	}
	mkRepo := func(t *testing.T) uuid.UUID {
		t.Helper()
		r := &catalog.BackupRepository{Type: "local", Location: "/tmp/" + uuid.NewString()}
		if err := repos.Create(ctx, r); err != nil {
			t.Fatalf("create repo: %v", err)
		}
		return r.ID
	}
	mkPolicy := func(t *testing.T, repoID uuid.UUID) uuid.UUID {
		t.Helper()
		p := &catalog.BackupPolicy{Name: "p-" + uuid.NewString()[:8], Engine: "restic", RepositoryID: &repoID}
		if err := policies.Create(ctx, p); err != nil {
			t.Fatalf("create policy: %v", err)
		}
		return p.ID
	}
	mkJob := func(t *testing.T, sysID, polID uuid.UUID) uuid.UUID {
		t.Helper()
		j := &catalog.BackupJob{SystemID: sysID, PolicyID: polID, Status: "pending"}
		if err := jobs.Create(ctx, j); err != nil {
			t.Fatalf("create job: %v", err)
		}
		return j.ID
	}
	countQ := func(t *testing.T, q string, args ...any) int {
		t.Helper()
		var n int
		if err := db.Pool().QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}
	runningGlobal := func(t *testing.T) int {
		return countQ(t, `SELECT count(*) FROM backup_jobs WHERE status='running'`)
	}
	runningRepo := func(t *testing.T, repoID uuid.UUID) int {
		return countQ(t, `SELECT count(*) FROM backup_jobs j JOIN backup_policies p ON p.id=j.policy_id
			WHERE j.status='running' AND p.repository_id=$1`, repoID)
	}

	// ── Global gate ───────────────────────────────────────────────────────────
	t.Run("global under/at/free limit", func(t *testing.T) {
		truncateTables(t, db)
		g := catalog.NewDispatchGuardWithLimits(db, catalog.DispatchLimits{Global: 2, PerRepository: 100, PerAgent: 100})
		repo := mkRepo(t)
		// three distinct agents on distinct policies (same repo, but repo limit is high)
		var ids []uuid.UUID
		for i := 0; i < 3; i++ {
			ids = append(ids, mkJob(t, mkSystem(t, fmt.Sprintf("g%d", i)), mkPolicy(t, repo)))
		}
		if dec, err := g.AdmitStart(ctx, ids[0]); err != nil || !dec.Allowed {
			t.Fatalf("1st under global limit must admit: %+v %v", dec, err)
		}
		if dec, err := g.AdmitStart(ctx, ids[1]); err != nil || !dec.Allowed {
			t.Fatalf("2nd under global limit must admit: %+v %v", dec, err)
		}
		dec, err := g.AdmitStart(ctx, ids[2])
		if err != nil {
			t.Fatalf("3rd: %v", err)
		}
		if dec.Allowed || dec.Reason != catalog.DispatchBlockedGlobalConcurrency {
			t.Fatalf("3rd at global limit must block global_concurrency_limit: %+v", dec)
		}
		// Free a global slot → the pending job is admitted.
		if err := jobs.CompleteJob(ctx, ids[0], 1); err != nil {
			t.Fatalf("complete: %v", err)
		}
		if dec, err := g.AdmitStart(ctx, ids[2]); err != nil || !dec.Allowed {
			t.Fatalf("after slot freed must admit: %+v %v", dec, err)
		}
	})

	// ── Repository gate ───────────────────────────────────────────────────────
	t.Run("repository at limit blocks; other repo parallel", func(t *testing.T) {
		truncateTables(t, db)
		g := catalog.NewDispatchGuardWithLimits(db, catalog.DispatchLimits{Global: 100, PerRepository: 2, PerAgent: 100})
		repoA, repoB := mkRepo(t), mkRepo(t)
		polA, polB := mkPolicy(t, repoA), mkPolicy(t, repoB)
		// two running on repoA (distinct agents)
		a1 := mkJob(t, mkSystem(t, "a1"), polA)
		a2 := mkJob(t, mkSystem(t, "a2"), polA)
		a3 := mkJob(t, mkSystem(t, "a3"), polA)
		b1 := mkJob(t, mkSystem(t, "b1"), polB)
		if dec, err := g.AdmitStart(ctx, a1); err != nil || !dec.Allowed {
			t.Fatalf("a1: %+v %v", dec, err)
		}
		if dec, err := g.AdmitStart(ctx, a2); err != nil || !dec.Allowed {
			t.Fatalf("a2: %+v %v", dec, err)
		}
		dec, err := g.AdmitStart(ctx, a3)
		if err != nil {
			t.Fatalf("a3: %v", err)
		}
		if dec.Allowed || dec.Reason != catalog.DispatchBlockedRepositoryConcurrency {
			t.Fatalf("a3 at repo limit must block repository_concurrency_limit: %+v", dec)
		}
		// A different repository is unaffected.
		if dec, err := g.AdmitStart(ctx, b1); err != nil || !dec.Allowed {
			t.Fatalf("other repo must run in parallel: %+v %v", dec, err)
		}
	})

	// ── Deterministic priority ────────────────────────────────────────────────
	t.Run("priority global over repo over agent", func(t *testing.T) {
		truncateTables(t, db)
		// global=1: one running anywhere blocks everything else with the GLOBAL reason,
		// even a job on a different repo/agent that would otherwise be fine.
		g := catalog.NewDispatchGuardWithLimits(db, catalog.DispatchLimits{Global: 1, PerRepository: 1, PerAgent: 1})
		repoA, repoB := mkRepo(t), mkRepo(t)
		j1 := mkJob(t, mkSystem(t, "p1"), mkPolicy(t, repoA))
		j2 := mkJob(t, mkSystem(t, "p2"), mkPolicy(t, repoB))
		if dec, err := g.AdmitStart(ctx, j1); err != nil || !dec.Allowed {
			t.Fatalf("j1: %+v %v", dec, err)
		}
		dec, _ := g.AdmitStart(ctx, j2)
		if dec.Reason != catalog.DispatchBlockedGlobalConcurrency {
			t.Fatalf("global must win priority: got %q", dec.Reason)
		}
	})

	t.Run("priority repository over agent", func(t *testing.T) {
		truncateTables(t, db)
		g := catalog.NewDispatchGuardWithLimits(db, catalog.DispatchLimits{Global: 100, PerRepository: 1, PerAgent: 1})
		repo := mkRepo(t)
		pol := mkPolicy(t, repo)
		j1 := mkJob(t, mkSystem(t, "r1"), pol)
		j2 := mkJob(t, mkSystem(t, "r2"), pol) // different agent, same repo
		if dec, err := g.AdmitStart(ctx, j1); err != nil || !dec.Allowed {
			t.Fatalf("j1: %+v %v", dec, err)
		}
		dec, _ := g.AdmitStart(ctx, j2)
		if dec.Reason != catalog.DispatchBlockedRepositoryConcurrency {
			t.Fatalf("repository must win over agent: got %q", dec.Reason)
		}
	})

	// ── Combination (spec §10): Global 10, RepoA 2, Agent 1 ───────────────────
	t.Run("combination global10 repoA2 agent1", func(t *testing.T) {
		truncateTables(t, db)
		g := catalog.NewDispatchGuardWithLimits(db, catalog.DispatchLimits{Global: 10, PerRepository: 2, PerAgent: 1})
		repoA, repoB := mkRepo(t), mkRepo(t)
		polA, polB := mkPolicy(t, repoA), mkPolicy(t, repoB)
		a1 := mkJob(t, mkSystem(t, "c1"), polA)
		a2 := mkJob(t, mkSystem(t, "c2"), polA)
		a3 := mkJob(t, mkSystem(t, "c3"), polA)
		b1 := mkJob(t, mkSystem(t, "c4"), polB)
		g.AdmitStart(ctx, a1) //nolint:errcheck
		g.AdmitStart(ctx, a2) //nolint:errcheck
		dec, _ := g.AdmitStart(ctx, a3)
		if dec.Reason != catalog.DispatchBlockedRepositoryConcurrency {
			t.Errorf("repoA full → repository_concurrency_limit, got %q", dec.Reason)
		}
		if dec, err := g.AdmitStart(ctx, b1); err != nil || !dec.Allowed {
			t.Errorf("repoB must start: %+v %v", dec, err)
		}
	})

	// ── Regressions: operation state + health still win over resource gates ────
	t.Run("operation state and health precede resource gates", func(t *testing.T) {
		truncateTables(t, db)
		g := catalog.NewDispatchGuardWithLimits(db, catalog.DispatchLimits{Global: 1, PerRepository: 1, PerAgent: 1})
		repo := mkRepo(t)
		pol := mkPolicy(t, repo)
		// saturate global with one running job
		if dec, err := g.AdmitStart(ctx, mkJob(t, mkSystem(t, "s0"), pol)); err != nil || !dec.Allowed {
			t.Fatalf("saturate: %+v %v", dec, err)
		}
		// maintenance system → system_maintenance despite global being full
		sysM := mkSystem(t, "sM")
		if _, err := systems.SetOperationState(ctx, sysM, string(catalog.OperationMaintenance)); err != nil {
			t.Fatalf("set state: %v", err)
		}
		dec, _ := g.AdmitStart(ctx, mkJob(t, sysM, pol))
		if dec.Reason != catalog.DispatchBlockedSystemMaintenance {
			t.Errorf("operation state must win over global: got %q", dec.Reason)
		}
		// offline system → system_offline despite global being full
		sysO := mkSystem(t, "sO")
		if _, err := db.Pool().Exec(ctx, `UPDATE systems SET last_seen=NOW()-INTERVAL '20 minutes' WHERE id=$1`, sysO); err != nil {
			t.Fatalf("set offline: %v", err)
		}
		dec, _ = g.AdmitStart(ctx, mkJob(t, sysO, pol))
		if dec.Reason != catalog.DispatchBlockedSystemOffline {
			t.Errorf("health must win over global: got %q", dec.Reason)
		}
	})

	// ── Race invariants ───────────────────────────────────────────────────────
	race := func(t *testing.T, ids []uuid.UUID, g *catalog.DispatchGuard) int {
		t.Helper()
		var wg sync.WaitGroup
		var mu sync.Mutex
		allowed := 0
		start := make(chan struct{})
		for _, id := range ids {
			wg.Add(1)
			go func(id uuid.UUID) {
				defer wg.Done()
				<-start
				if dec, err := g.AdmitStart(ctx, id); err == nil && dec.Allowed {
					mu.Lock()
					allowed++
					mu.Unlock()
				}
			}(id)
		}
		close(start)
		wg.Wait()
		return allowed
	}

	t.Run("race: global limit never exceeded", func(t *testing.T) {
		truncateTables(t, db)
		g := catalog.NewDispatchGuardWithLimits(db, catalog.DispatchLimits{Global: 5, PerRepository: 100, PerAgent: 100})
		var ids []uuid.UUID
		for i := 0; i < 20; i++ { // 20 distinct systems + repos → only global binds
			ids = append(ids, mkJob(t, mkSystem(t, fmt.Sprintf("rg%d", i)), mkPolicy(t, mkRepo(t))))
		}
		allowed := race(t, ids, g)
		if allowed != 5 || runningGlobal(t) != 5 {
			t.Errorf("global invariant violated: allowed=%d running=%d, want 5/5", allowed, runningGlobal(t))
		}
	})

	t.Run("race: repository limit never exceeded", func(t *testing.T) {
		truncateTables(t, db)
		g := catalog.NewDispatchGuardWithLimits(db, catalog.DispatchLimits{Global: 100, PerRepository: 3, PerAgent: 100})
		repo := mkRepo(t)
		pol := mkPolicy(t, repo)
		var ids []uuid.UUID
		for i := 0; i < 10; i++ { // 10 distinct agents, same repo → repo binds
			ids = append(ids, mkJob(t, mkSystem(t, fmt.Sprintf("rr%d", i)), pol))
		}
		allowed := race(t, ids, g)
		if allowed != 3 || runningRepo(t, repo) != 3 {
			t.Errorf("repo invariant violated: allowed=%d running=%d, want 3/3", allowed, runningRepo(t, repo))
		}
	})

	t.Run("race: agent limit regression (E1.1) never exceeded", func(t *testing.T) {
		truncateTables(t, db)
		g := catalog.NewDispatchGuardWithLimits(db, catalog.DispatchLimits{Global: 100, PerRepository: 100, PerAgent: 1})
		sys := mkSystem(t, "ra")
		pol := mkPolicy(t, mkRepo(t))
		var ids []uuid.UUID
		for i := 0; i < 10; i++ { // 10 jobs, same agent → agent binds
			ids = append(ids, mkJob(t, sys, pol))
		}
		allowed := race(t, ids, g)
		perAgent := countQ(t, `SELECT count(*) FROM backup_jobs WHERE system_id=$1 AND status='running'`, sys)
		if allowed != 1 || perAgent != 1 {
			t.Errorf("agent invariant violated: allowed=%d running=%d, want 1/1", allowed, perAgent)
		}
	})
}
