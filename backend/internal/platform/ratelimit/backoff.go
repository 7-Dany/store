package ratelimit

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/respond"
)

// BackoffLimiter enforces exponential backoff per key (typically client IP).
//
// After each recorded failure the key must wait:
//
//	delay = min(baseDelay * 2^(failures-1), maxDelay)
//
// A successful call to Reset clears the failure counter for that key. Keys idle
// for longer than idleTTL are evicted automatically by the store's TTL
// mechanism; StartCleanup runs the background sweep for in-memory stores.
//
// This is intentionally separate from the token-bucket IPRateLimiter:
//   - The token bucket limits total request rate (bursts of legitimate retries allowed).
//   - The backoff limiter forces increasing delays specifically after failures,
//     surviving token refreshes because it is keyed by IP rather than by token.
type BackoffLimiter struct {
	mu        sync.RWMutex
	store     kvstore.Store
	keyPrefix string
	baseDelay time.Duration
	maxDelay  time.Duration
	idleTTL   time.Duration
}

// backoffEntry is the serialised form stored in the KV store.
type backoffEntry struct {
	Failures  int       `json:"failures"`
	UnlocksAt time.Time `json:"unlocks_at"`
	LastSeen  time.Time `json:"last_seen"`
}

// NewBackoffLimiter constructs a BackoffLimiter backed by an in-process store.
//
//   - keyPrefix:       namespace prefix for every key stored (e.g. "verify:backoff:").
//     Must be non-empty to avoid collisions when multiple limiters share one store.
//   - baseDelay:       delay after the first failure (e.g. 2s)
//   - maxDelay:        ceiling for the exponential growth (e.g. 5min)
//   - idleTTL:         how long an entry lives without activity before eviction
//   - cleanupInterval: how often the store's eviction pass runs
//
// The in-process store is suitable for single-instance deployments only. In a
// multi-instance deployment each instance enforces independent backoff — an
// attacker rotating across N instances receives N × the allowed attempts before
// any single instance blocks them. For multi-instance production use, inject a
// shared Redis-backed store via NewBackoffLimiterWithStore.
func NewBackoffLimiter(keyPrefix string, baseDelay, maxDelay, idleTTL, cleanupInterval time.Duration) *BackoffLimiter {
	s := kvstore.NewInMemoryStore(cleanupInterval)
	return NewBackoffLimiterWithStore(s, keyPrefix, baseDelay, maxDelay, idleTTL)
}

// NewBackoffLimiterWithStore creates a BackoffLimiter backed by the provided
// kvstore.Store. Use this constructor when you want to inject a custom backend
// (Redis, test double, …).
//
//   - keyPrefix: namespace prefix for every key stored in s.
//     Must be non-empty to avoid collisions when multiple limiters share one store.
func NewBackoffLimiterWithStore(s kvstore.Store, keyPrefix string, baseDelay, maxDelay, idleTTL time.Duration) *BackoffLimiter {
	return &BackoffLimiter{
		store:     s,
		keyPrefix: keyPrefix,
		baseDelay: baseDelay,
		maxDelay:  maxDelay,
		idleTTL:   idleTTL,
	}
}

// RecordFailure increments the failure counter for key and sets the unlock time.
// Returns the resulting delay so callers can set Retry-After.
//
// Security: ctx is detached from the request context via context.WithoutCancel
// so a client-timed disconnect cannot abort the counter increment and grant
// unlimited retries without backoff.
//
// Security: when AtomicBackoffIncrement errors, the failure is written to the
// local in-memory store only — Redis is not updated. If Redis recovers before
// the local window expires, Allow will call AtomicBackoffAllow on a Redis key
// that has zero failures and return (true, 0), while the local store still
// records failures — a split-brain state. This is fail-open and conservative
// for availability; use a Redis-backed store via NewBackoffLimiterWithStore to
// avoid it entirely.
func (l *BackoffLimiter) RecordFailure(ctx context.Context, key string) time.Duration {
	// Security: detach from the request context so a client-timed disconnect
	// cannot abort the failure counter write.
	safeCtx := context.WithoutCancel(ctx)
	storeKey := l.keyPrefix + key

	if atomicStore, ok := l.store.(kvstore.AtomicBackoffStore); ok {
		unlocksAt, _, err := atomicStore.AtomicBackoffIncrement(safeCtx, storeKey, l.baseDelay, l.maxDelay, l.idleTTL)
		if err == nil {
			return time.Until(unlocksAt)
		}
		slog.WarnContext(ctx, "ratelimit: backoff atomic increment error, falling back to local path",
			"key", storeKey, "error", err)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recordFailureLocal(safeCtx, storeKey)
}

// recordFailureLocal is the non-atomic implementation. Callers must hold l.mu.
func (l *BackoffLimiter) recordFailureLocal(ctx context.Context, storeKey string) time.Duration {
	now := time.Now()

	e := l.loadEntry(ctx, storeKey)
	e.Failures++
	e.LastSeen = now

	// delay = baseDelay * 2^(failures-1), capped at maxDelay.
	exp := math.Pow(2, float64(e.Failures-1))
	delay := time.Duration(float64(l.baseDelay) * exp)
	if delay > l.maxDelay {
		delay = l.maxDelay
	}
	e.UnlocksAt = now.Add(delay)

	l.saveEntry(ctx, storeKey, e)
	return delay
}

// Reset clears the failure counter for key. Call after a successful attempt.
//
// Security: for the non-atomic path, a write lock is acquired to prevent a
// concurrent RecordFailure from racing: without it a goroutine could read
// failures=0 just as Reset deletes the key, then write failures=1, leaving the
// user in an incorrect backoff window after a successful verification.
//
// Note: unlike RecordFailure, Reset does not wrap ctx with context.WithoutCancel.
// A cancelled ctx may abort the Delete on the non-atomic path; if so, the
// backoff window remains in place until the key expires, which is the
// conservative safe behaviour.
func (l *BackoffLimiter) Reset(ctx context.Context, key string) {
	storeKey := l.keyPrefix + key

	if _, ok := l.store.(kvstore.AtomicBackoffStore); ok {
		// Atomic store: server-side atomicity makes the process-local mutex
		// unnecessary. Use the request context directly — a reset on success
		// is not a security-critical write.
		//nolint:errcheck // best-effort delete; if it fails the window expires naturally via idleTTL
		l.store.Delete(ctx, storeKey)
		return
	}

	// Non-atomic store: hold the write lock to prevent the race described above.
	l.mu.Lock()
	defer l.mu.Unlock()
	//nolint:errcheck // best-effort delete; if it fails the window expires naturally via idleTTL
	l.store.Delete(ctx, storeKey)
}

// Allow returns (true, 0) if key may proceed, or (false, remaining) if it is
// still within a backoff window.
func (l *BackoffLimiter) Allow(ctx context.Context, key string) (bool, time.Duration) {
	storeKey := l.keyPrefix + key

	if atomicStore, ok := l.store.(kvstore.AtomicBackoffStore); ok {
		allowed, remaining, err := atomicStore.AtomicBackoffAllow(ctx, storeKey)
		if err == nil {
			return allowed, remaining
		}
		// Security: when AtomicBackoffAllow errors, the local in-memory store may
		// have no entry for this key (failures written only to Redis are invisible
		// here) — the fallback is fail-open. Monitor for sustained WarnContext
		// bursts. Consider a circuit-breaker that blocks all requests during
		// prolonged Redis outages rather than allowing them.
		slog.WarnContext(ctx, "ratelimit: backoff atomic allow error, falling back to local path",
			"key", storeKey, "error", err)
	}

	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.allowLocal(ctx, storeKey)
}

// allowLocal is the non-atomic implementation. Callers must hold l.mu (read lock).
func (l *BackoffLimiter) allowLocal(ctx context.Context, storeKey string) (bool, time.Duration) {
	e := l.loadEntry(ctx, storeKey)

	if e.Failures == 0 {
		return true, 0
	}
	remaining := time.Until(e.UnlocksAt)
	if remaining <= 0 {
		return true, 0
	}
	return false, remaining
}

// Middleware returns chi-compatible middleware that rejects requests while the
// key derived by keyFn is in a backoff window.
//
// Content-Type and Retry-After are set before WriteHeader so they are captured
// correctly by ResponseWriters that snapshot headers on first write.
//
// Note: if keyFn returns an empty string all such requests share the key
// namespace prefix as a single bucket; callers should ensure keyFn returns a
// non-empty string for every request that should be independently limited.
func (l *BackoffLimiter) Middleware(keyFn func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFn(r)
			if ok, remaining := l.Allow(r.Context(), key); !ok {
				secs := int64(math.Ceil(remaining.Seconds()))
				w.Header().Set("Retry-After", strconv.FormatInt(secs, 10))
				respond.Error(w, http.StatusTooManyRequests, "too_many_attempts", "too many failed attempts — please wait before retrying")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// StartCleanup delegates background eviction of idle entries to the underlying
// store until ctx is cancelled. Run it in a goroutine:
//
//	go limiter.StartCleanup(ctx)
func (l *BackoffLimiter) StartCleanup(ctx context.Context) {
	l.store.StartCleanup(ctx)
}

// ─────────────────────────────────────────────────────────────
// internal helpers
// ─────────────────────────────────────────────────────────────

// loadEntry reads and deserialises the backoffEntry for storeKey.
// Returns a zero-value entry if the key is absent or cannot be decoded.
// Callers must hold l.mu.
func (l *BackoffLimiter) loadEntry(ctx context.Context, storeKey string) backoffEntry {
	raw, err := l.store.Get(ctx, storeKey)
	if err != nil {
		return backoffEntry{}
	}
	var e backoffEntry
	if jsonErr := json.Unmarshal([]byte(raw), &e); jsonErr != nil {
		return backoffEntry{}
	}
	return e
}

// saveEntry serialises e and writes it to the store with TTL = idleTTL.
// Callers must hold l.mu.
func (l *BackoffLimiter) saveEntry(ctx context.Context, storeKey string, e backoffEntry) {
	// backoffEntry contains only int and time.Time fields; json.Marshal never
	// returns an error for this type, so the error is intentionally discarded.
	data, _ := json.Marshal(e)
	//nolint:errcheck // best-effort persist; on failure the failure counter is lost and the next Allow may incorrectly return (true, 0)
	l.store.Set(ctx, storeKey, string(data), l.idleTTL)
}
