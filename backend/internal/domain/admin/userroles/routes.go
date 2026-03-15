// Package userroles registers the GET, PUT, and DELETE /users/{user_id}/role endpoints.
package userroles

import (
	"context"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	platformrbac "github.com/7-Dany/store/backend/internal/platform/rbac"
)

// Routes registers all user-role endpoints on r.
// Call from admin.Routes in internal/domain/admin/routes.go:
//
//	userroles.Routes(ctx, r, deps)
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

	r.With(deps.JWTAuth, deps.RBAC.Require(platformrbac.PermRBACRead)).
		Get("/users/{user_id}/role", h.GetUserRole)

	r.With(deps.JWTAuth, deps.RBAC.Require(platformrbac.PermRBACManage)).
		Put("/users/{user_id}/role", h.AssignRole)

	r.With(deps.JWTAuth, deps.RBAC.Require(platformrbac.PermRBACManage)).
		Delete("/users/{user_id}/role", h.RemoveRole)
}
