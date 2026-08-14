//go:build integration

package catalog_test

import (
	"context"
	"testing"

	"github.com/cerberus8484/opensourcebackup/internal/catalog"
)

func TestRepositoryStore_Create_AssignsIDAndCreatedAt(t *testing.T) {
	db := newTestDB(t)
	truncateTables(t, db)
	store := catalog.NewRepositoryStore(db)

	r := &catalog.BackupRepository{
		Type:     "restic",
		Location: "s3:my-bucket/backups",
	}
	if err := store.Create(context.Background(), r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if r.ID.String() == "" {
		t.Fatal("expected ID to be set after Create")
	}
	if r.CreatedAt.IsZero() {
		t.Fatal("expected CreatedAt to be set after Create")
	}
}

func TestRepositoryStore_GetByID_ReturnsRepository_WhenExists(t *testing.T) {
	db := newTestDB(t)
	truncateTables(t, db)
	store := catalog.NewRepositoryStore(db)

	enc := "aes256"
	created := &catalog.BackupRepository{
		Type:              "restic",
		Location:          "sftp:backup-host:/data",
		EncryptionMode:    &enc,
		ObjectLockEnabled: true,
	}
	if err := store.Create(context.Background(), created); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Type != "restic" {
		t.Errorf("Type: want restic, got %s", got.Type)
	}
	if !got.ObjectLockEnabled {
		t.Error("ObjectLockEnabled: want true")
	}
	if got.EncryptionMode == nil || *got.EncryptionMode != "aes256" {
		t.Error("EncryptionMode: want aes256")
	}
	if got.SecurityPosture.Encryption != catalog.SecurityProvenanceDeclared || got.SecurityPosture.ObjectLock != catalog.SecurityProvenanceDeclared || got.SecurityPosture.Immutability != catalog.SecurityProvenanceDeclared {
		t.Errorf("configured security claims must remain DECLARED, got %+v", got.SecurityPosture)
	}
}

func TestRepositoryStore_GetByID_ReturnsErrNotFound_WhenMissing(t *testing.T) {
	db := newTestDB(t)
	truncateTables(t, db)
	store := catalog.NewRepositoryStore(db)

	_, err := store.GetByID(context.Background(), [16]byte{0xFF})
	if err != catalog.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestRepositoryStore_List_ReturnsAllRepositories(t *testing.T) {
	db := newTestDB(t)
	truncateTables(t, db)
	store := catalog.NewRepositoryStore(db)

	for i, loc := range []string{"s3:bucket-a", "s3:bucket-b"} {
		_ = i
		if err := store.Create(context.Background(), &catalog.BackupRepository{Type: "restic", Location: loc}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	repos, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(repos) != 2 {
		t.Errorf("want 2 repos, got %d", len(repos))
	}
}

func TestRepositoryStore_Update_PersistsChanges(t *testing.T) {
	db := newTestDB(t)
	truncateTables(t, db)
	store := catalog.NewRepositoryStore(db)

	r := &catalog.BackupRepository{Type: "restic", Location: "s3:old-bucket"}
	if err := store.Create(context.Background(), r); err != nil {
		t.Fatalf("Create: %v", err)
	}

	r.Location = "s3:new-bucket"
	r.ObjectLockEnabled = true
	if err := store.Update(context.Background(), r); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := store.GetByID(context.Background(), r.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Location != "s3:new-bucket" {
		t.Errorf("Location: want s3:new-bucket, got %s", got.Location)
	}
	if got.SecurityPosture.ObjectLock != catalog.SecurityProvenanceDeclared {
		t.Errorf("object lock posture = %q, want DECLARED", got.SecurityPosture.ObjectLock)
	}
}

func TestRepositoryStore_Delete_RemovesRepository(t *testing.T) {
	db := newTestDB(t)
	truncateTables(t, db)
	store := catalog.NewRepositoryStore(db)

	r := &catalog.BackupRepository{Type: "restic", Location: "s3:to-delete"}
	if err := store.Create(context.Background(), r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Delete(context.Background(), r.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := store.GetByID(context.Background(), r.ID)
	if err != catalog.ErrNotFound {
		t.Fatalf("want ErrNotFound after Delete, got %v", err)
	}
}
