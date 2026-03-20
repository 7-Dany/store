# Watch — Technical Implementation

> **What this file is:** Implementation details for `POST /api/v1/bitcoin/watch`.
> Covers the Lua script, Go wiring, TTL goroutine, global watch count reconciliation,
> rate limiters, and the complete test inventory for this feature.
>
> **Read first:** `watch-feature.md` — behavioral contract and edge cases.
> **Shared platform details:** `../btc-shared.md` — ZMQ subscriber, RPC client,
> app.Deps wiring, domain goroutine shutdown.

---

## Table of Contents

1. [Handler Guard Ordering](#1--handler-guard-ordering)
2. [Per-User Watch Cap — Lua Script](#2--per-user-watch-cap--lua-script)
3. [Address Validation](#3--address-validation)
4. [TTL Goroutine](#4--ttl-goroutine)
5. [Global Watch Count Reconciliation](#5--global-watch-count-reconciliation)
6. [Redis Key Reference](#6--redis-key-reference)
7. [Test Inventory](#7--test-inventory)

---

## §1 — Handler Guard Ordering

`POST /bitcoin/watch`

All handlers apply `ratelimit.TrustedProxyRealIP` (mounted upstream) before any
guard runs, so `r.RemoteAddr` is already the true client IP everywhere below.

```
1. Cap body       http.MaxBytesReader → 413
2. Auth           token.UserIDFromContext → 401
3. Rate limit     watchLimiter.Limit middleware (ratelimit.RouteWithIP)
                    → 429 rate_limit_exceeded
                       audit EventBitcoinWatchRateLimitHit{sourceIP}
                       bitcoin_watch_rejected_total{reason="rate_limit"}.Inc()
4. Decode         respond.DecodeJSON[WatchRequest]
5. Validate       → 400 network_mismatch (req.Network != cfg.BitcoinNetwork)
                  → 400 too_few_addresses (len(req.Addresses) == 0)
                  → 400 too_many_addresses (> 20)
                  // validateAndNormalise (validators.go):
                  //   1. Trim whitespace
                  //   2. Lowercase all characters
                  //   3. Validate address format for cfg.BitcoinNetwork
                  → 400 invalid_address (any address fails format or network check)
                       audit EventBitcoinWatchInvalidAddress{userID, invalid_address_hmac, sourceIP}
                       bitcoin_watch_rejected_total{reason="invalid_address"}.Inc()
6. Service        svc.Watch(ctx, WatchInput{...})
                    → 503 if Redis unavailable
                    → 400 watch_limit_exceeded (Lua result[0]==0: count cap)
                           audit EventBitcoinWatchLimitExceeded{userID, sourceIP, reason:"count_cap"}
                           bitcoin_watch_rejected_total{reason="limit_exceeded"}.Inc()
                    → 400 watch_limit_exceeded, reason registration_window_expired
                           (Lua result[0]==-1: 7-day absolute cap lapsed)
                           audit EventBitcoinWatchLimitExceeded{..., reason:"registration_window_expired"}
                           bitcoin_watch_rejected_total{reason="registration_window_expired"}.Inc()
                    // Success-path operations run ONLY when added_count > 0.
                    // Re-registering existing addresses (added_count == 0) is a no-op:
                    // no counter increment, no cache invalidation, no pub/sub, no audit.
                    → success (added_count > 0):
                           kvstore.AtomicCounterStore.AtomicIncrement(btc:global:watch_count)
                    → success (added_count > 0):
                           kvstore.PubSubStore.Publish(btc:watch:invalidate:{userID})
                    → success (always): EXPIRE set atomically inside the Lua script
                    → success (added_count > 0): update in-memory SSE watch map cache
                    → success (added_count > 0): audit EventBitcoinAddressWatched
7. Response       respond.JSON(200, WatchResponse{watching: requestAddresses})
```

---

## §2 — Per-User Watch Cap — Lua Script

```lua
-- KEYS[1] = ({btc:user:{userID}}:addresses)      watch set
-- KEYS[2] = ({btc:user:{userID}}:registered_at)  set once, NEVER refreshed
-- KEYS[3] = ({btc:user:{userID}}:last_active)    refreshed on every call
--
-- CLUSTER SAFETY (Q-91): all three keys use hash tag {btc:user:{userID}}
-- so they hash to the same Redis Cluster slot.
--
-- ARGV[1] = limit
-- ARGV[2] = watch-set TTL in seconds (1800 = 30 min)
-- ARGV[3] = last_active TTL in seconds (1800)
-- ARGV[4..n] = candidate addresses (already lowercased by validateAndNormalise)
--
-- NOTE: ARGV[4] is the FIRST address. There is no timestamp argument.
-- The script uses Redis server time (TIME command) for all timestamps.
--
-- Returns: {success, new_count, added_count}
--   success=0  → count cap exceeded (rollback applied, watch set unchanged)
--   success=-1 → 7-day registration window expired
--   success=1  → at least one address added or all were already registered

local nowArr = redis.call('TIME')
local now = tonumber(nowArr[1])  -- Unix seconds from Redis server clock (M-10 fix)

-- 7-day absolute cap check
local regAt = redis.call('GET', KEYS[2])
if regAt ~= false then
    if now - tonumber(regAt) > 604800 then
        -- M-02/OD-07 fix: schedule registered_at cleanup after window expires.
        local elapsed = now - tonumber(regAt)
        local cleanupIn = 2592000 - elapsed  -- 30 days from first registration
        if cleanupIn < 86400 then cleanupIn = 86400 end  -- minimum 1-day grace
        redis.call('EXPIRE', KEYS[2], cleanupIn)
        return {-1, 0, 0}
    end
end

local current = redis.call('SCARD', KEYS[1])
local added = {}
for i = 4, #ARGV do
    if redis.call('SADD', KEYS[1], ARGV[i]) == 1 then
        added[#added+1] = ARGV[i]
    end
end

if current + #added > tonumber(ARGV[1]) then
    for _, addr in ipairs(added) do redis.call('SREM', KEYS[1], addr) end
    return {0, current, 0}
end

if #added > 0 then
    redis.call('EXPIRE', KEYS[1], ARGV[2])
    -- NX: set registered_at only on first registration; use server clock.
    redis.call('SET', KEYS[2], now, 'NX')
end
-- Always refresh last_active (even on re-registration).
redis.call('SET', KEYS[3], now, 'EX', ARGV[3])
return {1, current + #added, #added}
```

**Go call site:**
```go
result, err := svc.redisClient.Eval(ctx, watchCapLuaScript,
    []string{setKey, registeredAtKey, lastActiveKey},
    cfg.WatchAddressLimit, "1800", "1800",
    addresses..., // already lowercased by validateAndNormalise
).Slice()
```

- `result[0] == -1` → `400 watch_limit_exceeded` reason `registration_window_expired`
- `result[0] == 0`  → `400 watch_limit_exceeded`
- `result[2] > 0`   → run success-path side-effects (counter, pub/sub, cache, audit)
- No separate `EXPIRE` call after Lua — TTL is set atomically inside the script.
- `registered_at` is NEVER written or refreshed outside this Lua script.
- `last_active` is NEVER written outside this Lua script (but IS refreshed by the
  TTL goroutine — see §4).

---

## §3 — Address Validation

`validators.go` — `validateAndNormalise(address, network string) (string, error)`

```go
func validateAndNormalise(address, network string) (string, error) {
    address = strings.TrimSpace(address)
    address = strings.ToLower(address)
    if !isValidBitcoinAddress(address, network) {
        return "", ErrInvalidAddress
    }
    return address, nil
}
```

Supported address types (D-24):
- P2PKH — base58check, starts with `1` (mainnet) or `m`/`n` (testnet4)
- P2SH — base58check, starts with `3` (mainnet) or `2` (testnet4)
- P2WPKH / P2WSH — bech32, starts with `bc1` (mainnet) or `tb1` (testnet4)
- P2TR — bech32m, starts with `bc1p` (mainnet) or `tb1p` (testnet4)

Invalid address audit event HMAC:
```go
// Raw address is NEVER stored in audit events (PII/value risk).
// HMAC allows cross-event correlation for abuse detection without exposing the value.
hmacVal := hmac.SHA256(cfg.BitcoinSessionSecret, address)
audit.Write(ctx, audit.EventBitcoinWatchInvalidAddress, map[string]string{
    "userID":               userID,
    "invalid_address_hmac": hmacVal,
    "sourceIP":             sourceIP,
})
```

---

## §4 — TTL Goroutine

One goroutine is launched per active SSE connection (inside the GET /events handler).
It keeps the watch set alive while the connection is open.

```go
// C-01 fix: wg.Add(1) MUST be called BEFORE go func() in the calling frame.
svc.wg.Add(1)
go func() {
    defer svc.wg.Done()
    ticker := time.NewTicker(2 * time.Minute)
    defer ticker.Stop()
    setKey        := "{btc:user:" + userID + "}:addresses"
    lastActiveKey := "{btc:user:" + userID + "}:last_active"
    // registered_at is intentionally NOT refreshed here (D-22).
    for {
        select {
        case <-ticker.C:
            rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
            // C-04 fix: use svc.redisStore, NOT svc.kvStore (rate-limiter store).
            existed, err := svc.redisStore.RefreshTTL(rctx, setKey, 30*time.Minute)
            cancel()
            if err != nil {
                log.Error().Err(err).Msg("failed to refresh watch set TTL")
            } else if !existed {
                svc.EmitToUser(userID, Event{Type: "stream_requires_reregistration",
                    Data: map[string]string{"reason": "watch_list_expired", "network": network}})
                bitcoinStreamReregistrationTotal.WithLabelValues(network, "watch_list_expired").Inc()
            }
            rctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
            _, _ = svc.redisStore.RefreshTTL(rctx, lastActiveKey, 30*time.Minute)
            cancel()
            // CRITICAL #2 fix: Heartbeat refreshes the SSE connection counter TTL.
            rctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
            svc.connCounter.Heartbeat(rctx, userID)
            cancel()
        case <-ctx.Done():
            return  // HTTP handler context cancelled (client disconnected)
        case <-svc.ctx.Done():
            return  // Service shutting down — exit so svc.wg.Wait() completes
        }
    }
}()
```

**Key behaviors:**
- Ticks every 2 minutes; refreshes watch set TTL to 30 minutes.
- If `RefreshTTL` returns `existed=false`, the key has expired (Redis outage lasting
  >30 min or key eviction). Emits `stream_requires_reregistration` to the client.
- Does NOT refresh `registered_at` — that key is intentionally left to expire
  naturally per D-22.
- Exits on either the HTTP handler context (client disconnect) or the service context
  (shutdown). Both paths allow `svc.wg.Wait()` to complete within the 15-second ceiling.
- `context.WithoutCancel(ctx)` is used for each Redis call so that the 5-second timeout
  is independent from the handler context — a cancelled handler ctx would otherwise
  immediately cancel every Redis call in the goroutine.

---

## §5 — Global Watch Count Reconciliation

Launched in `NewService()`, tracked by `svc.wg`. Corrects drift between the
`btc:global:watch_count` atomic counter (incremented on add, never decremented) and
the actual count derived from scanning all address set keys.

```go
svc.wg.Add(1)
go func() {
    defer svc.wg.Done()
    ticker := time.NewTicker(15 * time.Minute)
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            total, err := reconcileGlobalWatchCount(svc.ctx, svc.redisClient)
            if err != nil {
                if errors.Is(err, context.Canceled) { return }
                log.Error().Err(err).Msg("global watch count reconciliation failed")
                bitcoinRedisErrors.WithLabelValues("reconcile_scan").Inc()
                continue
            }
            if err := svc.redisClient.Set(svc.ctx, "btc:global:watch_count", total, 0).Err(); err != nil {
                if !errors.Is(err, context.Canceled) {
                    log.Error().Err(err).Msg("global watch count SET failed")
                }
            } else {
                bitcoinGlobalWatchCountEstimate.WithLabelValues(svc.cfg.BitcoinNetwork).Set(float64(total))
            }
        case <-svc.ctx.Done():
            return
        }
    }
}()

func reconcileGlobalWatchCount(ctx context.Context, rdb *redis.Client) (int64, error) {
    var total int64
    var cursor uint64
    for {
        // ScanType requires Redis 6.0+ — prevents WRONGTYPE errors on non-SET keys.
        // Pattern "*:addresses" matches both plain and hash-tag key formats.
        keys, nextCursor, err := rdb.ScanType(ctx, cursor, "*:addresses", 100, "set").Result()
        if err != nil { return 0, fmt.Errorf("reconcileGlobalWatchCount: SCAN: %w", err) }
        for _, key := range keys {
            n, err := rdb.SCard(ctx, key).Result()
            if err != nil {
                log.Warn().Err(err).Str("key", key).Msg("reconcileGlobalWatchCount: SCARD failed; skipping")
                continue
            }
            total += n
        }
        cursor = nextCursor
        if cursor == 0 { break }
        // Check for cancellation between SCAN pages to allow clean shutdown.
        select {
        case <-ctx.Done(): return 0, ctx.Err()
        default:
        }
    }
    return total, nil
}
```

**⚠️ Redis Cluster warning:** SCAN operates on a single node. In a Redis Cluster
deployment this produces a significant undercount. Either use ClusterScan (iterate
all nodes) or disable this goroutine and rely solely on AtomicIncrement/Decrement
(which IS cluster-safe). The deployment README must document this limitation.

---

## §6 — Redis Key Reference

| Key | Type | TTL | Purpose |
|---|---|---|---|
| `{btc:user:{userID}}:addresses` | Set | 30 min (reset on new add) | User's watch set |
| `{btc:user:{userID}}:registered_at` | String | 30-day cleanup after 7-day window expires | First-registration timestamp; 7-day cap anchor |
| `{btc:user:{userID}}:last_active` | String | 30 min (reset every call) | Last activity tracking |
| `btc:global:watch_count` | String | Permanent (ttl=0) | Cross-instance advisory counter |
| `btc:watch:ip:{ip}` | String | 1 min | Watch endpoint IP rate limiter bucket |
| `btc:watch:invalidate:{userID}` | Pub/Sub channel | — | Cache invalidation signal |

All three user-scoped keys use the `{btc:user:{userID}}` hash tag to guarantee
they land on the same Redis Cluster slot. Single-key operations (rate limiter
buckets, global counter) are cluster-safe by default.

---

## §7 — Test Inventory

### Unit tests (no external deps)

| ID | Test | Notes |
|---|---|---|
| T-01 | `TestValidateAddress_P2PKH_Valid` | |
| T-02 | `TestValidateAddress_P2SH_Valid` | |
| T-03 | `TestValidateAddress_Bech32_Valid` | segwit v0 |
| T-04 | `TestValidateAddress_Bech32m_Valid` | P2TR |
| T-05 | `TestValidateAddress_WrongNetwork_Rejected` | mainnet addr on testnet4 |
| T-06 | `TestValidateAddress_Garbage_Rejected` | |
| T-07 | `TestValidateAddress_TooManyAddresses_Rejected` | |
| T-79 | `TestWatch_NetworkMismatch_Returns400` | req.Network != BTC_NETWORK |
| T-135 | `TestWatch_EmptyAddressArray_Returns400` | `{"addresses":[]}` → 400 `too_few_addresses` |
| T-152 | `TestWatch_Reregistration_NoCacheInvalidation` | added_count==0: no Publish, no counter incr |
| T-153 | `TestWatch_AddressNormalised_Lowercase` | `TB1Q...` stored as `tb1q...` |
| T-162 | `TestWatch_InvalidAddress_EmitsAuditEvent` | HMAC field present, correct userID+sourceIP |
| T-163 | `TestWatch_InvalidAddress_HMACIsConsistent` | same address → same HMAC both times |
| T-164 | `TestWatch_InvalidAddress_RawAddressNotInAuditLog` | no raw address in any audit field |

### Integration tests (require Redis)

| ID | Test | Notes |
|---|---|---|
| T-11 | `TestLuaScript_RollbackPreExisting` | cap hit: newly added addresses rolled back |
| T-12 | `TestLuaScript_UnderLimit` | |
| T-13 | `TestLuaScript_ExactLimit` | exactly at cap: success |
| T-14 | `TestLuaScript_OverLimit` | one over cap: reject entire batch |
| T-29 | `TestWatch_CapEnforced` | end-to-end cap via HTTP handler |
| T-30 | `TestWatch_PreExistingAddressesSurviveCapCheck` | pre-existing not counted against limit |
| T-31 | `TestWatch_RedisDown_Returns503` | |
| T-32 | `TestWatch_InvalidationPublished` | added_count>0: Publish fires |
| T-33 | `TestWatch_SSANUsed` | SSCAN pattern for reconcile |
| T-72 | `TestWatch_TTLSetAtomicallyInsideLua` | EXPIRE fires inside Lua, not after |
| T-106 | `TestWatch_AuditNotFiredOnReregistration` | added_count==0: no EventBitcoinAddressWatched |
| T-107 | `TestWatch_GlobalCountNotIncrementedOnReregistration` | same: no AtomicIncrement |
| T-118 | `TestWatch_7DayCapExpired_Returns400RegistrationWindowExpired` | |
| T-119 | `TestWatch_RegisteredAtCreatedOnFirstRegistration_NotRefreshedOnSecond` | |
| T-120 | `TestWatch_LastActiveCreatedByLua_RefreshedByTTLGoroutine` | |
| T-132 | `TestReconciliationGoroutine_CorrectsStaleness` | SCAN corrects drifted counter |
| T-133 | `TestReconciliationGoroutine_SkipsNonSetKeys` | SCAN TYPE filter works |
| T-134 | `TestReconciliationGoroutine_ExitsOnContextCancel` | clean shutdown mid-SCAN |

### TTL goroutine tests

| ID | Test | Notes |
|---|---|---|
| T-125 | `TestService_Shutdown_DrainsDomainGoroutines` | wg.Wait() completes in ≤15s |
| T-137 | `TestService_Shutdown_CalledBeforeGoroutineSchedules_NoPanic` | C-01 race: wg.Add before go func |
| T-149 | `TestTTLGoroutine_ExitsOnSvcCtx_WithoutHTTPServerShutdown` | svc.cancel() drains without HTTP shutdown |
| T-150 | `TestTTLGoroutine_DoesNotRefreshRegisteredAt` | registered_at TTL unchanged after tick |

### Startup validation tests

| ID | Test | Notes |
|---|---|---|
| T-51 | `TestStartup_MaxWatchCeiling_Rejected` | |
| T-73 | `TestStartup_CacheTTL_Range` | |
| T-114 | `TestStartup_ReconciliationStartHeight_NegativeRejects` | |
| T-138 | `TestStartup_ZMQIdleTimeout_ZeroUsesNetworkDefault` | mainnet→600s, testnet4→120s |
| T-158 | `TestReconcile_StartHeightBehindCursor_LogsWarning` | configured start behind cursor: warning |
| T-165 | `TestReconcile_MainnetZeroHeight_NoAllowFlag_ReturnsError` | |
| T-166 | `TestReconcile_MainnetZeroHeight_AllowFlag_LogsError_NoConfigError` | |

### Config package tests (watch-related)

| Test |
|---|
| `TestConfig_Bitcoin_DisabledByDefault` |
| `TestConfig_Bitcoin_RequiredFieldsMissing` |
| `TestConfig_Bitcoin_NetworkInvalid` |
| `TestConfig_Bitcoin_SecretsTooShort` |
| `TestConfig_Bitcoin_SecretsIdentical` |
| `TestConfig_Bitcoin_AllowedOriginsWildcard` |
| `TestConfig_Bitcoin_AllowedOriginsHttpMainnet` |
| `TestConfig_Bitcoin_ZMQIdleTimeoutRange` |
| `TestConfig_Bitcoin_ParseBoolEnvDefault_True` |
| `TestConfig_Bitcoin_ParseBoolEnvDefault_ExplicitFalse` |
