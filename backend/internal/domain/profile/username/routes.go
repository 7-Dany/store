// Package username registers the GET /username/available and PATCH /me/username endpoints.
package username

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
)

// Routes registers the username availability and mutation endpoints on r.
// Call from profile.Routes in internal/domain/profile/routes.go:
//
//	username.Routes(ctx, r, deps)
//
// Rate limits:
//   - GET   /me/username/available: 20 req / 1 min  per IP    ("unav:ip:")
//   - PATCH /me/username:            5 req / 10 min per user  ("uchg:usr:")
//
// Middleware ordering:
//
//	GET   /me/username/available: IPRateLimiter → handler.Available
//	PATCH /me/username:           JWTAuth → UserRateLimiter → handler.UpdateUsername
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	// 20 req / 1 min per IP — allows live frontend type-ahead without enabling
	// bulk username enumeration. rate = 20/60 ≈ 0.333 tokens/sec.
	// Prefix "unav:ip:" confirmed unique in Stage 0 §6 / D-10.
	availLimiter := ratelimit.NewIPRateLimiter(
		deps.KVStore, "unav:ip:", 20.0/60, 20, 1*time.Minute,
	)
	go availLimiter.StartCleanup(ctx)

	// 5 req / 10 min per user — limits username-squatting races and UI misuse.
	// rate = 5/(10*60) ≈ 0.00833 tokens/sec.
	// Prefix "uchg:usr:" confirmed unique in Stage 0 §6 / D-10.
	updateLimiter := ratelimit.NewUserRateLimiter(
		deps.KVStore, "uchg:usr:", 5.0/(10*60), 5, 10*time.Minute,
	)
	go updateLimiter.StartCleanup(ctx)

	store := NewStore(deps.Pool)
	svc := NewService(store)
	h := NewHandler(svc)

	// Public: availability check — no JWT required.
	r.With(availLimiter.Limit).Get("/me/username/available", h.Available)

	// Authenticated: username mutation.
	// JWTAuth validates the access token and injects the user ID into the
	// request context; UserRateLimiter reads from context, so it comes second.
	r.Group(func(r chi.Router) {
		r.Use(deps.JWTAuth)
		r.With(updateLimiter.Limit).Patch("/me/username", h.UpdateUsername)
	})
}
