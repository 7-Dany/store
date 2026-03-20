package username

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	profileshared "github.com/7-Dany/store/backend/internal/domain/profile/shared"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// Servicer is the business-logic contract for the username feature.
// *Service satisfies this interface; tests use UsernameFakeServicer from
// internal/domain/auth/shared/testutil.
type Servicer interface {
	// CheckUsernameAvailable normalises username and returns true when it is
	// not yet registered. Always returns (false, err) on validation or store
	// failure, never revealing whether a specific username is registered when
	// an error occurs.
	CheckUsernameAvailable(ctx context.Context, username string) (bool, error)

	// UpdateUsername atomically sets the authenticated user's username to the
	// value in in.Username after normalisation and validation. Returns
	// ErrSameUsername, ErrUsernameTaken, or profileshared.ErrUserNotFound on
	// expected failure paths.
	UpdateUsername(ctx context.Context, in UpdateUsernameInput) error
}

// Handler is the HTTP layer for the username feature. It parses requests,
// calls the service, and maps sentinel errors to HTTP status codes.
// It has no knowledge of pgtype, pgxpool, JWT signing, or the KV store.
type Handler struct {
	svc Servicer
}

// NewHandler constructs a Handler with the given service.
func NewHandler(svc Servicer) *Handler {
	return &Handler{svc: svc}
}

// Available handles GET /api/v1/profile/username/available?username=X.
//
// Public endpoint — no JWT required. Returns 200 with {"available": bool} on
// valid input so account existence is never leaked through HTTP status codes
// (anti-enumeration — Stage 0 D-03). Returns 422 on validation errors.
func (h *Handler) Available(w http.ResponseWriter, r *http.Request) {
	// No MaxBytesReader: GET requests carry no body (Stage 0 D-11).
	uname := r.URL.Query().Get("username")

	available, err := h.svc.CheckUsernameAvailable(r.Context(), uname)
	if err != nil {
		switch {
		case errors.Is(err, ErrUsernameEmpty),
			errors.Is(err, ErrUsernameTooShort),
			errors.Is(err, ErrUsernameTooLong),
			errors.Is(err, ErrUsernameInvalidChars),
			errors.Is(err, ErrUsernameInvalidFormat):
			respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		default:
			log.Error(r.Context(), "Available: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	respond.JSON(w, http.StatusOK, availableResponse{Available: available})
}

// UpdateUsername handles PATCH /api/v1/profile/me/username.
//
// Requires a valid JWT in the request context (set by JWTAuth middleware,
// which must run before this handler).
func (h *Handler) UpdateUsername(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	userID, ok := h.mustUserID(w, r)
	if !ok {
		return
	}

	req, ok := respond.DecodeJSON[updateUsernameRequest](w, r)
	if !ok {
		return
	}

	// Parse the JWT user ID string into the [16]byte that the service expects.
	uid, err := uuid.Parse(userID)
	if err != nil {
		log.Error(r.Context(), "UpdateUsername: parse user id", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	if err := h.svc.UpdateUsername(r.Context(), UpdateUsernameInput{
		UserID:    [16]byte(uid),
		Username:  req.Username,
		IPAddress: respond.ClientIP(r),
		UserAgent: r.UserAgent(),
	}); err != nil {
		switch {
		case errors.Is(err, ErrUsernameEmpty),
			errors.Is(err, ErrUsernameTooShort),
			errors.Is(err, ErrUsernameTooLong),
			errors.Is(err, ErrUsernameInvalidChars),
			errors.Is(err, ErrUsernameInvalidFormat):
			respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		case errors.Is(err, ErrSameUsername):
			respond.Error(w, http.StatusUnprocessableEntity, "same_username", err.Error())
		case errors.Is(err, ErrUsernameTaken):
			respond.Error(w, http.StatusConflict, "username_taken", err.Error())
		case errors.Is(err, profileshared.ErrUserNotFound):
			// Authenticated user whose row is absent — surfacing 404 would confirm
			// that a valid JWT references a deleted account, so treat as 500.
			log.Error(r.Context(), "UpdateUsername: user not found for authenticated request", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		default:
			log.Error(r.Context(), "UpdateUsername: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	respond.JSON(w, http.StatusOK, updateUsernameResponse{Message: "username updated successfully"})
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
