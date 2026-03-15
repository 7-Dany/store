// Package profile assembles the profile domain sub-router.
// Serves endpoints under /api/v1/profile.
package profile

import (
	"context"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/7-Dany/store/backend/internal/app"
	deleteaccount "github.com/7-Dany/store/backend/internal/domain/profile/delete-account"
	"github.com/7-Dany/store/backend/internal/domain/profile/email"
	"github.com/7-Dany/store/backend/internal/domain/profile/me"
	"github.com/7-Dany/store/backend/internal/domain/profile/session"
	setpassword "github.com/7-Dany/store/backend/internal/domain/profile/set-password"
	"github.com/7-Dany/store/backend/internal/domain/profile/username"
)

// Mount registers the profile sub-router at /profile on r.
// Call from domain.Mount in internal/domain/routes.go:
//
//	profile.Mount(ctx, r, deps)
func Mount(ctx context.Context, r chi.Router, deps *app.Deps) {
	r.Mount("/profile", Routes(ctx, deps))
}

// Routes returns a self-contained chi sub-router for all /profile endpoints.
// Called by Mount. Use directly in tests to exercise the profile sub-domain.
//
//	r.Mount("/profile", profile.Routes(ctx, deps))
//
// Endpoints:
//
//	GET    /me                      — authenticated user's public profile
//	PATCH  /me                      — update display_name and/or avatar_url
//	GET    /me/identities           — list linked OAuth identities
//	POST   /me/password             — set a password for OAuth-only accounts
//	GET    /me/sessions             — list active sessions
//	DELETE /me/sessions/{id}        — revoke a specific session
//	GET    /me/username/available   — check whether a username is unclaimed (public)
//	PATCH  /me/username             — set or update the authenticated user's username
//	POST   /me/email                — step 1: request email change OTP
//	POST   /me/email/verify         — step 2: verify current-email OTP, receive grant token
//	PUT    /me/email                — step 3: confirm new-email OTP, commit change
//	DELETE /me                      — initiate account deletion
//	DELETE /me/deletion             — cancel pending account deletion
//	GET    /me/deletion             — inspect deletion requirements
func Routes(ctx context.Context, deps *app.Deps) *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimiddleware.AllowContentType("application/json"))

	setpassword.Routes(ctx, r, deps)   // POST /me/password
	me.Routes(ctx, r, deps)            // GET /me, PATCH /me, GET /me/identities
	session.Routes(ctx, r, deps)       // GET /me/sessions; DELETE /me/sessions/{id}
	username.Routes(ctx, r, deps)      // GET /me/username/available; PATCH /me/username
	email.Routes(ctx, r, deps)         // POST /me/email; POST /me/email/verify; PUT /me/email
	deleteaccount.Routes(ctx, r, deps) // DELETE /me; DELETE /me/deletion; GET /me/deletion
	return r
}
