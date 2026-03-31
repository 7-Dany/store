// Package watch registers the Bitcoin watch CRUD endpoints.
package watch

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/audit"
	bitcoinshared "github.com/7-Dany/store/backend/internal/domain/bitcoin/shared"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
	"github.com/7-Dany/store/backend/internal/platform/respond"
)

// Routes registers the Bitcoin watch CRUD endpoints on r.
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	if deps.BitcoinAuditHMACKey == "" {
		panic("bitcoin/watch: BitcoinAuditHMACKey must not be empty — set the BTC_AUDIT_HMAC_KEY config value")
	}

	watchLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "btc:watch:ip:", 10.0/60, 10, 1*time.Minute)
	go watchLimiter.StartCleanup(ctx)

	store := NewStore(deps.Pool)
	svc := NewService(store, deps.Metrics, deps.BitcoinMaxWatchPerUser)
	h := NewHandler(svc, deps.Metrics, deps.BitcoinNetwork, deps.BitcoinAuditHMACKey)
	rateLimitMiddleware := watchIPRateLimitMiddleware(watchLimiter, store, deps.Metrics)

	r.With(deps.JWTAuth).Get("/watch", h.ListWatches)
	r.With(deps.JWTAuth).Get("/watch/{id}", h.GetWatch)
	r.With(rateLimitMiddleware, deps.JWTAuth).Post("/watch", h.CreateWatch)
	r.With(rateLimitMiddleware, deps.JWTAuth).Put("/watch/{id}", h.UpdateWatch)
	r.With(rateLimitMiddleware, deps.JWTAuth).Delete("/watch/{id}", h.DeleteWatch)
}

// watchIPRateLimitMiddleware records audit/metrics before returning a 429 response.
func watchIPRateLimitMiddleware(
	limiter *ratelimit.IPRateLimiter,
	store Storer,
	rec bitcoinshared.BitcoinRecorder,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := respond.ClientIP(r)
			if !limiter.Allow(r.Context(), ip) {
				auditCtx := context.WithoutCancel(r.Context())
				_ = store.WriteAuditLog(auditCtx, audit.EventBitcoinWatchRateLimitHit, "", ip, nil)
				rec.OnWatchRejected("rate_limit")
				w.Header().Set("Retry-After", limiter.RetryAfter())
				respond.Error(w, http.StatusTooManyRequests, "too_many_requests",
					"too_many_requests — please slow down")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
