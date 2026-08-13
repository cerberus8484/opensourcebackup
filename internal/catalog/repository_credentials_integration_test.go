//go:build integration

package catalog_test

import (
	"context"
	"testing"

	"github.com/cerberus8484/opensourcebackup/internal/catalog"
)

func TestRepositoryCredentialReferenceStore_PersistsMetadataOnly(t *testing.T) {
	db := newTestDB(t)
	truncateTables(t, db)
	ctx := context.Background()
	repo := &catalog.BackupRepository{Type: "restic", Location: "s3:credential-reference-test"}
	if err := catalog.NewRepositoryStore(db).Create(ctx, repo); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	ref := &catalog.RepositoryCredentialReference{
		RepositoryID: repo.ID, Purpose: catalog.CredentialPurposeBackupWrite,
		ProviderType: "external-vault", Reference: "backup/repo/write",
	}
	store := catalog.NewRepositoryCredentialReferenceStore(db)
	if err := store.Create(ctx, ref); err != nil {
		t.Fatalf("create credential reference: %v", err)
	}
	refs, err := store.ListByRepositoryID(ctx, repo.ID)
	if err != nil {
		t.Fatalf("list credential references: %v", err)
	}
	if len(refs) != 1 || refs[0].Reference != "backup/repo/write" {
		t.Fatalf("unexpected references: %+v", refs)
	}
}
