// Package session registers the GET /sessions and DELETE /sessions/{id} endpoints.
package session

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
)

// Routes registers the GET /sessions and DELETE /sessions/{id} endpoints on r.
// Call from profile.Routes in internal/domain/profile/routes.go:
//
//	session.Routes(ctx, r, deps)
//
// Rate limits:
//   - GET    /me/sessions:      10 req / 1 min  per IP  ("psess:ip:")
//   - DELETE /me/sessions/{id}:  3 req / 15 min per IP  ("rsess:ip:")
//
// Middleware ordering (all routes):
//
//	JWTAuth → IPRateLimiter → handler.{Method}
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	// 10 req / 1 min per IP — rate-limits session list polling.
	// rate = 10 / (1 * 60) ≈ 0.167 tokens/sec.
	sessionsLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "psess:ip:", 10.0/(1*60), 10, 1*time.Minute)
	go sessionsLimiter.StartCleanup(ctx)

	// 3 req / 15 min per IP — limits session revocation abuse.
	// rate = 3 / (15 * 60) ≈ 0.00333 tokens/sec.
	revokeSessionLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "rsess:ip:", 3.0/(15*60), 3, 15*time.Minute)
	go revokeSessionLimiter.StartCleanup(ctx)

	store := NewStore(deps.Pool)
	svc := NewService(store)
	h := NewHandler(svc)

	r.Group(func(r chi.Router) {
		r.Use(deps.JWTAuth)
		r.With(sessionsLimiter.Limit).Get("/me/sessions", h.Sessions)
		r.With(revokeSessionLimiter.Limit).Delete("/me/sessions/{id}", h.RevokeSession)
	})
}
