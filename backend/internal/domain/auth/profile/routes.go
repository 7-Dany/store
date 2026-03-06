package profile

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
)

// Routes registers the profile endpoints on r.
// Call from the auth root assembler:
//
//	profile.Routes(ctx, r, deps)
//
// Rate limits:
//   - GET    /me:             20 req / 1 min  per IP
//   - GET    /sessions:       10 req / 1 min  per IP
//   - DELETE /sessions/{id}:   3 req / 15 min per IP
//   - PATCH  /me/profile:     10 req / 1 min  per IP
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	// 20 req / 1 min per IP — prevents bulk profile enumeration.
	// rate = 20 / (1 * 60) ≈ 0.333 tokens/sec.
	meLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "pme:ip:", 20.0/(1*60), 20, 1*time.Minute)
	go meLimiter.StartCleanup(ctx)

	// 10 req / 1 min per IP — rate-limits session list polling.
	// rate = 10 / (1 * 60) ≈ 0.167 tokens/sec.
	sessionsLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "psess:ip:", 10.0/(1*60), 10, 1*time.Minute)
	go sessionsLimiter.StartCleanup(ctx)

	// 3 req / 15 min per IP — limits session revocation abuse.
	// rate = 3 / (15 * 60) ≈ 0.00333 tokens/sec.
	revokeSessionLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "rsess:ip:", 3.0/(15*60), 3, 15*time.Minute)
	go revokeSessionLimiter.StartCleanup(ctx)

	// 10 req / 1 min per IP — rate-limits profile update requests.
	// rate = 10 / (1 * 60) ≈ 0.167 tokens/sec.
	updateProfileLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "prof:ip:", 10.0/(1*60), 10, 1*time.Minute)
	go updateProfileLimiter.StartCleanup(ctx)

	store := NewStore(deps.Pool)
	svc := NewService(store)
	h := NewHandler(svc)

	r.Group(func(r chi.Router) {
		r.Use(deps.JWTAuth)
		r.With(meLimiter.Limit).Get("/me", h.Me)
		r.With(sessionsLimiter.Limit).Get("/sessions", h.Sessions)
		r.With(revokeSessionLimiter.Limit).Delete("/sessions/{id}", h.RevokeSession)
		r.With(updateProfileLimiter.Limit).Patch("/me/profile", h.UpdateProfile)
	})
}
