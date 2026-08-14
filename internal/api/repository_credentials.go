package api

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/cerberus8484/opensourcebackup/internal/audit"
	"github.com/cerberus8484/opensourcebackup/internal/catalog"
	"github.com/cerberus8484/opensourcebackup/internal/security"
)

func (h *Handler) listRepositoryCredentialReferences(w http.ResponseWriter, r *http.Request) {
	if h.credentialReferences == nil {
		writeError(w, http.StatusServiceUnavailable, "credential references are not configured")
		return
	}
	repositoryID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid repository id")
		return
	}
	refs, err := h.credentialReferences.ListByRepositoryID(r.Context(), repositoryID)
	if err != nil {
		h.log.Error("list repository credential references", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, refs)
}

func (h *Handler) createRepositoryCredentialReference(w http.ResponseWriter, r *http.Request) {
	if h.credentialReferences == nil {
		writeError(w, http.StatusServiceUnavailable, "credential references are not configured")
		return
	}
	repositoryID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid repository id")
		return
	}
	var ref catalog.RepositoryCredentialReference
	if err := decode(r, &ref); err != nil {
		handleDecodeError(w, err)
		return
	}
	ref.RepositoryID = repositoryID
	if !ref.Purpose.Valid() || strings.TrimSpace(ref.ProviderType) == "" || strings.TrimSpace(ref.Reference) == "" {
		writeError(w, http.StatusBadRequest, "purpose, provider type, and reference are required")
		return
	}
	if err := h.credentialReferences.Create(r.Context(), &ref); err != nil {
		h.log.Error("create repository credential reference", "error", err)
		writeError(w, httpStatusForError(err), safeErrorMessage(err))
		return
	}
	// Audit the metadata change without exposing provider or reference values.
	_ = h.auditStore.Append(r.Context(), audit.Event(audit.ActionRepositoryUpdated, audit.ResourceRepository, repositoryID.String()).
		By(audit.ActorAdmin).
		IP(security.ClientIPHashed(r)).
		UA(r.UserAgent()).
		Details("credential_reference_created").
		Build())
	writeJSON(w, http.StatusCreated, ref)
}

func (h *Handler) deleteRepositoryCredentialReference(w http.ResponseWriter, r *http.Request) {
	if h.credentialReferences == nil {
		writeError(w, http.StatusServiceUnavailable, "credential references are not configured")
		return
	}
	repositoryID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid repository id")
		return
	}
	credentialID, err := uuid.Parse(r.PathValue("credentialID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid credential reference id")
		return
	}
	if err := h.credentialReferences.Delete(r.Context(), repositoryID, credentialID); err != nil {
		writeError(w, httpStatusForError(err), safeErrorMessage(err))
		return
	}
	_ = h.auditStore.Append(r.Context(), audit.Event(audit.ActionRepositoryUpdated, audit.ResourceRepository, repositoryID.String()).
		By(audit.ActorAdmin).
		IP(security.ClientIPHashed(r)).
		UA(r.UserAgent()).
		Details("credential_reference_deleted").
		Severity(audit.SeverityWarning).
		Build())
	w.WriteHeader(http.StatusNoContent)
}
