package api

import (
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/cerberus8484/opensourcebackup/internal/audit"
	"github.com/cerberus8484/opensourcebackup/internal/catalog"
	"github.com/cerberus8484/opensourcebackup/internal/security"
)

func (h *Handler) listRepositories(w http.ResponseWriter, r *http.Request) {
	repos, err := h.repositories.List(r.Context())
	if err != nil {
		h.log.Error("list repositories", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if repos == nil {
		repos = []catalog.BackupRepository{}
	}
	writeJSON(w, http.StatusOK, repos)
}

func (h *Handler) createRepository(w http.ResponseWriter, r *http.Request) {
	var repo catalog.BackupRepository
	if err := decode(r, &repo); err != nil {
		handleDecodeError(w, err)
		return
	}
	if repo.Type == "" {
		writeError(w, http.StatusBadRequest, "type is required")
		return
	}
	if repo.Location == "" {
		writeError(w, http.StatusBadRequest, "location is required")
		return
	}
	if repo.ManagementMode != "" && repo.ManagementMode != catalog.RepositoryManagementLegacy && repo.ManagementMode != catalog.RepositoryManagementManaged {
		writeError(w, http.StatusBadRequest, "invalid management mode")
		return
	}
	if err := h.repositories.Create(r.Context(), &repo); err != nil {
		h.log.Error("create repository", "error", err)
		writeError(w, httpStatusForError(err), safeErrorMessage(err))
		return
	}
	_ = h.auditStore.Append(r.Context(), audit.Event(
		audit.ActionRepositoryCreated, audit.ResourceRepository, repo.ID.String()).
		By(audit.ActorAdmin).
		IP(security.ClientIPHashed(r)).
		UA(r.UserAgent()).
		Details(fmt.Sprintf("type=%s location=%s immutable_mode=%s", repo.Type, repo.Location, repo.ImmutableMode)).
		Build())
	writeJSON(w, http.StatusCreated, repo)
}

func (h *Handler) getRepository(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	repo, err := h.repositories.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, httpStatusForError(err), safeErrorMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, repo)
}

func (h *Handler) updateRepository(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	// Load existing to detect immutable_mode change
	existing, _ := h.repositories.GetByID(r.Context(), id)

	var repo catalog.BackupRepository
	if err := decode(r, &repo); err != nil {
		handleDecodeError(w, err)
		return
	}
	if repo.ManagementMode != "" && repo.ManagementMode != catalog.RepositoryManagementLegacy && repo.ManagementMode != catalog.RepositoryManagementManaged {
		writeError(w, http.StatusBadRequest, "invalid management mode")
		return
	}
	repo.ID = id
	if err := h.repositories.Update(r.Context(), &repo); err != nil {
		writeError(w, httpStatusForError(err), safeErrorMessage(err))
		return
	}

	// Determine action and severity
	action := audit.ActionRepositoryUpdated
	sev := audit.SeverityInfo
	details := fmt.Sprintf("type=%s", repo.Type)

	if existing != nil && existing.ImmutableMode != repo.ImmutableMode {
		action = audit.ActionRepositoryImmutableChanged
		sev = audit.SeverityWarning
		details = fmt.Sprintf("immutable_mode: %s → %s", existing.ImmutableMode, repo.ImmutableMode)
	}

	_ = h.auditStore.Append(r.Context(), audit.Event(action, audit.ResourceRepository, id.String()).
		By(audit.ActorAdmin).
		IP(security.ClientIPHashed(r)).
		UA(r.UserAgent()).
		Details(details).
		Severity(sev).
		Build())

	writeJSON(w, http.StatusOK, repo)
}

func (h *Handler) deleteRepository(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.repositories.Delete(r.Context(), id); err != nil {
		writeError(w, httpStatusForError(err), safeErrorMessage(err))
		return
	}
	_ = h.auditStore.Append(r.Context(), audit.Event(
		audit.ActionRepositoryDeleted, audit.ResourceRepository, id.String()).
		By(audit.ActorAdmin).
		IP(security.ClientIPHashed(r)).
		Severity(audit.SeverityWarning).
		Build())
	w.WriteHeader(http.StatusNoContent)
}
