// Package userpermissions provides the HTTP handler, service, and store for
// direct user-permission grants.
package userpermissions

import (
	"encoding/json"
	"time"
)

// UserPermission is the service-layer representation of an active direct grant.
type UserPermission struct {
	ID            string
	CanonicalName string
	Name          string
	ResourceType  string
	Scope         string          // "own" | "all"
	Conditions    json.RawMessage // may be nil (maps to `{}`)
	ExpiresAt     time.Time
	GrantedAt     time.Time
	GrantedReason string
}

// GrantPermissionInput is the service-layer input for POST /permissions.
// The granter identity is passed separately as actingUserID to GrantPermission
// so the service validates it independently of the permission request payload.
type GrantPermissionInput struct {
	PermissionID  string
	GrantedReason string
	Scope         string          // "own" | "all"; default "own" when empty
	Conditions    json.RawMessage // nil → store as `{}`
	ExpiresAt     time.Time       // required; DB trigger enforces 5 min – 90 days
}

// GrantPermissionTxInput is the store-layer input with parsed [16]byte IDs.
type GrantPermissionTxInput struct {
	UserID        [16]byte
	PermissionID  [16]byte
	GrantedBy     [16]byte
	GrantedReason string
	Scope         string
	Conditions    json.RawMessage
	ExpiresAt     time.Time
}
