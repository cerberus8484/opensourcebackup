// Package metrics implements a Prometheus collector for OpenSourceBackup.
//
// Design principles:
//   - No shadow data: every metric is computed fresh from the stores on each scrape.
//   - Same truth as the UI: recovery score and agent status use identical logic to the dashboard.
//   - No background goroutines: the collector is stateless between scrapes.
//   - No global variables: all state lives in the Collector struct.
package metrics

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/cerberus8484/opensourcebackup/internal/catalog"
	apphealth "github.com/cerberus8484/opensourcebackup/internal/health"
)

// Thresholds for agent online/idle/offline classification.
// Must match the identical thresholds in the web dashboard (Dashboard.tsx).
const (
	onlineThreshold = 2 * time.Minute
	idleThreshold   = 15 * time.Minute
	scrapeTimeout   = 10 * time.Second
)

// Stores is the set of read-only data sources the collector needs.
type Stores struct {
	Systems      catalog.SystemStore
	Jobs         catalog.JobStore
	Snapshots    catalog.SnapshotStore
	RestoreTests catalog.RestoreTestStore
	Repositories catalog.RepositoryStore
	Policies     catalog.PolicyStore
}

// Collector implements prometheus.Collector.
// It queries stores on every scrape — no caching, always current.
type Collector struct {
	stores Stores
	log    *slog.Logger

	// Metric descriptors
	jobsTotal            *prometheus.Desc
	jobsLast24hTotal     *prometheus.Desc
	restoreTestsTotal    *prometheus.Desc
	restoreVerifiedRatio *prometheus.Desc
	systemsTotal         *prometheus.Desc
	agentsOnline         *prometheus.Desc
	agentsIdle           *prometheus.Desc
	agentsOffline        *prometheus.Desc
	snapshotsTotal       *prometheus.Desc
	snapshotsVerified    *prometheus.Desc
	recoveryScore        *prometheus.Desc
	scrapeErrors         *prometheus.Desc
}

// New creates a Collector wired to the given stores.
func New(stores Stores, log *slog.Logger) *Collector {
	const ns = "opensourcebackup"

	return &Collector{
		stores: stores,
		log:    log,

		jobsTotal: prometheus.NewDesc(
			ns+"_jobs_total",
			"Total number of backup jobs by status.",
			[]string{"status"}, nil,
		),
		jobsLast24hTotal: prometheus.NewDesc(
			ns+"_jobs_last_24h_total",
			"Backup jobs completed in the last 24 hours by status.",
			[]string{"status"}, nil,
		),
		restoreTestsTotal: prometheus.NewDesc(
			ns+"_restore_tests_total",
			"Total number of restore tests by status.",
			[]string{"status"}, nil,
		),
		restoreVerifiedRatio: prometheus.NewDesc(
			ns+"_restore_verified_ratio",
			"Fraction of snapshots with at least one successful restore test (0.0–1.0). "+
				"Same calculation as the dashboard Restore Verified % metric.",
			nil, nil,
		),
		systemsTotal: prometheus.NewDesc(
			ns+"_systems_total",
			"Total number of registered systems.",
			nil, nil,
		),
		agentsOnline: prometheus.NewDesc(
			ns+"_agents_online_total",
			"Agents with last heartbeat <= 2 minutes ago.",
			nil, nil,
		),
		agentsIdle: prometheus.NewDesc(
			ns+"_agents_idle_total",
			"Agents with last heartbeat between 2 and 15 minutes ago.",
			nil, nil,
		),
		agentsOffline: prometheus.NewDesc(
			ns+"_agents_offline_total",
			"Agents with last heartbeat > 15 minutes ago or never seen.",
			nil, nil,
		),
		snapshotsTotal: prometheus.NewDesc(
			ns+"_snapshots_total",
			"Total number of backup snapshots in the catalog.",
			nil, nil,
		),
		snapshotsVerified: prometheus.NewDesc(
			ns+"_snapshots_verified_total",
			"Snapshots with at least one successful restore test.",
			nil, nil,
		),
		recoveryScore: prometheus.NewDesc(
			ns+"_recovery_score",
			"Overall recovery readiness score (0–100). "+
				"Same formula as the dashboard Recovery Score widget. "+
				"Deductions: -30 no restore tests, -15 partial restore coverage, "+
				"-20 failed jobs in last 24h, -10 overall failure rate >20%%.",
			nil, nil,
		),
		scrapeErrors: prometheus.NewDesc(
			ns+"_scrape_errors_total",
			"Number of errors encountered during the last metrics scrape.",
			nil, nil,
		),
	}
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.jobsTotal
	ch <- c.jobsLast24hTotal
	ch <- c.restoreTestsTotal
	ch <- c.restoreVerifiedRatio
	ch <- c.systemsTotal
	ch <- c.agentsOnline
	ch <- c.agentsIdle
	ch <- c.agentsOffline
	ch <- c.snapshotsTotal
	ch <- c.snapshotsVerified
	ch <- c.recoveryScore
	ch <- c.scrapeErrors
}

// Collect implements prometheus.Collector.
// Queries all stores, computes metrics, and sends them to ch.
// A single scrape timeout covers all store queries.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), scrapeTimeout)
	defer cancel()

	errors := 0

	// ── Systems + Agent status ────────────────────────────────────────────────
	systems, err := c.stores.Systems.List(ctx)
	if err != nil {
		c.log.Warn("metrics: list systems failed", "error", err)
		errors++
		systems = nil
	}
	now := time.Now()
	online, idle, offline := 0, 0, 0
	for _, sys := range systems {
		switch agentStatus(sys.LastSeen, now) {
		case "online":
			online++
		case "idle":
			idle++
		default:
			offline++
		}
	}
	ch <- prometheus.MustNewConstMetric(c.systemsTotal, prometheus.GaugeValue, float64(len(systems)))
	ch <- prometheus.MustNewConstMetric(c.agentsOnline, prometheus.GaugeValue, float64(online))
	ch <- prometheus.MustNewConstMetric(c.agentsIdle, prometheus.GaugeValue, float64(idle))
	ch <- prometheus.MustNewConstMetric(c.agentsOffline, prometheus.GaugeValue, float64(offline))

	// ── Jobs ──────────────────────────────────────────────────────────────────
	jobs, err := c.stores.Jobs.List(ctx)
	if err != nil {
		c.log.Warn("metrics: list jobs failed", "error", err)
		errors++
		jobs = nil
	}
	jobCounts := countByStatus(jobs, func(j catalog.BackupJob) string { return j.Status })
	cutoff24h := now.Add(-24 * time.Hour)
	job24hCounts := make(map[string]int)
	for _, j := range jobs {
		if j.CreatedAt.After(cutoff24h) {
			job24hCounts[j.Status]++
		}
	}
	for _, status := range []string{"success", "failed", "running", "pending"} {
		ch <- prometheus.MustNewConstMetric(c.jobsTotal,
			prometheus.GaugeValue, float64(jobCounts[status]), status)
		ch <- prometheus.MustNewConstMetric(c.jobsLast24hTotal,
			prometheus.GaugeValue, float64(job24hCounts[status]), status)
	}

	// ── Snapshots ─────────────────────────────────────────────────────────────
	snapshots, err := c.stores.Snapshots.List(ctx)
	if err != nil {
		c.log.Warn("metrics: list snapshots failed", "error", err)
		errors++
		snapshots = nil
	}

	// ── Restore Tests ─────────────────────────────────────────────────────────
	rts, err := c.stores.RestoreTests.List(ctx)
	if err != nil {
		c.log.Warn("metrics: list restore tests failed", "error", err)
		errors++
		rts = nil
	}
	rtCounts := countByStatus(rts, func(rt catalog.RestoreTest) string { return rt.Status })
	for _, status := range []string{"success", "failed", "running", "pending"} {
		ch <- prometheus.MustNewConstMetric(c.restoreTestsTotal,
			prometheus.GaugeValue, float64(rtCounts[status]), status)
	}

	// Verified = snapshots with at least one successful restore test
	verifiedCount := 0
	for _, snap := range snapshots {
		for _, rt := range rts {
			if rt.SnapshotID == snap.ID && rt.Status == "success" {
				verifiedCount++
				break
			}
		}
	}
	ch <- prometheus.MustNewConstMetric(c.snapshotsTotal, prometheus.GaugeValue, float64(len(snapshots)))
	ch <- prometheus.MustNewConstMetric(c.snapshotsVerified, prometheus.GaugeValue, float64(verifiedCount))

	verifiedRatio := 0.0
	if len(snapshots) > 0 {
		verifiedRatio = float64(verifiedCount) / float64(len(snapshots))
	}
	ch <- prometheus.MustNewConstMetric(c.restoreVerifiedRatio, prometheus.GaugeValue, verifiedRatio)

	// ── Recovery Score — uses canonical health package ────────────────────────
	// Single source of truth: health.Calculate is also used by GET /v1/health/score.
	failedLast24h := job24hCounts["failed"]
	var lastSuccessAt *time.Time
	for _, j := range jobs {
		if j.Status == "success" && j.Type == catalog.JobTypeBackup {
			if j.FinishedAt != nil && (lastSuccessAt == nil || j.FinishedAt.After(*lastSuccessAt)) {
				lastSuccessAt = j.FinishedAt
			}
		}
	}
	// Load restore tests for freshness check
	var lastRestoreTestAt *time.Time
	allRTs, rtErr := c.stores.RestoreTests.List(ctx)
	if rtErr != nil {
		allRTs = nil
	}
	for _, rt := range allRTs {
		if rt.Status == "success" && rt.FinishedAt != nil {
			if lastRestoreTestAt == nil || rt.FinishedAt.After(*lastRestoreTestAt) {
				lastRestoreTestAt = rt.FinishedAt
			}
		}
	}
	// Re-count verified with dedup
	verifiedDedup := 0
	snapIDMap := make(map[string]bool, len(snapshots))
	for _, sn := range snapshots {
		snapIDMap[sn.ID.String()] = true
	}
	seen := make(map[string]bool)
	for _, rt := range allRTs {
		if rt.Status == "success" && snapIDMap[rt.SnapshotID.String()] && !seen[rt.SnapshotID.String()] {
			verifiedDedup++
			seen[rt.SnapshotID.String()] = true
		}
	}
	// Load repos for security checks
	repos, _ := c.stores.Repositories.List(ctx)
	unprotected, unencrypted := 0, 0
	for _, repo := range repos {
		if repo.SecurityPosture.Immutability != catalog.SecurityProvenanceVerified {
			unprotected++
		}
		if repo.SecurityPosture.Encryption != catalog.SecurityProvenanceVerified {
			unencrypted++
		}
	}
	// Load policies for retention check
	policies, _ := c.stores.Policies.List(ctx)
	policiesWithRetention := 0
	for _, p := range policies {
		if p.RetentionPlan.HasRules() {
			policiesWithRetention++
		}
	}
	healthInput := apphealth.Input{
		TotalSystems:          len(systems),
		OnlineAgents:          online,
		IdleAgents:            idle,
		OfflineAgents:         offline,
		TotalJobs:             jobCounts["success"] + jobCounts["failed"],
		SuccessJobs:           jobCounts["success"],
		FailedJobs:            jobCounts["failed"],
		FailedLast24h:         failedLast24h,
		LastSuccessAt:         lastSuccessAt,
		TotalSnapshots:        len(snapshots),
		VerifiedSnapshots:     verifiedDedup,
		LastRestoreTestAt:     lastRestoreTestAt,
		TotalRepos:            len(repos),
		UnprotectedRepos:      unprotected,
		UnencryptedRepos:      unencrypted,
		PoliciesWithRetention: policiesWithRetention,
		Now:                   now,
	}
	scoreResult := apphealth.Calculate(healthInput)
	ch <- prometheus.MustNewConstMetric(c.recoveryScore, prometheus.GaugeValue, float64(scoreResult.Score))

	// ── Scrape errors ─────────────────────────────────────────────────────────
	ch <- prometheus.MustNewConstMetric(c.scrapeErrors, prometheus.GaugeValue, float64(errors))
}

// ── Pure functions (testable without stores) ──────────────────────────────────

// agentStatus classifies an agent as online/idle/offline based on LastSeen.
// Thresholds are identical to Dashboard.tsx: online<=2min, idle<=15min.
func agentStatus(lastSeen *time.Time, now time.Time) string {
	if lastSeen == nil {
		return "offline"
	}
	age := now.Sub(*lastSeen)
	switch {
	case age <= onlineThreshold:
		return "online"
	case age <= idleThreshold:
		return "idle"
	default:
		return "offline"
	}
}

// calcRecoveryScore computes the 0–100 recovery score.
// MUST stay in sync with the TypeScript implementation in Dashboard.tsx.
//
// Maximum total deduction with the current formula:
//
//	-30 (no restore tests) + -20 (failed 24h) + -10 (failure rate) = -60 → minimum score = 40
//
// The score floor at 0 is a safety guard for future formula extensions.
// If the formula is extended with additional deductions, the floor prevents negative values.
func calcRecoveryScore(totalSnaps, verifiedSnaps, failedLast24h int, failureRatePct float64) int {
	score := 100

	if totalSnaps > 0 && verifiedSnaps == 0 {
		score -= 30 // no restore tests at all
	} else if totalSnaps > 0 && verifiedSnaps < totalSnaps {
		score -= 15 // partial restore coverage
	}

	if failedLast24h > 0 {
		score -= 20 // recent failures
	}

	if failureRatePct > 20 {
		score -= 10 // high overall failure rate
	}

	if score < 0 {
		return 0
	}
	return score
}

// countByStatus groups a slice by a status string extracted by statusFn.
func countByStatus[T any](items []T, statusFn func(T) string) map[string]int {
	counts := make(map[string]int)
	for _, item := range items {
		counts[statusFn(item)]++
	}
	return counts
}
