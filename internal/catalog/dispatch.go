package catalog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// DispatchDecision is the outcome of a dispatch-guard evaluation for one job.
//
// Allowed == false is NOT an error: it is normal backpressure ("not now"). The
// job is deliberately left pending and is retried later by the scheduler/agent.
// Genuine technical failures are returned as a separate error and never folded
// into a decision — BLOCKED and ERROR must stay distinguishable (see AdmitStart).
type DispatchDecision struct {
	Allowed bool
	Reason  string // stable, structured code when blocked; empty when allowed

	// Agent-level counters (E1.1). RunningCount/Limit are the AGENT running count and
	// limit — kept under these names for backward compatibility.
	RunningCount int
	Limit        int

	// Global + repository counters (E1.4) — for observability.
	GlobalRunning int
	GlobalLimit   int
	RepoRunning   int
	RepoLimit     int
	RepositoryID  string // the job's repository (via its policy); "" when none

	// Health snapshot (E1.3a) considered at decision time.
	HealthState    string // online|degraded|offline|unknown
	LastSeenAgeSec int64  // age of last_seen in seconds (0 when never seen)
}

// Structured, stable block reasons. Cockpit, metrics and audit build on these
// exact strings, so they must not change casually.
const (
	// Resource-guard reasons (E1.1 / E1.4). Priority: global → repository → agent.
	DispatchBlockedGlobalConcurrency     = "global_concurrency_limit"
	DispatchBlockedRepositoryConcurrency = "repository_concurrency_limit"
	DispatchBlockedAgentConcurrency      = "agent_concurrency_limit"
	// Operation-state reasons (E1.2). The system's admin-set state forbids new jobs.
	DispatchBlockedSystemMaintenance = "system_maintenance"
	DispatchBlockedSystemDraining    = "system_draining"
	DispatchBlockedSystemSuspended   = "system_suspended"
	// Health reasons (E1.3a). Observed health forbids new jobs. DEGRADED does NOT block.
	DispatchBlockedSystemOffline       = "system_offline"
	DispatchBlockedSystemHealthUnknown = "system_health_unknown"
)

// healthDispatchBlockReason maps a health state to a dispatch block reason, or ""
// when the state permits new jobs. Only OFFLINE and UNKNOWN block; ONLINE and
// DEGRADED are permitted (DEGRADED is surfaced as a warning by the caller).
func healthDispatchBlockReason(h HealthState) string {
	switch h {
	case HealthOffline:
		return DispatchBlockedSystemOffline
	case HealthUnknown:
		return DispatchBlockedSystemHealthUnknown
	default:
		return ""
	}
}

// Conservative safety defaults for E1.4. A future slice may make these configurable
// per repository / policy; for now they are central server defaults (no migration).
const (
	DefaultMaxConcurrentJobsGlobal        = 20
	DefaultMaxConcurrentJobsPerRepository = 5
	DefaultMaxConcurrentJobsPerAgent      = 1
)

// globalDispatchLockKey is the single advisory-lock key that serializes the whole
// dispatch DECISION (count + admit) platform-wide. Using one lock keeps the
// multi-level guard deadlock-free (there is only ever one lock to acquire). The
// lock is held for the duration of a decision (milliseconds) — never during a
// backup — so serializing decisions is acceptable at the current scale. Splitting
// into per-repository locks for more decision parallelism is a future optimization.
const globalDispatchLockKey int64 = 0x4F53420044 // "OSB\0D"

// DispatchLimits holds the three concurrency ceilings evaluated at dispatch.
type DispatchLimits struct {
	Global        int // max concurrently running jobs across the whole platform
	PerRepository int // max concurrently running jobs writing to one repository
	PerAgent      int // max concurrently running jobs on one agent (system)
}

// DefaultDispatchLimits returns the conservative E1.4 defaults (20 / 5 / 1).
func DefaultDispatchLimits() DispatchLimits {
	return DispatchLimits{
		Global:        DefaultMaxConcurrentJobsGlobal,
		PerRepository: DefaultMaxConcurrentJobsPerRepository,
		PerAgent:      DefaultMaxConcurrentJobsPerAgent,
	}
}

// normalized coerces every limit to >= 1 so a misconfiguration can never open the
// gate to unlimited concurrency.
func (l DispatchLimits) normalized() DispatchLimits {
	if l.Global < 1 {
		l.Global = 1
	}
	if l.PerRepository < 1 {
		l.PerRepository = 1
	}
	if l.PerAgent < 1 {
		l.PerAgent = 1
	}
	return l
}

// DispatchGuard decides whether a pending job may transition to running.
//
// It enforces three concurrency ceilings — global, per-repository, per-agent —
// atomically against PostgreSQL, which stays the single source of truth. The
// count checks and the pending→running transition happen inside ONE transaction
// under a single global advisory lock, so concurrent dispatchers (even across
// multiple control-plane instances) can never both admit a job past any limit.
//
// It is intentionally small. It is the seam where earlier dispatch stages (system
// operation state E1.2, health E1.3a) compose in front of these resource gates.
type DispatchGuard struct {
	db     *DB
	limits DispatchLimits
}

// NewDispatchGuard returns a guard with the default global/repository limits and
// the given per-agent limit. Kept for backward compatibility with E1.1 callers.
func NewDispatchGuard(db *DB, maxPerAgent int) *DispatchGuard {
	l := DefaultDispatchLimits()
	l.PerAgent = maxPerAgent
	return NewDispatchGuardWithLimits(db, l)
}

// NewDispatchGuardWithLimits returns a guard enforcing the given limits (each
// coerced to >= 1).
func NewDispatchGuardWithLimits(db *DB, limits DispatchLimits) *DispatchGuard {
	return &DispatchGuard{db: db, limits: limits.normalized()}
}

// MaxPerAgent returns the configured per-agent concurrency limit.
func (g *DispatchGuard) MaxPerAgent() int { return g.limits.PerAgent }

// Limits returns the configured dispatch limits.
func (g *DispatchGuard) Limits() DispatchLimits { return g.limits }

// AdmitStart atomically evaluates the guard for the given pending job and, when
// allowed, transitions it pending→running (occupying a slot).
//
// Returns:
//   - (Allowed=true, ...), nil          → job is now running; the agent may work
//   - (Allowed=false, Reason=...), nil  → blocked; job left pending (retry later)
//   - _, ErrNotFound                    → job does not exist
//   - _, ErrIllegalTransition           → job is not pending (already running/terminal)
//   - _, other error                    → technical failure
//
// Gate order (deterministic block-reason priority):
//
//	Operation State → Health → Global → Repository → Agent → Admit
//
// Only the "running" state occupies a slot. A running job with a pending cancel
// request still occupies its slot until the agent confirms "cancelled".
func (g *DispatchGuard) AdmitStart(ctx context.Context, jobID uuid.UUID) (DispatchDecision, error) {
	tx, err := g.db.pool.Begin(ctx)
	if err != nil {
		return DispatchDecision{}, fmt.Errorf("dispatch guard: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after a successful commit is a no-op

	// 1. Lock the job row and read its agent (system), status, and repository (via the
	//    policy). FOR UPDATE OF j locks only the job row, not the policy.
	var (
		rawSysID  pgtype.UUID
		status    string
		rawRepoID pgtype.UUID
	)
	err = tx.QueryRow(ctx, `
		SELECT j.system_id, j.status, p.repository_id
		FROM backup_jobs j
		JOIN backup_policies p ON p.id = j.policy_id
		WHERE j.id = $1
		FOR UPDATE OF j`,
		pgtype.UUID{Bytes: jobID, Valid: true},
	).Scan(&rawSysID, &status, &rawRepoID)
	if errors.Is(err, pgx.ErrNoRows) {
		return DispatchDecision{}, ErrNotFound
	}
	if err != nil {
		return DispatchDecision{}, fmt.Errorf("dispatch guard: load job: %w", err)
	}
	if status != "pending" {
		return DispatchDecision{}, fmt.Errorf("%w: job is %q", ErrIllegalTransition, status)
	}
	repoID := ""
	if rawRepoID.Valid {
		repoID = uuid.UUID(rawRepoID.Bytes).String()
	}

	// 2. Read the system's operation state (E1.2) and last_seen (E1.3a health).
	var (
		opState  string
		lastSeen *time.Time
	)
	if err = tx.QueryRow(ctx,
		`SELECT operation_state, last_seen FROM systems WHERE id=$1`, rawSysID,
	).Scan(&opState, &lastSeen); err != nil {
		return DispatchDecision{}, fmt.Errorf("dispatch guard: load system state: %w", err)
	}

	// Health snapshot (derived, never stored). last_seen is refreshed by the agent's
	// heartbeat at the START of each poll, BEFORE it ever reaches this StartJob, so a
	// live agent is ONLINE here and cannot lock itself out.
	health := DeriveHealthState(lastSeen, time.Now())
	var ageSec int64
	if lastSeen != nil {
		ageSec = int64(time.Since(*lastSeen).Seconds())
	}
	// base stamps the constant metadata (limits, repo, health) on every decision;
	// the running counts are filled in per return.
	base := func(d DispatchDecision) DispatchDecision {
		d.Limit = g.limits.PerAgent
		d.GlobalLimit = g.limits.Global
		d.RepoLimit = g.limits.PerRepository
		d.RepositoryID = repoID
		d.HealthState = string(health)
		d.LastSeenAgeSec = ageSec
		return d
	}
	commitBlocked := func(d DispatchDecision) (DispatchDecision, error) {
		if cerr := tx.Commit(ctx); cerr != nil {
			return DispatchDecision{}, fmt.Errorf("dispatch guard: commit (blocked): %w", cerr)
		}
		return base(d), nil
	}

	// 3. Operation-state gate (E1.2). Evaluated FIRST so the block reason is
	//    deterministic: e.g. MAINTENANCE + OFFLINE always reports system_maintenance.
	if reason := OperationState(opState).DispatchBlockReason(); reason != "" {
		return commitBlocked(DispatchDecision{Allowed: false, Reason: reason})
	}

	// 4. Health gate (E1.3a): OFFLINE / UNKNOWN block; DEGRADED is allowed.
	if reason := healthDispatchBlockReason(health); reason != "" {
		return commitBlocked(DispatchDecision{Allowed: false, Reason: reason})
	}

	// 5. Serialize the whole resource decision platform-wide with a single global
	//    advisory lock — one lock, so the multi-level guard is deadlock-free. Held
	//    only for this (millisecond) decision, never during a backup.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, globalDispatchLockKey); err != nil {
		return DispatchDecision{}, fmt.Errorf("dispatch guard: advisory lock: %w", err)
	}

	// 6. Global gate. Only "running" occupies a slot; the count is bounded by the
	//    global limit, so this scan is cheap.
	var globalRunning int
	if err = tx.QueryRow(ctx,
		`SELECT count(*) FROM backup_jobs WHERE status='running'`,
	).Scan(&globalRunning); err != nil {
		return DispatchDecision{}, fmt.Errorf("dispatch guard: count global running: %w", err)
	}
	if globalRunning >= g.limits.Global {
		return commitBlocked(DispatchDecision{
			Allowed: false, Reason: DispatchBlockedGlobalConcurrency, GlobalRunning: globalRunning,
		})
	}

	// 7. Repository gate. Skipped when the job's policy has no repository (the agent
	//    fails such a job later) — we cannot meaningfully count "jobs on no repository".
	var repoRunning int
	if repoID != "" {
		if err = tx.QueryRow(ctx, `
			SELECT count(*)
			FROM backup_jobs j
			JOIN backup_policies p ON p.id = j.policy_id
			WHERE j.status='running' AND p.repository_id = $1`,
			rawRepoID,
		).Scan(&repoRunning); err != nil {
			return DispatchDecision{}, fmt.Errorf("dispatch guard: count repository running: %w", err)
		}
		if repoRunning >= g.limits.PerRepository {
			return commitBlocked(DispatchDecision{
				Allowed: false, Reason: DispatchBlockedRepositoryConcurrency,
				GlobalRunning: globalRunning, RepoRunning: repoRunning,
			})
		}
	}

	// 8. Agent gate (E1.1).
	var agentRunning int
	if err = tx.QueryRow(ctx,
		`SELECT count(*) FROM backup_jobs WHERE system_id=$1 AND status='running'`,
		rawSysID,
	).Scan(&agentRunning); err != nil {
		return DispatchDecision{}, fmt.Errorf("dispatch guard: count agent running: %w", err)
	}
	if agentRunning >= g.limits.PerAgent {
		return commitBlocked(DispatchDecision{
			Allowed: false, Reason: DispatchBlockedAgentConcurrency,
			RunningCount: agentRunning, GlobalRunning: globalRunning, RepoRunning: repoRunning,
		})
	}

	// 9. Admit — transition pending→running inside the same locked transaction.
	tag, err := tx.Exec(ctx,
		`UPDATE backup_jobs SET status='running', started_at=NOW()
		 WHERE id=$1 AND status='pending'`,
		pgtype.UUID{Bytes: jobID, Valid: true},
	)
	if err != nil {
		return DispatchDecision{}, fmt.Errorf("dispatch guard: start job: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// The row was pending under FOR UPDATE moments ago; reaching here would be a
		// genuine anomaly. Reject rather than silently allow.
		return DispatchDecision{}, fmt.Errorf("%w: job no longer pending", ErrIllegalTransition)
	}
	if err = tx.Commit(ctx); err != nil {
		return DispatchDecision{}, fmt.Errorf("dispatch guard: commit (admit): %w", err)
	}
	return base(DispatchDecision{
		Allowed:       true,
		RunningCount:  agentRunning + 1,
		GlobalRunning: globalRunning + 1,
		RepoRunning:   repoRunning + 1,
	}), nil
}
