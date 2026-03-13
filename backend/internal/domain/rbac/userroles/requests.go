package userroles

import "time"

// ── HTTP request structs ──────────────────────────────────────────────────────

// assignRoleRequest is the JSON body for PUT /admin/rbac/users/{user_id}/role.
type assignRoleRequest struct {
	RoleID        string     `json:"role_id"`
	GrantedReason string     `json:"granted_reason"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
}

// ── HTTP response structs ─────────────────────────────────────────────────────

// userRoleResponse is the JSON shape returned for GET and PUT.
type userRoleResponse struct {
	UserID        string     `json:"user_id"`
	RoleID        string     `json:"role_id"`
	RoleName      string     `json:"role_name"`
	IsOwnerRole   bool       `json:"is_owner_role"`
	GrantedReason string     `json:"granted_reason"`
	GrantedAt     time.Time  `json:"granted_at"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
}

// ── Mapper ────────────────────────────────────────────────────────────────────

func toUserRoleResponse(ur UserRole) userRoleResponse {
	return userRoleResponse{
		UserID:        ur.UserID,
		RoleID:        ur.RoleID,
		RoleName:      ur.RoleName,
		IsOwnerRole:   ur.IsOwnerRole,
		GrantedReason: ur.GrantedReason,
		GrantedAt:     ur.GrantedAt,
		ExpiresAt:     ur.ExpiresAt,
	}
}
