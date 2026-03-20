# Prerequisites — ratelimit Extensions

> **Package:** `internal/platform/ratelimit/`
> **Files affected:** new file `connection_counter.go`, new file `connection_counter_test.go`
>
> **Status:** Must be merged before any bitcoin domain code is written.
> **Depends on:** `kvstore.AtomicCounterStore` and `kvstore.Store.RefreshTTL` (kvstore.md §1, §2).
> **Blocks:** All bitcoin domain services that manage SSE connections.

---

## Overview

The existing ratelimit package provides `IPRateLimiter` and `UserRateLimiter` for
request-rate limiting (token bucket, requests per minute). The bitcoin domain needs
a different primitive: a concurrent connection ceiling — acquire on connect, hold
for the duration, release on disconnect.

One new type is added: `ConnectionCounter`.

No changes to existing files. No changes to `IPRateLimiter`, `UserRateLimiter`,
`TrustedProxyRealIP`, or any other existing ratelimit type.

---

## `ConnectionCounter`

**File:** `internal/platform/ratelimit/connection_counter.go`

```go
// ErrAtCapacity is returned by Acquire when the ceiling is reached.
var ErrAtCapacity = fmt.Errorf("ratelimit: connection counter at capacity")

// DefaultBTCSSEConnKeyPrefix is the canonical Redis key prefix for the bitcoin
// SSE per-user connection counter. All code that reads or writes this counter
// MUST use this constant — never a string literal — to prevent key name
// inconsistencies across packages and refactors.
const DefaultBTCSSEConnKeyPrefix = "btc:sse:conn:"

type ConnectionCounter struct {
    store     kvstore.AtomicCounterStore
    storeBase kvstore.Store  // same pointer; used for RefreshTTL in Heartbeat
    keyPrefix string
    max       int
    slotTTL   time.Duration
}
```

**Constructor:**
```go
// NewConnectionCounter constructs a ConnectionCounter.
//
//   store:     must implement BOTH kvstore.AtomicCounterStore AND kvstore.Store.
//              Panics at construction time if the kvstore.Store assertion fails —
//              surfaces the contract violation immediately in tests, not at first Heartbeat.
//   keyPrefix: namespace prefix (e.g. DefaultBTCSSEConnKeyPrefix = "btc:sse:conn:").
//   max:       maximum concurrent connections per identifier (must be ≥1).
//   slotTTL:   safety TTL applied on every Acquire; prevents immortal counters
//              on process crash. Set to 2× the expected maximum connection lifetime.
//              Also used by Heartbeat to refresh the TTL on long-lived connections.
func NewConnectionCounter(store kvstore.AtomicCounterStore, keyPrefix string, max int, slotTTL time.Duration) *ConnectionCounter
```

**Methods:**

```go
// Acquire atomically increments the connection count for identifier if below max.
// Returns nil on success (slot acquired).
// Returns ErrAtCapacity when at the ceiling — caller returns 429/503.
// Returns a wrapped store error on failure — caller fails closed (503).
func (c *ConnectionCounter) Acquire(ctx context.Context, identifier string) error

// Release decrements the connection count for identifier.
// ALWAYS uses context.Background() with a 5-second timeout so a cancelled
// handler context never skips the decrement and permanently leaks a slot.
// Logs at ERROR and increments platform_connection_counter_release_failures_total on failure.
// Callers defer this and do not check the return value.
//
// The slotTTL is forwarded to AtomicDecrement so the key TTL is refreshed
// when the resulting count is still > 0 (other connections remain open).
// This prevents the safety TTL from expiring while connections are active — C-03 fix.
func (c *ConnectionCounter) Release(identifier string)

// Heartbeat refreshes the safety TTL for the connection counter key.
// Must be called periodically from the SSE TTL goroutine (every 2 minutes) to
// prevent the key from expiring while connections are held open with no churn.
//
// PROBLEM IT SOLVES (CRITICAL #2): The C-03 fix (Release refreshes TTL) only
// keeps the key alive when a connection disconnects. If MAX connections are all
// held open continuously for > slotTTL with zero churn, the key expires. The
// next Acquire sees count=0 (key missing) and admits an extra connection, bypassing
// the per-user cap. Heartbeat prevents this by refreshing the key on every
// TTL-goroutine tick.
//
// When RefreshTTL returns (false, nil), the key no longer exists — the cap was
// already potentially bypassed. Logs a WARNING and increments
// platform_connection_counter_heartbeat_misses_total.
func (c *ConnectionCounter) Heartbeat(ctx context.Context, identifier string)

// Count returns the current connection count for identifier.
// Returns 0 on error or missing key. Non-authoritative; for pre-check monitoring only.
// Uses strict integer parsing — corrupted values (non-integer, negative) log a
// WARNING and return 0 rather than misreporting an incorrect count.
func (c *ConnectionCounter) Count(ctx context.Context, identifier string) int64
```

**Prometheus metrics:**
```go
// platform_connection_counter_release_failures_total
// Label: key_prefix
// Fires when Release() fails to decrement — slot may be permanently leaked.
// Alert: BitcoinSSECapDecrFailure on any increment within 5 minutes.

// platform_connection_counter_heartbeat_misses_total
// Label: key_prefix
// Fires when Heartbeat() finds the key missing while connections are expected to be active.
// Indicates the per-user cap may have been transiently bypassed.
// Alert: BitcoinSSECapHeartbeatMiss on any increment (high-severity).
```

---

## Test Inventory

**File:** `internal/platform/ratelimit/connection_counter_test.go`

| Test | Notes |
|---|---|
| `TestConnectionCounter_AcquireUnderCap_Succeeds` | |
| `TestConnectionCounter_AcquireAtCap_ReturnsErrAtCapacity` | |
| `TestConnectionCounter_ReleaseDecrementsCount` | |
| `TestConnectionCounter_ReleaseBelowZero_FloorsAtZero` | |
| `TestConnectionCounter_ReleaseUsesBackgroundContext` | decrement fires even when caller ctx cancelled |
| `TestConnectionCounter_Concurrent_NeverExceedsCap` | `-race` |
| `TestConnectionCounter_Count_ReturnsCurrentValue` | plain GET, no side effects |
| `TestConnectionCounter_Count_InvalidValue_LogsWarning` | non-integer in store → returns 0, warning logged |
| `TestConnectionCounter_Count_NegativeValue_TreatedAsZero` | negative integer → 0, warning logged |
| `TestAtomicDecrement_NegativeCounter_RepairedToZero` | inject key=-3; decrement returns 0; key set to 0 |
| `TestAtomicAcquire_NegativeCounter_RepairedBeforeCapCheck` | inject key=-2, max=3; acquire returns 1 |
| `TestAtomicAcquire_NegativeCounter_RepairDoesNotAllowExceedingCap` | inject key=-1, max=1, one real connection; acquire returns -1 (at cap) |
| `TestAtomicDecrement_CorruptedNegative_RepairPreservesTTL` | key=-3 TTL=5min; decrement; TTL preserved (KEEPTTL fix) |
| `TestAtomicAcquire_CorruptedNegative_RepairPreservesTTL` | key=-2 TTL=2h; acquire succeeds; TTL still set |
| `TestAtomicAcquire_SetWithPX_NoPartialExecution` | concurrent goroutines; count>0 always has TTL>0; `-race` |
| **C-03 suite:** | |
| `TestConnectionCounter_C03_HalfTTL_ReleaseRefreshes` | MAX connections; advance clock to slotTTL/2; Release; cap still enforced |
| `TestConnectionCounter_C03_FullTTL_NoChurn_HeartbeatPreventsExpiry` | MAX connections; advance past slotTTL WITH heartbeats; MAX+1 still rejected |
| `TestConnectionCounter_C03_FullTTL_NoChurn_NoHeartbeat_CapBypassed_Documented` | same without heartbeats; bypass documented; confirms fix is load-bearing |
| `TestConnectionCounter_Heartbeat_KeyExists_TTLRefreshed` | Heartbeat on existing key; TTL extended; no warning |
| `TestConnectionCounter_Heartbeat_KeyMissing_WarnsAndMetricIncrements` | Heartbeat on non-existent key; warning logged; metric incremented |
| `TestConnectionCounter_NewConnectionCounter_StoreNotKVStore_Panics` | mock implements AtomicCounterStore but not kvstore.Store; constructor panics |
| `TestConnectionCounter_DefaultBTCSSEConnKeyPrefix_IsCanonical` | asserts DefaultBTCSSEConnKeyPrefix == "btc:sse:conn:" |
