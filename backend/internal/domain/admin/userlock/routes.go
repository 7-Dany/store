// Package userlock registers the POST, DELETE, and GET /users/{user_id}/lock endpoints.
package userlock

import (
	"context"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	platformrbac "github.com/7-Dany/store/backend/internal/platform/rbac"
)

// Routes registers all user-lock endpoints on r.
// Call from admin.Routes in internal/domain/admin/routes.go:
//
//	userlock.Routes(ctx, r, deps)
//
// Rate limits: none — access is controlled by the RBAC middleware on every route.
//
// Middleware ordering (all routes):
//
//	JWTAuth → RBAC.Require(Perm*) → handler.{Method}
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	store := NewStore(deps.Pool)
	svc := NewService(store, deps.KVStore)
	h := NewHandler(svc)

	r.With(deps.JWTAuth, deps.RBAC.Require(platformrbac.PermUserLock)).
		Post("/users/{user_id}/lock", h.LockUser)

	r.With(deps.JWTAuth, deps.RBAC.Require(platformrbac.PermUserLock)).
		Delete("/users/{user_id}/lock", h.UnlockUser)

	r.With(deps.JWTAuth, deps.RBAC.Require(platformrbac.PermUserRead)).
		Get("/users/{user_id}/lock", h.GetLockStatus)
}
