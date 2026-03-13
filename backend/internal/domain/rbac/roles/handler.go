package roles

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Servicer is the subset of the service that the handler requires.
// *Service satisfies this interface; tests supply a RolesFakeServicer.
type Servicer interface {
	ListRoles(ctx context.Context) ([]Role, error)
	GetRole(ctx context.Context, roleID string) (Role, error)
	CreateRole(ctx context.Context, in CreateRoleInput) (Role, error)
	UpdateRole(ctx context.Context, roleID string, in UpdateRoleInput) (Role, error)
	DeleteRole(ctx context.Context, roleID string) error
	ListRolePermissions(ctx context.Context, roleID string) ([]RolePermission, error)
	AddRolePermission(ctx context.Context, roleID string, in AddRolePermissionInput) error
	RemoveRolePermission(ctx context.Context, roleID, permID, actingUserID string) error
}

// Handler is the HTTP layer for the roles package.
type Handler struct {
	svc Servicer
}

// NewHandler constructs a Handler.
func NewHandler(svc Servicer) *Handler {
	return &Handler{svc: svc}
}

// ListRoles handles GET /admin/rbac/roles.
func (h *Handler) ListRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := h.svc.ListRoles(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "roles.ListRoles: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	resp := make([]roleResponse, len(roles))
	for i, role := range roles {
		resp[i] = toRoleResponse(role)
	}
	respond.JSON(w, http.StatusOK, map[string]any{"roles": resp})
}

// CreateRole handles POST /admin/rbac/roles.
func (h *Handler) CreateRole(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)
	req, ok := respond.DecodeJSON[createRoleRequest](w, r)
	if !ok {
		return
	}
	if err := validateCreateRole(&req); err != nil {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	role, err := h.svc.CreateRole(r.Context(), CreateRoleInput{
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "roles.CreateRole: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	respond.JSON(w, http.StatusCreated, toRoleResponse(role))
}

// GetRole handles GET /admin/rbac/roles/{id}.
func (h *Handler) GetRole(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	role, err := h.svc.GetRole(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, ErrRoleNotFound):
			respond.Error(w, http.StatusNotFound, "role_not_found", "role not found")
		default:
			slog.ErrorContext(r.Context(), "roles.GetRole: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}
	respond.JSON(w, http.StatusOK, toRoleResponse(role))
}

// UpdateRole handles PATCH /admin/rbac/roles/{id}.
func (h *Handler) UpdateRole(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)
	req, ok := respond.DecodeJSON[updateRoleRequest](w, r)
	if !ok {
		return
	}
	if err := validateUpdateRole(&req); err != nil {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	role, err := h.svc.UpdateRole(r.Context(), id, UpdateRoleInput{
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrRoleNotFound):
			respond.Error(w, http.StatusNotFound, "role_not_found", "role not found")
		case errors.Is(err, rbac.ErrSystemRoleImmutable):
			respond.Error(w, http.StatusConflict, "system_role_immutable", "system roles cannot be modified")
		case errors.Is(err, ErrRoleNameConflict):
			respond.Error(w, http.StatusConflict, "role_name_conflict", "a role with this name already exists")
		default:
			slog.ErrorContext(r.Context(), "roles.UpdateRole: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}
	respond.JSON(w, http.StatusOK, toRoleResponse(role))
}

// DeleteRole handles DELETE /admin/rbac/roles/{id}.
func (h *Handler) DeleteRole(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	err := h.svc.DeleteRole(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, ErrRoleNotFound):
			respond.Error(w, http.StatusNotFound, "role_not_found", "role not found")
		case errors.Is(err, rbac.ErrSystemRoleImmutable):
			respond.Error(w, http.StatusConflict, "system_role_immutable", "system roles cannot be modified")
		default:
			slog.ErrorContext(r.Context(), "roles.DeleteRole: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}
	respond.NoContent(w)
}

// ListRolePermissions handles GET /admin/rbac/roles/{id}/permissions.
func (h *Handler) ListRolePermissions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	perms, err := h.svc.ListRolePermissions(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, ErrRoleNotFound):
			respond.Error(w, http.StatusNotFound, "role_not_found", "role not found")
		default:
			slog.ErrorContext(r.Context(), "roles.ListRolePermissions: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}
	resp := make([]rolePermissionResponse, len(perms))
	for i, p := range perms {
		resp[i] = toRolePermissionResponse(p)
	}
	respond.JSON(w, http.StatusOK, map[string]any{"permissions": resp})
}

// AddRolePermission handles POST /admin/rbac/roles/{id}/permissions.
func (h *Handler) AddRolePermission(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.mustUserID(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)
	req, ok := respond.DecodeJSON[addRolePermissionRequest](w, r)
	if !ok {
		return
	}
	if err := validateAddRolePermission(&req); err != nil {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	permUUID, err := uuid.Parse(req.PermissionID)
	if err != nil {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", "invalid permission_id")
		return
	}
	callerUUID, _ := uuid.Parse(userID) // always valid — from JWT

	if err := h.svc.AddRolePermission(r.Context(), id, AddRolePermissionInput{
		PermissionID:  [16]byte(permUUID),
		GrantedBy:     [16]byte(callerUUID),
		GrantedReason: req.GrantedReason,
		AccessType:    req.AccessType,
		Scope:         req.Scope,
		Conditions:    req.Conditions,
		// The service applies the "{}" default when Conditions is empty.
	}); err != nil {
		switch {
		case errors.Is(err, ErrRoleNotFound):
			respond.Error(w, http.StatusNotFound, "role_not_found", "role not found")
		case errors.Is(err, ErrPermissionNotFound):
			respond.Error(w, http.StatusNotFound, "permission_not_found", "permission not found")
		case errors.Is(err, ErrGrantAlreadyExists):
			respond.Error(w, http.StatusConflict, "grant_already_exists",
				"this permission is already granted to the role — remove it first to change access_type or scope")
		case errors.Is(err, ErrAccessTypeNotAllowed):
			respond.Error(w, http.StatusUnprocessableEntity, "access_type_not_allowed",
				"access_type is not permitted for this permission")
		case errors.Is(err, ErrScopeNotAllowed):
			respond.Error(w, http.StatusUnprocessableEntity, "scope_not_allowed",
				"scope is not permitted for this permission")
		default:
			slog.ErrorContext(r.Context(), "roles.AddRolePermission: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}
	respond.NoContent(w)
}

// RemoveRolePermission handles DELETE /admin/rbac/roles/{id}/permissions/{perm_id}.
// mustUserID extracts the authenticated caller whose ID is recorded in the audit trail.
func (h *Handler) RemoveRolePermission(w http.ResponseWriter, r *http.Request) {
	actingUserID, ok := h.mustUserID(w, r)
	if !ok {
		return
	}
	roleID := chi.URLParam(r, "id")
	permID := chi.URLParam(r, "perm_id")
	err := h.svc.RemoveRolePermission(r.Context(), roleID, permID, actingUserID)
	if err != nil {
		switch {
		case errors.Is(err, ErrRolePermissionNotFound):
			respond.Error(w, http.StatusNotFound, "role_permission_not_found", "role permission grant not found")
		default:
			slog.ErrorContext(r.Context(), "roles.RemoveRolePermission: service error", "error", err)
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
