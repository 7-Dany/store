package roles

import (
	"context"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/go-chi/chi/v5"
)

// Routes registers all roles endpoints on r.
// Called from adminRoutes in internal/domain/rbac/routes.go:
//
//	roles.Routes(ctx, r, deps)
//
// Rate limits: none — access is controlled by the RBAC middleware
// (deps.RBAC.Require) on every route. Endpoint-level rate limiting
// can be added here when required.
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	store := NewStore(deps.Pool)
	svc := NewService(store)
	h := NewHandler(svc)

	r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACRead)).
		Get("/rbac/roles", h.ListRoles)

	r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACManage)).
		Post("/rbac/roles", h.CreateRole)

	r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACRead)).
		Get("/rbac/roles/{id}", h.GetRole)

	r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACManage)).
		Patch("/rbac/roles/{id}", h.UpdateRole)

	r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACManage)).
		Delete("/rbac/roles/{id}", h.DeleteRole)

	r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACRead)).
		Get("/rbac/roles/{id}/permissions", h.ListRolePermissions)

	r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACManage)).
		Post("/rbac/roles/{id}/permissions", h.AddRolePermission)

	r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACManage)).
		Delete("/rbac/roles/{id}/permissions/{perm_id}", h.RemoveRolePermission)
}
