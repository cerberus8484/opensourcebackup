package catalog

import "time"

// OperationState is the admin-set, persisted operation state of a system (E1.2).
// It is deliberately SEPARATE from the observed HealthState: a system can be
// e.g. MAINTENANCE + ONLINE, or ACTIVE + OFFLINE. Operation state governs whether
// new jobs may be dispatched; it says nothing about whether the agent is reachable.
type OperationState string

const (
	// OperationActive: normal operation — new automatic and manual jobs allowed.
	OperationActive OperationState = "active"
	// OperationMaintenance: no new jobs; already-running jobs keep running.
	OperationMaintenance OperationState = "maintenance"
	// OperationDraining: no new jobs; running jobs are allowed to finish (E1.2 does
	// NOT auto-cancel and does NOT auto-transition to maintenance — that is E1.2b).
	OperationDraining OperationState = "draining"
	// OperationSuspended: no new jobs; running jobs are NOT auto-terminated in E1.2
	// (a later governance/emergency slice may define explicit stop actions).
	OperationSuspended OperationState = "suspended"
)

// Valid reports whether o is one of the four known operation states.
func Valid(o OperationState) bool {
	switch o {
	case OperationActive, OperationMaintenance, OperationDraining, OperationSuspended:
		return true
	default:
		return false
	}
}

// AllowsNewJobs reports whether a system in this operation state may start new jobs.
// Only ACTIVE admits new work; every other state defers dispatch.
func (o OperationState) AllowsNewJobs() bool { return o == OperationActive }

// DispatchBlockReason returns the stable, machine-readable block reason for a
// non-ACTIVE state (empty string when the state allows new jobs). Cockpit, metrics
// and audit build on these exact strings.
func (o OperationState) DispatchBlockReason() string {
	switch o {
	case OperationMaintenance:
		return DispatchBlockedSystemMaintenance
	case OperationDraining:
		return DispatchBlockedSystemDraining
	case OperationSuspended:
		return DispatchBlockedSystemSuspended
	default:
		return ""
	}
}

// ── Health State (observed, derived — NOT stored, NOT admin-set) ───────────────

// HealthState is the observed liveness of a system's agent, derived purely from
// its last heartbeat (last_seen). It is a different dimension from OperationState
// and never influences it. Computed on read; never persisted.
type HealthState string

const (
	HealthOnline   HealthState = "online"   // heartbeat within HealthOnlineWithin
	HealthDegraded HealthState = "degraded" // heartbeat stale but within HealthDegradedWithin
	HealthOffline  HealthState = "offline"  // heartbeat older than HealthDegradedWithin
	HealthUnknown  HealthState = "unknown"  // never seen (no heartbeat yet)
)

// Health thresholds — mirror the values used by the health score so the two
// views of "online" stay consistent (see internal/health).
const (
	HealthOnlineWithin   = 2 * time.Minute
	HealthDegradedWithin = 15 * time.Minute
)

// DeriveHealthState maps a last-seen timestamp to a HealthState. A nil lastSeen
// (agent never checked in) is UNKNOWN — deliberately not OFFLINE, since "never
// seen" and "was seen, now gone" are different signals.
func DeriveHealthState(lastSeen *time.Time, now time.Time) HealthState {
	if lastSeen == nil {
		return HealthUnknown
	}
	age := now.Sub(*lastSeen)
	switch {
	case age <= HealthOnlineWithin:
		return HealthOnline
	case age <= HealthDegradedWithin:
		return HealthDegraded
	default:
		return HealthOffline
	}
}
