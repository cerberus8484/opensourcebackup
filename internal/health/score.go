// Package health computes the Backup Health Score — a 0–100 measure of
// overall backup and recoverability posture.
//
// Design principles:
//   - Single canonical implementation: Dashboard + Prometheus + API all use this package.
//   - Explainable: every deduction has a human-readable reason.
//   - Honest: no fabricated factors; only real catalog data.
//   - Stable: changing the formula is a documented decision, not a quiet tweak.
//
// Formula version: 2.0 (B_SC)
// Changes from v1: agent connectivity, repository security, retention, restore age.
package health

import (
	"time"
)

// ScoreVersion identifies the formula version — increment when deductions change.
const ScoreVersion = "2.0"

// Thresholds — all in one place so tests and docs stay in sync.
const (
	OnlineThreshold   = 2 * time.Minute     // agent considered online
	IdleThreshold     = 15 * time.Minute    // agent considered idle (not offline)
	RestoreTestMaxAge = 30 * 24 * time.Hour // most recent restore test must be < 30 days
	BackupMaxAge24h   = 24 * time.Hour      // backup required within last 24h
)

// Deduction is a single scored penalty with a human-readable explanation.
type Deduction struct {
	Points int    `json:"points"`
	Code   string `json:"code"`   // machine-readable, e.g. "no_restore_tests"
	Reason string `json:"reason"` // shown to operator
}

// ScoreResult is the full output of the score calculation.
type ScoreResult struct {
	Score      int         `json:"score"`      // 0–100
	Label      string      `json:"label"`      // Excellent / Good / Fair / At Risk
	Color      string      `json:"color"`      // hex or CSS variable name
	Version    string      `json:"version"`    // formula version
	Deductions []Deduction `json:"deductions"` // what reduced the score (empty = perfect)
	Factors    []string    `json:"factors"`    // positive factors confirmed (for display)
}

// Input holds all the data the score calculator needs.
// Collected by the API handler from the catalog stores — no DB access in this package.
type Input struct {
	// Systems
	TotalSystems  int
	OnlineAgents  int // last_seen <= 2 min
	IdleAgents    int // last_seen <= 15 min
	OfflineAgents int // last_seen > 15 min or null

	// Jobs
	TotalJobs     int
	SuccessJobs   int
	FailedJobs    int
	FailedLast24h int
	LastSuccessAt *time.Time // most recent successful backup

	// Snapshots + Restore Tests
	TotalSnapshots    int
	VerifiedSnapshots int        // with successful restore test
	LastRestoreTestAt *time.Time // most recent successful restore test

	// Repositories
	TotalRepos int
	// UnprotectedRepos and UnencryptedRepos count repositories without a
	// VERIFIED protection/encryption posture. A declaration alone earns no
	// positive security credit.
	UnprotectedRepos int
	UnencryptedRepos int

	// Retention
	PoliciesWithRetention int // policies with at least one keep_* > 0

	Now time.Time
}

// Calculate computes the health score from the given input.
// This is a pure function — no side effects, fully testable.
func Calculate(in Input) ScoreResult {
	if in.Now.IsZero() {
		in.Now = time.Now()
	}

	score := 100
	var deductions []Deduction
	var factors []string

	add := func(pts int, code, reason string) {
		score -= pts
		deductions = append(deductions, Deduction{Points: pts, Code: code, Reason: reason})
	}
	good := func(f string) { factors = append(factors, f) }

	// ── Restore test coverage ─────────────────────────────────────────────────

	if in.TotalSnapshots > 0 {
		if in.VerifiedSnapshots == 0 {
			add(30, "no_restore_tests",
				"No snapshots have been restore-tested — recoverability is unproven")
		} else if in.VerifiedSnapshots < in.TotalSnapshots {
			add(15, "partial_restore_coverage",
				"Some snapshots have not been restore-tested")
		} else {
			good("All snapshots restore-tested")
		}
	}

	// ── Restore test freshness ────────────────────────────────────────────────

	if in.TotalSnapshots > 0 && in.VerifiedSnapshots > 0 {
		if in.LastRestoreTestAt == nil || in.Now.Sub(*in.LastRestoreTestAt) > RestoreTestMaxAge {
			add(10, "restore_test_stale",
				"Most recent restore test is older than 30 days")
		} else {
			good("Recent restore test within 30 days")
		}
	}

	// ── Backup freshness ──────────────────────────────────────────────────────

	if in.TotalSystems > 0 {
		if in.LastSuccessAt == nil {
			add(20, "no_successful_backup",
				"No successful backup has been recorded yet")
		} else if in.Now.Sub(*in.LastSuccessAt) > BackupMaxAge24h {
			add(15, "backup_stale_24h",
				"No successful backup in the last 24 hours")
		} else {
			good("Successful backup within last 24 hours")
		}
	}

	// ── Failed jobs (last 24h) ────────────────────────────────────────────────

	if in.FailedLast24h > 0 {
		msg := "1 backup job failed in the last 24 hours"
		if in.FailedLast24h > 1 {
			msg = formatCount(in.FailedLast24h, "backup job") + " failed in the last 24 hours"
		}
		add(20, "failed_jobs_24h", msg)
	} else if in.TotalJobs > 0 {
		good("No failed jobs in last 24 hours")
	}

	// ── Overall failure rate ──────────────────────────────────────────────────

	if in.TotalJobs > 0 {
		rate := float64(in.FailedJobs) / float64(in.TotalJobs) * 100
		if rate > 20 {
			add(10, "high_failure_rate",
				"Overall backup failure rate is above 20%")
		}
	}

	// ── Agent connectivity ────────────────────────────────────────────────────

	if in.TotalSystems > 0 {
		if in.OfflineAgents > 0 {
			add(10, "agents_offline",
				formatCount(in.OfflineAgents, "agent")+" offline (no heartbeat in 15+ minutes)")
		} else if in.IdleAgents > 0 {
			add(5, "agents_idle",
				formatCount(in.IdleAgents, "agent")+" idle (no heartbeat in 2–15 minutes)")
		} else {
			good("All agents online")
		}
	}

	// ── Repository security ───────────────────────────────────────────────────

	if in.TotalRepos > 0 {
		if in.UnprotectedRepos > 0 {
			add(10, "repos_not_immutable",
				formatCount(in.UnprotectedRepos, "repository")+" without verified write protection")
		} else {
			good("All repositories have write protection configured")
		}

		if in.UnencryptedRepos > 0 {
			add(5, "repos_not_encrypted",
				formatCount(in.UnencryptedRepos, "repository")+" without verified encryption")
		} else {
			good("All repositories have encryption configured")
		}
	}

	// ── Retention ────────────────────────────────────────────────────────────

	if in.TotalSystems > 0 && in.PoliciesWithRetention == 0 {
		add(5, "no_retention_policy",
			"No backup policies have retention rules configured — snapshots will grow indefinitely")
	} else if in.PoliciesWithRetention > 0 {
		good("Retention rules configured")
	}

	// ── Clamp ────────────────────────────────────────────────────────────────

	if score < 0 {
		score = 0
	}

	label, color := classify(score)
	return ScoreResult{
		Score:      score,
		Label:      label,
		Color:      color,
		Version:    ScoreVersion,
		Deductions: deductions,
		Factors:    factors,
	}
}

func classify(score int) (label, color string) {
	switch {
	case score >= 90:
		return "Excellent", "#22c55e"
	case score >= 75:
		return "Good", "#4ade80"
	case score >= 55:
		return "Fair", "#f59e0b"
	default:
		return "At Risk", "#ef4444"
	}
}

func formatCount(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return itoa(n) + " " + noun + "s"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
