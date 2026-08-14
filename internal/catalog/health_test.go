package catalog_test

import (
	"testing"
	"time"

	"github.com/cerberus8484/opensourcebackup/internal/catalog"
)

// TestDeriveHealthState proves the health dimension is derived purely from the
// last heartbeat — it takes ONLY last_seen and knows nothing about operation state.
func TestDeriveHealthState(t *testing.T) {
	now := time.Now()
	ago := func(d time.Duration) *time.Time { ts := now.Add(-d); return &ts }

	tests := []struct {
		name     string
		lastSeen *time.Time
		want     catalog.HealthState
	}{
		{"never seen → unknown", nil, catalog.HealthUnknown},
		{"10s ago → online", ago(10 * time.Second), catalog.HealthOnline},
		{"exactly 2min → online", ago(2 * time.Minute), catalog.HealthOnline},
		{"5min ago → degraded", ago(5 * time.Minute), catalog.HealthDegraded},
		{"exactly 15min → degraded", ago(15 * time.Minute), catalog.HealthDegraded},
		{"20min ago → offline", ago(20 * time.Minute), catalog.HealthOffline},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := catalog.DeriveHealthState(tt.lastSeen, now); got != tt.want {
				t.Errorf("DeriveHealthState = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestOperationStateHelpers proves the operation-state semantics and that they are
// independent of health (only ACTIVE admits new work; every other state carries a
// stable block reason).
func TestOperationStateHelpers(t *testing.T) {
	if !catalog.OperationActive.AllowsNewJobs() {
		t.Error("ACTIVE must allow new jobs")
	}
	if catalog.OperationActive.DispatchBlockReason() != "" {
		t.Error("ACTIVE must have no block reason")
	}
	blocking := map[catalog.OperationState]string{
		catalog.OperationMaintenance: catalog.DispatchBlockedSystemMaintenance,
		catalog.OperationDraining:    catalog.DispatchBlockedSystemDraining,
		catalog.OperationSuspended:   catalog.DispatchBlockedSystemSuspended,
	}
	for st, wantReason := range blocking {
		if st.AllowsNewJobs() {
			t.Errorf("%s must not allow new jobs", st)
		}
		if got := st.DispatchBlockReason(); got != wantReason {
			t.Errorf("%s block reason = %q, want %q", st, got, wantReason)
		}
	}
	if !catalog.Valid(catalog.OperationActive) || catalog.Valid(catalog.OperationState("bogus")) {
		t.Error("Valid() mismatch for known/unknown states")
	}
}
