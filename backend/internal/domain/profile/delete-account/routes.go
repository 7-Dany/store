// Package deleteaccount registers the DELETE /me, POST /me/cancel-deletion, and GET /me/deletion-method endpoints.
package deleteaccount

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
)

// Routes registers the DELETE /me, POST /me/cancel-deletion, and GET /me/deletion-method endpoints on r.
// Call from profile.Routes in internal/domain/profile/routes.go:
//
//	deleteaccount.Routes(ctx, r, deps)
//
// Rate limits:
//   - DELETE /me:           10 req / 1 hr  per user ("del:usr:")
//   - DELETE /me/deletion:  10 req / 10 min per user ("delc:usr:")
//   - GET    /me/deletion:  10 req / 1 min per user ("delm:usr:")
//
// Middleware ordering:
//
//	JWTAuth → UserRateLimiter → handler.{Method}
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	// 10 req / 1 hr per user.
	// rate = 10.0 / (60 * 60) ≈ 0.00278 tokens/sec.
	deleteLimiter := ratelimit.NewUserRateLimiter(
		deps.KVStore, "del:usr:", 10.0/(60*60), 10, 1*time.Hour,
	)
	go deleteLimiter.StartCleanup(ctx)

	// 10 req / 10 min per user.
	// rate = 10.0 / (10 * 60) ≈ 0.01667 tokens/sec.
	cancelLimiter := ratelimit.NewUserRateLimiter(
		deps.KVStore, "delc:usr:", 10.0/(10*60), 10, 10*time.Minute,
	)
	go cancelLimiter.StartCleanup(ctx)

	store := NewStore(deps.Pool)
	svc := NewService(store, deps.OTPTokenTTL, deps.OAuth.TelegramBotToken)
	h := NewHandler(svc, deps.Mailer, deps.MailQueue, deps.MailDeliveryTimeout, deps.OTPTokenTTL)

	// 10 req / 1 min per user.
	// rate = 10.0 / 60 ≈ 0.1667 tokens/sec.
	deletionMethodLimiter := ratelimit.NewUserRateLimiter(
		deps.KVStore, "delm:usr:", 10.0/60, 10, 1*time.Minute,
	)
	go deletionMethodLimiter.StartCleanup(ctx)

	r.Group(func(r chi.Router) {
		r.Use(deps.JWTAuth)
		r.With(deleteLimiter.Limit).Delete("/me", h.Delete)
		r.With(cancelLimiter.Limit).Delete("/me/deletion", h.CancelDeletion)
		r.With(deletionMethodLimiter.Limit).Get("/me/deletion", h.DeletionMethod)
	})
}
