// Package rbacshared holds primitives shared across all rbac feature sub-packages.
// It must never import any feature package (bootstrap, admin, …).
package rbacshared

import "errors"

// ── Cross-feature sentinel errors ────────────────────────────────────────────

// ErrUserNotFound is returned when the target user record cannot be located.
var ErrUserNotFound = errors.New("user not found")

// ErrScopeNotAllowed is returned when a permission grant specifies a scope
// value that the permission's scope_policy does not permit.
// Defined here so both the roles and userpermissions packages can return and
// check the same sentinel without importing each other (ADR-010).
var ErrScopeNotAllowed = errors.New("scope is not permitted for this permission")
