// Package email registers the POST /me/email/request-change, /me/email/verify-current, and /me/email/confirm-change endpoints.
package email

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
)

// Routes registers the three email-change endpoints on r.
// Call from profile.Routes in internal/domain/profile/routes.go:
//
//	email.Routes(ctx, r, deps)
//
// Rate limits:
//   - POST /me/email:        3 req / 10 min per user  ("echg:usr:")
//   - POST /me/email/verify: 5 req / 15 min per user  ("echg:usr:vfy:")
//   - PUT  /me/email:        5 req / 15 min per user  ("echg:usr:cnf:")
//
// Middleware ordering (all three routes):
//
//	JWTAuth → UserRateLimiter → handler.{Method}
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	// 3 req / 10 min per user.
	// rate = 3.0 / (10 * 60) ≈ 0.005 tokens/sec.
	// Prefix "echg:usr:" confirmed unique in Stage 0 §6.
	requestLimiter := ratelimit.NewUserRateLimiter(
		deps.KVStore, "echg:usr:", 3.0/(10*60), 3, 10*time.Minute,
	)
	go requestLimiter.StartCleanup(ctx)

	// 5 req / 15 min per user.
	// rate = 5.0 / (15 * 60) ≈ 0.00556 tokens/sec.
	// Prefix "echg:usr:vfy:" confirmed unique in Stage 0 §6.
	verifyLimiter := ratelimit.NewUserRateLimiter(
		deps.KVStore, "echg:usr:vfy:", 5.0/(15*60), 5, 15*time.Minute,
	)
	go verifyLimiter.StartCleanup(ctx)

	// 5 req / 15 min per user.
	// rate = 5.0 / (15 * 60) ≈ 0.00556 tokens/sec.
	// Prefix "echg:usr:cnf:" confirmed unique in Stage 0 §6.
	confirmLimiter := ratelimit.NewUserRateLimiter(
		deps.KVStore, "echg:usr:cnf:", 5.0/(15*60), 5, 15*time.Minute,
	)
	go confirmLimiter.StartCleanup(ctx)

	store := NewStore(deps.Pool)
	svc := NewService(store, deps.KVStore, deps.Blocklist, deps.OTPTokenTTL, deps.JWTConfig.AccessTTL)
	h := NewHandler(svc, deps)

	// All three endpoints are authenticated. JWTAuth validates the access token
	// and injects user ID + jti into the request context; rate limiters read
	// user ID from context, so they come after JWTAuth.
	r.Group(func(r chi.Router) {
		r.Use(deps.JWTAuth)
		r.With(requestLimiter.Limit).Post("/me/email", h.RequestChange)
		r.With(verifyLimiter.Limit).Post("/me/email/verify", h.VerifyCurrent)
		r.With(confirmLimiter.Limit).Put("/me/email", h.ConfirmChange)
	})
}
