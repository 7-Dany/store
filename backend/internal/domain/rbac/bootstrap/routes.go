package bootstrap

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
)

// Routes registers POST /bootstrap on r.
// Call from the owner sub-router assembler:
//
//	bootstrap.Routes(ctx, r, deps)
//
// Security:
//   - Requires a valid JWT access token (deps.JWTAuth middleware).
//   - Requires deps.BootstrapSecret (sourced from config.BootstrapSecret) to be
//     non-empty; config.Load rejects a missing BOOTSTRAP_SECRET at startup so
//     this is guaranteed by the time Routes is called.
//
// Rate limit: 3 req / 15 min per IP.
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	secret := deps.BootstrapSecret

	// 3 req / 15 min per IP — tight limit to protect the one-time bootstrap path.
	ipLimiter := ratelimit.NewIPRateLimiter(
		deps.KVStore, "bstrp:ip:",
		3.0/(15*60), 3,
		15*time.Minute,
	)
	go ipLimiter.StartCleanup(ctx)

	store := NewStore(deps.Pool)
	svc := NewService(store)
	h := NewHandler(svc, secret)

	r.Group(func(r chi.Router) {
		r.Use(deps.JWTAuth)
		r.With(ipLimiter.Limit).Post("/bootstrap", h.Bootstrap)
	})
}
