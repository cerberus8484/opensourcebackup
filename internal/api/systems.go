package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/cerberus8484/opensourcebackup/internal/audit"
	"github.com/cerberus8484/opensourcebackup/internal/auth"
	"github.com/cerberus8484/opensourcebackup/internal/catalog"
	"github.com/cerberus8484/opensourcebackup/internal/security"
)

// systemView is the API representation of a system: the stored model plus the
// derived (non-stored) health state, so operation state and health are visible
// as the two independent dimensions they are (E1.2).
type systemView struct {
	catalog.System
	HealthState catalog.HealthState `json:"HealthState"`
}

func toSystemView(s catalog.System) systemView {
	return systemView{System: s, HealthState: catalog.DeriveHealthState(s.LastSeen, time.Now().UTC())}
}

func (h *Handler) listSystems(w http.ResponseWriter, r *http.Request) {
	systems, err := h.systems.List(r.Context())
	if err != nil {
		h.log.Error("list systems", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	views := make([]systemView, 0, len(systems))
	for _, s := range systems {
		views = append(views, toSystemView(s))
	}
	writeJSON(w, http.StatusOK, views)
}

func (h *Handler) createSystem(w http.ResponseWriter, r *http.Request) {
	var s catalog.System
	if err := decode(r, &s); err != nil {
		handleDecodeError(w, err)
		return
	}
	if s.Hostname == "" {
		writeError(w, http.StatusBadRequest, "hostname is required")
		return
	}
	if s.RiskClass == "" {
		s.RiskClass = "standard"
	}
	if err := h.systems.Create(r.Context(), &s); err != nil {
		h.log.Error("create system", "error", err)
		writeError(w, httpStatusForError(err), safeErrorMessage(err))
		return
	}
	writeJSON(w, http.StatusCreated, s)
}

func (h *Handler) getSystem(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	s, err := h.systems.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, httpStatusForError(err), safeErrorMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, toSystemView(*s))
}

func (h *Handler) updateSystem(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var s catalog.System
	if err := decode(r, &s); err != nil {
		handleDecodeError(w, err)
		return
	}
	s.ID = id
	if err := h.systems.Update(r.Context(), &s); err != nil {
		writeError(w, httpStatusForError(err), safeErrorMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *Handler) deleteSystem(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.systems.Delete(r.Context(), id); err != nil {
		writeError(w, httpStatusForError(err), safeErrorMessage(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// setOperationState handles PUT /v1/systems/{id}/operation-state (E1.2).
//
// Changes the admin-set operation state (active|maintenance|draining|suspended).
// RBAC-guarded at the route (operator+). The change is audited with actor,
// old→new state and an optional reason. It never touches jobs or health: switching
// to MAINTENANCE/DRAINING/SUSPENDED only blocks NEW dispatches; running jobs are
// unaffected (E1.2 does not auto-cancel or auto-drain).
func (h *Handler) setOperationState(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		State  string `json:"state"`
		Reason string `json:"reason"`
	}
	if err := decode(r, &body); err != nil {
		handleDecodeError(w, err)
		return
	}
	state := catalog.OperationState(body.State)
	if !catalog.Valid(state) {
		writeError(w, http.StatusBadRequest,
			"state must be one of: active, maintenance, draining, suspended")
		return
	}

	oldState, err := h.systems.SetOperationState(r.Context(), id, body.State)
	if err != nil {
		writeError(w, httpStatusForError(err), safeErrorMessage(err))
		return
	}

	// Audit: actor + system + old→new + reason. Actor identity lives in the audit
	// log, not on the system row (no duplication).
	actor := "system"
	if s, ok := auth.SessionFromContext(r.Context()); ok && s.UserEmail != "" {
		actor = s.UserEmail
	}
	details := fmt.Sprintf("old=%s new=%s", oldState, body.State)
	if reason := auditSafe(body.Reason); reason != "" {
		details += " reason=" + reason
	}
	_ = h.auditStore.Append(r.Context(), audit.Event(
		audit.ActionSystemOperationStateChanged, audit.ResourceSystem, id.String()).
		By(audit.ActorAdmin).Actor(actor).
		IP(security.ClientIPHashed(r)).
		Details(details).
		Severity(audit.SeverityInfo).Build())

	fresh, err := h.systems.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, httpStatusForError(err), safeErrorMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, toSystemView(*fresh))
}

// auditSafe trims and bounds a free-text reason so it is safe for the audit log:
// single line, no control characters, capped length. It carries no secrets by
// contract (operator-supplied reason text).
func auditSafe(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	const maxReasonLen = 200
	if len(s) > maxReasonLen {
		s = s[:maxReasonLen]
	}
	return s
}
