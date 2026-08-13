//go:build integration

package catalog_test

import (
	"context"
	"testing"
	"time"

	"github.com/cerberus8484/opensourcebackup/internal/catalog"
)

func createFixtureSystemAndPolicy(t *testing.T, db *catalog.DB) (*catalog.System, *catalog.BackupPolicy) {
	t.Helper()
	ctx := context.Background()

	// Stamp last_seen=now so the fixture system is ONLINE — the E1.3a health gate
	// blocks OFFLINE/UNKNOWN systems, and dispatch tests need a healthy agent to
	// reach the operation-state and concurrency gates.
	now := time.Now().UTC()
	sys := &catalog.System{Hostname: "fixture-host", RiskClass: "standard", LastSeen: &now}
	if err := catalog.NewSystemStore(db).Create(ctx, sys); err != nil {
		t.Fatalf("fixture system: %v", err)
	}

	pol := &catalog.BackupPolicy{Name: "fixture-policy", Engine: "restic"}
	if err := catalog.NewPolicyStore(db).Create(ctx, pol); err != nil {
		t.Fatalf("fixture policy: %v", err)
	}
	return sys, pol
}

func TestJobStore_Create_AssignsIDAndCreatedAt(t *testing.T) {
	db := newTestDB(t)
	truncateTables(t, db)
	sys, pol := createFixtureSystemAndPolicy(t, db)
	store := catalog.NewJobStore(db)

	j := &catalog.BackupJob{
		SystemID: sys.ID,
		PolicyID: pol.ID,
		Status:   "running",
	}
	if err := store.Create(context.Background(), j); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if j.ID.String() == "" {
		t.Fatal("expected ID after Create")
	}
	if j.CreatedAt.IsZero() {
		t.Fatal("expected CreatedAt after Create")
	}
}

func TestJobStore_GetByID_ReturnsJob_WhenExists(t *testing.T) {
	db := newTestDB(t)
	truncateTables(t, db)
	sys, pol := createFixtureSystemAndPolicy(t, db)
	store := catalog.NewJobStore(db)

	created := &catalog.BackupJob{SystemID: sys.ID, PolicyID: pol.ID, Status: "success"}
	if err := store.Create(context.Background(), created); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != "success" {
		t.Errorf("Status: want success, got %s", got.Status)
	}
	if got.SystemID != sys.ID {
		t.Error("SystemID mismatch")
	}
}

func TestJobStore_GetByID_ReturnsErrNotFound_WhenMissing(t *testing.T) {
	db := newTestDB(t)
	truncateTables(t, db)
	store := catalog.NewJobStore(db)

	_, err := store.GetByID(context.Background(), [16]byte{0xFF})
	if err != catalog.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestJobStore_List_ReturnsAllJobs(t *testing.T) {
	db := newTestDB(t)
	truncateTables(t, db)
	sys, pol := createFixtureSystemAndPolicy(t, db)
	store := catalog.NewJobStore(db)

	for range 3 {
		if err := store.Create(context.Background(), &catalog.BackupJob{
			SystemID: sys.ID, PolicyID: pol.ID, Status: "success",
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	jobs, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(jobs) != 3 {
		t.Errorf("want 3 jobs, got %d", len(jobs))
	}
}

func TestJobStore_ListBySystemID_ReturnsOnlyMatchingJobs(t *testing.T) {
	db := newTestDB(t)
	truncateTables(t, db)
	sys, pol := createFixtureSystemAndPolicy(t, db)

	sys2 := &catalog.System{Hostname: "other-host", RiskClass: "standard"}
	catalog.NewSystemStore(db).Create(context.Background(), sys2)

	store := catalog.NewJobStore(db)
	store.Create(context.Background(), &catalog.BackupJob{SystemID: sys.ID, PolicyID: pol.ID, Status: "success"})
	store.Create(context.Background(), &catalog.BackupJob{SystemID: sys.ID, PolicyID: pol.ID, Status: "failed"})
	store.Create(context.Background(), &catalog.BackupJob{SystemID: sys2.ID, PolicyID: pol.ID, Status: "success"})

	jobs, err := store.ListBySystemID(context.Background(), sys.ID)
	if err != nil {
		t.Fatalf("ListBySystemID: %v", err)
	}
	if len(jobs) != 2 {
		t.Errorf("want 2 jobs for sys, got %d", len(jobs))
	}
}

func TestJobStore_Update_PersistsStatus(t *testing.T) {
	db := newTestDB(t)
	truncateTables(t, db)
	sys, pol := createFixtureSystemAndPolicy(t, db)
	store := catalog.NewJobStore(db)

	j := &catalog.BackupJob{SystemID: sys.ID, PolicyID: pol.ID, Status: "running"}
	store.Create(context.Background(), j)

	j.Status = "success"
	if err := store.Update(context.Background(), j); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, _ := store.GetByID(context.Background(), j.ID)
	if got.Status != "success" {
		t.Errorf("want success, got %s", got.Status)
	}
}

func TestJobStore_Delete_RemovesJob(t *testing.T) {
	db := newTestDB(t)
	truncateTables(t, db)
	sys, pol := createFixtureSystemAndPolicy(t, db)
	store := catalog.NewJobStore(db)

	j := &catalog.BackupJob{SystemID: sys.ID, PolicyID: pol.ID, Status: "success"}
	store.Create(context.Background(), j)
	store.Delete(context.Background(), j.ID)

	_, err := store.GetByID(context.Background(), j.ID)
	if err != catalog.ErrNotFound {
		t.Fatalf("want ErrNotFound after Delete, got %v", err)
	}
}
