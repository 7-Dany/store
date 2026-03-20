// Package session registers the POST /refresh and POST /logout endpoints.
package session

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
)

// Routes registers the session endpoints on r.
// Call from auth.Routes in internal/domain/auth/routes.go:
//
//	session.Routes(ctx, r, deps)
//
// Rate limits:
//   - POST /refresh: 5 req / 15 min per IP  ("rfsh:ip:")
//   - POST /logout:  5 req / 1 min  per IP  ("lgout:ip:")
//
// Middleware ordering:
//
//	POST /refresh, /logout: IPRateLimiter → handler.{Method}
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	// 5 req / 15 min per IP — prevents token-refresh abuse from a single origin
	// while allowing legitimate multi-tab browser sessions to refresh concurrently.
	// rate = 5 / (15 * 60) = 0.00556 tokens/sec.
	refreshLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "rfsh:ip:", 5.0/(15*60), 5, 15*time.Minute)
	go refreshLimiter.StartCleanup(ctx)

	// 5 req / 1 min per IP — enough for a shared NAT (office, university) where
	// a handful of users may log out simultaneously, while still blocking automated
	// bulk logout spam that could exhaust blocklist write capacity.
	// rate = 5 / (1 * 60) ≈ 0.0833 tokens/sec. Retry-After = ceil(1/(5/60)) = 12 s.
	logoutLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "lgout:ip:", 5.0/(1*60), 5, 1*time.Minute)
	go logoutLimiter.StartCleanup(ctx)

	store := NewStore(deps.Pool)
	svc := NewService(store)
	h := NewHandler(svc, deps.JWTConfig, deps.Blocklist, deps.Metrics)

	ratelimit.RouteWithIP(r, http.MethodPost, "/refresh", h.Refresh, refreshLimiter)
	ratelimit.RouteWithIP(r, http.MethodPost, "/logout", h.Logout, logoutLimiter)
}
