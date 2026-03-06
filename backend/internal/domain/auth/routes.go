// Package auth assembles the auth domain sub-router.
package auth

import (
	"context"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/domain/auth/login"
	"github.com/7-Dany/store/backend/internal/domain/auth/password"
	"github.com/7-Dany/store/backend/internal/domain/auth/profile"
	"github.com/7-Dany/store/backend/internal/domain/auth/register"
	"github.com/7-Dany/store/backend/internal/domain/auth/session"
	"github.com/7-Dany/store/backend/internal/domain/auth/unlock"
	"github.com/7-Dany/store/backend/internal/domain/auth/verification"
)

// Routes returns a self-contained chi sub-router for all /auth endpoints.
// Mount at /api/v1/auth in the server router:
//
//	r.Mount("/auth", auth.Routes(ctx, deps))
//
// ctx is the application root context passed to every sub-router so their
// rate-limiter cleanup goroutines stop on graceful shutdown.
func Routes(ctx context.Context, deps *app.Deps) *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimiddleware.AllowContentType("application/json"))

	register.Routes(ctx, r, deps)
	verification.Routes(ctx, r, deps)
	login.Routes(ctx, r, deps)
	session.Routes(ctx, r, deps)
	unlock.Routes(ctx, r, deps)
	password.Routes(ctx, r, deps)
	profile.Routes(ctx, r, deps)
	profile.Routes(ctx, r, deps)

	return r
}
