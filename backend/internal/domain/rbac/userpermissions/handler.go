package userpermissions

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	rbacshared "github.com/7-Dany/store/backend/internal/domain/rbac/shared"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
	"github.com/go-chi/chi/v5"
)

// Servicer is the subset of the service that the handler requires.
// *Service satisfies this interface; tests supply a UserPermissionsFakeServicer.
type Servicer interface {
	ListPermissions(ctx context.Context, targetUserID string) ([]UserPermission, error)
	GrantPermission(ctx context.Context, targetUserID, actingUserID string, in GrantPermissionInput) (UserPermission, error)
	RevokePermission(ctx context.Context, targetUserID, grantID, actingUserID string) error
}

// Handler is the HTTP layer for the userpermissions package.
type Handler struct {
	svc Servicer
}

// NewHandler constructs a Handler.
func NewHandler(svc Servicer) *Handler {
	return &Handler{svc: svc}
}

// ListPermissions handles GET /admin/rbac/users/{user_id}/permissions.
func (h *Handler) ListPermissions(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")

	perms, err := h.svc.ListPermissions(r.Context(), userID)
	if err != nil {
		if errors.Is(err, ErrGrantNotFound) {
			respond.Error(w, http.StatusNotFound, "user_not_found", "user not found")
		} else {
			slog.ErrorContext(r.Context(), "userpermissions.ListPermissions: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	resp := make([]userPermissionResponse, len(perms))
	for i, p := range perms {
		resp[i] = toPermissionResponse(p)
	}
	respond.JSON(w, http.StatusOK, listPermissionsResponse{Permissions: resp})
}

// GrantPermission handles POST /admin/rbac/users/{user_id}/permissions.
func (h *Handler) GrantPermission(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	userID := chi.URLParam(r, "user_id")

	actingUserID, ok := h.mustUserID(w, r)
	if !ok {
		return
	}

	req, ok := respond.DecodeJSON[grantPermissionRequest](w, r)
	if !ok {
		return
	}

	// Handler-level validation (defence-in-depth; service validates again).
	in := GrantPermissionInput{
		PermissionID:  req.PermissionID,
		GrantedReason: req.GrantedReason,
		Scope:         req.Scope,
		Conditions:    req.Conditions,
	}
	if req.ExpiresAt != nil {
		in.ExpiresAt = *req.ExpiresAt
	}

	if err := validateGrantPermission(in); err != nil {
		h.writeGrantError(w, r, err)
		return
	}

	perm, err := h.svc.GrantPermission(r.Context(), userID, actingUserID, in)
	if err != nil {
		h.writeGrantError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusCreated, toPermissionResponse(perm))
}

// RevokePermission handles DELETE /admin/rbac/users/{user_id}/permissions/{grant_id}.
func (h *Handler) RevokePermission(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	grantID := chi.URLParam(r, "grant_id")

	actingUserID, ok := h.mustUserID(w, r)
	if !ok {
		return
	}

	err := h.svc.RevokePermission(r.Context(), userID, grantID, actingUserID)
	if err != nil {
		if errors.Is(err, ErrGrantNotFound) {
			respond.Error(w, http.StatusNotFound, "grant_not_found", "permission grant not found")
		} else {
			slog.ErrorContext(r.Context(), "userpermissions.RevokePermission: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}
	respond.NoContent(w)
}

// ── private helpers ───────────────────────────────────────────────────────────

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

// writeGrantError maps any error from validateGrantPermission or svc.GrantPermission
// to the appropriate HTTP response. Validation sentinels and service-layer
// sentinels share the same switch so the mapping is defined exactly once.
func (h *Handler) writeGrantError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrPermissionIDEmpty):
		respond.Error(w, http.StatusUnprocessableEntity, "permission_id_required", "permission_id is required")
	case errors.Is(err, ErrGrantedReasonEmpty):
		respond.Error(w, http.StatusUnprocessableEntity, "granted_reason_required", "granted_reason is required")
	case errors.Is(err, ErrExpiresAtRequired):
		respond.Error(w, http.StatusUnprocessableEntity, "expires_at_required", "expires_at is required")
	case errors.Is(err, ErrExpiresAtInPast):
		respond.Error(w, http.StatusUnprocessableEntity, "expires_at_in_past", "expires_at must be in the future")
	case errors.Is(err, rbacshared.ErrScopeNotAllowed):
		respond.Error(w, http.StatusUnprocessableEntity, "scope_not_allowed", "scope is not permitted for this permission")
	case errors.Is(err, ErrPermissionNotFound):
		respond.Error(w, http.StatusUnprocessableEntity, "permission_not_found", "permission not found")
	case errors.Is(err, ErrPermissionAlreadyGranted):
		respond.Error(w, http.StatusConflict, "permission_already_granted", "permission already granted to this user")
	case errors.Is(err, ErrPrivilegeEscalation):
		respond.Error(w, http.StatusForbidden, "privilege_escalation", "granter does not hold this permission")
	default:
		slog.ErrorContext(r.Context(), "userpermissions.GrantPermission: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
