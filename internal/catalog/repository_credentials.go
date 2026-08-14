package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// RepositoryCredentialReferenceStore persists reference metadata only. Secret
// material is intentionally outside the control-plane database in E2.1a.
type RepositoryCredentialReferenceStore interface {
	Create(ctx context.Context, reference *RepositoryCredentialReference) error
	ListByRepositoryID(ctx context.Context, repositoryID uuid.UUID) ([]RepositoryCredentialReference, error)
	Delete(ctx context.Context, repositoryID, id uuid.UUID) error
}

type pgRepositoryCredentialReferenceStore struct{ db *DB }

func NewRepositoryCredentialReferenceStore(db *DB) RepositoryCredentialReferenceStore {
	return &pgRepositoryCredentialReferenceStore{db: db}
}

func (s *pgRepositoryCredentialReferenceStore) Create(ctx context.Context, reference *RepositoryCredentialReference) error {
	if !reference.Purpose.Valid() || strings.TrimSpace(reference.ProviderType) == "" || strings.TrimSpace(reference.Reference) == "" {
		return fmt.Errorf("invalid credential reference metadata")
	}
	if reference.Status == "" {
		reference.Status = CredentialReferenceActive
	}
	if reference.Status != CredentialReferenceActive && reference.Status != CredentialReferenceDisabled && reference.Status != CredentialReferenceRevoked {
		return fmt.Errorf("invalid credential reference status")
	}

	row := s.db.pool.QueryRow(ctx, `
		INSERT INTO repository_credential_references (repository_id, purpose, provider_type, reference, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at, updated_at`,
		pgtype.UUID{Bytes: reference.RepositoryID, Valid: true}, string(reference.Purpose),
		strings.TrimSpace(reference.ProviderType), strings.TrimSpace(reference.Reference), string(reference.Status),
	)
	var rawID pgtype.UUID
	if err := row.Scan(&rawID, &reference.CreatedAt, &reference.UpdatedAt); err != nil {
		return err
	}
	reference.ID = uuid.UUID(rawID.Bytes)
	return nil
}

func (s *pgRepositoryCredentialReferenceStore) ListByRepositoryID(ctx context.Context, repositoryID uuid.UUID) ([]RepositoryCredentialReference, error) {
	rows, err := s.db.pool.Query(ctx, `
		SELECT id, repository_id, purpose, provider_type, reference, status, created_at, updated_at
		FROM repository_credential_references WHERE repository_id = $1 ORDER BY purpose`,
		pgtype.UUID{Bytes: repositoryID, Valid: true},
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	refs := make([]RepositoryCredentialReference, 0)
	for rows.Next() {
		ref, err := scanRepositoryCredentialReference(rows)
		if err != nil {
			return nil, err
		}
		refs = append(refs, *ref)
	}
	return refs, rows.Err()
}

func (s *pgRepositoryCredentialReferenceStore) Delete(ctx context.Context, repositoryID, id uuid.UUID) error {
	tag, err := s.db.pool.Exec(ctx, `DELETE FROM repository_credential_references WHERE id = $1 AND repository_id = $2`,
		pgtype.UUID{Bytes: id, Valid: true}, pgtype.UUID{Bytes: repositoryID, Valid: true})
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanRepositoryCredentialReference(row rowScanner) (*RepositoryCredentialReference, error) {
	var reference RepositoryCredentialReference
	var rawID, rawRepositoryID pgtype.UUID
	var purpose, status string
	if err := row.Scan(&rawID, &rawRepositoryID, &purpose, &reference.ProviderType, &reference.Reference, &status, &reference.CreatedAt, &reference.UpdatedAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	reference.ID = uuid.UUID(rawID.Bytes)
	reference.RepositoryID = uuid.UUID(rawRepositoryID.Bytes)
	reference.Purpose = CredentialPurpose(purpose)
	reference.Status = CredentialReferenceStatus(status)
	return &reference, nil
}
