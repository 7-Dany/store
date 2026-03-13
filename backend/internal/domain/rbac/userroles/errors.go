package userroles

import "errors"

// ErrUserRoleNotFound is returned when GetUserRole finds no active assignment for the user.
var ErrUserRoleNotFound = errors.New("user has no active role assignment")

// ErrRoleNotFound is returned when AssignRole receives a role_id that does not
// correspond to an active role.
var ErrRoleNotFound = errors.New("role not found")

// ErrLastOwnerRemoval is returned when RemoveRole would leave the system with
// no active owner. Maps from the fn_prevent_orphaned_owner trigger
// (SQLSTATE 23000 — integrity_constraint_violation).
var ErrLastOwnerRemoval = errors.New("cannot remove the last active owner")

// ErrGrantedReasonEmpty is returned when granted_reason is blank after trimming.
var ErrGrantedReasonEmpty = errors.New("granted_reason is required")

// ErrRoleIDEmpty is returned when role_id is blank after trimming.
var ErrRoleIDEmpty = errors.New("role_id is required")
