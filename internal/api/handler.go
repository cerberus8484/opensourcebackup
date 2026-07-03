package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cerberus8484/opensourcebackup/internal/audit"
	"github.com/cerberus8484/opensourcebackup/internal/auth"
	"github.com/cerberus8484/opensourcebackup/internal/catalog"
)

// PolicyChangeNotifier is called after any policy create/update/delete
// so the scheduler can reload its cron entries without a restart.
// Using an interface keeps the API layer decoupled from the scheduler (DIP).
type PolicyChangeNotifier interface {
	PoliciesChanged(ctx context.Context)
}

// ErrBodyTooLarge is returned by decode when the request body exceeds the configured limit.
var ErrBodyTooLarge = errors.New("request body too large")

// Handler holds all store dependencies for the HTTP API.
type Handler struct {
	systems          catalog.SystemStore
	repositories     catalog.RepositoryStore
	policies         catalog.PolicyStore
	jobs             catalog.JobStore
	snapshots        catalog.SnapshotStore
	restoreTests     catalog.RestoreTestStore
	enrollmentTokens auth.EnrollmentTokenStore
	agentTokens      auth.AgentTokenStore
	policyNotifier   PolicyChangeNotifier         // may be nil
	webAuth          *auth.WebAuthenticator       // legacy single-password (fallback)
	sessions         *auth.RBACSessionManager     // multi-user sessions; nil = legacy mode
	users            auth.UserStore               // nil = legacy single-password mode
	auditStore       audit.Store
	dbPool           *pgxpool.Pool                // for direct DB operations (notifications etc.)
	log              *slog.Logger
}

// New creates a Handler wired to the given stores.
// auditStore must not be nil — use audit.NoopStore{} to disable auditing.
func New(
	systems catalog.SystemStore,
	repositories catalog.RepositoryStore,
	policies catalog.PolicyStore,
	jobs catalog.JobStore,
	snapshots catalog.SnapshotStore,
	restoreTests catalog.RestoreTestStore,
	enrollmentTokens auth.EnrollmentTokenStore,
	agentTokens auth.AgentTokenStore,
	auditStore audit.Store,
	log *slog.Logger,
) *Handler {
	if auditStore == nil {
		auditStore = audit.NoopStore{}
	}
	return &Handler{
		systems:          systems,
		repositories:     repositories,
		policies:         policies,
		jobs:             jobs,
		snapshots:        snapshots,
		restoreTests:     restoreTests,
		enrollmentTokens: enrollmentTokens,
		agentTokens:      agentTokens,
		auditStore:       auditStore,
		log:              log,
	}
}

// WithWebAuth enables legacy single-password authentication (fallback).
func (h *Handler) WithWebAuth(wa *auth.WebAuthenticator) *Handler {
	h.webAuth = wa
	return h
}

// WithDBPool stores the raw pool for direct DB operations.
func (h *Handler) WithDBPool(pool *pgxpool.Pool) *Handler {
	h.dbPool = pool
	return h
}

// WithRBAC enables multi-user RBAC authentication.
// When set, this takes precedence over the legacy WebAuth.
func (h *Handler) WithRBAC(sessions *auth.RBACSessionManager, users auth.UserStore) *Handler {
	h.sessions = sessions
	h.users = users
	return h
}

// WithPolicyNotifier wires a PolicyChangeNotifier into the handler.
func (h *Handler) WithPolicyNotifier(n PolicyChangeNotifier) *Handler {
	h.policyNotifier = n
	return h
}

// notifyPoliciesChanged calls the notifier if one is registered.
func (h *Handler) notifyPoliciesChanged(ctx context.Context) {
	if h.policyNotifier != nil {
		h.policyNotifier.PoliciesChanged(ctx)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, "encoding error", http.StatusInternalServerError)
	}
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func decode(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return ErrBodyTooLarge
		}
		return err
	}
	return nil
}

// handleDecodeError writes the appropriate HTTP error for a decode failure.
func handleDecodeError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrBodyTooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid request body")
}

// httpStatusForError maps catalog errors to HTTP status codes.
func httpStatusForError(err error) int {
	switch {
	case errors.Is(err, catalog.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, catalog.ErrConflict),
		errors.Is(err, catalog.ErrIllegalTransition):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// safeErrorMessage returns a client-safe message for a store error.
// Known sentinel errors keep their (non-sensitive) text; everything else is
// collapsed to a generic message so raw DB errors (table names, constraints,
// driver internals) never reach the client. Detailed errors should be logged
// server-side at the call site.
func safeErrorMessage(err error) string {
	switch {
	case errors.Is(err, catalog.ErrNotFound):
		return "not found"
	case errors.Is(err, catalog.ErrConflict):
		return "conflict"
	case errors.Is(err, catalog.ErrIllegalTransition):
		return "job is no longer in a state that allows this change"
	default:
		return "internal error"
	}
}
