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
// Call from the auth root assembler:
//
//	session.Routes(ctx, r, deps)
//
// Rate limits:
//   - POST /refresh: 5 req  / 15 min per IP
//   - POST /logout:  5 req / 1 min  per IP
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
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
	h := NewHandler(svc, deps.JWTConfig, deps.Blocklist)

	ratelimit.RouteWithIP(r, http.MethodPost, "/refresh", h.Refresh, refreshLimiter)
	ratelimit.RouteWithIP(r, http.MethodPost, "/logout", h.Logout, logoutLimiter)
}
