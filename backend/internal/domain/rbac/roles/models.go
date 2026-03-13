// Package roles provides the HTTP handler, service, and store for the roles
// admin API: CRUD on roles and management of role-permission grants.
package roles

import (
	"encoding/json"
	"time"
)

// Role is the service-layer representation of an RBAC role.
type Role struct {
	ID           string
	Name         string
	Description  string
	IsSystemRole bool
	IsOwnerRole  bool
	IsActive     bool
	CreatedAt    time.Time
}

// RolePermission is a permission assigned to a role with its access metadata.
type RolePermission struct {
	PermissionID  string
	CanonicalName string
	ResourceType  string
	Name          string
	AccessType    string
	Scope         string
	Conditions    json.RawMessage
	GrantedAt     time.Time
}

// CreateRoleInput is the service-layer input for creating a role.
type CreateRoleInput struct {
	Name        string
	Description string
}

// UpdateRoleInput is the service-layer input for patching a role.
// Only non-nil fields are applied (partial update).
type UpdateRoleInput struct {
	Name        *string
	Description *string
}

// PermissionCaps carries the capability flags for a single permission.
// Returned by GetPermissionCaps and used by AddRolePermission to validate
// that the incoming access_type and scope are legal for this permission.
type PermissionCaps struct {
	ID               [16]byte
	CanonicalName    string
	ScopePolicy      string // "none" | "own" | "all" | "any"
	AllowConditional bool
	AllowRequest     bool
}

// AddRolePermissionInput is the service-layer input for adding a permission to a role.
type AddRolePermissionInput struct {
	PermissionID  [16]byte
	GrantedBy     [16]byte
	GrantedReason string
	AccessType    string          // validated against db.PermissionAccessType values before passing here
	Scope         string          // validated against db.PermissionScope values before passing here
	Conditions    json.RawMessage // defaults to '{}' when not provided; applied by the service
}
