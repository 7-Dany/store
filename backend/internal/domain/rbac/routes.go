package rbac

import (
	"context"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/domain/rbac/bootstrap"
	"github.com/7-Dany/store/backend/internal/domain/rbac/permissions"
	"github.com/7-Dany/store/backend/internal/domain/rbac/roles"
)

// Routes builds and returns the full rbac chi.Mux, mounting the /owner and
// /admin sub-routers internally. Callers in server/routes.go mount the result
// at the api root:
//
//	r.Mount("/", rbac.Routes(ctx, deps))
func Routes(ctx context.Context, deps *app.Deps) *chi.Mux {
	r := chi.NewRouter()
	r.Mount("/owner", ownerRoutes(ctx, deps))
	r.Mount("/admin", adminRoutes(ctx, deps))
	return r
}

// ownerRoutes returns the /owner sub-router (unauthenticated).
func ownerRoutes(ctx context.Context, deps *app.Deps) *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimiddleware.AllowContentType("application/json"))
	bootstrap.Routes(ctx, r, deps)
	return r
}

// adminRoutes returns the /admin sub-router (JWT-auth required on all routes).
func adminRoutes(ctx context.Context, deps *app.Deps) *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimiddleware.AllowContentType("application/json"))
	permissions.Routes(ctx, r, deps)
	roles.Routes(ctx, r, deps) // Phase 6
	// Phases 7–9 will mount here.
	return r
}
