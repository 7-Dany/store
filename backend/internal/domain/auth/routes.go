// Package auth assembles the auth domain sub-router.
// Serves endpoints under /api/v1/auth.
package auth

import (
	"context"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/domain/auth/login"
	"github.com/7-Dany/store/backend/internal/domain/auth/password"
	"github.com/7-Dany/store/backend/internal/domain/auth/register"
	"github.com/7-Dany/store/backend/internal/domain/auth/session"
	"github.com/7-Dany/store/backend/internal/domain/auth/unlock"
	"github.com/7-Dany/store/backend/internal/domain/auth/verification"
)

// Mount registers the auth sub-router at /auth on r.
// Call from domain.Mount in internal/domain/routes.go:
//
//	auth.Mount(ctx, r, deps)
func Mount(ctx context.Context, r chi.Router, deps *app.Deps) {
	r.Mount("/auth", Routes(ctx, deps))
}

// Routes returns a self-contained chi sub-router for all /auth endpoints.
// Called by Mount. Use directly in tests to exercise the auth sub-domain.
//
//	r.Mount("/auth", auth.Routes(ctx, deps))
//
// Endpoints:
//
//	POST  /register              — create a new account
//	POST  /verification          — verify email address with OTP
//	POST  /verification/resend   — resend verification OTP
//	POST  /login                 — authenticate and receive tokens
//	POST  /refresh               — rotate refresh token, issue new access token
//	POST  /logout                — revoke refresh token
//	POST  /password/reset        — request password-reset OTP
//	POST  /password/reset/verify — verify reset OTP, receive grant token
//	PUT   /password/reset        — set new password with grant token
//	PATCH /password              — change password (authenticated)
//	POST  /unlock                — request account-unlock OTP
//	PUT   /unlock                — confirm unlock OTP
func Routes(ctx context.Context, deps *app.Deps) *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimiddleware.AllowContentType("application/json"))

	register.Routes(ctx, r, deps)      // POST /register
	verification.Routes(ctx, r, deps)  // POST /verification, /verification/resend
	login.Routes(ctx, r, deps)         // POST /login
	session.Routes(ctx, r, deps)       // POST /refresh, /logout
	unlock.Routes(ctx, r, deps)        // POST /unlock; PUT /unlock
	password.Routes(ctx, r, deps)      // POST /password/reset, /password/reset/verify; PUT /password/reset; PATCH /password

	return r
}
