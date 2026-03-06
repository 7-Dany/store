// Package profile assembles the profile domain sub-router.
// All routes are currently served under /auth because the profile domain shares
// the /api/v1/auth base path. When profile gets its own prefix the mount in
// internal/domain/auth/routes.go will move to internal/server/routes.go.
// See docs/map/PROFILE_MIGRATION.md.
package profile

import (
	"context"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	setpassword "github.com/7-Dany/store/backend/internal/domain/profile/set-password"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
)

// Routes registers all profile domain endpoints on r.
// r is the auth sub-router (already mounted at /api/v1/auth), so all profile
// endpoints inherit the /auth base path.
func Routes(ctx context.Context, deps *app.Deps) *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimiddleware.AllowContentType("application/json"))

	setpassword.Routes(ctx, r, deps)

	return r
}
