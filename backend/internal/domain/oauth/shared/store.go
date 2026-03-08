// Package oauthshared holds primitives shared by all oauth feature sub-packages.
package oauthshared

import (
	"github.com/jackc/pgx/v5/pgxpool"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
)

// BaseStore is a type alias for authshared.BaseStore, providing OAuth packages
// with the same pool, BeginOrBind, IsNoRows, and pgtype conversion helpers
// without duplicating the implementation.
type BaseStore = authshared.BaseStore

// NewBaseStore returns an oauthshared.BaseStore backed by the given pool.
// OAuth feature stores embed this as their base.
func NewBaseStore(pool *pgxpool.Pool) BaseStore {
	return authshared.NewBaseStore(pool)
}
