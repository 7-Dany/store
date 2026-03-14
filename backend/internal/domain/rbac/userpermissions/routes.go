package userpermissions

import (
	"context"

	"github.com/7-Dany/store/backend/internal/app"
	platformrbac "github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/go-chi/chi/v5"
)

// Routes registers all user-permission endpoints on r.
// Called from adminRoutes in internal/domain/rbac/routes.go:
//
//	userpermissions.Routes(ctx, r, deps)
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	store := NewStore(deps.Pool)
	svc := NewService(store)
	h := NewHandler(svc)

	r.With(deps.JWTAuth, deps.RBAC.Require(platformrbac.PermRBACRead)).
		Get("/rbac/users/{user_id}/permissions", h.ListPermissions)

	r.With(deps.JWTAuth, deps.RBAC.Require(platformrbac.PermRBACGrantUserPerm)).
		Post("/rbac/users/{user_id}/permissions", h.GrantPermission)

	r.With(deps.JWTAuth, deps.RBAC.Require(platformrbac.PermRBACGrantUserPerm)).
		Delete("/rbac/users/{user_id}/permissions/{grant_id}", h.RevokePermission)
}
