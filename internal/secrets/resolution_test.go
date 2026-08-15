package secrets

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/cerberus8484/opensourcebackup/internal/catalog"
)

func TestAuthorizedReferenceResolver(t *testing.T) {
	ctx := context.Background()
	systemID := uuid.New()
	otherSystemID := uuid.New()
	jobID := uuid.New()
	policyID := uuid.New()
	repositoryID := uuid.New()

	tests := []struct {
		name          string
		job           *catalog.BackupJob
		policy        *catalog.BackupPolicy
		repository    *catalog.BackupRepository
		references    []catalog.RepositoryCredentialReference
		referenceErr  error
		systemID      uuid.UUID
		wantPurpose   catalog.CredentialPurpose
		wantReference string
		wantErr       error
	}{
		{
			name:       "backup resolves only active backup write reference",
			job:        &catalog.BackupJob{ID: jobID, SystemID: systemID, PolicyID: policyID, Type: catalog.JobTypeBackup},
			policy:     &catalog.BackupPolicy{ID: policyID, RepositoryID: &repositoryID},
			repository: &catalog.BackupRepository{ID: repositoryID},
			references: []catalog.RepositoryCredentialReference{
				{ID: uuid.New(), RepositoryID: repositoryID, Purpose: catalog.CredentialPurposeBackupWrite, ProviderType: "test", Reference: "backup-write", Status: catalog.CredentialReferenceActive},
				{ID: uuid.New(), RepositoryID: repositoryID, Purpose: catalog.CredentialPurposeRestoreRead, ProviderType: "test", Reference: "restore-read", Status: catalog.CredentialReferenceActive},
				{ID: uuid.New(), RepositoryID: repositoryID, Purpose: catalog.CredentialPurposeRetention, ProviderType: "test", Reference: "retention", Status: catalog.CredentialReferenceActive},
				{ID: uuid.New(), RepositoryID: repositoryID, Purpose: catalog.CredentialPurposeRepositoryAdmin, ProviderType: "test", Reference: "admin", Status: catalog.CredentialReferenceActive},
			},
			systemID:      systemID,
			wantPurpose:   catalog.CredentialPurposeBackupWrite,
			wantReference: "backup-write",
		},
		{
			name:        "verify resolves restore read reference",
			job:         &catalog.BackupJob{ID: jobID, SystemID: systemID, PolicyID: policyID, Type: "verify"},
			policy:      &catalog.BackupPolicy{ID: policyID, RepositoryID: &repositoryID},
			repository:  &catalog.BackupRepository{ID: repositoryID},
			references:  []catalog.RepositoryCredentialReference{{ID: uuid.New(), RepositoryID: repositoryID, Purpose: catalog.CredentialPurposeRestoreRead, ProviderType: "test", Reference: "restore-read", Status: catalog.CredentialReferenceActive}},
			systemID:    systemID,
			wantPurpose: catalog.CredentialPurposeRestoreRead,
		},
		{
			name:        "retention has its own canonical purpose",
			job:         &catalog.BackupJob{ID: jobID, SystemID: systemID, PolicyID: policyID, Type: catalog.JobTypeRetention},
			policy:      &catalog.BackupPolicy{ID: policyID, RepositoryID: &repositoryID},
			repository:  &catalog.BackupRepository{ID: repositoryID},
			references:  []catalog.RepositoryCredentialReference{{ID: uuid.New(), RepositoryID: repositoryID, Purpose: catalog.CredentialPurposeRetention, ProviderType: "test", Reference: "retention", Status: catalog.CredentialReferenceActive}},
			systemID:    systemID,
			wantPurpose: catalog.CredentialPurposeRetention,
		},
		{
			name:       "another system is denied without revealing reference",
			job:        &catalog.BackupJob{ID: jobID, SystemID: systemID, PolicyID: policyID, Type: catalog.JobTypeBackup},
			policy:     &catalog.BackupPolicy{ID: policyID, RepositoryID: &repositoryID},
			repository: &catalog.BackupRepository{ID: repositoryID},
			references: []catalog.RepositoryCredentialReference{{ID: uuid.New(), RepositoryID: repositoryID, Purpose: catalog.CredentialPurposeBackupWrite, ProviderType: "test", Reference: "must-not-leak", Status: catalog.CredentialReferenceActive}},
			systemID:   otherSystemID,
			wantErr:    ErrUnauthorizedJob,
		},
		{
			name:     "missing job is controlled failure",
			systemID: systemID,
			wantErr:  ErrJobLookup,
		},
		{
			name:     "missing policy is controlled failure",
			job:      &catalog.BackupJob{ID: jobID, SystemID: systemID, PolicyID: policyID, Type: catalog.JobTypeBackup},
			systemID: systemID,
			wantErr:  ErrPolicyLookup,
		},
		{
			name:     "unsupported job type is denied",
			job:      &catalog.BackupJob{ID: jobID, SystemID: systemID, PolicyID: policyID, Type: "unsupported"},
			systemID: systemID,
			wantErr:  ErrUnsupportedJobPurpose,
		},
		{
			name:     "job without repository is rejected",
			job:      &catalog.BackupJob{ID: jobID, SystemID: systemID, PolicyID: policyID, Type: catalog.JobTypeBackup},
			policy:   &catalog.BackupPolicy{ID: policyID},
			systemID: systemID,
			wantErr:  ErrMissingRepository,
		},
		{
			name:     "missing repository is controlled failure",
			job:      &catalog.BackupJob{ID: jobID, SystemID: systemID, PolicyID: policyID, Type: catalog.JobTypeBackup},
			policy:   &catalog.BackupPolicy{ID: policyID, RepositoryID: &repositoryID},
			systemID: systemID,
			wantErr:  ErrRepositoryLookup,
		},
		{
			name:         "credential reference lookup is controlled failure",
			job:          &catalog.BackupJob{ID: jobID, SystemID: systemID, PolicyID: policyID, Type: catalog.JobTypeBackup},
			policy:       &catalog.BackupPolicy{ID: policyID, RepositoryID: &repositoryID},
			repository:   &catalog.BackupRepository{ID: repositoryID},
			references:   nil,
			referenceErr: errors.New("reference store unavailable"),
			systemID:     systemID,
			wantErr:      ErrCredentialReferenceLookup,
		},
		{
			name:       "missing purpose reference is controlled failure",
			job:        &catalog.BackupJob{ID: jobID, SystemID: systemID, PolicyID: policyID, Type: catalog.JobTypeBackup},
			policy:     &catalog.BackupPolicy{ID: policyID, RepositoryID: &repositoryID},
			repository: &catalog.BackupRepository{ID: repositoryID},
			systemID:   systemID,
			wantErr:    ErrMissingCredentialReference,
		},
		{
			name:       "disabled reference is denied",
			job:        &catalog.BackupJob{ID: jobID, SystemID: systemID, PolicyID: policyID, Type: catalog.JobTypeBackup},
			policy:     &catalog.BackupPolicy{ID: policyID, RepositoryID: &repositoryID},
			repository: &catalog.BackupRepository{ID: repositoryID},
			references: []catalog.RepositoryCredentialReference{{ID: uuid.New(), RepositoryID: repositoryID, Purpose: catalog.CredentialPurposeBackupWrite, ProviderType: "test", Reference: "disabled", Status: catalog.CredentialReferenceDisabled}},
			systemID:   systemID,
			wantErr:    ErrInactiveCredentialReference,
		},
		{
			name:       "ambiguous reference state fails closed",
			job:        &catalog.BackupJob{ID: jobID, SystemID: systemID, PolicyID: policyID, Type: catalog.JobTypeBackup},
			policy:     &catalog.BackupPolicy{ID: policyID, RepositoryID: &repositoryID},
			repository: &catalog.BackupRepository{ID: repositoryID},
			references: []catalog.RepositoryCredentialReference{
				{ID: uuid.New(), RepositoryID: repositoryID, Purpose: catalog.CredentialPurposeBackupWrite, ProviderType: "test", Reference: "first", Status: catalog.CredentialReferenceActive},
				{ID: uuid.New(), RepositoryID: repositoryID, Purpose: catalog.CredentialPurposeBackupWrite, ProviderType: "test", Reference: "second", Status: catalog.CredentialReferenceActive},
			},
			systemID: systemID,
			wantErr:  ErrAmbiguousCredentialReference,
		},
		{
			name:       "revoked reference is denied",
			job:        &catalog.BackupJob{ID: jobID, SystemID: systemID, PolicyID: policyID, Type: catalog.JobTypeBackup},
			policy:     &catalog.BackupPolicy{ID: policyID, RepositoryID: &repositoryID},
			repository: &catalog.BackupRepository{ID: repositoryID},
			references: []catalog.RepositoryCredentialReference{{ID: uuid.New(), RepositoryID: repositoryID, Purpose: catalog.CredentialPurposeBackupWrite, ProviderType: "test", Reference: "revoked", Status: catalog.CredentialReferenceRevoked}},
			systemID:   systemID,
			wantErr:    ErrInactiveCredentialReference,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewAuthorizedReferenceResolver(
				stubJobs{job: tt.job},
				stubPolicies{policy: tt.policy},
				stubRepositories{repository: tt.repository},
				stubReferences{references: tt.references, err: tt.referenceErr},
			)

			got, err := resolver.ResolveForAgent(ctx, tt.systemID, jobID)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ResolveForAgent() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveForAgent() unexpected error: %v", err)
			}
			if got.Purpose != tt.wantPurpose {
				t.Errorf("resolved purpose = %q, want %q", got.Purpose, tt.wantPurpose)
			}
			if tt.wantReference != "" && got.Reference != tt.wantReference {
				t.Errorf("resolved reference = %q, want %q", got.Reference, tt.wantReference)
			}
		})
	}
}

func TestLegacyFallbackPermitted(t *testing.T) {
	repositoryID := uuid.New()
	for _, tt := range []struct {
		name       string
		references []catalog.RepositoryCredentialReference
		want       bool
	}{
		{name: "no managed backup reference", want: true},
		{name: "active managed backup reference", references: []catalog.RepositoryCredentialReference{{RepositoryID: repositoryID, Purpose: catalog.CredentialPurposeBackupWrite, Status: catalog.CredentialReferenceActive}}, want: false},
		{name: "disabled managed backup reference", references: []catalog.RepositoryCredentialReference{{RepositoryID: repositoryID, Purpose: catalog.CredentialPurposeBackupWrite, Status: catalog.CredentialReferenceDisabled}}, want: false},
		{name: "revoked managed backup reference", references: []catalog.RepositoryCredentialReference{{RepositoryID: repositoryID, Purpose: catalog.CredentialPurposeBackupWrite, Status: catalog.CredentialReferenceRevoked}}, want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := LegacyFallbackPermitted(tt.references, catalog.CredentialPurposeBackupWrite); got != tt.want {
				t.Errorf("LegacyFallbackPermitted() = %t, want %t", got, tt.want)
			}
		})
	}
}

type stubJobs struct{ job *catalog.BackupJob }

func (s stubJobs) GetByID(_ context.Context, _ uuid.UUID) (*catalog.BackupJob, error) {
	if s.job == nil {
		return nil, catalog.ErrNotFound
	}
	return s.job, nil
}

type stubPolicies struct{ policy *catalog.BackupPolicy }

func (s stubPolicies) GetByID(_ context.Context, _ uuid.UUID) (*catalog.BackupPolicy, error) {
	if s.policy == nil {
		return nil, catalog.ErrNotFound
	}
	return s.policy, nil
}

type stubRepositories struct{ repository *catalog.BackupRepository }

func (s stubRepositories) GetByID(_ context.Context, _ uuid.UUID) (*catalog.BackupRepository, error) {
	if s.repository == nil {
		return nil, catalog.ErrNotFound
	}
	return s.repository, nil
}

type stubReferences struct {
	references []catalog.RepositoryCredentialReference
	err        error
}

func (s stubReferences) ListByRepositoryID(_ context.Context, _ uuid.UUID) ([]catalog.RepositoryCredentialReference, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.references, nil
}
