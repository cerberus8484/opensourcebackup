//go:build integration

package auth_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	root "github.com/cerberus8484/opensourcebackup"
	"github.com/cerberus8484/opensourcebackup/internal/auth"
	"github.com/cerberus8484/opensourcebackup/internal/catalog"
	"github.com/cerberus8484/opensourcebackup/internal/migrate"
)

var (
	migrateOnce sync.Once
	migrateErr  error
)

func openTestDB(t *testing.T) *catalog.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
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

func createFixtureSystem(t *testing.T, db *catalog.DB) uuid.UUID {
	t.Helper()
	s := &catalog.System{Hostname: "auth-test-host", RiskClass: "standard"}
	if err := catalog.NewSystemStore(db).Create(context.Background(), s); err != nil {
		t.Fatalf("fixture system: %v", err)
	}
	return s.ID
}

func TestEnrollmentTokenStore_CreateAndConsume(t *testing.T) {
	db := openTestDB(t)
	systemID := createFixtureSystem(t, db)
	store := auth.NewEnrollmentTokenStore(db)

	raw, _ := auth.GenerateToken()
	hash := auth.HashToken(raw)
	expiresAt := time.Now().Add(30 * time.Minute)

	if _, err := store.Create(context.Background(), systemID, hash, expiresAt); err != nil {
		t.Fatalf("Create: %v", err)
	}

	et, err := store.Consume(context.Background(), hash)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if et.SystemID != systemID {
		t.Error("SystemID mismatch")
	}
	if et.UsedAt == nil {
		t.Error("expected UsedAt to be set after Consume")
	}
}

func TestEnrollmentTokenStore_Consume_RejectsAlreadyUsed(t *testing.T) {
	db := openTestDB(t)
	systemID := createFixtureSystem(t, db)
	store := auth.NewEnrollmentTokenStore(db)

	raw, _ := auth.GenerateToken()
	hash := auth.HashToken(raw)
	store.Create(context.Background(), systemID, hash, time.Now().Add(time.Hour))
	store.Consume(context.Background(), hash) // first use

	_, err := store.Consume(context.Background(), hash)
	if err != auth.ErrTokenAlreadyUsed {
		t.Errorf("want ErrTokenAlreadyUsed, got %v", err)
	}
}

func TestEnrollmentTokenStore_Consume_RejectsExpired(t *testing.T) {
	db := openTestDB(t)
	systemID := createFixtureSystem(t, db)
	store := auth.NewEnrollmentTokenStore(db)

	raw, _ := auth.GenerateToken()
	hash := auth.HashToken(raw)
	store.Create(context.Background(), systemID, hash, time.Now().Add(-time.Minute)) // already expired

	_, err := store.Consume(context.Background(), hash)
	if err != auth.ErrInvalidToken {
		t.Errorf("want ErrInvalidToken for expired token, got %v", err)
	}
}

func TestEnrollmentTokenStore_Consume_RejectsUnknown(t *testing.T) {
	db := openTestDB(t)
	_ = createFixtureSystem(t, db)
	store := auth.NewEnrollmentTokenStore(db)

	_, err := store.Consume(context.Background(), "nonexistent-hash")
	if err != auth.ErrInvalidToken {
		t.Errorf("want ErrInvalidToken for unknown token, got %v", err)
	}
}

func TestAgentTokenStore_CreateAndValidate(t *testing.T) {
	db := openTestDB(t)
	systemID := createFixtureSystem(t, db)
	store := auth.NewAgentTokenStore(db)

	raw, _ := auth.GenerateToken()
	hash := auth.HashToken(raw)
	if _, err := store.Create(context.Background(), systemID, hash); err != nil {
		t.Fatalf("Create: %v", err)
	}

	gotID, err := store.ValidateAndTouch(context.Background(), hash)
	if err != nil {
		t.Fatalf("ValidateAndTouch: %v", err)
	}
	if gotID != systemID {
		t.Errorf("SystemID: want %s, got %s", systemID, gotID)
	}
}

func TestAgentTokenStore_ValidateAndTouch_RejectsRevoked(t *testing.T) {
	db := openTestDB(t)
	systemID := createFixtureSystem(t, db)
	store := auth.NewAgentTokenStore(db)

	raw, _ := auth.GenerateToken()
	hash := auth.HashToken(raw)
	tok, _ := store.Create(context.Background(), systemID, hash)
	store.Revoke(context.Background(), tok.ID)

	_, err := store.ValidateAndTouch(context.Background(), hash)
	if err != auth.ErrInvalidToken {
		t.Errorf("want ErrInvalidToken for revoked token, got %v", err)
	}
}

func TestAgentTokenStore_ValidateAndTouch_RejectsUnknown(t *testing.T) {
	db := openTestDB(t)
	_ = createFixtureSystem(t, db)
	store := auth.NewAgentTokenStore(db)

	_, err := store.ValidateAndTouch(context.Background(), "unknown-hash")
	if err != auth.ErrInvalidToken {
		t.Errorf("want ErrInvalidToken, got %v", err)
	}
}
