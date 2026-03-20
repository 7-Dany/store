package userroles

import (
	"context"
	"errors"
	"net/http"

	adminshared "github.com/7-Dany/store/backend/internal/domain/admin/shared"
	platformrbac "github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/go-chi/chi/v5"
)

// Servicer is the subset of the service that the handler requires.
// *Service satisfies this interface; tests supply a UserRolesFakeServicer.
type Servicer interface {
	GetUserRole(ctx context.Context, targetUserID string) (UserRole, error)
	AssignRole(ctx context.Context, targetUserID, actingUserID string, in AssignRoleInput) (UserRole, error)
	RemoveRole(ctx context.Context, targetUserID, actingUserID string) error
}

// Handler is the HTTP layer for the userroles package.
type Handler struct {
	svc Servicer
}

// NewHandler constructs a Handler.
func NewHandler(svc Servicer) *Handler {
	return &Handler{svc: svc}
}

// GetUserRole handles GET /admin/users/{user_id}/role.
func (h *Handler) GetUserRole(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	role, err := h.svc.GetUserRole(r.Context(), userID)
	if err != nil {
		switch {
		case errors.Is(err, ErrUserRoleNotFound):
			respond.Error(w, http.StatusNotFound, "user_role_not_found", "user has no active role assignment")
		default:
			log.Error(r.Context(), "GetUserRole: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}
	respond.JSON(w, http.StatusOK, toUserRoleResponse(role))
}

// AssignRole handles PUT /admin/users/{user_id}/role.
func (h *Handler) AssignRole(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	userID := chi.URLParam(r, "user_id")

	actingUserID, ok := adminshared.MustUserID(w, r)
	if !ok {
		return
	}

	req, ok := respond.DecodeJSON[assignRoleRequest](w, r)
	if !ok {
		return
	}

	in := AssignRoleInput{
		RoleID:        req.RoleID,
		GrantedReason: req.GrantedReason,
		ExpiresAt:     req.ExpiresAt,
	}
	if err := validateAssignRole(in); err != nil {
		switch {
		case errors.Is(err, ErrRoleIDEmpty):
			respond.Error(w, http.StatusUnprocessableEntity,
				"role_id_required", "role_id is required")
		case errors.Is(err, ErrGrantedReasonEmpty):
			respond.Error(w, http.StatusUnprocessableEntity,
				"granted_reason_required", "granted_reason is required")
		default:
			respond.Error(w, http.StatusUnprocessableEntity,
				"validation_error", err.Error())
		}
		return
	}

	role, err := h.svc.AssignRole(r.Context(), userID, actingUserID, in)
	if err != nil {
		switch {
		case errors.Is(err, platformrbac.ErrCannotModifyOwnRole):
			respond.Error(w, http.StatusConflict, "cannot_modify_own_role", "you cannot modify your own role assignment")
		case errors.Is(err, platformrbac.ErrCannotReassignOwner):
			respond.Error(w, http.StatusConflict, "cannot_reassign_owner", "owner role cannot be reassigned via this route")
		case errors.Is(err, ErrRoleNotFound):
			respond.Error(w, http.StatusUnprocessableEntity, "role_not_found", "role not found")
		default:
			log.Error(r.Context(), "AssignRole: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}
	respond.JSON(w, http.StatusOK, toUserRoleResponse(role))
}

// RemoveRole handles DELETE /admin/users/{user_id}/role.
func (h *Handler) RemoveRole(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")

	actingUserID, ok := adminshared.MustUserID(w, r)
	if !ok {
		return
	}

	err := h.svc.RemoveRole(r.Context(), userID, actingUserID)
	if err != nil {
		switch {
		case errors.Is(err, platformrbac.ErrCannotModifyOwnRole):
			respond.Error(w, http.StatusConflict, "cannot_modify_own_role", "you cannot modify your own role assignment")
		case errors.Is(err, platformrbac.ErrCannotReassignOwner):
			respond.Error(w, http.StatusConflict, "cannot_reassign_owner", "owner role cannot be reassigned via this route")
		case errors.Is(err, ErrUserRoleNotFound):
			respond.Error(w, http.StatusNotFound, "user_role_not_found", "user has no active role assignment")
		case errors.Is(err, ErrLastOwnerRemoval):
			respond.Error(w, http.StatusConflict, "last_owner_removal", "cannot remove the last active owner")
		default:
			log.Error(r.Context(), "RemoveRole: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}
	respond.NoContent(w)
}


