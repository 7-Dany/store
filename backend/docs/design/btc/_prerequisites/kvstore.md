# Prerequisites — kvstore Extensions

> **Package:** `internal/platform/kvstore/`
> **Files affected:** `store.go`, `memory.go`, `redis.go`, `kvstore_test.go`, `memory_test.go`, `redis_test.go`
>
> **Status:** Must be merged before any bitcoin domain code is written.
> **Depends on:** Nothing — pure platform extension.
> **Blocks:** ratelimit/connection_counter, all bitcoin domain services.

---

## Overview

Four new interface extensions are needed on the existing `kvstore` package. The
existing `Store`, `TokenBlocklist`, `AtomicBucketStore`, and `AtomicBackoffStore`
interfaces are already present. The bitcoin domain requires four more:

| Interface | Purpose |
|---|---|
| `Store.RefreshTTL` | Refresh key TTL without modifying value; detect expired keys |
| `AtomicCounterStore` | Atomic increment/decrement/acquire for connection caps and global counts |
| `ListStore` | LPUSH/BRPOP/LLEN for settlement overflow queue |
| `PubSubStore` | Cross-instance cache invalidation pub/sub |

The SSE JTI one-time enforcement reuses the existing `TokenBlocklist` interface via
a custom Lua script — no new interface needed (see `ssetoken.md`).

---

## Platform Version Gate

**Redis 6.0+ is required** for:
- `SCAN TYPE` command used in global watch count reconciliation.
- `SET ... KEEPTTL` option in atomic counter corruption repair.

Redis 5.x will fail at runtime — `ScanType` returns an error on first reconciliation
tick; `KEEPTTL` in the Lua repair path silently corrupts counters.

Add a CI integration test:
```go
// TestPlatformRequirements_RedisVersion — calls redis.Info("server"),
// parses redis_version, fails if < 6.0.
// Runs against the real Redis container in CI — canonical version gate.
// Must pass before any bitcoin platform code is merged.
```

---

## §1 — `Store.RefreshTTL`

**Added to the existing `Store` interface in `store.go`.**

```go
// RefreshTTL resets the TTL of an existing key without modifying its value.
// Returns (true, nil)  — key existed, TTL updated.
// Returns (false, nil) — key does not exist (expired or never created).
//                        Caller should treat this as expiry and take recovery action
//                        (e.g. emit stream_requires_reregistration to the SSE client).
// Returns (false, err) — store error; caller should log and retry.
// Passing ttl ≤ 0 returns an error; a zero TTL would delete the key immediately.
RefreshTTL(ctx context.Context, key string, ttl time.Duration) (existed bool, err error)
```

**InMemoryStore (`memory.go`):**
```go
func (s *InMemoryStore) RefreshTTL(_ context.Context, key string, ttl time.Duration) (bool, error) {
    if ttl <= 0 {
        return false, fmt.Errorf("kvstore.RefreshTTL: ttl must be positive, got %v", ttl)
    }
    s.mu.Lock()
    defer s.mu.Unlock()
    it, ok := s.items[key]
    if !ok || it.expired(time.Now()) {
        delete(s.items, key)
        return false, nil
    }
    it.expiresAt = time.Now().Add(ttl)
    return true, nil
}
```

**RedisStore (`redis.go`):**
```go
// Redis EXPIRE returns 1 when key exists and TTL was set, 0 when key does not exist.
func (s *RedisStore) RefreshTTL(ctx context.Context, key string, ttl time.Duration) (bool, error) {
    if ttl <= 0 {
        return false, fmt.Errorf("kvstore.RefreshTTL: ttl must be positive, got %v", ttl)
    }
    n, err := s.client.Expire(ctx, key, ttl).Result()
    if err != nil {
        return false, fmt.Errorf("kvstore.RefreshTTL: redis expire: %w", err)
    }
    return n == 1, nil
}
```

**Tests:**
- `TestRefreshTTL_ExistingKey_ReturnsTrue`
- `TestRefreshTTL_MissingKey_ReturnsFalse`
- `TestRefreshTTL_ExpiredKey_ReturnsFalse`
- `TestRefreshTTL_ZeroTTL_ReturnsError`

---

## §2 — `AtomicCounterStore`

**New interface in `store.go`. Implemented by `InMemoryStore` and `RedisStore`.**

```go
type AtomicCounterStore interface {
    Store

    // AtomicIncrement increments the counter at key by 1.
    // New key initialised to 1. ttl>0 sets/refreshes the TTL. ttl==0 is permanent.
    // If key exists and ttl>0, existing TTL is refreshed.
    // If key exists and ttl==0, existing TTL is preserved.
    // Returns the new count.
    AtomicIncrement(ctx context.Context, key string, ttl time.Duration) (int64, error)

    // AtomicDecrement decrements the counter at key by 1, flooring at 0.
    // Returns 0 when key does not exist. Never returns a negative value.
    // If ttl>0 AND resulting count>0, the TTL is refreshed. This prevents
    // the safety TTL from expiring while other connections remain open (C-03 fix).
    AtomicDecrement(ctx context.Context, key string, ttl time.Duration) (int64, error)

    // AtomicAcquire increments the counter if current count < max.
    // Returns the new count (≥1) on success.
    // Returns (-1, nil) when already at or above max — caller returns 429/503.
    // Returns (0, err) on store error — caller fails closed.
    AtomicAcquire(ctx context.Context, key string, max int, ttl time.Duration) (int64, error)
}
```

**Lua scripts (pre-loaded at `NewRedisStore` via `SCRIPT LOAD`):**

Three scripts replace the unsafe INCR+PEXPIRE pattern:
- `atomicIncrementScript` — GET + SET with PX (atomic; no partial-execution window).
- `atomicDecrementScript` — GET + DECR with floor-at-0, optional PEXPIRE, corruption repair via `SET 0 KEEPTTL`.
- `atomicAcquireScript` — GET + cap check + SET with PX (atomic; no partial-execution window).

All three use `SET ... KEEPTTL` in their corruption repair path — **requires Redis 6.0+**.

The scripts follow the existing `evalScript` fallback pattern (`EvalSha` first, fall
back to `Eval` on `NOSCRIPT`). `NewRedisStore` returns an error if any `SCRIPT LOAD`
call fails.

**`item.expired()` invariant (critical for InMemoryStore):**
```go
// A zero-value expiresAt means the item NEVER expires.
// MUST check !IsZero() to avoid treating Unix epoch as "expired at epoch".
func (it *item) expired(now time.Time) bool {
    return !it.expiresAt.IsZero() && now.After(it.expiresAt)
}
```
This invariant is required for permanent counters (`btc:global:watch_count` with `ttl=0`).

**Tests:**
- `TestAtomicIncrement_NewKey_InitialisesWithTTL`
- `TestAtomicIncrement_ExistingKey_IncrementsAndRefreshesTTL`
- `TestAtomicIncrement_ZeroTTL_PermanentCounter` — N calls; count==N each time; item never expires
- `TestAtomicIncrement_ZeroTTL_NewKey_CountPersists` — key with ttl=0 survives past 1ms
- `TestAtomicIncrement_ZeroTTL_ExistingTTL_Preserved` — increment with ttl=0 does not clear an existing TTL
- `TestAtomicIncrement_TTLPositive_SetWithPX_Atomic` — concurrent goroutines; every key with count>0 always has TTL>0; `-race`
- `TestAtomicDecrement_FloorsAtZero`
- `TestAtomicDecrement_MissingKey_ReturnsZero`
- `TestAtomicDecrement_CorruptedNegative_RepairPreservesTTL` — key=-3 TTL=5min; decrement returns 0; TTL preserved (KEEPTTL)
- `TestAtomicAcquire_UnderCap_Succeeds`
- `TestAtomicAcquire_AtCap_ReturnsMinus1`
- `TestAtomicAcquire_ConcurrentRequests_NeverExceedsCap` — `-race`
- `TestAtomicAcquire_CorruptedNegative_RepairPreservesTTL` — key=-2 TTL=2h; acquire returns 1; TTL preserved
- `TestAtomicAcquire_SetWithPX_NoPartialExecution` — concurrent goroutines; key with count>0 always has TTL>0; `-race`
- `TestAtomicAcquire_ZeroTTL_NoPExpire` — ttl=0 creates key without TTL

---

## §3 — `ListStore`

**New interface in `store.go`. InMemoryStore: non-blocking. RedisStore: full LPUSH/BRPOP/LLEN.**

```go
type ListStore interface {
    Store

    // LPush prepends one or more values to the list at key.
    // Creates the list if it does not exist.
    // Returns the new list length after all values are pushed.
    LPush(ctx context.Context, key string, values ...string) (int64, error)

    // BRPop pops the rightmost element from the first non-empty list.
    // Blocks up to timeout if all lists are empty.
    // Returns ("", "", ErrNotFound) on timeout (both Redis and InMemoryStore).
    // InMemoryStore does NOT block — returns ErrNotFound immediately when empty.
    // Use a polling loop with a ticker when using InMemoryStore.
    BRPop(ctx context.Context, timeout time.Duration, keys ...string) (key, value string, err error)

    // LLen returns the current list length. Returns 0 when key does not exist.
    LLen(ctx context.Context, key string) (int64, error)
}
```

**Tests:**
- `TestLPush_CreatesListAndReturnsLength`
- `TestBRPop_NonEmptyList_ReturnsElement`
- `TestBRPop_EmptyList_ReturnsNotFound`
- `TestLLen_ExistingList_ReturnsLength`
- `TestLLen_MissingKey_ReturnsZero`
- `TestLPushBRPop_FIFOOrder` — last pushed = first popped (rightmost)

---

## §4 — `PubSubStore`

**New interface in `store.go`. InMemoryStore: in-process channels. RedisStore: PUBLISH/SUBSCRIBE with reconnect.**

```go
type PubSubMessage struct {
    Channel string
    Payload string
}

type PubSubStore interface {
    Store

    // Publish sends payload to all subscribers of channel.
    // Returns subscriber count (0 is not an error).
    Publish(ctx context.Context, channel, payload string) (int64, error)

    // Subscribe returns a channel receiving messages and a cancel func.
    // The message channel is closed when cancel() is called or ctx is cancelled.
    // Callers MUST always call the returned cancel func.
    //
    // RedisStore: reconnects with exponential backoff (1s initial, 60s ceiling).
    // Messages published during a reconnect window are permanently lost.
    // Callers must NOT assume guaranteed delivery — use periodic full reloads
    // as the authoritative recovery path.
    //
    // InMemoryStore: single-process only; no reconnection. Messages sent before
    // Subscribe is called are lost. Not suitable for multi-instance deployments.
    Subscribe(ctx context.Context, channels ...string) (<-chan PubSubMessage, func())
}
```

InMemoryStore implementation: `sync.RWMutex`-protected `map[channelName][]chan PubSubMessage` — one slice per channel, one Go channel per active subscriber. Required to satisfy `TestPubSub_MultipleSubscribers_AllReceive`.

**Tests:**
- `TestPublish_DeliveredToSubscriber`
- `TestPublish_NoSubscribers_ReturnsZero`
- `TestSubscribe_CancelClosesChannel`
- `TestSubscribe_ContextCancelClosesChannel`
- `TestPubSub_MultipleSubscribers_AllReceive`
- `TestSubscribe_RedisDisconnect_ReconnectsWithBackoff` — (integration) disconnect mid-subscription; new messages delivered after reconnect; backoff ceiling ≤60s
- `TestSubscribe_MessagesLostDuringReconnect_NoDeliveryAfterReconnect` — messages published during reconnect window are NOT delivered (no buffering)

---

## §5 — Compile-Time Interface Assertions

Add to `memory.go`:
```go
var _ AtomicCounterStore = (*InMemoryStore)(nil)  // NEW
var _ ListStore           = (*InMemoryStore)(nil)  // NEW
var _ PubSubStore         = (*InMemoryStore)(nil)  // NEW
```

Add to `redis.go`:
```go
var _ AtomicCounterStore = (*RedisStore)(nil)  // NEW
var _ ListStore           = (*RedisStore)(nil)  // NEW
var _ PubSubStore         = (*RedisStore)(nil)  // NEW
```

Add three SHA fields to `RedisStore` struct:
```go
counterIncrSHA string  // atomicIncrementScript
counterDecrSHA string  // atomicDecrementScript
counterAcqSHA  string  // atomicAcquireScript
```

`NewRedisStore` must return an error if any `SCRIPT LOAD` fails — do not silently
continue with a zero SHA.

---

## §6 — SSE JTI: No New Interface Needed

The SSE one-time token enforcement reuses `deps.BitcoinRedis` directly via a custom
`luaConsumeSSEToken` Lua script (SET-NX with PX) in `events/ssetoken.go`. This does
NOT go through `TokenBlocklist` because the required atomicity (SET-NX + TTL as a
single call) cannot be expressed through the existing interface without adding a new
method, and the key namespace (`btc:token:`) differs from the existing blocklist
prefix (`blocklist:jti:`).

See `events/events-technical.md §4` for the full token implementation.
