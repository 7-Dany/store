package roles

import "errors"

// ── Store / service sentinel errors ──────────────────────────────────────────

// ErrRoleNotFound is returned when GetRoleByID finds no matching row, or when
// a service method receives an ID string that is not a valid UUID.
var ErrRoleNotFound = errors.New("role not found")

// ErrRolePermissionNotFound is returned when RemoveRolePermission affects 0 rows,
// or when either ID string supplied to RemoveRolePermission is not a valid UUID.
var ErrRolePermissionNotFound = errors.New("role permission grant not found")

// ErrPermissionNotFound is returned when AddRolePermission receives a
// permission_id that does not correspond to any active permission.
var ErrPermissionNotFound = errors.New("permission not found")

// ErrGrantAlreadyExists is returned when AddRolePermission finds the
// (role_id, permission_id) grant already exists on this role.
// The caller must remove the existing grant before re-adding with different
// access_type or scope.
var ErrGrantAlreadyExists = errors.New("permission grant already exists on this role")

// ── Validation sentinel errors ────────────────────────────────────────────────

// ErrNameEmpty is returned when a required name field is blank after trimming.
var ErrNameEmpty = errors.New("name is required")

// ErrNameTooLong is returned when a name field exceeds 100 characters.
var ErrNameTooLong = errors.New("name must be 100 characters or fewer")

// ErrNoUpdateFields is returned when an update request carries no fields to apply.
var ErrNoUpdateFields = errors.New("at least one field (name or description) must be provided")

// ErrInvalidAccessType is returned when access_type is not one of the recognised values.
var ErrInvalidAccessType = errors.New("access_type must be one of: direct, conditional, request, denied")

// ErrInvalidScope is returned when scope is not one of the recognised values.
var ErrInvalidScope = errors.New("scope must be one of: own, all")

// ErrPermissionIDEmpty is returned when the permission_id field is blank after trimming.
var ErrPermissionIDEmpty = errors.New("permission_id is required")

// ErrGrantedReasonEmpty is returned when the granted_reason field is blank after trimming.
var ErrGrantedReasonEmpty = errors.New("granted_reason is required")

// ErrRoleNameConflict is returned when a create or update would violate the
// unique constraint on role names (roles_name_key).
var ErrRoleNameConflict = errors.New("a role with this name already exists")

// ErrAccessTypeNotAllowed is returned when AddRolePermission receives an
// access_type that the permission's capability flags do not permit.
// E.g. access_type = 'conditional' when allow_conditional = FALSE.
var ErrAccessTypeNotAllowed = errors.New("access_type is not permitted for this permission")

// ErrScopeNotAllowed is returned when AddRolePermission receives a scope value
// that the permission's scope_policy does not permit.
// E.g. scope = 'own' when scope_policy = 'all'.
var ErrScopeNotAllowed = errors.New("scope is not permitted for this permission")
