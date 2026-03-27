// Package watch registers the POST /bitcoin/watch endpoint.
package watch

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/audit"
	bitcoinshared "github.com/7-Dany/store/backend/internal/domain/bitcoin/shared"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
	"github.com/7-Dany/store/backend/internal/platform/respond"
)

// Routes registers the POST /bitcoin/watch endpoint on r.
// Call from the bitcoin domain assembler:
//
//	watch.Routes(ctx, r, deps)
//
// Rate limits:
//   - POST /watch: 10 req / 1 min per IP  ("btc:watch:ip:")
//
// Authentication: Bearer JWT (required; token.UserIDFromContext).
// The rate limiter middleware runs BEFORE JWT auth so that IP-based rejection
// happens before the more expensive JWT validation.
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	// Security: BitcoinAuditHMACKey must be non-empty. An empty key would make
	// all HMAC values trivially reproducible by any observer of the audit log,
	// defeating the purpose of hashing invalid addresses before storage.
	if deps.BitcoinAuditHMACKey == "" {
		panic("bitcoin/watch: BitcoinAuditHMACKey must not be empty — set the BTC_AUDIT_HMAC_KEY config value")
	}

	// 10 req / 1 min per IP — protects against address enumeration via
	// repeated watch registrations.
	// rate = 10 / 60 = 0.1667 tokens/sec.
	watchLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "btc:watch:ip:", 10.0/60, 10, 1*time.Minute)
	go watchLimiter.StartCleanup(ctx)

	// Type-assert deps.KVStore to the four interfaces Store requires.
	// All four are satisfied by *kvstore.RedisStore. Panics at startup if Redis
	// is not configured, which is the correct failure mode (bitcoin requires Redis).
	watchKV := deps.KVStore.(kvstore.WatchCapStore)
	sets := deps.KVStore.(kvstore.SetStore)
	counter := deps.KVStore.(kvstore.AtomicCounterStore)
	pubsub := deps.KVStore.(kvstore.PubSubStore)

	store := NewStore(watchKV, sets, counter, pubsub, deps.Pool, deps.BitcoinNetwork)

	// NewService does NOT start the reconciliation goroutine — Start() is
	// called here so routes.go owns the goroutine lifecycle (RULES.md §2.6).
	svc := NewService(ctx, store, deps.Metrics, deps.BitcoinNetwork, deps.BitcoinMaxWatchPerUser)
	svc.Start()

	h := NewHandler(svc, deps.Metrics, deps.BitcoinNetwork, deps.BitcoinAuditHMACKey)

	rateLimitMiddleware := watchIPRateLimitMiddleware(watchLimiter, store, deps.Metrics)

	// Rate limit runs BEFORE JWT auth: IP-based rejection is cheaper than JWT
	// validation and prevents abusers from consuming JWT parsing budget.
	r.With(rateLimitMiddleware, deps.JWTAuth).Post("/watch", h.Watch)
}

// ── Rate-limit middleware ──────────────────────────────────────────────────────

// watchIPRateLimitMiddleware returns chi middleware that:
//  1. Checks the IP rate limiter (non-consuming peek first, then consume).
//  2. On 429: writes EventBitcoinWatchRateLimitHit audit event and increments
//     bitcoin_watch_rejected_total{reason="rate_limit"} before responding.
//
// This wraps the limiter.Allow method instead of limiter.Limit so that the
// audit event and metric can be emitted before the 429 response is written.
func watchIPRateLimitMiddleware(
	limiter *ratelimit.IPRateLimiter,
	store Storer,
	rec bitcoinshared.BitcoinRecorder,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := respond.ClientIP(r)
			if !limiter.Allow(r.Context(), ip) {
				// Security: WithoutCancel so a client disconnect (or the 429
				// response being written) cannot abort the audit write. The user
				// is not yet authenticated at this point so userID is "".
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
