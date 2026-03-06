package setpassword

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
)

// Routes registers the set-password endpoint on r.
// Call from the profile domain assembler:
//
//	setpassword.Routes(ctx, r, deps)
//
// Rate limit:
//   - POST /set-password: 5 req / 15 min per authenticated user (spw:usr:)
//
// Middleware ordering:
//
//	JWTAuth → UserRateLimiter → handler.SetPassword
//
// The user rate limiter runs AFTER JWTAuth because it keys on the user ID
// available only after token validation (see Stage 0 D-02 and Stage 4 design note).
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	// 5 req / 15 min per user — limits brute-force probing of account state
	// and prevents accidental repeated submissions.
	// rate = 5 / (15 * 60) ≈ 0.00556 tokens/sec.
	// Prefix "spw:usr:" does not collide with any existing KV prefix (Stage 0 §6).
	setPasswordLimiter := ratelimit.NewUserRateLimiter(
		deps.KVStore, "spw:usr:", 5.0/(15*60), 5, 15*time.Minute,
	)
	go setPasswordLimiter.StartCleanup(ctx)

	store := NewStore(deps.Pool)
	svc := NewService(store)
	h := NewHandler(svc)

	r.Group(func(r chi.Router) {
		// JWTAuth first: validates the access token and injects the user ID into
		// the request context. The UserRateLimiter reads from context, so it must
		// come second.
		r.Use(deps.JWTAuth)
		r.With(setPasswordLimiter.Limit).Post("/set-password", h.SetPassword)
	})
}
