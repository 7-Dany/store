// Package ratelimitest provides test helpers for the ratelimit package.
// It must never be imported by production code.
package ratelimitest

import (
	"net"
	"strings"
	"time"

	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
)

// ─────────────────────────────────────────────────────────────
// BackoffLimiter
// ─────────────────────────────────────────────────────────────

// NewTestBackoffLimiter returns a real *BackoffLimiter configured for fast tests.
//
// baseDelay=8ms, maxDelay=12ms — backoff windows expire in milliseconds, so
// tests do not need to sleep more than ~20ms to observe window expiry:
//
//	l := ratelimitest.NewTestBackoffLimiter()
//	l.RecordFailure(ctx, "1.2.3.4")
//	// window is blocked now
//	time.Sleep(20 * time.Millisecond)
//	ok, _ := l.Allow(ctx, "1.2.3.4") // true — window has elapsed
//
// StartCleanup is never called by this constructor; the background goroutine is
// only needed in production. Tests that want to exercise eviction can call it
// explicitly with a cancellable context.
func NewTestBackoffLimiter() *ratelimit.BackoffLimiter {
	return ratelimit.NewBackoffLimiter(
		"test:backoff:",      // keyPrefix
		8*time.Millisecond,  // baseDelay — large enough that a second in-process call lands inside the window, small enough that time.Sleep(20ms) clears it
		12*time.Millisecond, // maxDelay  — caps growth; still well under the 20ms sleep used in tests
		5*time.Minute,       // idleTTL
		1*time.Minute,       // cleanupInterval
	)
}

// ─────────────────────────────────────────────────────────────
// IPRateLimiter
// ─────────────────────────────────────────────────────────────

// NewTestIPRateLimiter returns a real *IPRateLimiter backed by an in-memory
// store with the given rate (tokens/sec) and burst. Use this when a test needs
// precise control over limiting behaviour:
//
//	// A limiter that exhausts after one request:
//	l := ratelimitest.NewTestIPRateLimiter(0, 1)
func NewTestIPRateLimiter(rate, burst float64) *ratelimit.IPRateLimiter {
	s := kvstore.NewInMemoryStore(5 * time.Minute)
	return ratelimit.NewIPRateLimiter(s, "test:ip:", rate, burst, 10*time.Minute)
}

// NewPermissiveIPRateLimiter returns a real *IPRateLimiter that will not block
// any request in a normal test run (rate=10 000 req/s, burst=10 000). Use this
// when a test needs the middleware wired into the handler chain but does not
// intend to exercise limiting logic.
func NewPermissiveIPRateLimiter() *ratelimit.IPRateLimiter {
	return NewTestIPRateLimiter(10_000, 10_000)
}

// ─────────────────────────────────────────────────────────────
// TrustedProxyRealIP
// ─────────────────────────────────────────────────────────────

// MustParseTrustedProxies parses one or more CIDR strings and returns the
// corresponding []*net.IPNet. It panics on any malformed CIDR so it can be
// used in test setup code without threading *testing.T:
//
//	cidrs := ratelimitest.MustParseTrustedProxies("10.0.0.0/8", "172.16.0.0/12")
//	mw := ratelimit.TrustedProxyRealIP(cidrs)
//
// Pass no arguments (or a single empty string) to get a nil slice, which
// disables proxy-header rewriting — equivalent to no trusted proxies.
func MustParseTrustedProxies(cidrs ...string) []*net.IPNet {
	joined := strings.Join(cidrs, ",")
	nets, err := ratelimit.ParseTrustedProxies(joined)
	if err != nil {
		panic("ratelimitest.MustParseTrustedProxies: " + err.Error())
	}
	return nets
}
