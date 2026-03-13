package userroles

import (
	"context"

	"github.com/7-Dany/store/backend/internal/app"
	platformrbac "github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/go-chi/chi/v5"
)

// Routes registers all user-role endpoints on r.
// Called from adminRoutes in internal/domain/rbac/routes.go:
//
//	userroles.Routes(ctx, r, deps)
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	store := NewStore(deps.Pool)
	svc := NewService(store)
	h := NewHandler(svc)

	r.With(deps.JWTAuth, deps.RBAC.Require(platformrbac.PermRBACRead)).
		Get("/rbac/users/{user_id}/role", h.GetUserRole)

	r.With(deps.JWTAuth, deps.RBAC.Require(platformrbac.PermRBACManage)).
		Put("/rbac/users/{user_id}/role", h.AssignRole)

	r.With(deps.JWTAuth, deps.RBAC.Require(platformrbac.PermRBACManage)).
		Delete("/rbac/users/{user_id}/role", h.RemoveRole)
}
