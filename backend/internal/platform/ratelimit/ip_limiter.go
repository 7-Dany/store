// Package ratelimit provides token-bucket and exponential-backoff rate-limiting
// middleware for HTTP handlers.
package ratelimit

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/respond"
)

// ─────────────────────────────────────────────────────────────
// bucketState — token-bucket state serialised into the KV store
// ─────────────────────────────────────────────────────────────

type bucketState struct {
	Tokens    float64   `json:"tokens"`
	LastRefil time.Time `json:"last_refil"`
}

// ─────────────────────────────────────────────────────────────
// rateLimiter — shared token-bucket engine
// ─────────────────────────────────────────────────────────────

// rateLimiter is the private, embeddable core of IPRateLimiter. It holds the
// token-bucket parameters and drives all read-modify-write operations against
// a kvstore.Store.
//
// When the store implements kvstore.AtomicBucketStore the read-modify-write is
// delegated to the server side (atomic across every app instance). Otherwise mu
// serialises the read-modify-write within a single process.
type rateLimiter struct {
	mu             sync.Mutex
	store          kvstore.Store
	rate           float64
	burst          float64
	idleTTL        time.Duration
	retryAfterSecs string // Retry-After header value: ceil(1/rate) seconds, i.e. time to earn one token
}

func newRateLimiter(s kvstore.Store, rate, burst float64, idleTTL time.Duration) *rateLimiter {
	// Compute the honest lower-bound for Retry-After: the time it takes to
	// earn exactly one new token when the bucket is empty = ceil(1 / rate).
	// This is a floor estimate — the actual wait is longer if the client needs
	// more than one token — but it is always accurate and never misleads the
	// client into retrying far too early (unlike the previous hardcoded "1").
	ra := 1
	if rate > 0 {
		ra = int(math.Ceil(1.0 / rate))
	}
	return &rateLimiter{
		store:          s,
		rate:           rate,
		burst:          burst,
		idleTTL:        idleTTL,
		retryAfterSecs: strconv.Itoa(ra),
	}
}

// allow consumes one token from the bucket identified by key.
// Returns true if a token was available, false if the bucket is empty.
//
// When the backing store implements kvstore.AtomicBucketStore (i.e. Redis),
// the entire read-modify-write is executed as an atomic Lua script on the
// server, making it safe across any number of app instances.
//
// When the store does not implement kvstore.AtomicBucketStore (i.e.
// InMemoryStore in single-process deployments or tests), the existing
// process-local mutex path is used, which is correct for a single instance.
//
// On a transient Redis error the method falls back to the local mutex path
// rather than failing open or closed.
func (rl *rateLimiter) allow(ctx context.Context, key string) bool {
	// ── Atomic server-side path (Redis) ─────────────────────────────────────
	if atomic, ok := rl.store.(kvstore.AtomicBucketStore); ok {
		allowed, err := atomic.AtomicBucketAllow(ctx, key, rl.rate, rl.burst, rl.idleTTL)
		if err == nil {
			return allowed
		}
		// Transient store error — warn and fall through to the local path so a
		// brief Redis hiccup does not take down all rate limiting.
		//
		// Security: each app instance starts a fresh burst-sized bucket on Redis
		// error — the effective rate limit is multiplied by the number of running
		// instances until Redis recovers. Monitor for sustained WarnContext bursts
		// in production.
		slog.WarnContext(ctx, "ratelimit: atomic store error, falling back to local bucket",
			"key", key, "error", err)
	}

	// ── Local mutex path (InMemoryStore or fallback on Redis error) ──────────
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	// Load existing bucket or start fresh.
	b := bucketState{Tokens: rl.burst, LastRefil: now}
	if raw, err := rl.store.Get(ctx, key); err == nil {
		var loaded bucketState
		if json.Unmarshal([]byte(raw), &loaded) == nil {
			b = loaded
		}
		// Corrupt entry — treat as a fresh bucket; the bad entry will be
		// overwritten by the Set below.
	}

	// Refill tokens proportional to elapsed time.
	elapsed := now.Sub(b.LastRefil).Seconds()
	b.Tokens = min(rl.burst, b.Tokens+elapsed*rl.rate)
	b.LastRefil = now

	allowed := b.Tokens >= 1
	if allowed {
		b.Tokens--
	}

	// Persist with TTL = idleTTL so idle keys expire automatically.
	if data, err := json.Marshal(b); err == nil {
		//nolint:errcheck // best-effort persist; on failure the next request starts with a fresh bucket (fail-open)
		rl.store.Set(ctx, key, string(data), rl.idleTTL)
	}

	return allowed
}

// startCleanup delegates background eviction to the underlying store.
// It blocks until ctx is cancelled; run it in a goroutine.
func (rl *rateLimiter) startCleanup(ctx context.Context) {
	rl.store.StartCleanup(ctx)
}

// ─────────────────────────────────────────────────────────────
// IPRateLimiter
// ─────────────────────────────────────────────────────────────

// IPRateLimiter rate-limits requests by client IP address using a token-bucket
// algorithm backed by a kvstore.Store. The storage backend can be swapped
// (in-memory → Redis) without changing this type.
type IPRateLimiter struct {
	*rateLimiter
	keyPrefix string
}

// NewIPRateLimiter returns an IPRateLimiter backed by s.
//
//   - keyPrefix: namespace prefix for every key stored in s (e.g. "login:ip:").
//     Must be non-empty to avoid collisions when multiple limiters share one store.
//   - rate:      tokens replenished per second; rate=0 disables token refill entirely.
//   - burst:     maximum tokens (also the initial bucket size);
//     burst=0 blocks every request immediately regardless of rate.
//   - idleTTL:   how long an idle key lives in the store before expiry
//
// Perf: the Redis atomic path produces near-zero Go allocations per call;
// the in-memory local-mutex path allocates for JSON encode/decode on every request.
func NewIPRateLimiter(s kvstore.Store, keyPrefix string, rate, burst float64, idleTTL time.Duration) *IPRateLimiter {
	return &IPRateLimiter{
		rateLimiter: newRateLimiter(s, rate, burst, idleTTL),
		keyPrefix:   keyPrefix,
	}
}

// StartCleanup delegates background maintenance to the store. It blocks until
// ctx is cancelled; run it in a goroutine:
//
//	go limiter.StartCleanup(ctx)
func (l *IPRateLimiter) StartCleanup(ctx context.Context) { l.startCleanup(ctx) }

// Allow returns true if the given IP address has a token available.
func (l *IPRateLimiter) Allow(ctx context.Context, ip string) bool {
	return l.allow(ctx, l.keyPrefix+ip)
}

// Limit returns chi-compatible middleware that enforces the IP limiter.
func (l *IPRateLimiter) Limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.Allow(r.Context(), remoteIP(r)) {
			// Retry-After is ceil(1/rate): the minimum time until the next token
			// is available. Computed at limiter construction so no bucket state
			// needs to be exposed here.
			w.Header().Set("Retry-After", l.retryAfterSecs)
			respond.Error(w, http.StatusTooManyRequests, "rate_limited", "too many requests — please slow down")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ─────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────

// remoteIP extracts and normalises the host portion from a host:port RemoteAddr
// string. The result is canonical: IPv4-mapped IPv6 addresses (e.g. ::ffff:1.2.3.4)
// are reduced to dotted IPv4 notation so dual-stack clients cannot bypass limits
// by alternating address families.
//
// If r.RemoteAddr is empty or cannot be split, the raw value is returned as-is;
// this produces a shared prefix-only bucket key — every request with a blank
// RemoteAddr shares one bucket. This is safe but may trigger limiting on
// legitimate traffic if a reverse proxy misconfiguration leaves RemoteAddr blank;
// monitor for unexpectedly high 429 rates.
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// Bare IP or empty — normalise what we have.
		if r.RemoteAddr == "" {
			// Empty RemoteAddr: all such requests share the prefix-only bucket key.
			slog.Warn("ratelimit: empty RemoteAddr; sharing prefix bucket",
				"method", r.Method, "path", r.URL.Path)
		}
		if ip := net.ParseIP(r.RemoteAddr); ip != nil {
			if v4 := ip.To4(); v4 != nil {
				return v4.String()
			}
			return ip.String()
		}
		return r.RemoteAddr
	}
	// Normalise: collapse ::ffff:1.2.3.4 → 1.2.3.4 so IPv4 and IPv4-mapped
	// IPv6 addresses produce the same bucket key.
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return v4.String()
		}
		return ip.String()
	}
	return host
}


