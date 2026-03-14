package userpermissions

import "errors"

// ErrPermissionNotFound is returned when GrantPermission receives a
// permission_id that does not correspond to an active permission.
var ErrPermissionNotFound = errors.New("permission not found")

// ErrGrantNotFound is returned when RevokePermission targets a grant_id
// that either does not exist or belongs to a different user.
var ErrGrantNotFound = errors.New("permission grant not found")

// ErrPermissionAlreadyGranted is returned when GrantPermission finds an
// active (non-expired) grant for the same (user, permission) pair.
// The caller must revoke the existing grant before re-granting.
var ErrPermissionAlreadyGranted = errors.New("permission already granted to this user")

// ErrPrivilegeEscalation is returned when the granter does not hold the
// permission themselves. Maps from fn_prevent_privilege_escalation trigger
// (SQLSTATE 23514 + "privilege escalation" in the message).
var ErrPrivilegeEscalation = errors.New("granter does not hold this permission")

// Validation sentinels
var ErrPermissionIDEmpty  = errors.New("permission_id is required")
var ErrGrantedReasonEmpty = errors.New("granted_reason is required")
var ErrExpiresAtRequired  = errors.New("expires_at is required")
var ErrExpiresAtInPast    = errors.New("expires_at must be in the future")
