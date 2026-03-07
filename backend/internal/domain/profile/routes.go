// Package profile assembles the profile domain sub-router.
// Serves endpoints under /api/v1/profile.
// See docs/map/PROFILE_MIGRATION.md.
package profile

import (
	"context"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/domain/profile/email"
	"github.com/7-Dany/store/backend/internal/domain/profile/me"
	"github.com/7-Dany/store/backend/internal/domain/profile/session"
	setpassword "github.com/7-Dany/store/backend/internal/domain/profile/set-password"
	"github.com/7-Dany/store/backend/internal/domain/profile/username"
)

// Routes registers all profile domain endpoints on r.
//
// Endpoints:
//   - GET    /me                 — authenticated user's public profile
//   - PATCH  /me                 — update display_name and/or avatar_url
//   - POST   /set-password       — set a password for OAuth-only accounts
//   - GET    /sessions           — list active sessions
//   - DELETE /sessions/{id}      — revoke a specific session
//   - GET    /username/available — check whether a username is unclaimed (public)
//   - PATCH  /me/username        — set or update the authenticated user's username
//   - POST   /email/request-change  — step 1: request email change OTP (authenticated)
//   - POST   /email/verify-current  — step 2: verify current-email OTP, receive grant token (authenticated)
//   - POST   /email/confirm-change  — step 3: confirm new-email OTP, commit change (authenticated)
func Routes(ctx context.Context, deps *app.Deps) *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimiddleware.AllowContentType("application/json"))

	setpassword.Routes(ctx, r, deps)
	me.Routes(ctx, r, deps)
	session.Routes(ctx, r, deps)
	username.Routes(ctx, r, deps)
	email.Routes(ctx, r, deps)
	return r
}
