package bootstrap

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"

	rbacshared "github.com/7-Dany/store/backend/internal/domain/rbac/shared"
	"github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
	"github.com/google/uuid"
)

// Servicer is the subset of the service that the handler requires.
type Servicer interface {
	Bootstrap(ctx context.Context, in BootstrapInput) (BootstrapResult, error)
}

// Handler is the HTTP layer for the bootstrap feature.
type Handler struct {
	svc    Servicer
	secret string // BOOTSTRAP_SECRET env value; never empty after Routes wires it
}

// NewHandler constructs a Handler.
// secret must be the value of BOOTSTRAP_SECRET — callers (routes.go) are
// responsible for ensuring it is non-empty before wiring the route.
func NewHandler(svc Servicer, secret string) *Handler {
	return &Handler{svc: svc, secret: secret}
}

// Bootstrap handles POST /owner/bootstrap.
//
// Guards (in order):
//  1. JWT middleware (applied in routes.go) — rejects unauthenticated callers.
//  2. bootstrap_secret field — must match BOOTSTRAP_SECRET (constant-time compare).
//  3. Service layer — rejects if an active owner already exists.
//
// The authenticated caller's user ID is derived from their JWT; no user_id
// field in the body is accepted, preventing privilege transfer to a third party.
func (h *Handler) Bootstrap(w http.ResponseWriter, r *http.Request) {
	// 1. Extract the authenticated caller's user ID from the JWT context.
	userID, ok := h.mustUserID(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	req, ok := respond.DecodeJSON[bootstrapRequest](w, r)
	if !ok {
		return
	}

	if err := validateBootstrapRequest(&req); err != nil {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}

	// 2. Constant-time comparison prevents timing attacks against the secret.
	if subtle.ConstantTimeCompare([]byte(req.BootstrapSecret), []byte(h.secret)) != 1 {
		respond.Error(w, http.StatusForbidden, "forbidden", "invalid bootstrap secret")
		return
	}

	// userID was validated by the JWT middleware; parse is safe.
	parsed, _ := uuid.Parse(userID)

	result, err := h.svc.Bootstrap(r.Context(), BootstrapInput{
		UserID:    [16]byte(parsed),
		IPAddress: respond.ClientIP(r),
		UserAgent: r.UserAgent(),
	})
	if err == nil {
		respond.JSON(w, http.StatusCreated, result)
		return
	}

	switch {
	case errors.Is(err, rbac.ErrOwnerAlreadyExists):
		respond.Error(w, http.StatusConflict, "owner_already_exists", "an active owner already exists")
	case errors.Is(err, rbacshared.ErrUserNotFound):
		// The token already authenticated this user, so ErrUserNotFound here means
		// the account was deleted between auth and this call — treat as 404.
		respond.Error(w, http.StatusNotFound, "user_not_found", "authenticated user account no longer exists")
	case errors.Is(err, ErrUserNotActive):
		respond.Error(w, http.StatusUnprocessableEntity, "user_not_active", "user account is not active")
	case errors.Is(err, ErrUserNotVerified):
		respond.Error(w, http.StatusUnprocessableEntity, "email_not_verified", "user email address must be verified before bootstrapping")
	default:
		slog.ErrorContext(r.Context(), "bootstrap.Bootstrap: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

// ── private helpers ──────────────────────────────────────────────────────────────────────────────

// mustUserID extracts the authenticated user ID from the JWT context.
// If absent or empty it writes a 401 and returns ("", false).
func (h *Handler) mustUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, ok := token.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "missing or invalid access token")
		return "", false
	}
	return userID, true
}
