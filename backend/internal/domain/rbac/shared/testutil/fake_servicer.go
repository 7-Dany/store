// Package rbacsharedtest provides test-only helpers shared across all rbac
// feature sub-packages. It must never be imported by production code.
package rbacsharedtest

import (
	"context"

	"github.com/7-Dany/store/backend/internal/domain/rbac/bootstrap"
	"github.com/7-Dany/store/backend/internal/domain/rbac/permissions"
)

// ─────────────────────────────────────────────────────────────────────────────
// BootstrapFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

// BootstrapFakeServicer is a hand-written implementation of bootstrap.Servicer
// for handler unit tests. Set BootstrapFn to control the response; leave it nil
// to return a zero BootstrapResult and nil error.
type BootstrapFakeServicer struct {
	BootstrapFn func(ctx context.Context, in bootstrap.BootstrapInput) (bootstrap.BootstrapResult, error)
}

// compile-time interface check.
var _ bootstrap.Servicer = (*BootstrapFakeServicer)(nil)

// Bootstrap delegates to BootstrapFn if set.
// Default: returns (BootstrapResult{}, nil).
func (f *BootstrapFakeServicer) Bootstrap(ctx context.Context, in bootstrap.BootstrapInput) (bootstrap.BootstrapResult, error) {
	if f.BootstrapFn != nil {
		return f.BootstrapFn(ctx, in)
	}
	return bootstrap.BootstrapResult{}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// PermissionsFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

// PermissionsFakeServicer is a hand-written implementation of permissions.Servicer
// for handler unit tests. Set the Fn fields to control responses; leave nil
// to return an empty slice and nil error.
type PermissionsFakeServicer struct {
	ListPermissionsFn      func(ctx context.Context) ([]permissions.Permission, error)
	ListPermissionGroupsFn func(ctx context.Context) ([]permissions.PermissionGroup, error)
}

// compile-time interface check.
var _ permissions.Servicer = (*PermissionsFakeServicer)(nil)

// ListPermissions delegates to ListPermissionsFn if set.
// Default: returns ([]permissions.Permission{}, nil).
func (f *PermissionsFakeServicer) ListPermissions(ctx context.Context) ([]permissions.Permission, error) {
	if f.ListPermissionsFn != nil {
		return f.ListPermissionsFn(ctx)
	}
	return []permissions.Permission{}, nil
}

// ListPermissionGroups delegates to ListPermissionGroupsFn if set.
// Default: returns ([]permissions.PermissionGroup{}, nil).
func (f *PermissionsFakeServicer) ListPermissionGroups(ctx context.Context) ([]permissions.PermissionGroup, error) {
	if f.ListPermissionGroupsFn != nil {
		return f.ListPermissionGroupsFn(ctx)
	}
	return []permissions.PermissionGroup{}, nil
}
