// Package profile assembles the profile domain sub-router.
// Serves endpoints under /api/v1/profile.
// See docs/map/PROFILE_MIGRATION.md.
package profile

import (
	"context"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/domain/profile/me"
	"github.com/7-Dany/store/backend/internal/domain/profile/session"
	setpassword "github.com/7-Dany/store/backend/internal/domain/profile/set-password"
)

// Routes registers all profile domain endpoints on r.
//
// Endpoints:
//   - GET    /me           — authenticated user's public profile
//   - PATCH  /me           — update display_name and/or avatar_url
//   - POST   /set-password — set a password for OAuth-only accounts
//   - GET    /sessions     — list active sessions
//   - DELETE /sessions/{id} — revoke a specific session
func Routes(ctx context.Context, deps *app.Deps) *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimiddleware.AllowContentType("application/json"))

	setpassword.Routes(ctx, r, deps)
	me.Routes(ctx, r, deps)
	session.Routes(ctx, r, deps)

	return r
}
