// Package profileshared holds primitives shared by all profile feature
// sub-packages. It must never import any feature package.
package profileshared

import authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"

// BaseStore is the profile domain's store base. It delegates all infrastructure
// to authshared.BaseStore, which is intentionally shared across the auth and
// profile domains until the profile domain has its own pool and helpers.
//
// See docs/map/PROFILE_MIGRATION.md for the long-term migration plan.
type BaseStore = authshared.BaseStore

// NewBaseStore constructs a BaseStore for a profile feature store.
var NewBaseStore = authshared.NewBaseStore
