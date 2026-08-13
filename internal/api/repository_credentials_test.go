package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cerberus8484/opensourcebackup/internal/api"
	"github.com/cerberus8484/opensourcebackup/internal/catalog"
)

type stubCredentialReferenceStore struct {
	refs []catalog.RepositoryCredentialReference
}

func (s *stubCredentialReferenceStore) Create(_ context.Context, ref *catalog.RepositoryCredentialReference) error {
	ref.ID = uuid.New()
	ref.CreatedAt = time.Now()
	ref.UpdatedAt = ref.CreatedAt
	s.refs = append(s.refs, *ref)
	return nil
}
func (s *stubCredentialReferenceStore) ListByRepositoryID(_ context.Context, repositoryID uuid.UUID) ([]catalog.RepositoryCredentialReference, error) {
	result := make([]catalog.RepositoryCredentialReference, 0)
	for _, ref := range s.refs {
		if ref.RepositoryID == repositoryID {
			result = append(result, ref)
		}
	}
	return result, nil
}
func (s *stubCredentialReferenceStore) Delete(_ context.Context, repositoryID, id uuid.UUID) error {
	for i, ref := range s.refs {
		if ref.RepositoryID == repositoryID && ref.ID == id {
			s.refs = append(s.refs[:i], s.refs[i+1:]...)
			return nil
		}
	}
	return catalog.ErrNotFound
}

func TestRepositoryCredentialReferences_AreAdminOnlyAndMetadataOnly(t *testing.T) {
	store := &stubCredentialReferenceStore{}
	h := newTestHandler().WithRepositoryCredentialReferenceStore(store)
	mux := newMux(h)
	repositoryID := uuid.New()

	viewer := httptest.NewRecorder()
	mux.ServeHTTP(viewer, httptest.NewRequest(http.MethodGet, "/v1/repositories/"+repositoryID.String()+"/credential-references", nil))
	if viewer.Code != http.StatusForbidden {
		t.Fatalf("viewer status = %d, want %d", viewer.Code, http.StatusForbidden)
	}

	create := httptest.NewRecorder()
	body := `{"Purpose":"BACKUP_WRITE","ProviderType":"external-vault","Reference":"backup/repo-a/write","Status":"ACTIVE"}`
	mux.ServeHTTP(create, withAdmin(httptest.NewRequest(http.MethodPost, "/v1/repositories/"+repositoryID.String()+"/credential-references", strings.NewReader(body))))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", create.Code, create.Body.String())
	}
	if strings.Contains(create.Body.String(), "secret-value") {
		t.Fatal("credential secret value leaked in API response")
	}

	list := httptest.NewRecorder()
	mux.ServeHTTP(list, withAdmin(httptest.NewRequest(http.MethodGet, "/v1/repositories/"+repositoryID.String()+"/credential-references", nil)))
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", list.Code, list.Body.String())
	}
	var refs []catalog.RepositoryCredentialReference
	if err := json.NewDecoder(list.Body).Decode(&refs); err != nil {
		t.Fatalf("decode refs: %v", err)
	}
	if len(refs) != 1 || refs[0].Purpose != catalog.CredentialPurposeBackupWrite {
		t.Fatalf("unexpected references: %+v", refs)
	}
}

func TestRepositoryCredentialReferences_RejectInvalidPurpose(t *testing.T) {
	h := newTestHandler().WithRepositoryCredentialReferenceStore(&stubCredentialReferenceStore{})
	mux := newMux(h)
	rec := httptest.NewRecorder()
	body := `{"Purpose":"DELETE_ALL","ProviderType":"external-vault","Reference":"repo/a"}`
	mux.ServeHTTP(rec, withAdmin(httptest.NewRequest(http.MethodPost, "/v1/repositories/"+uuid.New().String()+"/credential-references", strings.NewReader(body))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestRepositorySecurityDeclarations_DoNotEarnVerifiedHealthCredit(t *testing.T) {
	repos := &stubRepositoryStore{repos: map[uuid.UUID]*catalog.BackupRepository{}}
	encryption := "aes256"
	repos.repos[uuid.New()] = &catalog.BackupRepository{
		ID: uuid.New(), Type: "restic", Location: "s3:declared-only", EncryptionMode: &encryption,
		ImmutableMode: catalog.ImmutableObjectLock,
		SecurityPosture: catalog.RepositorySecurityPosture{
			Encryption: catalog.SecurityProvenanceDeclared, Immutability: catalog.SecurityProvenanceDeclared,
		},
	}
	h := api.New(newStubSystemStore(), repos, newStubPolicyStore(), newStubJobStore(), newStubSnapshotStore(), newStubRestoreTestStore(), newStubEnrollmentTokenStore(), newStubAgentTokenStore(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/health/score", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("score status = %d: %s", rec.Code, rec.Body.String())
	}
	var result struct{ Deductions []struct{ Code string } }
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode score: %v", err)
	}
	seen := map[string]bool{}
	for _, deduction := range result.Deductions {
		seen[deduction.Code] = true
	}
	if !seen["repos_not_immutable"] || !seen["repos_not_encrypted"] {
		t.Fatalf("declared security must not earn verified credit, deductions: %+v", result.Deductions)
	}
}
