package setpassword

import (
	"context"
	"errors"
	"net/http"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	profileshared "github.com/7-Dany/store/backend/internal/domain/profile/shared"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// Servicer is the subset of the service that the handler requires.
// *Service satisfies this interface; tests may supply a fake implementation.
type Servicer interface {
	// SetPassword adds a password to an OAuth-only account that currently has
	// no password_hash. Returns ErrPasswordAlreadySet if the account already
	// has a password; profileshared.ErrUserNotFound if the user row is gone.
	SetPassword(ctx context.Context, in SetPasswordInput) error
}

// Handler is the HTTP layer for POST /set-password. It parses requests,
// calls the service, and maps sentinel errors to HTTP status codes.
// It has no knowledge of pgtype, pgxpool, JWT signing, or the KV store.
type Handler struct {
	svc Servicer
}

// NewHandler constructs a Handler with the given service.
func NewHandler(svc Servicer) *Handler {
	return &Handler{svc: svc}
}

// SetPassword handles POST /api/v1/auth/set-password.
//
// Requires a valid JWT in the request context (set by JWTAuth middleware,
// which must run before this handler).
func (h *Handler) SetPassword(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	userID, ok := h.mustUserID(w, r)
	if !ok {
		return
	}

	req, ok := respond.DecodeJSON[setPasswordRequest](w, r)
	if !ok {
		return
	}

	if err := validateSetPasswordRequest(&req); err != nil {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}

	err := h.svc.SetPassword(r.Context(), SetPasswordInput{
		UserID:      userID,
		NewPassword: req.NewPassword,
		IPAddress:   respond.ClientIP(r),
		UserAgent:   r.UserAgent(),
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrPasswordAlreadySet):
			respond.Error(w, http.StatusUnprocessableEntity, "password_already_set", err.Error())
		case errors.Is(err, profileshared.ErrUserNotFound):
			respond.Error(w, http.StatusNotFound, "not_found", "user not found")
		case authshared.IsPasswordStrengthError(err):
			// Service-level strength error: service called ValidatePassword after
			// the handler validator — this path is exercised by service unit tests
			// but should be unreachable in production (handler validates first).
			respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		default:
			log.Error(r.Context(), "SetPassword: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	respond.JSON(w, http.StatusOK, map[string]string{"message": "password set successfully"})
}

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
