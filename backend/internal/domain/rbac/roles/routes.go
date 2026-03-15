// Package roles registers the GET/POST /roles, GET/PATCH/DELETE /roles/{id}, GET/POST /roles/{id}/permissions, and DELETE /roles/{id}/permissions/{perm_id} endpoints.
package roles

import (
	"context"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/rbac"
)

// Routes registers all roles endpoints on r.
// Call from rbac.Routes in internal/domain/rbac/routes.go:
//
//	roles.Routes(ctx, r, deps)
//
// Rate limits: none — access is controlled by the RBAC middleware on every route.
//
// Middleware ordering (all routes):
//
//	JWTAuth → RBAC.Require(Perm*) → handler.{Method}
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	store := NewStore(deps.Pool)
	svc := NewService(store)
	h := NewHandler(svc)

	r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACRead)).
		Get("/roles", h.ListRoles)

	r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACManage)).
		Post("/roles", h.CreateRole)

	r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACRead)).
		Get("/roles/{id}", h.GetRole)

	r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACManage)).
		Patch("/roles/{id}", h.UpdateRole)

	r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACManage)).
		Delete("/roles/{id}", h.DeleteRole)

	r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACRead)).
		Get("/roles/{id}/permissions", h.ListRolePermissions)

	r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACManage)).
		Post("/roles/{id}/permissions", h.AddRolePermission)

	r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACManage)).
		Delete("/roles/{id}/permissions/{perm_id}", h.RemoveRolePermission)
}
