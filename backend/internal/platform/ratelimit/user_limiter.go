package ratelimit

import (
	"context"
	"net/http"
	"time"

	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// UserRateLimiter rate-limits requests by authenticated user ID using a
// token-bucket algorithm backed by a kvstore.Store. The user ID is read from
// the JWT context injected by the JWTAuth middleware.
//
// IMPORTANT: This limiter MUST be placed AFTER JWTAuth in the middleware stack.
// If placed before JWTAuth, no user ID is available and every request gets a 401.
type UserRateLimiter struct {
	*rateLimiter
	keyPrefix string
}

// NewUserRateLimiter returns a UserRateLimiter backed by s.
//
//   - keyPrefix: namespace prefix for every key (e.g. "spw:usr:"). Must be
//     non-empty to avoid collisions when multiple limiters share one store.
//   - rate:      tokens replenished per second.
//   - burst:     maximum tokens (also the initial bucket size).
//   - idleTTL:   how long an idle key lives in the store before expiry.
func NewUserRateLimiter(s kvstore.Store, keyPrefix string, rate, burst float64, idleTTL time.Duration) *UserRateLimiter {
	return &UserRateLimiter{
		rateLimiter: newRateLimiter(s, rate, burst, idleTTL),
		keyPrefix:   keyPrefix,
	}
}

// StartCleanup delegates background maintenance to the store.
// Run in a goroutine:
//
//	go limiter.StartCleanup(ctx)
func (l *UserRateLimiter) StartCleanup(ctx context.Context) { l.startCleanup(ctx) }

// Allow returns true if the given user ID has a token available.
func (l *UserRateLimiter) Allow(ctx context.Context, userID string) bool {
	return l.allow(ctx, l.keyPrefix+userID)
}

// Limit returns chi-compatible middleware that enforces the per-user rate limit.
// It reads the authenticated user ID from the JWT context (set by JWTAuth).
//
// If no user ID is present in the context, it responds 401 — this indicates a
// misconfigured middleware stack (Limit placed before JWTAuth).
func (l *UserRateLimiter) Limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, ok := token.UserIDFromContext(r.Context())
		if !ok || userID == "" {
			respond.Error(w, http.StatusUnauthorized, "unauthorized", "missing authentication")
			return
		}
		if !l.Allow(r.Context(), userID) {
			w.Header().Set("Retry-After", l.retryAfterSecs)
			respond.Error(w, http.StatusTooManyRequests, "rate_limited", "too many requests — please slow down")
			return
		}
		next.ServeHTTP(w, r)
	})
}
