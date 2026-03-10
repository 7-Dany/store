package me

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
	"github.com/google/uuid"
)

// Servicer is the subset of the service that the handler requires.
// *Service satisfies this interface; tests may supply a fake implementation.
type Servicer interface {
	GetUserProfile(ctx context.Context, userID string) (UserProfile, error)
	UpdateProfile(ctx context.Context, in UpdateProfileInput) error
	GetUserIdentities(ctx context.Context, userID string) ([]LinkedIdentity, error) // §E-1
}

// Handler is the HTTP layer for the me sub-package. It parses requests,
// calls the service, and maps sentinel errors to HTTP status codes.
type Handler struct {
	svc Servicer
}

// NewHandler constructs a Handler.
func NewHandler(svc Servicer) *Handler {
	return &Handler{svc: svc}
}

// Me handles GET /me.
// Returns the authenticated user's public profile.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.mustUserID(w, r)
	if !ok {
		return
	}

	profile, err := h.svc.GetUserProfile(r.Context(), userID)
	if err != nil {
		if errors.Is(err, authshared.ErrUserNotFound) {
			respond.Error(w, http.StatusNotFound, "not_found", "user not found")
			return
		}
		slog.ErrorContext(r.Context(), "profile.Me: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	// AdminLocked is intentionally excluded from the response — admin-initiated locks
	// require a support contact and must not be surfaced as user-visible state.
	respond.JSON(w, http.StatusOK, meResponse{
		ID:                  uuid.UUID(profile.ID).String(),
		Email:               profile.Email,
		DisplayName:         profile.DisplayName,
		Username:            profile.Username,
		AvatarURL:           profile.AvatarURL,
		EmailVerified:       profile.EmailVerified,
		IsActive:            profile.IsActive,
		IsLocked:            profile.IsLocked,
		LastLoginAt:         profile.LastLoginAt,
		CreatedAt:           profile.CreatedAt,
		ScheduledDeletionAt: profile.ScheduledDeletionAt,
	})
}

// UpdateProfile handles PATCH /me.
// Updates the authenticated user's display_name and/or avatar_url.
func (h *Handler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.mustUserID(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)
	req, ok := respond.DecodeJSON[updateProfileRequest](w, r)
	if !ok {
		return
	}

	if err := validateAndNormaliseUpdateProfile(&req); err != nil {
		switch {
		case errors.Is(err, ErrEmptyPatch):
			respond.Error(w, http.StatusUnprocessableEntity, "validation_error", "at least one field must be provided")
		case errors.Is(err, authshared.ErrDisplayNameEmpty):
			respond.Error(w, http.StatusUnprocessableEntity, "validation_error", "display_name is required")
		case errors.Is(err, authshared.ErrDisplayNameTooLong):
			respond.Error(w, http.StatusUnprocessableEntity, "validation_error", "display_name must not exceed 100 characters")
		case errors.Is(err, authshared.ErrDisplayNameInvalid):
			respond.Error(w, http.StatusUnprocessableEntity, "validation_error", "display_name contains invalid characters")
		case errors.Is(err, ErrAvatarURLTooLong):
			respond.Error(w, http.StatusUnprocessableEntity, "validation_error", "avatar_url must not exceed 2048 characters")
		case errors.Is(err, ErrAvatarURLInvalid):
			respond.Error(w, http.StatusUnprocessableEntity, "validation_error", "avatar_url must be a valid http or https URL")
		default:
			slog.ErrorContext(r.Context(), "profile.UpdateProfile: unexpected validation error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	uid, err := authshared.ParseUserID("profile.UpdateProfile", userID)
	if err != nil {
		slog.ErrorContext(r.Context(), "profile.UpdateProfile: parse user id", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	if err := h.svc.UpdateProfile(r.Context(), UpdateProfileInput{
		UserID:      uid,
		DisplayName: req.DisplayName,
		AvatarURL:   req.AvatarURL,
		IPAddress:   respond.ClientIP(r),
		UserAgent:   r.UserAgent(),
	}); err != nil {
		slog.ErrorContext(r.Context(), "profile.UpdateProfile: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	respond.JSON(w, http.StatusOK, updateProfileResponse{Message: "profile updated successfully"})
}

// Identities handles GET /me/identities.
// Returns all linked OAuth identities for the authenticated user.
// access_token and refresh_token_provider are never present in the response —
// they are excluded at the SQL and service layers.
func (h *Handler) Identities(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.mustUserID(w, r)
	if !ok {
		return
	}

	identities, err := h.svc.GetUserIdentities(r.Context(), userID)
	if err != nil {
		slog.ErrorContext(r.Context(), "profile.Identities: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	items := make([]identityItem, 0, len(identities))
	for _, id := range identities {
		items = append(items, identityItem{
			Provider:      id.Provider,
			ProviderUID:   id.ProviderUID,
			ProviderEmail: id.ProviderEmail,
			DisplayName:   id.DisplayName,
			AvatarURL:     id.AvatarURL,
			CreatedAt:     id.CreatedAt,
		})
	}

	respond.JSON(w, http.StatusOK, identitiesResponse{Identities: items})
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
