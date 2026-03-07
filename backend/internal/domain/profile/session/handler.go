package session

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Servicer is the subset of the service that the handler requires.
// *Service satisfies this interface; tests may supply a fake implementation.
type Servicer interface {
	GetActiveSessions(ctx context.Context, userID string) ([]ActiveSession, error)
	RevokeSession(ctx context.Context, userID, sessionID, ipAddress, userAgent string) error
}

// Handler is the HTTP layer for the session sub-package. It parses requests,
// calls the service, and maps sentinel errors to HTTP status codes.
type Handler struct {
	svc Servicer
}

// NewHandler constructs a Handler.
func NewHandler(svc Servicer) *Handler {
	return &Handler{svc: svc}
}

// Sessions handles GET /sessions.
// Returns all active sessions for the authenticated user.
func (h *Handler) Sessions(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.mustUserID(w, r)
	if !ok {
		return
	}

	sessions, err := h.svc.GetActiveSessions(r.Context(), userID)
	if err != nil {
		slog.ErrorContext(r.Context(), "profile.Sessions: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	currentSessionID, _ := token.SessionIDFromContext(r.Context())

	out := make([]sessionJSON, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, sessionJSON{
			ID:           uuid.UUID(s.ID).String(),
			IPAddress:    s.IPAddress,
			UserAgent:    s.UserAgent,
			StartedAt:    s.StartedAt,
			LastActiveAt: s.LastActiveAt,
			IsCurrent:    currentSessionID != "" && uuid.UUID(s.ID).String() == currentSessionID,
		})
	}

	respond.JSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// RevokeSession handles DELETE /sessions/{id}.
// Revokes a specific session belonging to the authenticated user.
func (h *Handler) RevokeSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.mustUserID(w, r)
	if !ok {
		return
	}

	sessionIDStr := chi.URLParam(r, "id")
	if sessionIDStr == "" {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", "session id is required")
		return
	}
	if _, err := uuid.Parse(sessionIDStr); err != nil {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", "session id is not a valid UUID")
		return
	}

	err := h.svc.RevokeSession(r.Context(), userID, sessionIDStr, respond.ClientIP(r), r.UserAgent())
	if err != nil {
		if errors.Is(err, authshared.ErrSessionNotFound) {
			respond.Error(w, http.StatusNotFound, "not_found", "session not found")
			return
		}
		slog.ErrorContext(r.Context(), "profile.RevokeSession: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	respond.NoContent(w)
}

// ── private helpers ───────────────────────────────────────────────────────────

// mustUserID extracts the authenticated user ID from the request context.
// If absent or empty it writes a 401 and returns ("", false) so the caller
// can return immediately.
func (h *Handler) mustUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, ok := token.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "missing user id in context")
		return "", false
	}
	return userID, true
}
