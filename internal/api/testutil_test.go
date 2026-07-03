package api_test

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/cerberus8484/opensourcebackup/internal/auth"
	"github.com/cerberus8484/opensourcebackup/internal/catalog"
)

// ensure time import is used
var _ = time.Now

// withAdmin returns a copy of r with a synthetic admin session in its context.
// Use this for any request that hits a requireRoleFn-protected route in unit tests.
func withAdmin(r *http.Request) *http.Request {
	return r.WithContext(auth.WithSession(r.Context(), &auth.Session{
		UserID:    uuid.New(),
		UserEmail: "test-admin@osb",
		Role:      auth.RoleAdmin,
		CreatedAt: time.Now(),
	}))
}

// withOperator returns a copy of r with a synthetic operator session.
func withOperator(r *http.Request) *http.Request {
	return r.WithContext(auth.WithSession(r.Context(), &auth.Session{
		UserID:    uuid.New(),
		UserEmail: "test-operator@osb",
		Role:      auth.RoleOperator,
		CreatedAt: time.Now(),
	}))
}

// stubEnrollmentTokenStore — in-memory for tests.
type stubEnrollmentTokenStore struct{}

func newStubEnrollmentTokenStore() *stubEnrollmentTokenStore { return &stubEnrollmentTokenStore{} }

func (s *stubEnrollmentTokenStore) Create(_ context.Context, systemID uuid.UUID, _ string, expiresAt time.Time) (*auth.EnrollmentToken, error) {
	return &auth.EnrollmentToken{ID: uuid.New(), SystemID: systemID, ExpiresAt: expiresAt, CreatedAt: time.Now()}, nil
}
func (s *stubEnrollmentTokenStore) Consume(_ context.Context, _ string) (*auth.EnrollmentToken, error) {
	return nil, auth.ErrInvalidToken
}
func (s *stubEnrollmentTokenStore) Revoke(_ context.Context, _ uuid.UUID) error { return nil }

// stubAgentTokenStore — in-memory for tests.
type stubAgentTokenStore struct {
	tokens map[string]uuid.UUID // hash → systemID
}

func newStubAgentTokenStore() *stubAgentTokenStore {
	return &stubAgentTokenStore{tokens: make(map[string]uuid.UUID)}
}

func (s *stubAgentTokenStore) Create(_ context.Context, systemID uuid.UUID, hash string) (*auth.AgentToken, error) {
	s.tokens[hash] = systemID
	return &auth.AgentToken{ID: uuid.New(), SystemID: systemID, CreatedAt: time.Now()}, nil
}
func (s *stubAgentTokenStore) ValidateAndTouch(_ context.Context, hash string) (uuid.UUID, error) {
	if id, ok := s.tokens[hash]; ok {
		return id, nil
	}
	return uuid.Nil, auth.ErrInvalidToken
}
func (s *stubAgentTokenStore) Revoke(_ context.Context, _ uuid.UUID) error { return nil }

// stubSystemStore is an in-memory SystemStore for unit tests.
type stubSystemStore struct {
	systems map[uuid.UUID]*catalog.System
}

func newStubSystemStore() *stubSystemStore {
	return &stubSystemStore{systems: make(map[uuid.UUID]*catalog.System)}
}

func (s *stubSystemStore) Create(_ context.Context, sys *catalog.System) error {
	sys.ID = uuid.New()
	cp := *sys
	s.systems[sys.ID] = &cp
	return nil
}

func (s *stubSystemStore) GetByID(_ context.Context, id uuid.UUID) (*catalog.System, error) {
	sys, ok := s.systems[id]
	if !ok {
		return nil, catalog.ErrNotFound
	}
	cp := *sys
	return &cp, nil
}

func (s *stubSystemStore) List(_ context.Context) ([]catalog.System, error) {
	out := make([]catalog.System, 0, len(s.systems))
	for _, sys := range s.systems {
		out = append(out, *sys)
	}
	return out, nil
}

func (s *stubSystemStore) Update(_ context.Context, sys *catalog.System) error {
	if _, ok := s.systems[sys.ID]; !ok {
		return catalog.ErrNotFound
	}
	cp := *sys
	s.systems[sys.ID] = &cp
	return nil
}

func (s *stubSystemStore) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := s.systems[id]; !ok {
		return catalog.ErrNotFound
	}
	delete(s.systems, id)
	return nil
}

func (s *stubSystemStore) UpdateLastSeen(_ context.Context, id uuid.UUID, _ time.Time) error {
	if _, ok := s.systems[id]; !ok {
		return catalog.ErrNotFound
	}
	return nil
}

// stubRepositoryStore is an in-memory RepositoryStore for unit tests.
type stubRepositoryStore struct {
	repos map[uuid.UUID]*catalog.BackupRepository
}

func newStubRepositoryStore() *stubRepositoryStore {
	return &stubRepositoryStore{repos: make(map[uuid.UUID]*catalog.BackupRepository)}
}

func (s *stubRepositoryStore) Create(_ context.Context, r *catalog.BackupRepository) error {
	r.ID = uuid.New()
	cp := *r
	s.repos[r.ID] = &cp
	return nil
}

func (s *stubRepositoryStore) GetByID(_ context.Context, id uuid.UUID) (*catalog.BackupRepository, error) {
	r, ok := s.repos[id]
	if !ok {
		return nil, catalog.ErrNotFound
	}
	cp := *r
	return &cp, nil
}

func (s *stubRepositoryStore) List(_ context.Context) ([]catalog.BackupRepository, error) {
	out := make([]catalog.BackupRepository, 0, len(s.repos))
	for _, r := range s.repos {
		out = append(out, *r)
	}
	return out, nil
}

func (s *stubRepositoryStore) Update(_ context.Context, r *catalog.BackupRepository) error {
	if _, ok := s.repos[r.ID]; !ok {
		return catalog.ErrNotFound
	}
	cp := *r
	s.repos[r.ID] = &cp
	return nil
}

func (s *stubRepositoryStore) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := s.repos[id]; !ok {
		return catalog.ErrNotFound
	}
	delete(s.repos, id)
	return nil
}

// stubJobStore is an in-memory JobStore for unit tests.
type stubJobStore struct {
	jobs map[uuid.UUID]*catalog.BackupJob
}

func newStubJobStore() *stubJobStore {
	return &stubJobStore{jobs: make(map[uuid.UUID]*catalog.BackupJob)}
}

func (s *stubJobStore) Create(_ context.Context, j *catalog.BackupJob) error {
	j.ID = uuid.New()
	cp := *j
	s.jobs[j.ID] = &cp
	return nil
}

func (s *stubJobStore) GetByID(_ context.Context, id uuid.UUID) (*catalog.BackupJob, error) {
	j, ok := s.jobs[id]
	if !ok {
		return nil, catalog.ErrNotFound
	}
	cp := *j
	return &cp, nil
}

func (s *stubJobStore) List(_ context.Context) ([]catalog.BackupJob, error) {
	out := make([]catalog.BackupJob, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, *j)
	}
	return out, nil
}

func (s *stubJobStore) ListPendingBySystemID(_ context.Context, systemID uuid.UUID) ([]catalog.BackupJob, error) {
	var out []catalog.BackupJob
	for _, j := range s.jobs {
		if j.SystemID == systemID && j.Status == "pending" {
			out = append(out, *j)
		}
	}
	return out, nil
}

func (s *stubJobStore) LatestByPolicyID(_ context.Context, _ uuid.UUID) (*catalog.BackupJob, error) {
	return nil, catalog.ErrNotFound
}

func (s *stubJobStore) ListPendingRetentionBySystemID(_ context.Context, _ uuid.UUID) ([]catalog.BackupJob, error) {
	return nil, nil
}

func (s *stubJobStore) ListBySystemID(_ context.Context, systemID uuid.UUID) ([]catalog.BackupJob, error) {
	var out []catalog.BackupJob
	for _, j := range s.jobs {
		if j.SystemID == systemID {
			out = append(out, *j)
		}
	}
	return out, nil
}

func (s *stubJobStore) Update(_ context.Context, j *catalog.BackupJob) error {
	if _, ok := s.jobs[j.ID]; !ok {
		return catalog.ErrNotFound
	}
	cp := *j
	s.jobs[j.ID] = &cp
	return nil
}

func (s *stubJobStore) UpdateProgress(_ context.Context, id uuid.UUID, p catalog.JobProgress) error {
	j, ok := s.jobs[id]
	if !ok {
		return catalog.ErrNotFound
	}
	j.ProgressPhase = p.Phase
	j.ProgressPercent = p.Percent
	j.ProgressBytesDone = p.BytesDone
	j.ProgressBytesTotal = p.BytesTotal
	j.ProgressFilesDone = p.FilesDone
	j.ProgressFilesTotal = p.FilesTotal
	j.ProgressThroughputBps = p.ThroughputBps
	return nil
}

func (s *stubJobStore) FinalizeProgress(_ context.Context, id uuid.UUID) error {
	j, ok := s.jobs[id]
	if !ok {
		return catalog.ErrNotFound
	}
	j.ProgressPercent = 100
	return nil
}

func (s *stubJobStore) RequestCancel(_ context.Context, id uuid.UUID, reason string) error {
	j, ok := s.jobs[id]
	if !ok {
		return catalog.ErrNotFound
	}
	now := time.Now()
	j.CancelRequestedAt = &now
	j.CancelReason = reason
	return nil
}

func (s *stubJobStore) FailStaleJobs(_ context.Context, _, _, _ time.Duration) (int, error) {
	return 0, nil
}
// transition mirrors the real guarded store: only from the allowed source state,
// otherwise ErrIllegalTransition (or ErrNotFound). Keeps handler tests faithful.
func (s *stubJobStore) transition(id uuid.UUID, from string, mutate func(*catalog.BackupJob)) error {
	j, ok := s.jobs[id]
	if !ok {
		return catalog.ErrNotFound
	}
	if j.Status != from {
		return catalog.ErrIllegalTransition
	}
	mutate(j)
	return nil
}

func (s *stubJobStore) StartJob(_ context.Context, id uuid.UUID) error {
	return s.transition(id, "pending", func(j *catalog.BackupJob) {
		now := time.Now()
		j.Status = "running"
		j.StartedAt = &now
	})
}
func (s *stubJobStore) CompleteJob(_ context.Context, id uuid.UUID, bytes int64) error {
	return s.transition(id, "running", func(j *catalog.BackupJob) {
		now := time.Now()
		j.Status = "success"
		j.FinishedAt = &now
		j.BytesUploaded = &bytes
	})
}
func (s *stubJobStore) FailJob(_ context.Context, id uuid.UUID, errSummary string) error {
	return s.transition(id, "running", func(j *catalog.BackupJob) {
		now := time.Now()
		j.Status = "failed"
		j.FinishedAt = &now
		j.ErrorSummary = &errSummary
	})
}
func (s *stubJobStore) CancelJob(_ context.Context, id uuid.UUID, reason string) error {
	return s.transition(id, "running", func(j *catalog.BackupJob) {
		now := time.Now()
		j.Status = "cancelled"
		j.FinishedAt = &now
		if reason != "" {
			r := reason
			j.ErrorSummary = &r
		}
	})
}

func (s *stubJobStore) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := s.jobs[id]; !ok {
		return catalog.ErrNotFound
	}
	delete(s.jobs, id)
	return nil
}

// stubSnapshotStore is an in-memory SnapshotStore for unit tests.
type stubSnapshotStore struct {
	snaps map[uuid.UUID]*catalog.Snapshot
}

func newStubSnapshotStore() *stubSnapshotStore {
	return &stubSnapshotStore{snaps: make(map[uuid.UUID]*catalog.Snapshot)}
}

func (s *stubSnapshotStore) Create(_ context.Context, snap *catalog.Snapshot) error {
	snap.ID = uuid.New()
	cp := *snap
	s.snaps[snap.ID] = &cp
	return nil
}

func (s *stubSnapshotStore) GetByID(_ context.Context, id uuid.UUID) (*catalog.Snapshot, error) {
	snap, ok := s.snaps[id]
	if !ok {
		return nil, catalog.ErrNotFound
	}
	cp := *snap
	return &cp, nil
}

func (s *stubSnapshotStore) List(_ context.Context) ([]catalog.Snapshot, error) {
	out := make([]catalog.Snapshot, 0, len(s.snaps))
	for _, snap := range s.snaps {
		out = append(out, *snap)
	}
	return out, nil
}

func (s *stubSnapshotStore) ListBySystem(_ context.Context, _ uuid.UUID) ([]catalog.Snapshot, error) {
	return nil, nil
}

func (s *stubSnapshotStore) ListByJobID(_ context.Context, jobID uuid.UUID) ([]catalog.Snapshot, error) {
	var out []catalog.Snapshot
	for _, snap := range s.snaps {
		if snap.JobID == jobID {
			out = append(out, *snap)
		}
	}
	return out, nil
}

func (s *stubSnapshotStore) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := s.snaps[id]; !ok {
		return catalog.ErrNotFound
	}
	delete(s.snaps, id)
	return nil
}

// stubRestoreTestStore is an in-memory RestoreTestStore for unit tests.
type stubRestoreTestStore struct {
	tests map[uuid.UUID]*catalog.RestoreTest
}

func newStubRestoreTestStore() *stubRestoreTestStore {
	return &stubRestoreTestStore{tests: make(map[uuid.UUID]*catalog.RestoreTest)}
}

func (s *stubRestoreTestStore) Create(_ context.Context, rt *catalog.RestoreTest) error {
	rt.ID = uuid.New()
	cp := *rt
	s.tests[rt.ID] = &cp
	return nil
}
func (s *stubRestoreTestStore) GetByID(_ context.Context, id uuid.UUID) (*catalog.RestoreTest, error) {
	rt, ok := s.tests[id]
	if !ok {
		return nil, catalog.ErrNotFound
	}
	cp := *rt
	return &cp, nil
}
func (s *stubRestoreTestStore) List(_ context.Context) ([]catalog.RestoreTest, error) {
	out := make([]catalog.RestoreTest, 0, len(s.tests))
	for _, rt := range s.tests {
		out = append(out, *rt)
	}
	return out, nil
}
func (s *stubRestoreTestStore) ListBySnapshotID(_ context.Context, id uuid.UUID) ([]catalog.RestoreTest, error) {
	var out []catalog.RestoreTest
	for _, rt := range s.tests {
		if rt.SnapshotID == id {
			out = append(out, *rt)
		}
	}
	return out, nil
}
func (s *stubRestoreTestStore) ListBySystemID(_ context.Context, id uuid.UUID) ([]catalog.RestoreTest, error) {
	var out []catalog.RestoreTest
	for _, rt := range s.tests {
		if rt.SystemID == id {
			out = append(out, *rt)
		}
	}
	return out, nil
}
func (s *stubRestoreTestStore) Update(_ context.Context, rt *catalog.RestoreTest) error {
	if _, ok := s.tests[rt.ID]; !ok {
		return catalog.ErrNotFound
	}
	cp := *rt
	s.tests[rt.ID] = &cp
	return nil
}
func (s *stubRestoreTestStore) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := s.tests[id]; !ok {
		return catalog.ErrNotFound
	}
	delete(s.tests, id)
	return nil
}
func (s *stubRestoreTestStore) ClaimNextPending(_ context.Context, _ uuid.UUID) (*catalog.RestoreTest, error) {
	return nil, catalog.ErrNotFound
}

func (s *stubRestoreTestStore) HasSuccessfulTest(_ context.Context, snapshotID uuid.UUID) (bool, error) {
	for _, rt := range s.tests {
		if rt.SnapshotID == snapshotID && rt.Status == "success" {
			return true, nil
		}
	}
	return false, nil
}

// stubPolicyStore is an in-memory PolicyStore for unit tests.
type stubPolicyStore struct {
	policies map[uuid.UUID]*catalog.BackupPolicy
}

func newStubPolicyStore() *stubPolicyStore {
	return &stubPolicyStore{policies: make(map[uuid.UUID]*catalog.BackupPolicy)}
}

func (s *stubPolicyStore) Create(_ context.Context, p *catalog.BackupPolicy) error {
	p.ID = uuid.New()
	cp := *p
	s.policies[p.ID] = &cp
	return nil
}

func (s *stubPolicyStore) GetByID(_ context.Context, id uuid.UUID) (*catalog.BackupPolicy, error) {
	p, ok := s.policies[id]
	if !ok {
		return nil, catalog.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (s *stubPolicyStore) ListWithRetention(_ context.Context) ([]catalog.BackupPolicy, error) {
	return nil, nil
}

func (s *stubPolicyStore) List(_ context.Context) ([]catalog.BackupPolicy, error) {
	out := make([]catalog.BackupPolicy, 0, len(s.policies))
	for _, p := range s.policies {
		out = append(out, *p)
	}
	return out, nil
}

func (s *stubPolicyStore) Update(_ context.Context, p *catalog.BackupPolicy) error {
	if _, ok := s.policies[p.ID]; !ok {
		return catalog.ErrNotFound
	}
	cp := *p
	s.policies[p.ID] = &cp
	return nil
}

func (s *stubPolicyStore) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := s.policies[id]; !ok {
		return catalog.ErrNotFound
	}
	delete(s.policies, id)
	return nil
}
