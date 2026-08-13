//go:build integration

package catalog_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"

	root "github.com/cerberus8484/opensourcebackup"
	"github.com/cerberus8484/opensourcebackup/internal/catalog"
	"github.com/cerberus8484/opensourcebackup/internal/migrate"
)

var (
	migrateOnce sync.Once
	migrateErr  error
)

func newTestDB(t *testing.T) *catalog.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	migrateOnce.Do(func() {
		migrateErr = migrate.Run(
			context.Background(), dsn, root.MigrationsFS,
			slog.New(slog.NewTextHandler(io.Discard, nil)),
		)
	})
	if migrateErr != nil {
		t.Fatalf("migrate integration database: %v", migrateErr)
	}
	db, err := catalog.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func truncateTables(t *testing.T, db *catalog.DB) {
	t.Helper()
	_, err := db.Pool().Exec(context.Background(),
		"TRUNCATE snapshots, backup_jobs, backup_policies, repositories, systems RESTART IDENTITY CASCADE",
	)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}
