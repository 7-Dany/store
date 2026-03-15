package userlock

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	adminshared "github.com/7-Dany/store/backend/internal/domain/admin/shared"
	platformrbac "github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/go-chi/chi/v5"
)

// Servicer is the subset of the service that the handler requires.
// *Service satisfies this interface; tests supply a UserLockFakeServicer.
type Servicer interface {
	LockUser(ctx context.Context, targetUserID, actingUserID string, in LockUserInput) error
	UnlockUser(ctx context.Context, targetUserID, actingUserID string) error
	GetLockStatus(ctx context.Context, targetUserID string) (UserLockStatus, error)
}

// Handler is the HTTP layer for the userlock package.
type Handler struct {
	svc Servicer
}

// NewHandler constructs a Handler.
func NewHandler(svc Servicer) *Handler {
	return &Handler{svc: svc}
}

// LockUser handles POST /admin/users/{user_id}/lock → 204.
func (h *Handler) LockUser(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	userID := chi.URLParam(r, "user_id")

	actingUserID, ok := adminshared.MustUserID(w, r)
	if !ok {
		return
	}

	req, ok := respond.DecodeJSON[lockUserRequest](w, r)
	if !ok {
		return
	}

	// Validate structural input (empty fields, format) before calling the service.
	// Semantic guards that require a parsed identity or a DB read live in the service.
	if err := validateLockUser(LockUserInput{Reason: req.Reason}); err != nil {
		h.writeLockError(w, r, err)
		return
	}

	err := h.svc.LockUser(r.Context(), userID, actingUserID, LockUserInput{Reason: req.Reason})
	if err != nil {
		h.writeLockError(w, r, err)
		return
	}
	respond.NoContent(w)
}

// UnlockUser handles DELETE /admin/users/{user_id}/lock → 204.
func (h *Handler) UnlockUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")

	actingUserID, ok := adminshared.MustUserID(w, r)
	if !ok {
		return
	}

	err := h.svc.UnlockUser(r.Context(), userID, actingUserID)
	if err != nil {
		h.writeLockError(w, r, err)
		return
	}
	respond.NoContent(w)
}

// GetLockStatus handles GET /admin/users/{user_id}/lock → 200.
func (h *Handler) GetLockStatus(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")

	status, err := h.svc.GetLockStatus(r.Context(), userID)
	if err != nil {
		h.writeLockError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, toLockStatusResponse(status))
}

// writeLockError maps service errors to the appropriate HTTP response.
func (h *Handler) writeLockError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrUserNotFound):
		respond.Error(w, http.StatusNotFound, "user_not_found", "user not found")
	case errors.Is(err, ErrReasonRequired):
		respond.Error(w, http.StatusUnprocessableEntity, "reason_required", "reason is required")
	case errors.Is(err, platformrbac.ErrCannotLockSelf):
		respond.Error(w, http.StatusConflict, "cannot_lock_self", "you cannot lock your own account")
	case errors.Is(err, platformrbac.ErrCannotLockOwner):
		respond.Error(w, http.StatusConflict, "cannot_lock_owner", "owner accounts cannot be admin-locked")
	default:
		slog.ErrorContext(r.Context(), "userlock: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
