// Package secrets defines server-side authorization boundaries for future
// repository secret resolution. It never stores or delivers secret material.
package secrets

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/cerberus8484/opensourcebackup/internal/catalog"
)

var (
	// ErrUnauthorizedJob intentionally does not disclose whether a job or a
	// credential reference exists for another system.
	ErrUnauthorizedJob              = errors.New("secret resolution unauthorized")
	ErrUnsupportedJobPurpose        = errors.New("secret resolution has unsupported job purpose")
	ErrJobLookup                    = errors.New("secret resolution job lookup failed")
	ErrPolicyLookup                 = errors.New("secret resolution policy lookup failed")
	ErrRepositoryLookup             = errors.New("secret resolution repository lookup failed")
	ErrMissingRepository            = errors.New("secret resolution policy has no repository")
	ErrCredentialReferenceLookup    = errors.New("secret resolution credential reference lookup failed")
	ErrMissingCredentialReference   = errors.New("secret resolution credential reference missing")
	ErrInactiveCredentialReference  = errors.New("secret resolution credential reference inactive")
	ErrAmbiguousCredentialReference = errors.New("secret resolution credential reference ambiguous")
)

// SecretProvider is the future external-secret boundary. This slice declares no
// provider implementation and does not invoke it. The returned bytes must only
// be held for the calling execution and must never be persisted or logged.
type SecretProvider interface {
	Resolve(ctx context.Context, ref catalog.RepositoryCredentialReference) ([]byte, error)
}

type jobLookup interface {
	GetByID(ctx context.Context, id uuid.UUID) (*catalog.BackupJob, error)
}

type policyLookup interface {
	GetByID(ctx context.Context, id uuid.UUID) (*catalog.BackupPolicy, error)
}

type repositoryLookup interface {
	GetByID(ctx context.Context, id uuid.UUID) (*catalog.BackupRepository, error)
}

type credentialReferenceLookup interface {
	ListByRepositoryID(ctx context.Context, repositoryID uuid.UUID) ([]catalog.RepositoryCredentialReference, error)
}

// AuthorizedReferenceResolver derives a credential reference from trusted
// system and job IDs. Callers cannot select a repository, purpose, provider or
// reference directly.
type AuthorizedReferenceResolver struct {
	jobs         jobLookup
	policies     policyLookup
	repositories repositoryLookup
	references   credentialReferenceLookup
}

func NewAuthorizedReferenceResolver(
	jobs jobLookup,
	policies policyLookup,
	repositories repositoryLookup,
	references credentialReferenceLookup,
) *AuthorizedReferenceResolver {
	return &AuthorizedReferenceResolver{
		jobs:         jobs,
		policies:     policies,
		repositories: repositories,
		references:   references,
	}
}

// ResolveForAgent returns only active metadata for the credential purpose that
// is implied by the job. It deliberately does not call SecretProvider.
func (r *AuthorizedReferenceResolver) ResolveForAgent(ctx context.Context, systemID, jobID uuid.UUID) (catalog.RepositoryCredentialReference, error) {
	job, err := r.jobs.GetByID(ctx, jobID)
	if err != nil {
		return catalog.RepositoryCredentialReference{}, fmt.Errorf("%w: %v", ErrJobLookup, err)
	}
	if job.SystemID != systemID {
		return catalog.RepositoryCredentialReference{}, ErrUnauthorizedJob
	}

	purpose, err := purposeForJobType(job.Type)
	if err != nil {
		return catalog.RepositoryCredentialReference{}, err
	}

	policy, err := r.policies.GetByID(ctx, job.PolicyID)
	if err != nil {
		return catalog.RepositoryCredentialReference{}, fmt.Errorf("%w: %v", ErrPolicyLookup, err)
	}
	if policy.RepositoryID == nil {
		return catalog.RepositoryCredentialReference{}, ErrMissingRepository
	}

	if _, err := r.repositories.GetByID(ctx, *policy.RepositoryID); err != nil {
		return catalog.RepositoryCredentialReference{}, fmt.Errorf("%w: %v", ErrRepositoryLookup, err)
	}

	references, err := r.references.ListByRepositoryID(ctx, *policy.RepositoryID)
	if err != nil {
		return catalog.RepositoryCredentialReference{}, fmt.Errorf("%w: %v", ErrCredentialReferenceLookup, err)
	}
	var selected *catalog.RepositoryCredentialReference
	for i := range references {
		ref := &references[i]
		if ref.Purpose != purpose {
			continue
		}
		if selected != nil {
			return catalog.RepositoryCredentialReference{}, ErrAmbiguousCredentialReference
		}
		selected = ref
	}
	if selected == nil {
		return catalog.RepositoryCredentialReference{}, ErrMissingCredentialReference
	}
	if selected.Status != catalog.CredentialReferenceActive {
		return catalog.RepositoryCredentialReference{}, fmt.Errorf("%w: %s", ErrInactiveCredentialReference, selected.Status)
	}
	return *selected, nil
}

// LegacyFallbackPermitted documents the future compatibility rule: once a
// repository has a reference for a purpose, its state governs that purpose and
// the legacy RESTIC_PASSWORD path must not be used as a fallback.
func LegacyFallbackPermitted(references []catalog.RepositoryCredentialReference, purpose catalog.CredentialPurpose) bool {
	for _, ref := range references {
		if ref.Purpose == purpose {
			return false
		}
	}
	return true
}

func purposeForJobType(jobType string) (catalog.CredentialPurpose, error) {
	switch jobType {
	case catalog.JobTypeBackup:
		return catalog.CredentialPurposeBackupWrite, nil
	case "verify":
		// "verify" predates a canonical job-type constant. Keep the mapping
		// centralized until that technical debt is addressed.
		return catalog.CredentialPurposeRestoreRead, nil
	case catalog.JobTypeRetention:
		return catalog.CredentialPurposeRetention, nil
	default:
		return "", ErrUnsupportedJobPurpose
	}
}
