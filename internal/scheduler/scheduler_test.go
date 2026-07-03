package scheduler_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cerberus8484/opensourcebackup/internal/catalog"
	"github.com/cerberus8484/opensourcebackup/internal/scheduler"
)

var testLog = slog.New(slog.NewTextHandler(os.Stderr, nil))

// --- stubs ---

type stubPolicyStore struct{ policies []catalog.BackupPolicy }

func (s *stubPolicyStore) Create(_ context.Context, p *catalog.BackupPolicy) error {
	p.ID = uuid.New()
	s.policies = append(s.policies, *p)
	return nil
}
func (s *stubPolicyStore) GetByID(_ context.Context, id uuid.UUID) (*catalog.BackupPolicy, error) {
	for _, p := range s.policies {
		if p.ID == id {
			return &p, nil
		}
	}
	return nil, catalog.ErrNotFound
}
func (s *stubPolicyStore) List(_ context.Context) ([]catalog.BackupPolicy, error) {
	return s.policies, nil
}
func (s *stubPolicyStore) ListWithRetention(_ context.Context) ([]catalog.BackupPolicy, error) {
	return nil, nil
}
func (s *stubPolicyStore) Update(_ context.Context, p *catalog.BackupPolicy) error { return nil }
func (s *stubPolicyStore) Delete(_ context.Context, _ uuid.UUID) error             { return nil }

type stubJobStore struct{ created []*catalog.BackupJob }

func (s *stubJobStore) Create(_ context.Context, j *catalog.BackupJob) error {
	j.ID = uuid.New()
	j.CreatedAt = time.Now()
	cp := *j
	s.created = append(s.created, &cp)
	return nil
}
func (s *stubJobStore) GetByID(_ context.Context, _ uuid.UUID) (*catalog.BackupJob, error) {
	return nil, catalog.ErrNotFound
}
func (s *stubJobStore) List(_ context.Context) ([]catalog.BackupJob, error) { return nil, nil }
func (s *stubJobStore) ListBySystemID(_ context.Context, _ uuid.UUID) ([]catalog.BackupJob, error) {
	return nil, nil
}

func (s *stubJobStore) ListPendingBySystemID(_ context.Context, _ uuid.UUID) ([]catalog.BackupJob, error) {
	return nil, nil
}
func (s *stubJobStore) ListPendingRetentionBySystemID(_ context.Context, _ uuid.UUID) ([]catalog.BackupJob, error) {
	return nil, nil
}
func (s *stubJobStore) LatestByPolicyID(_ context.Context, policyID uuid.UUID) (*catalog.BackupJob, error) {
	for i := len(s.created) - 1; i >= 0; i-- {
		if s.created[i].PolicyID == policyID {
			return s.created[i], nil
		}
	}
	return nil, catalog.ErrNotFound
}
func (s *stubJobStore) Update(_ context.Context, _ *catalog.BackupJob) error { return nil }
func (s *stubJobStore) Delete(_ context.Context, _ uuid.UUID) error          { return nil }
func (s *stubJobStore) UpdateProgress(_ context.Context, _ uuid.UUID, _ catalog.JobProgress) error {
	return nil
}
func (s *stubJobStore) FinalizeProgress(_ context.Context, _ uuid.UUID) error { return nil }
func (s *stubJobStore) RequestCancel(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}
func (s *stubJobStore) FailStaleJobs(_ context.Context, _, _, _ time.Duration) (int, error) {
	return 0, nil
}
func (s *stubJobStore) StartJob(_ context.Context, _ uuid.UUID) error              { return nil }
func (s *stubJobStore) CompleteJob(_ context.Context, _ uuid.UUID, _ int64) error  { return nil }
func (s *stubJobStore) FailJob(_ context.Context, _ uuid.UUID, _ string) error     { return nil }
func (s *stubJobStore) CancelJob(_ context.Context, _ uuid.UUID, _ string) error   { return nil }

// --- tests ---

func TestScheduler_Start_SkipsPoliciesWithoutSchedule(t *testing.T) {
	sched := "* * * * *"
	policies := &stubPolicyStore{policies: []catalog.BackupPolicy{
		{ID: uuid.New(), Name: "with-schedule", Engine: "restic", Schedule: &sched},
		{ID: uuid.New(), Name: "no-schedule", Engine: "restic"},
	}}
	jobs := &stubJobStore{}

	s := scheduler.New(policies, jobs, testLog)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Start returns when ctx is canceled — no error expected
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
}

func TestScheduler_Start_ReturnsError_WhenPolicyLoadFails(t *testing.T) {
	s := scheduler.New(&failingPolicyStore{}, &stubJobStore{}, testLog)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if err := s.Start(ctx); err == nil {
		t.Fatal("expected error when policy store fails")
	}
}

type failingPolicyStore struct{ stubPolicyStore }

func (f *failingPolicyStore) List(_ context.Context) ([]catalog.BackupPolicy, error) {
	return nil, catalog.ErrNotFound
}
