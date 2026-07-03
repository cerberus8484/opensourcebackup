// Package migrate applies embedded SQL migrations at control-plane startup.
//
// It is deliberately small and dependency-free (just pgx). It reads the current
// version from the golang-migrate-compatible schema_migrations table, then applies
// every pending *.up.sql migration in ascending order, each in its own
// transaction. On any failure it stops and returns an error so the control plane
// refuses to start with a half-applied schema — a bad schema must never serve
// traffic. Because each migration runs in a transaction, a failure rolls back
// cleanly and leaves the recorded version unchanged (no "dirty" limbo).
package migrate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

type migration struct {
	version int
	name    string
	sql     string
}

// Run connects to dsn, ensures the schema is at the latest embedded version, and
// returns an error if any pending migration fails. Safe to call on every startup:
// when the schema is already current it is a no-op.
func Run(ctx context.Context, dsn string, fsys fs.FS, log *slog.Logger) error {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("migrate: parse dsn: %w", err)
	}
	// Simple protocol so a migration file with multiple statements runs as one Exec.
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("migrate: connect: %w", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (version bigint NOT NULL PRIMARY KEY, dirty boolean NOT NULL)`,
	); err != nil {
		return fmt.Errorf("migrate: ensure schema_migrations: %w", err)
	}

	version, dirty, err := currentVersion(ctx, conn)
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("migrate: schema is dirty at version %d — a previous migration left it inconsistent; repair manually before starting", version)
	}

	migs, err := loadMigrations(fsys)
	if err != nil {
		return err
	}

	applied := 0
	for _, m := range migs {
		if m.version <= version {
			continue
		}
		log.Info("applying migration", "version", m.version, "name", m.name)
		if err := applyOne(ctx, conn, m); err != nil {
			return err
		}
		applied++
	}

	if applied == 0 {
		log.Info("database schema up to date", "version", version)
	} else {
		log.Info("database migrations applied", "count", applied, "from_version", version)
	}
	return nil
}

func applyOne(ctx context.Context, conn *pgx.Conn, m migration) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrate: begin %d: %w", m.version, err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after a successful commit is a no-op

	if _, err := tx.Exec(ctx, m.sql); err != nil {
		return fmt.Errorf("migrate: migration %d (%s) failed: %w", m.version, m.name, err)
	}
	// Record the new version atomically with the change. Values are ints we control,
	// so string interpolation is safe (simple protocol has no bind parameters).
	if _, err := tx.Exec(ctx,
		fmt.Sprintf("DELETE FROM schema_migrations; INSERT INTO schema_migrations (version, dirty) VALUES (%d, false)", m.version),
	); err != nil {
		return fmt.Errorf("migrate: record version %d: %w", m.version, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migrate: commit %d: %w", m.version, err)
	}
	return nil
}

func currentVersion(ctx context.Context, conn *pgx.Conn) (int, bool, error) {
	var v int
	var dirty bool
	err := conn.QueryRow(ctx,
		`SELECT version, dirty FROM schema_migrations ORDER BY version DESC LIMIT 1`,
	).Scan(&v, &dirty)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("migrate: read current version: %w", err)
	}
	return v, dirty, nil
}

func loadMigrations(fsys fs.FS) ([]migration, error) {
	names, err := fs.Glob(fsys, "migrations/*.up.sql")
	if err != nil {
		return nil, fmt.Errorf("migrate: glob migrations: %w", err)
	}
	migs := make([]migration, 0, len(names))
	for _, name := range names {
		base := path.Base(name)
		idx := strings.IndexByte(base, '_')
		if idx <= 0 {
			return nil, fmt.Errorf("migrate: unexpected migration filename %q", base)
		}
		v, err := strconv.Atoi(base[:idx])
		if err != nil {
			return nil, fmt.Errorf("migrate: bad version in %q: %w", base, err)
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("migrate: read %s: %w", name, err)
		}
		migs = append(migs, migration{version: v, name: base, sql: string(data)})
	}
	sort.Slice(migs, func(i, j int) bool { return migs[i].version < migs[j].version })
	return migs, nil
}
