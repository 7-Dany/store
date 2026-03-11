// Package rbacsharedtest provides test-only helpers shared across all rbac
// feature sub-packages. It must never be imported by production code.
package rbacsharedtest

import (
	"context"

	"github.com/7-Dany/store/backend/internal/domain/rbac/bootstrap"
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
