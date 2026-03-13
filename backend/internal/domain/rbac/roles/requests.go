package roles

import (
	"encoding/json"
	"time"
)

// ── HTTP request structs ──────────────────────────────────────────────────────

// createRoleRequest is the JSON body for POST /admin/rbac/roles.
type createRoleRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// updateRoleRequest is the JSON body for PATCH /admin/rbac/roles/:id.
// All fields are optional — at least one must be non-nil after parsing
// (enforced by validateUpdateRole).
type updateRoleRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// addRolePermissionRequest is the JSON body for POST /admin/rbac/roles/:id/permissions.
type addRolePermissionRequest struct {
	PermissionID  string          `json:"permission_id"`
	AccessType    string          `json:"access_type"`
	Scope         string          `json:"scope"`
	Conditions    json.RawMessage `json:"conditions,omitempty"`
	GrantedReason string          `json:"granted_reason"`
}

// ── HTTP response structs ─────────────────────────────────────────────────────

// roleResponse is the JSON representation of a Role returned to callers.
type roleResponse struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description,omitempty"`
	IsSystemRole bool      `json:"is_system_role"`
	IsOwnerRole  bool      `json:"is_owner_role"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
}

// rolePermissionResponse is the JSON representation of a RolePermission.
type rolePermissionResponse struct {
	PermissionID  string          `json:"permission_id"`
	CanonicalName string          `json:"canonical_name"`
	ResourceType  string          `json:"resource_type"`
	Name          string          `json:"name"`
	AccessType    string          `json:"access_type"`
	Scope         string          `json:"scope"`
	Conditions    json.RawMessage `json:"conditions,omitempty"`
	GrantedAt     time.Time       `json:"granted_at"`
}

// ── Mappers ───────────────────────────────────────────────────────────────────

func toRoleResponse(r Role) roleResponse {
	return roleResponse{
		ID:           r.ID,
		Name:         r.Name,
		Description:  r.Description,
		IsSystemRole: r.IsSystemRole,
		IsOwnerRole:  r.IsOwnerRole,
		IsActive:     r.IsActive,
		CreatedAt:    r.CreatedAt,
	}
}

func toRolePermissionResponse(p RolePermission) rolePermissionResponse {
	return rolePermissionResponse{
		PermissionID:  p.PermissionID,
		CanonicalName: p.CanonicalName,
		ResourceType:  p.ResourceType,
		Name:          p.Name,
		AccessType:    p.AccessType,
		Scope:         p.Scope,
		Conditions:    p.Conditions,
		GrantedAt:     p.GrantedAt,
	}
}
