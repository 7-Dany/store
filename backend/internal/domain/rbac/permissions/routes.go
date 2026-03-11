package permissions

import (
	"context"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/rbac"
)

// Routes registers GET /permissions and GET /permissions/groups on r.
// Call from AdminRoutes in internal/domain/rbac/routes.go:
//
//	permissions.Routes(ctx, r, deps)
//
// Both routes require a valid JWT and the rbac:read permission.
// No additional rate limiter — admin routes are already JWT-gated.
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	store := NewStore(deps.Pool)
	svc := NewService(store)
	h := NewHandler(svc)

	r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACRead)).
		Get("/permissions", h.ListPermissions)

	r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACRead)).
		Get("/permissions/groups", h.ListPermissionGroups)
}
