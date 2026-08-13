//go:build integration

package migrate_test

import (
	"context"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"testing"

	root "github.com/cerberus8484/opensourcebackup"
	"github.com/cerberus8484/opensourcebackup/internal/migrate"
	"github.com/jackc/pgx/v5"
)

// migrationsUpTo presents the embedded chain as it existed before a version.
// It lets the upgrade path be exercised without maintaining a duplicate copy.
type migrationsUpTo struct {
	fs.FS
	maxVersion int
}

func (f migrationsUpTo) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(f.FS, name)
	if err != nil { return nil, err }
	filtered := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".up.sql") { filtered = append(filtered, entry); continue }
		prefix, _, _ := strings.Cut(name, "_")
		version, err := strconv.Atoi(prefix)
		if err != nil || version <= f.maxVersion { filtered = append(filtered, entry) }
	}
	return filtered, nil
}

func TestRepositorySecurityMigration_UpgradesExistingSchema(t *testing.T) {
	if os.Getenv("OSB_DISPOSABLE_MIGRATION_TEST") != "1" {
		t.Skip("requires an explicitly disposable DATABASE_URL")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" { t.Skip("DATABASE_URL not set") }
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := migrate.Run(ctx, dsn, migrationsUpTo{FS: root.MigrationsFS, maxVersion: 25}, log); err != nil {
		t.Fatalf("migrate through 25: %v", err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil { t.Fatalf("connect: %v", err) }
	defer conn.Close(ctx)
	var id string
	if err := conn.QueryRow(ctx, `INSERT INTO repositories (type, location, encryption_mode, immutable_mode) VALUES ('restic', 's3:upgrade-preserve', 'aes256', 'object_lock') RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("seed existing repository: %v", err)
	}
	if err := migrate.Run(ctx, dsn, root.MigrationsFS, log); err != nil { t.Fatalf("migrate 26: %v", err) }
	var backend, management, encryption, immutability string
	if err := conn.QueryRow(ctx, `SELECT backend_type, management_mode, encryption_posture, immutability_posture FROM repositories WHERE id = $1`, id).Scan(&backend, &management, &encryption, &immutability); err != nil {
		t.Fatalf("read upgraded repository: %v", err)
	}
	if backend != "S3" || management != "LEGACY" || encryption != "DECLARED" || immutability != "DECLARED" {
		t.Fatalf("upgrade values = backend=%s management=%s encryption=%s immutability=%s", backend, management, encryption, immutability)
	}
}
