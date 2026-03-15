// Package login registers the POST /login endpoint.
package login

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
)

// Routes registers the login endpoint on r.
// Call from auth.Routes in internal/domain/auth/routes.go:
//
//	login.Routes(ctx, r, deps)
//
// Rate limits:
//   - POST /login: 12 req / 15 min per IP  ("lgn:ip:")
//
// Middleware ordering:
//
//	POST /login: IPRateLimiter → handler.Login
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	// 12 req / 15 min per IP — burst is set above the per-user lockout threshold
	// (IncrementLoginFailures fires at 10 consecutive failures) so that the
	// time-based lockout can be reached from a single IP before the IP rate
	// limiter fires. Without this, burst=5 would short-circuit the handler on
	// the 6th attempt and the per-user counter would never reach 10 from one IP,
	// making login_locked unreachable in the single-IP path.
	// rate = 12 / (15 * 60) = 0.01333 tokens/sec.
	ipLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "lgn:ip:", 12.0/(15*60), 12, 15*time.Minute)
	go ipLimiter.StartCleanup(ctx)

	store := NewStore(deps.Pool)
	svc := NewService(store)
	h := NewHandler(svc, deps.JWTConfig)

	r.With(ipLimiter.Limit).Post("/login", h.Login)
}
