package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// SystemStore defines data access for the systems table.
type SystemStore interface {
	Create(ctx context.Context, s *System) error
	GetByID(ctx context.Context, id uuid.UUID) (*System, error)
	List(ctx context.Context) ([]System, error)
	Update(ctx context.Context, s *System) error
	Delete(ctx context.Context, id uuid.UUID) error
	// UpdateLastSeen stamps last_seen = seenAt for the given system.
	// Called on every agent heartbeat — intentionally lightweight (single-column UPDATE).
	//
	// Naming: the DB column is "last_seen" (not "last_seen_at") — this is intentional.
	// The column was created in migration 000001 before this convention was discussed.
	// Renaming would require a migration + model update + JSON API change.
	// Consistency across DB / Go model (LastSeen) / JSON (LastSeen) / React (LastSeen) is maintained.
	// If renamed in future: migration 000014 + update all four layers together.
	UpdateLastSeen(ctx context.Context, id uuid.UUID, seenAt time.Time) error
	// SetOperationState changes the system's operation state (E1.2) and returns the
	// previous state, atomically (SELECT … FOR UPDATE + UPDATE in one transaction).
	// Returns ErrNotFound if the system does not exist. Does NOT touch last_seen or
	// any job — operation state and health/jobs are independent dimensions.
	SetOperationState(ctx context.Context, id uuid.UUID, newState string) (oldState string, err error)
}

type pgSystemStore struct {
	db *DB
}

// NewSystemStore returns a PostgreSQL-backed SystemStore.
func NewSystemStore(db *DB) SystemStore {
	return &pgSystemStore{db: db}
}

func (r *pgSystemStore) Create(ctx context.Context, s *System) error {
	tagsBytes, err := marshalJSONB(s.Tags)
	if err != nil {
		return err
	}
	row := r.db.pool.QueryRow(ctx, `
		INSERT INTO systems (hostname, os, agent_version, last_seen, tags, risk_class)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at`,
		s.Hostname, s.OS, s.AgentVersion, s.LastSeen, tagsBytes, s.RiskClass,
	)
	var rawID pgtype.UUID
	if err := row.Scan(&rawID, &s.CreatedAt); err != nil {
		return err
	}
	s.ID = uuid.UUID(rawID.Bytes)
	// New systems default to ACTIVE (DB default) — reflect that in the returned model.
	if s.OperationState == "" {
		s.OperationState = string(OperationActive)
	}
	return nil
}

func (r *pgSystemStore) GetByID(ctx context.Context, id uuid.UUID) (*System, error) {
	row := r.db.pool.QueryRow(ctx, `
		SELECT id, hostname, os, agent_version, last_seen, tags, risk_class,
		       operation_state, operation_state_changed_at, created_at
		FROM systems WHERE id = $1`,
		pgtype.UUID{Bytes: id, Valid: true},
	)
	s, err := scanSystem(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return s, err
}

func (r *pgSystemStore) List(ctx context.Context) ([]System, error) {
	rows, err := r.db.pool.Query(ctx, `
		SELECT id, hostname, os, agent_version, last_seen, tags, risk_class,
		       operation_state, operation_state_changed_at, created_at
		FROM systems ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var systems []System
	for rows.Next() {
		s, err := scanSystem(rows)
		if err != nil {
			return nil, err
		}
		systems = append(systems, *s)
	}
	return systems, rows.Err()
}

func (r *pgSystemStore) Update(ctx context.Context, s *System) error {
	tagsBytes, err := marshalJSONB(s.Tags)
	if err != nil {
		return err
	}
	tag, err := r.db.pool.Exec(ctx, `
		UPDATE systems
		SET hostname=$1, os=$2, agent_version=$3, last_seen=$4, tags=$5, risk_class=$6
		WHERE id=$7`,
		s.Hostname, s.OS, s.AgentVersion, s.LastSeen, tagsBytes, s.RiskClass,
		pgtype.UUID{Bytes: s.ID, Valid: true},
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *pgSystemStore) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.db.pool.Exec(ctx, `DELETE FROM systems WHERE id = $1`,
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

func (r *pgSystemStore) UpdateLastSeen(ctx context.Context, id uuid.UUID, seenAt time.Time) error {
	tag, err := r.db.pool.Exec(ctx,
		`UPDATE systems SET last_seen = $1 WHERE id = $2`,
		seenAt, pgtype.UUID{Bytes: id, Valid: true},
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetOperationState changes operation_state and returns the previous value.
// The read-then-write runs in one transaction with FOR UPDATE, so concurrent
// changes serialize and the returned oldState is always consistent with the write
// (important for a correct old→new audit record). It touches only the systems row.
func (r *pgSystemStore) SetOperationState(ctx context.Context, id uuid.UUID, newState string) (string, error) {
	tx, err := r.db.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("set operation state: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after a successful commit is a no-op

	var oldState string
	err = tx.QueryRow(ctx,
		`SELECT operation_state FROM systems WHERE id=$1 FOR UPDATE`,
		pgtype.UUID{Bytes: id, Valid: true},
	).Scan(&oldState)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("set operation state: load: %w", err)
	}

	if _, err = tx.Exec(ctx,
		`UPDATE systems SET operation_state=$2, operation_state_changed_at=NOW() WHERE id=$1`,
		pgtype.UUID{Bytes: id, Valid: true}, newState,
	); err != nil {
		return "", fmt.Errorf("set operation state: update: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("set operation state: commit: %w", err)
	}
	return oldState, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSystem(row rowScanner) (*System, error) {
	var (
		s       System
		rawID   pgtype.UUID
		tagsRaw []byte
	)
	if err := row.Scan(
		&rawID, &s.Hostname, &s.OS, &s.AgentVersion,
		&s.LastSeen, &tagsRaw, &s.RiskClass,
		&s.OperationState, &s.OperationStateChangedAt, &s.CreatedAt,
	); err != nil {
		return nil, err
	}
	s.ID = uuid.UUID(rawID.Bytes)
	if len(tagsRaw) > 0 {
		if err := json.Unmarshal(tagsRaw, &s.Tags); err != nil {
			return nil, err
		}
	}
	return &s, nil
}

func marshalJSONB(v any) ([]byte, error) {
	if v == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(v)
}
