package userlock

import (
	"errors"

	rbacshared "github.com/7-Dany/store/backend/internal/domain/rbac/shared"
)

// ErrUserNotFound aliases rbacshared.ErrUserNotFound so callers can reference
// the sentinel via this package and cross-package errors.Is checks still match.
// Applies to all three operations (lock, unlock, get status).
var ErrUserNotFound = rbacshared.ErrUserNotFound

// ErrReasonRequired is returned when LockUser receives an empty reason string.
var ErrReasonRequired = errors.New("reason is required")
