// Package userroles provides the HTTP handler, service, and store for
// user role assignment in the RBAC domain.
package userroles

import "time"

// UserRole is the service-layer representation of a user's active role assignment.
// GrantedBy is intentionally omitted: the GetUserRole SQL query does not SELECT
// ur.granted_by (it is stored in user_roles but not needed by the read endpoint).
// Use AssignRoleTxInput.GrantedBy when writing; do not read it back via GetUserRole.
type UserRole struct {
	UserID        string
	RoleID        string
	RoleName      string
	IsOwnerRole   bool
	GrantedReason string
	GrantedAt     time.Time
	ExpiresAt     *time.Time
}

// AssignRoleInput is the service-layer input for PUT /users/:user_id/role.
// GrantedBy is the calling user's ID — set by the handler from the JWT context,
// never taken from the request body.
type AssignRoleInput struct {
	RoleID        string
	GrantedBy     string
	GrantedReason string
	ExpiresAt     *time.Time
}

// AssignRoleTxInput is the store-layer input for the upsert + re-read transaction.
// Carries parsed [16]byte IDs so the store does no string parsing.
type AssignRoleTxInput struct {
	UserID        [16]byte
	RoleID        [16]byte
	GrantedBy     [16]byte
	GrantedReason string
	ExpiresAt     *time.Time
}
