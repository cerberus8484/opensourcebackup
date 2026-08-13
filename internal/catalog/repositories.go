package catalog

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const repoSelect = `
	SELECT id, type, location, backend_type, management_mode, encryption_mode, object_lock_enabled, immutable_mode,
	       encryption_posture, object_lock_posture, immutability_posture, retention_policy_id, created_at
	FROM repositories`

// RepositoryStore defines data access for the repositories table.
type RepositoryStore interface {
	Create(ctx context.Context, r *BackupRepository) error
	GetByID(ctx context.Context, id uuid.UUID) (*BackupRepository, error)
	List(ctx context.Context) ([]BackupRepository, error)
	Update(ctx context.Context, r *BackupRepository) error
	Delete(ctx context.Context, id uuid.UUID) error
}

type pgRepositoryStore struct {
	db *DB
}

// NewRepositoryStore returns a PostgreSQL-backed RepositoryStore.
func NewRepositoryStore(db *DB) RepositoryStore {
	return &pgRepositoryStore{db: db}
}

func (s *pgRepositoryStore) Create(ctx context.Context, r *BackupRepository) error {
	mode := r.ImmutableMode
	if mode == "" {
		if r.ObjectLockEnabled {
			mode = ImmutableObjectLock
		} else {
			mode = ImmutableNone
		}
	}
	r.ImmutableMode = mode
	r.BackendType = ClassifyRepositoryBackend(r.Type, r.Location)
	if r.ManagementMode == "" {
		r.ManagementMode = RepositoryManagementLegacy
	}
	r.SecurityPosture = DeclaredSecurityPosture(r)
	row := s.db.pool.QueryRow(ctx, `
		INSERT INTO repositories
		  (type, location, backend_type, management_mode, encryption_mode, object_lock_enabled, immutable_mode,
		   encryption_posture, object_lock_posture, immutability_posture, retention_policy_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING id, created_at`,
		r.Type, r.Location, string(r.BackendType), string(r.ManagementMode), r.EncryptionMode, r.ObjectLockEnabled, string(mode),
		string(r.SecurityPosture.Encryption), string(r.SecurityPosture.ObjectLock), string(r.SecurityPosture.Immutability),
		uuidPtrToRaw(r.RetentionPolicyID),
	)
	var rawID pgtype.UUID
	if err := row.Scan(&rawID, &r.CreatedAt); err != nil {
		return err
	}
	r.ID = uuid.UUID(rawID.Bytes)
	return nil
}

func (s *pgRepositoryStore) GetByID(ctx context.Context, id uuid.UUID) (*BackupRepository, error) {
	row := s.db.pool.QueryRow(ctx,
		repoSelect+` WHERE id = $1`,
		pgtype.UUID{Bytes: id, Valid: true},
	)
	r, err := scanRepository(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func (s *pgRepositoryStore) List(ctx context.Context) ([]BackupRepository, error) {
	rows, err := s.db.pool.Query(ctx, repoSelect+` ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var repos []BackupRepository
	for rows.Next() {
		r, err := scanRepository(rows)
		if err != nil {
			return nil, err
		}
		repos = append(repos, *r)
	}
	return repos, rows.Err()
}

func (s *pgRepositoryStore) Update(ctx context.Context, r *BackupRepository) error {
	mode := r.ImmutableMode
	if mode == "" {
		mode = ImmutableNone
	}
	r.ImmutableMode = mode
	r.BackendType = ClassifyRepositoryBackend(r.Type, r.Location)
	if r.ManagementMode == "" {
		r.ManagementMode = RepositoryManagementLegacy
	}
	r.SecurityPosture = DeclaredSecurityPosture(r)
	tag, err := s.db.pool.Exec(ctx, `
		UPDATE repositories
		SET type=$1, location=$2, backend_type=$3, management_mode=$4, encryption_mode=$5, object_lock_enabled=$6,
		    immutable_mode=$7, encryption_posture=$8, object_lock_posture=$9, immutability_posture=$10, retention_policy_id=$11
		WHERE id=$12`,
		r.Type, r.Location, string(r.BackendType), string(r.ManagementMode), r.EncryptionMode, r.ObjectLockEnabled,
		string(mode), string(r.SecurityPosture.Encryption), string(r.SecurityPosture.ObjectLock), string(r.SecurityPosture.Immutability), uuidPtrToRaw(r.RetentionPolicyID),
		pgtype.UUID{Bytes: r.ID, Valid: true},
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *pgRepositoryStore) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.db.pool.Exec(ctx, `DELETE FROM repositories WHERE id = $1`,
		pgtype.UUID{Bytes: id, Valid: true},
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanRepository(row rowScanner) (*BackupRepository, error) {
	var (
		r                                                                                            BackupRepository
		rawID                                                                                        pgtype.UUID
		rawRetID                                                                                     pgtype.UUID
		backendType, managementMode, mode, encryptionPosture, objectLockPosture, immutabilityPosture string
	)
	if err := row.Scan(
		&rawID, &r.Type, &r.Location, &backendType, &managementMode, &r.EncryptionMode,
		&r.ObjectLockEnabled, &mode, &encryptionPosture, &objectLockPosture, &immutabilityPosture, &rawRetID, &r.CreatedAt,
	); err != nil {
		return nil, err
	}
	r.ID = uuid.UUID(rawID.Bytes)
	r.BackendType = RepositoryBackendType(backendType)
	r.ManagementMode = RepositoryManagementMode(managementMode)
	r.ImmutableMode = ImmutableMode(mode)
	r.SecurityPosture = RepositorySecurityPosture{Encryption: SecurityProvenance(encryptionPosture), ObjectLock: SecurityProvenance(objectLockPosture), Immutability: SecurityProvenance(immutabilityPosture)}
	if rawRetID.Valid {
		id := uuid.UUID(rawRetID.Bytes)
		r.RetentionPolicyID = &id
	}
	return &r, nil
}

func uuidPtrToRaw(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{Valid: false}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}
