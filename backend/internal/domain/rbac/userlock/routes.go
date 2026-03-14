package userlock

import (
	"context"

	"github.com/7-Dany/store/backend/internal/app"
	platformrbac "github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/go-chi/chi/v5"
)

// Routes registers all user-lock endpoints on r.
// Called from adminRoutes in internal/domain/rbac/routes.go:
//
//	userlock.Routes(ctx, r, deps)
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
