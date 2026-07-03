// Package scheduler dispatches backup, restore-test, and retention jobs
// according to per-policy cron schedules.
//
// Three schedule types per policy:
//   - Backup schedule      → creates a pending BackupJob
//   - Restore-test schedule → creates a pending RestoreTest for the latest snapshot
//   - Retention schedule   → creates a pending retention BackupJob
//
// All schedules are timezone-aware via IANA timezone strings.
package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"fmt"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"github.com/cerberus8484/opensourcebackup/internal/catalog"
)

// PolicyChangeNotifier is implemented by the Scheduler.
type PolicyChangeNotifier interface {
	PoliciesChanged(ctx context.Context)
}

// Scheduler dispatches jobs according to policy cron schedules.
type Scheduler struct {
	policies     catalog.PolicyStore
	jobs         catalog.JobStore
	snapshots    catalog.SnapshotStore
	restoreTests catalog.RestoreTestStore
	log          *slog.Logger

	mu   sync.Mutex
	cron *cron.Cron
}

// New creates a Scheduler. Pass nil for snapshots/restoreTests to disable those schedule types.
func New(policies catalog.PolicyStore, jobs catalog.JobStore, log *slog.Logger) *Scheduler {
	return &Scheduler{
		policies: policies,
		jobs:     jobs,
		log:      log,
		cron:     cron.New(),
	}
}

// WithStores adds optional stores for restore-test and retention scheduling.
func (s *Scheduler) WithStores(snapshots catalog.SnapshotStore, restoreTests catalog.RestoreTestStore) *Scheduler {
	s.snapshots = snapshots
	s.restoreTests = restoreTests
	return s
}

// Start loads all scheduled policies, registers cron entries, and runs the
// dead-man's switch until ctx is canceled.
func (s *Scheduler) Start(ctx context.Context) error {
	if err := s.reload(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	s.cron.Start()
	s.mu.Unlock()

	go s.runDeadManSwitch(ctx)
	go s.runStaleTestCleaner(ctx)
	go s.runStaleJobReaper(ctx)

	<-ctx.Done()
	s.mu.Lock()
	s.cron.Stop()
	s.mu.Unlock()
	s.log.Info("scheduler stopped")
	return nil
}

// PoliciesChanged reloads the cron schedule. Safe to call from any goroutine.
func (s *Scheduler) PoliciesChanged(ctx context.Context) {
	s.log.Info("scheduler: policies changed — reloading")
	if err := s.reload(ctx); err != nil {
		s.log.Error("scheduler reload failed", "error", err)
	}
}

// reload stops the current cron, loads all policies, and registers entries.
func (s *Scheduler) reload(ctx context.Context) error {
	policies, err := s.policies.List(ctx)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.cron.Stop()
	s.cron = cron.New() // standard 5-field cron (min hour dom month dow) — no WithSeconds()

	scheduled := 0
	for _, p := range policies {
		pol := p // capture
		loc := locationFor(pol.ScheduleConfig.Timezone)

		// ── Backup schedule ───────────────────────────────────────────────────

		backupCron := pol.ScheduleConfig.Cron
		if backupCron == "" && pol.Schedule != nil {
			backupCron = *pol.Schedule
		}
		if backupCron != "" {
			expr := withLocation(backupCron, loc)
			if _, err := s.cron.AddFunc(expr, func() {
				s.dispatchBackup(context.Background(), pol)
			}); err != nil {
				s.log.Warn("invalid backup cron — skipping",
					"policy", pol.Name, "cron", backupCron, "error", err)
			} else {
				scheduled++
			}
		}

		// ── Restore-test schedule ────────────────────────────────────────────
		// If no explicit cron is set but the policy has a backup schedule,
		// fall back to a weekly test every Sunday at 03:00 UTC.
		const defaultRestoreTestCron = "0 3 * * 0"

		restoreTestCron := pol.ScheduleConfig.RestoreTestCron
		if restoreTestCron == "" && backupCron != "" {
			restoreTestCron = defaultRestoreTestCron
			s.log.Debug("restore-test: no cron configured, using default weekly schedule",
				"policy", pol.Name, "default_cron", defaultRestoreTestCron)
		}

		if restoreTestCron != "" && s.restoreTests != nil {
			expr := withLocation(restoreTestCron, loc)
			if _, err := s.cron.AddFunc(expr, func() {
				s.dispatchRestoreTest(context.Background(), pol)
			}); err != nil {
				s.log.Warn("invalid restore-test cron — skipping",
					"policy", pol.Name, "cron", restoreTestCron, "error", err)
			} else {
				s.log.Debug("restore-test schedule registered",
					"policy", pol.Name, "cron", restoreTestCron)
			}
		}

		// ── Retention schedule ────────────────────────────────────────────────

		if pol.ScheduleConfig.RetentionCron != "" && pol.RetentionPlan.HasRules() {
			expr := withLocation(pol.ScheduleConfig.RetentionCron, loc)
			if _, err := s.cron.AddFunc(expr, func() {
				s.dispatchRetention(context.Background(), pol)
			}); err != nil {
				s.log.Warn("invalid retention cron — skipping",
					"policy", pol.Name, "cron", pol.ScheduleConfig.RetentionCron, "error", err)
			} else {
				s.log.Debug("retention schedule registered",
					"policy", pol.Name, "cron", pol.ScheduleConfig.RetentionCron)
			}
		}
	}

	s.cron.Start()
	s.log.Info("scheduler reloaded", "scheduled_policies", scheduled)
	return nil
}

// ── Dispatch functions ─────────────────────────────────────────────────────────

// inBackupWindow returns true if the current time is within the allowed backup window.
// If no window is configured (empty strings), always returns true.
func inBackupWindow(cfg catalog.ScheduleConfig, loc *time.Location) bool {
	if cfg.WindowStart == "" || cfg.WindowEnd == "" {
		return true
	}
	now := time.Now().In(loc)
	nowMin := now.Hour()*60 + now.Minute()

	parseHHMM := func(s string) int {
		var h, m int
		fmt.Sscanf(s, "%d:%d", &h, &m)
		return h*60 + m
	}
	start := parseHHMM(cfg.WindowStart)
	end   := parseHHMM(cfg.WindowEnd)

	if start <= end {
		return nowMin >= start && nowMin < end
	}
	// Window spans midnight (e.g. 22:00–06:00)
	return nowMin >= start || nowMin < end
}

// dispatchBackup creates a pending BackupJob for all systems that recently ran this policy.
func (s *Scheduler) dispatchBackup(ctx context.Context, p catalog.BackupPolicy) {
	// Check backup window — skip if outside allowed hours
	loc := locationFor(p.ScheduleConfig.Timezone)
	if !inBackupWindow(p.ScheduleConfig, loc) {
		s.log.Info("backup dispatch: outside backup window — skipping",
			"policy", p.Name,
			"window", p.ScheduleConfig.WindowStart+"–"+p.ScheduleConfig.WindowEnd,
		)
		return
	}

	systems, err := s.systemsForPolicy(ctx, p.ID)
	if err != nil || len(systems) == 0 {
		s.log.Warn("backup dispatch: no systems found for policy",
			"policy_id", p.ID, "policy_name", p.Name)
		return
	}
	for _, sysID := range systems {
		j := &catalog.BackupJob{
			SystemID: sysID,
			PolicyID: p.ID,
			Type:     catalog.JobTypeBackup,
			Status:   "pending",
		}
		if err := s.jobs.Create(ctx, j); err != nil {
			s.log.Error("failed to dispatch backup job",
				"policy", p.Name, "system_id", sysID, "error", err)
			continue
		}
		s.log.Info("backup job dispatched",
			"job_id", j.ID, "policy", p.Name, "system_id", sysID)
	}
}

// dispatchRestoreTest creates a RestoreTest for the most recent snapshot from this policy.
func (s *Scheduler) dispatchRestoreTest(ctx context.Context, p catalog.BackupPolicy) {
	if s.snapshots == nil || s.restoreTests == nil {
		return
	}
	// Find latest snapshot for jobs of this policy
	latest, err := s.jobs.LatestByPolicyID(ctx, p.ID)
	if errors.Is(err, catalog.ErrNotFound) || latest == nil {
		s.log.Warn("restore-test dispatch: no jobs found for policy", "policy", p.Name)
		return
	}
	if err != nil {
		s.log.Error("restore-test dispatch: job lookup failed", "policy", p.Name, "error", err)
		return
	}
	// Find snapshot for this job
	snaps, err := s.snapshots.ListByJobID(ctx, latest.ID)
	if err != nil || len(snaps) == 0 {
		s.log.Warn("restore-test dispatch: no snapshots for latest job",
			"policy", p.Name, "job_id", latest.ID)
		return
	}
	snap := snaps[len(snaps)-1] // most recent
	rt := &catalog.RestoreTest{
		SnapshotID:   snap.ID,
		SystemID:     latest.SystemID,
		RepositoryID: snap.RepositoryID,
		Status:       "pending",
	}
	if err := s.restoreTests.Create(ctx, rt); err != nil {
		s.log.Error("restore-test dispatch: create failed", "policy", p.Name, "error", err)
		return
	}
	s.log.Info("restore test dispatched",
		"restore_test_id", rt.ID, "snapshot_id", snap.ID, "policy", p.Name)
}

// dispatchRetention creates a pending retention job for each system using this policy.
func (s *Scheduler) dispatchRetention(ctx context.Context, p catalog.BackupPolicy) {
	systems, err := s.systemsForPolicy(ctx, p.ID)
	if err != nil || len(systems) == 0 {
		s.log.Warn("retention dispatch: no systems found for policy",
			"policy", p.Name)
		return
	}
	for _, sysID := range systems {
		j := &catalog.BackupJob{
			SystemID: sysID,
			PolicyID: p.ID,
			Type:     catalog.JobTypeRetention,
			Status:   "pending",
		}
		if err := s.jobs.Create(ctx, j); err != nil {
			s.log.Error("retention job dispatch failed",
				"policy", p.Name, "system_id", sysID, "error", err)
			continue
		}
		s.log.Info("retention job dispatched",
			"job_id", j.ID, "policy", p.Name, "system_id", sysID)
	}
}

// systemsForPolicy returns distinct system IDs that have run this policy.
func (s *Scheduler) systemsForPolicy(ctx context.Context, policyID uuid.UUID) ([]uuid.UUID, error) {
	allJobs, err := s.jobs.List(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[uuid.UUID]bool)
	for _, j := range allJobs {
		if j.PolicyID == policyID && j.Type == catalog.JobTypeBackup {
			seen[j.SystemID] = true
		}
	}
	out := make([]uuid.UUID, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	return out, nil
}

// ── Dead-man's switch ──────────────────────────────────────────────────────────

func (s *Scheduler) runDeadManSwitch(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkDeadMan(ctx)
		}
	}
}

func (s *Scheduler) checkDeadMan(ctx context.Context) {
	policies, err := s.policies.List(ctx)
	if err != nil {
		s.log.Error("dead-man: failed to load policies", "error", err)
		return
	}
	for _, p := range policies {
		backupCron := p.ScheduleConfig.Cron
		if backupCron == "" && p.Schedule != nil {
			backupCron = *p.Schedule
		}
		if backupCron == "" {
			continue
		}
		interval, err := cronInterval(backupCron)
		if err != nil {
			continue
		}
		deadline := time.Now().Add(-time.Duration(float64(interval) * 1.5))
		latest, err := s.jobs.LatestByPolicyID(ctx, p.ID)
		if errors.Is(err, catalog.ErrNotFound) {
			s.log.Warn("dead-man: no job ever ran", "policy", p.Name)
			continue
		}
		if err != nil {
			s.log.Error("dead-man: query failed", "policy", p.ID, "error", err)
			continue
		}
		if latest.CreatedAt.Before(deadline) {
			s.log.Warn("dead-man: overdue", "policy", p.Name, "last_job_at", latest.CreatedAt)
		}
	}
}

// ── Stale restore-test cleaner ────────────────────────────────────────────────

// runStaleTestCleaner periodically marks restore tests that have been
// "running" for too long as failed. Protects against agent crashes or
// network partitions leaving tests stuck in running state forever.
func (s *Scheduler) runStaleTestCleaner(ctx context.Context) {
	const staleAfter  = 30 * time.Minute
	const checkEvery  = 10 * time.Minute
	ticker := time.NewTicker(checkEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.cleanStaleRestoreTests(ctx, staleAfter)
		}
	}
}

func (s *Scheduler) cleanStaleRestoreTests(ctx context.Context, staleAfter time.Duration) {
	if s.restoreTests == nil {
		return
	}
	tests, err := s.restoreTests.List(ctx)
	if err != nil {
		s.log.Error("stale-cleaner: failed to list restore tests", "error", err)
		return
	}
	deadline := time.Now().Add(-staleAfter)
	cleaned := 0
	for _, rt := range tests {
		if rt.Status != "running" {
			continue
		}
		startedAt := rt.UpdatedAt // UpdatedAt is set when status → running
		if startedAt.After(deadline) {
			continue
		}
		reason := fmt.Sprintf("timed out after %s — agent may have crashed or lost connection", staleAfter)
		rt.Status = "failed"
		rt.ErrorSummary = &reason
		if err := s.restoreTests.Update(ctx, &rt); err != nil {
			s.log.Error("stale-cleaner: update failed", "id", rt.ID, "error", err)
			continue
		}
		s.log.Warn("stale-cleaner: marked restore test as failed",
			"id", rt.ID, "running_since", startedAt)
		cleaned++
	}
	if cleaned > 0 {
		s.log.Info("stale-cleaner: cleaned up stale tests", "count", cleaned)
	}
}

// ── Stale-Job-Reaper ───────────────────────────────────────────────────────────

// runStaleJobReaper periodically auto-fails jobs the dashboard would otherwise
// show as stuck: "running" jobs without an agent heartbeat, and "pending" jobs an
// offline agent never claimed. Without this, a crashed or offline agent leaves
// jobs running forever or piling up pending — both hide the fact that the backup
// never completed and make the dashboard untrustworthy.
//
// Three grace periods (see catalog.JobStore.FailStaleJobs):
//   - progressGrace: a job that reported progress, then went silent.
//   - startGrace:    a job that never reported progress (older agent builds),
//                    measured from start — generous so genuinely long backups
//                    on non-reporting agents are not killed prematurely.
//   - pendingGrace:  a job created but never picked up — a healthy agent claims
//                    pending work within seconds, so this is generous enough to
//                    survive a brief control-plane/agent restart, but catches
//                    agents that are actually offline.
func (s *Scheduler) runStaleJobReaper(ctx context.Context) {
	const progressGrace = 30 * time.Minute // silent since last progress report
	const startGrace    = 12 * time.Hour   // no progress ever, running this long
	const pendingGrace  = 6 * time.Hour    // created, never claimed by an agent
	const checkEvery    = 10 * time.Minute

	ticker := time.NewTicker(checkEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := s.jobs.FailStaleJobs(ctx, progressGrace, startGrace, pendingGrace)
			if err != nil {
				s.log.Error("stale-job-reaper: failed to reap stale jobs", "error", err)
				continue
			}
			if n > 0 {
				s.log.Warn("stale-job-reaper: auto-failed stuck jobs",
					"count", n, "progress_grace", progressGrace,
					"start_grace", startGrace, "pending_grace", pendingGrace)
			}
		}
	}
}

// ── Helpers ────────────────────────────────────────────────────────────────────

func locationFor(tz string) *time.Location {
	if tz == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}

// withLocation prepends "TZ=..." to a cron expression so robfig/cron
// evaluates it in the correct timezone.
func withLocation(expr string, loc *time.Location) string {
	if loc == time.UTC || loc == nil {
		return expr
	}
	return "TZ=" + loc.String() + " " + expr
}

func cronInterval(expr string) (time.Duration, error) {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(expr)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	next1 := sched.Next(now)
	next2 := sched.Next(next1)
	return next2.Sub(next1), nil
}
