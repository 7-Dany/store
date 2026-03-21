package ratelimit

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/7-Dany/store/backend/internal/platform/kvstore"
)

// ErrAtCapacity is returned by Acquire when the ceiling is reached.
var ErrAtCapacity = fmt.Errorf("ratelimit: connection counter at capacity")

// DefaultBTCSSEConnKeyPrefix is the canonical Redis key prefix for the bitcoin
// SSE per-user connection counter. All code that reads or writes this counter
// MUST use this constant — never a string literal — to prevent key name
// inconsistencies across packages and refactors.
const DefaultBTCSSEConnKeyPrefix = "btc:sse:conn:"

// ── ConnCounterRecorder ─────────────────────────────────────────────────────

// ConnCounterRecorder is the narrow metrics interface required by
// ConnectionCounter. *telemetry.Registry satisfies this interface structurally.
// Pass nil in tests that do not need metric assertions.
type ConnCounterRecorder interface {
	// OnConnCounterReleaseFailed is called when Release() fails to decrement,
	// indicating a connection slot may be permanently leaked.
	OnConnCounterReleaseFailed(keyPrefix string)
	// OnConnCounterHeartbeatMiss is called when Heartbeat() finds the counter
	// key missing while connections are expected to be active.
	OnConnCounterHeartbeatMiss(keyPrefix string)
}

// ── ConnectionCounter ───────────────────────────────────────────────────────

// ConnectionCounter enforces a concurrent connection ceiling using an atomic
// Redis (or in-memory) counter. It is distinct from the token-bucket rate
// limiters: it tracks held connections, not request rate.
//
// Usage pattern:
//
//	// On connect:
//	if err := counter.Acquire(ctx, userID); err != nil { /* reject */ }
//	defer counter.Release(userID)
//
//	// In a long-running SSE TTL goroutine, every ~2 minutes:
//	counter.Heartbeat(ctx, userID)
type ConnectionCounter struct {
	store     kvstore.AtomicCounterStore
	keyPrefix string
	max       int
	slotTTL   time.Duration
	recorder  ConnCounterRecorder // nil-safe; may be nil in tests
}

// NewConnectionCounter constructs a ConnectionCounter.
//
//   - store:     must implement kvstore.AtomicCounterStore (which embeds kvstore.Store,
//     providing RefreshTTL for Heartbeat). Panics at construction time if the
//     assertion fails — surfaces the contract violation immediately in tests.
//   - keyPrefix: namespace prefix (e.g. DefaultBTCSSEConnKeyPrefix).
//   - max:       maximum concurrent connections per identifier (must be ≥ 1).
//   - slotTTL:   safety TTL applied on every Acquire; prevents immortal counters
//     on process crash. Set to 2× the expected maximum connection lifetime.
//     Also used by Heartbeat to refresh the TTL on long-lived connections.
//   - recorder:  metrics recorder; pass nil to disable metric reporting (e.g. in tests).
func NewConnectionCounter(
	store kvstore.AtomicCounterStore,
	keyPrefix string,
	max int,
	slotTTL time.Duration,
	recorder ConnCounterRecorder,
) *ConnectionCounter {
	// Panic at construction time if the store does not also satisfy kvstore.Store
	// for the RefreshTTL call in Heartbeat. Both InMemoryStore and RedisStore do —
	// this guard catches misconfigured test mocks early.
	if _, ok := store.(kvstore.Store); !ok {
		panic("ratelimit.NewConnectionCounter: store must implement kvstore.Store (for RefreshTTL); " +
			"both InMemoryStore and RedisStore satisfy this — check your test mock")
	}
	return &ConnectionCounter{
		store:     store,
		keyPrefix: keyPrefix,
		max:       max,
		slotTTL:   slotTTL,
		recorder:  recorder,
	}
}

// Acquire atomically increments the connection count for identifier if below max.
// Returns nil on success (slot acquired).
// Returns ErrAtCapacity when at the ceiling — caller returns 429/503.
// Returns a wrapped store error on failure — caller fails closed (503).
func (c *ConnectionCounter) Acquire(ctx context.Context, identifier string) error {
	key := c.keyPrefix + identifier
	n, err := c.store.AtomicAcquire(ctx, key, c.max, c.slotTTL)
	if err != nil {
		return fmt.Errorf("ratelimit: connection counter acquire: %w", err)
	}
	if n == -1 {
		return ErrAtCapacity
	}
	return nil
}

// Release decrements the connection count for identifier.
// ALWAYS uses context.Background() with a 5-second timeout so a cancelled
// handler context never skips the decrement and permanently leaks a slot.
// Logs at ERROR and records a metric on failure.
// Callers defer this and do not check the return value.
//
// The slotTTL is forwarded to AtomicDecrement so the key TTL is refreshed
// when the resulting count is still > 0 (other connections remain open).
// This prevents the safety TTL from expiring while connections are active (C-03 fix).
func (c *ConnectionCounter) Release(identifier string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key := c.keyPrefix + identifier
	if _, err := c.store.AtomicDecrement(ctx, key, c.slotTTL); err != nil {
		slog.ErrorContext(ctx, "ratelimit: connection counter release failed — slot may be leaked",
			"key_prefix", c.keyPrefix, "identifier", identifier, "error", err)
		if c.recorder != nil {
			c.recorder.OnConnCounterReleaseFailed(c.keyPrefix)
		}
	}
}

// Heartbeat refreshes the safety TTL for the connection counter key.
// Must be called periodically from the SSE TTL goroutine (every ~2 minutes)
// to prevent the key from expiring while connections are held open with no churn.
//
// When RefreshTTL returns (false, nil), the key no longer exists — the cap was
// potentially bypassed. Logs a WARNING and records a metric.
func (c *ConnectionCounter) Heartbeat(ctx context.Context, identifier string) {
	key := c.keyPrefix + identifier
	// Store embeds kvstore.Store (validated in constructor), which provides RefreshTTL.
	existed, err := c.store.RefreshTTL(ctx, key, c.slotTTL)
	if err != nil {
		slog.WarnContext(ctx, "ratelimit: connection counter heartbeat error",
			"key_prefix", c.keyPrefix, "identifier", identifier, "error", err)
		return
	}
	if !existed {
		slog.WarnContext(ctx, "ratelimit: connection counter heartbeat key missing — "+
			"per-user cap may have been transiently bypassed",
			"key_prefix", c.keyPrefix, "identifier", identifier)
		if c.recorder != nil {
			c.recorder.OnConnCounterHeartbeatMiss(c.keyPrefix)
		}
	}
}

// Count returns the current connection count for identifier.
// Returns 0 on error or missing key. Non-authoritative; for pre-check monitoring only.
// Uses strict integer parsing — corrupted values log a WARNING and return 0.
func (c *ConnectionCounter) Count(ctx context.Context, identifier string) int64 {
	key := c.keyPrefix + identifier
	val, err := c.store.Get(ctx, key)
	if err != nil {
		// ErrNotFound is expected when count is 0; swallow silently.
		return 0
	}
	n, err := strconv.ParseInt(val, 10, 64)
	if err != nil || n < 0 {
		slog.WarnContext(ctx, "ratelimit: connection counter corrupted value",
			"key_prefix", c.keyPrefix, "identifier", identifier, "value", val)
		return 0
	}
	return n
}
