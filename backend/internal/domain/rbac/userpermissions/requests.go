package userpermissions

import (
	"encoding/json"
	"time"
)

// ── HTTP request structs ──────────────────────────────────────────────────────

type grantPermissionRequest struct {
	PermissionID  string          `json:"permission_id"`
	GrantedReason string          `json:"granted_reason"`
	Scope         string          `json:"scope,omitempty"`
	Conditions    json.RawMessage `json:"conditions,omitempty"`
	ExpiresAt     *time.Time      `json:"expires_at"`
}

// ── HTTP response structs ─────────────────────────────────────────────────────

type userPermissionResponse struct {
	ID            string          `json:"id"`
	CanonicalName string          `json:"canonical_name"`
	Name          string          `json:"name"`
	ResourceType  string          `json:"resource_type"`
	Scope         string          `json:"scope"`
	Conditions    json.RawMessage `json:"conditions,omitempty"`
	ExpiresAt     time.Time       `json:"expires_at"`
	GrantedAt     time.Time       `json:"granted_at"`
	GrantedReason string          `json:"granted_reason"`
}

type listPermissionsResponse struct {
	Permissions []userPermissionResponse `json:"permissions"`
}

// ── Mapper ────────────────────────────────────────────────────────────────────

func toPermissionResponse(p UserPermission) userPermissionResponse {
	return userPermissionResponse{
		ID:            p.ID,
		CanonicalName: p.CanonicalName,
		Name:          p.Name,
		ResourceType:  p.ResourceType,
		Scope:         p.Scope,
		Conditions:    p.Conditions,
		ExpiresAt:     p.ExpiresAt,
		GrantedAt:     p.GrantedAt,
		GrantedReason: p.GrantedReason,
	}
}
