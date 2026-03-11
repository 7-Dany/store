// Package rbacshared holds primitives shared across all rbac feature sub-packages.
// It must never import any feature package (bootstrap, admin, …).
package rbacshared

import "errors"

// ── Cross-feature sentinel errors ────────────────────────────────────────────

// ErrUserNotFound is returned when the target user record cannot be located.
var ErrUserNotFound = errors.New("user not found")
