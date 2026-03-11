// Package rbacsharedtest provides test-only helpers shared across all rbac
// feature sub-packages. It must never be imported by production code.
package rbacsharedtest

import (
	"context"

	"github.com/7-Dany/store/backend/internal/domain/rbac/bootstrap"
)

// ─────────────────────────────────────────────────────────────────────────────
// BootstrapFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// BootstrapFakeStorer is a hand-written implementation of bootstrap.Storer for
// service unit tests. Each method delegates to its Fn field if non-nil,
// otherwise returns a safe default so tests only configure the fields they need.
//
// Defaults are chosen so that the happy path succeeds without any configuration:
//   - CountActiveOwners → (0, nil): no owner exists, service may proceed.
//   - GetOwnerRoleID    → ([16]byte{}, nil): zero UUID, sufficient for unit tests.
//   - GetActiveUserByID → (BootstrapUser{IsActive: true, EmailVerified: true}, nil):
//     a valid, fully-verified user; avoids false guard failures in tests that do
//     not care about user-state checks.
//   - BootstrapOwnerTx  → (BootstrapResult{}, nil): zero result, nil error.
type BootstrapFakeStorer struct {
	CountActiveOwnersFn func(ctx context.Context) (int64, error)
	GetOwnerRoleIDFn    func(ctx context.Context) ([16]byte, error)
	GetActiveUserByIDFn func(ctx context.Context, userID [16]byte) (bootstrap.BootstrapUser, error)
	BootstrapOwnerTxFn  func(ctx context.Context, in bootstrap.BootstrapTxInput) (bootstrap.BootstrapResult, error)
}

// compile-time interface check.
var _ bootstrap.Storer = (*BootstrapFakeStorer)(nil)

// CountActiveOwners delegates to CountActiveOwnersFn if set.
// Default: returns (0, nil) — no active owner, service proceeds to the next step.
func (f *BootstrapFakeStorer) CountActiveOwners(ctx context.Context) (int64, error) {
	if f.CountActiveOwnersFn != nil {
		return f.CountActiveOwnersFn(ctx)
	}
	return 0, nil
}

// GetOwnerRoleID delegates to GetOwnerRoleIDFn if set.
// Default: returns a zero [16]byte and nil error.
func (f *BootstrapFakeStorer) GetOwnerRoleID(ctx context.Context) ([16]byte, error) {
	if f.GetOwnerRoleIDFn != nil {
		return f.GetOwnerRoleIDFn(ctx)
	}
	return [16]byte{}, nil
}

// GetActiveUserByID delegates to GetActiveUserByIDFn if set.
// Default: returns a fully-active, email-verified BootstrapUser so tests that
// do not configure this field never trip the is_active or email_verified guard.
func (f *BootstrapFakeStorer) GetActiveUserByID(ctx context.Context, userID [16]byte) (bootstrap.BootstrapUser, error) {
	if f.GetActiveUserByIDFn != nil {
		return f.GetActiveUserByIDFn(ctx, userID)
	}
	return bootstrap.BootstrapUser{IsActive: true, EmailVerified: true}, nil
}

// BootstrapOwnerTx delegates to BootstrapOwnerTxFn if set.
// Default: returns a zero BootstrapResult and nil error.
func (f *BootstrapFakeStorer) BootstrapOwnerTx(ctx context.Context, in bootstrap.BootstrapTxInput) (bootstrap.BootstrapResult, error) {
	if f.BootstrapOwnerTxFn != nil {
		return f.BootstrapOwnerTxFn(ctx, in)
	}
	return bootstrap.BootstrapResult{}, nil
}
