# Events — Technical Implementation

> **What this file is:** Implementation details for `POST /events/token`,
> `GET /events`, and `GET /status`. Covers guard sequences, token internals,
> mempool tracker data structures, SSE broker, connection counting, the liveness
> goroutine, and the complete test inventory for this feature.
>
> **Read first:** `events-feature.md` — behavioral contract and edge cases.
> **Shared platform details:** `../btc-shared.md` — ZMQ subscriber, RPC client,
> app.Deps wiring, domain goroutine shutdown.

---

## Table of Contents

1. [Handler Guard Ordering — POST /events/token](#1--handler-guard-ordering--post-eventstoken)
2. [Handler Guard Ordering — GET /events](#2--handler-guard-ordering--get-events)
3. [Handler Guard Ordering — GET /status](#3--handler-guard-ordering--get-status)
4. [SSE Token — JWT Structure](#4--sse-token--jwt-structure)
5. [Mempool Tracker Internals](#5--mempool-tracker-internals)
6. [BlockEvent Handler — confirmed_tx Generation](#6--blockevent-handler--confirmed_tx-generation)
7. [SSE Broker — Connection Management](#7--sse-broker--connection-management)
8. [Per-User Connection Counter](#8--per-user-connection-counter)
9. [ZMQ Health Liveness Goroutine](#9--zmq-health-liveness-goroutine)
10. [Event Payload Shapes](#10--event-payload-shapes)
11. [Redis Key Reference](#11--redis-key-reference)
12. [Rate Limiters](#12--rate-limiters)
13. [Test Inventory](#13--test-inventory)

---

## §1 — Handler Guard Ordering — POST /events/token

```
1. Auth           token.UserIDFromContext → 401
2. Rate limit     tokenLimiter.Limit middleware (5 req/min)
3. Redis check    if unavailable → 503
4. Generate       sessionID = token.SessionIDFromContext(r.Context())
                  jti       = uuid.New().String()
                  // H-01 fix: length-prefixed encoding prevents second-preimage
                  // collisions if sessionID ever contains ':'.
                  sid = hmac.SHA256(BTC_SESSION_SECRET,
                            fmt.Sprintf("%d:%s:%s", len(sessionID), sessionID, jti))
                  ipClaim = "" (IPv6 or binding disabled)
                          | remoteHost/24 CIDR (IPv4 + BTC_SSE_TOKEN_BIND_IP=true)
                  // Store session_id server-side in Redis — NOT in the JWT.
                  // Key: "btc:token:sid:" + jti  TTL: BTC_SSE_TOKEN_TTL seconds
                  // If this SET fails → return 503; no token is issued.
                  if err := redis.Set("btc:token:sid:"+jti, sessionID, tokenTTL); err != nil {
                      return 503
                  }
5. Audit          EventBitcoinSSETokenIssued{userID, sha256(jti), exp, sourceIP}
6. Response       Set-Cookie: btc_sse_jti=<signedJWT>; HttpOnly; Secure; SameSite=Strict;
                             Path=/api/v1/bitcoin/events; MaxAge=BTC_SSE_TOKEN_TTL
                  respond.NoContent(w)  // 204
```

**Cookie attributes:**

| Attribute | Value |
|---|---|
| Name | `btc_sse_jti` |
| HttpOnly | true |
| Secure | true |
| SameSite | Strict |
| Path | `/api/v1/bitcoin/events` |
| MaxAge | `BTC_SSE_TOKEN_TTL` seconds (default 60) |

---

## §2 — Handler Guard Ordering — GET /events

```
1. Rate limit     eventsLimiter.Limit middleware — FIRST, before any I/O

2. Origin check   BTC_ALLOWED_ORIGINS.Contains(r.Header.Get("Origin")) → 403
                  // Empty Origin → 403. Browser-only endpoint.
                  // Runs before JWT parse to avoid timing/error leakage.
                  // "null" and wildcard origins rejected at startup.
                  // NOTE: non-browser clients can set any Origin header.
                  // Origin is BROWSER PROTECTION ONLY, not identity.

3. JWT parse      cookie, err := r.Cookie("btc_sse_jti")
                  if err != nil → 401 (cookie absent or malformed)
                  watch.ParseBitcoinSSEToken(cookie.Value, BTC_SSE_SIGNING_SECRET)
                  → 401 on any failure (aud="bitcoin-sse", iss="store", exp, iat validated)
                  // NEVER token.ParseAccessToken — wrong audience.
                  // GUARD: if time.Until(claims.ExpiresAt) < 1 second → return 401
                  //   (prevents EX=0 on the Lua JTI script which Redis rejects)

4. Session check  storedSessionID, err = redis.GetDel("btc:token:sid:" + claims.ID)
                  if err != nil → 503 (key missing, expired, or Redis down — fail closed)
                  expected = hmac.SHA256(BTC_SESSION_SECRET,
                                 fmt.Sprintf("%d:%s:%s",
                                     len(storedSessionID), storedSessionID, sseClaims.ID))
                  sseClaims.SID != expected → audit EventBitcoinSSETokenConsumeFailure{reason:"sid_mismatch"}
                                           → bitcoinTokenConsumeFailures{"sid_mismatch"}.Inc()
                                           → 401 sid_mismatch

5. IP check       if sseClaims.IPClaim != "" {
                      subnet = parseCIDR(sseClaims.IPClaim) — parse error → 401
                      !subnet.Contains(clientIP) → 401 ip_mismatch
                  }

6. Cap pre-check  count = svc.connCounter.Count(ctx, userID)  // read-only
                  count >= max → audit EventBitcoinSSECapExceeded{reason:"user_cap"}
                                  → return 429 user_connection_limit
                                  // JTI NOT consumed — client retries same token

7. JTI consume    Atomic Lua: SET key IF NOT EXISTS
                  result == 0 → audit EventBitcoinSSETokenConsumeFailure{reason:"already_used"}
                                → return 401
                  Redis unavailable → 503

8. Acquire slot   acquired = false
                  err = svc.connCounter.Acquire(ctx, userID)
                  err == ErrAtCapacity → audit EventBitcoinSSECapExceeded{reason:"user_cap"}
                                          → return 429 user_connection_limit
                                          // NOTE: JTI already consumed at step 7.
                                          // Only fires under extreme concurrency.
                                          // Client must re-issue token.
                  other error → return 503
                  acquired = true

9. Process cap    subscribed = false
                  ch, err = svc.Subscribe(ctx, userID)
                  err == ErrCapReached → audit EventBitcoinSSECapExceeded{reason:"process_cap"}
                                          → return 503 sse_cap_reached
                  subscribed = true

10. ZMQ health    !svc.IsZMQRunning() → return 500 internal_error

11. SSE headers   Content-Type: text/event-stream; Cache-Control: no-cache
                  X-Accel-Buffering: no

12. Audit         EventBitcoinSSEConnected{userID, sourceIP}

13. Loop          select on event channel, ping ticker, ctx.Done()
                  write error → doCleanup() + return
```

**doCleanup via sync.Once + defer:**
```go
var acquired, subscribed bool
var cleanOnce sync.Once
doCleanup := func() {
    cleanOnce.Do(func() {
        if subscribed && connCh != nil { svc.Unsubscribe(userID, connCh) }
        if acquired { svc.connCounter.Release(userID) }  // background ctx, 5s timeout
        auditCtx, auditCancel := context.WithTimeout(context.Background(), 3*time.Second)
        defer auditCancel()
        audit.Write(auditCtx, EventBitcoinSSEDisconnected, ...)
        // MUST use context.Background() — handler ctx is cancelled at this point.
    })
}
defer doCleanup()
```

The `acquired` and `subscribed` flags prevent Release/Unsubscribe being called
for resources that were never acquired (e.g. exit at step 7 before step 8).

---

## §3 — Handler Guard Ordering — GET /status

```
1. Auth       token.UserIDFromContext → 401
2. Rate limit statusLimiter.Limit middleware (20 req/min)
3. Service    svc.Status(ctx)
4. Response   respond.JSON(200, StatusResponse{...})
```

---

## §4 — SSE Token — JWT Structure

```go
// BitcoinSSEClaims — JWT payload for SSE one-time tokens.
// session_id is NOT a field — it is stored server-side in Redis.
type BitcoinSSEClaims struct {
    SID     string `json:"sid"`          // HMAC(BTC_SESSION_SECRET, len:sessionID:jti)
    IPClaim string `json:"ip,omitempty"` // /24 CIDR, IPv4 only; omitted for IPv6
    jwt.RegisteredClaims
    // aud = "bitcoin-sse" (distinct from token.AudienceAccess)
    // iss = token.Issuer ("store")
    // sub = userID
    // jti = uuid.New().String()
    // iat = issuance time
    // exp = iat + BTC_SSE_TOKEN_TTL
}

func GenerateBitcoinSSEToken(in BitcoinSSETokenInput) (string, error)
// Caller stores session_id in Redis at "btc:token:sid:"+jti BEFORE calling this.
// token.Sign validates ExpiresAt is non-zero before signing.
// NEVER token.GenerateAccessToken — wrong audience.

func ParseBitcoinSSEToken(tokenString, secret string) (*BitcoinSSEClaims, error)
// Validates: HS256, iss="store", aud="bitcoin-sse", exp required.
// Does NOT validate sid or ip — handler does that in steps 4 and 5.
// NEVER token.ParseAccessToken — wrong audience.
```

**Two distinct secrets (both required, both ≥32 bytes, must differ):**
- `BTC_SESSION_SECRET` — HMAC key for `sid` computation and verification
- `BTC_SSE_SIGNING_SECRET` — HS256 JWT signing key

**sid HMAC — length-prefixed encoding (H-01 fix):**
```go
// Format: "{len(sessionID)}:{sessionID}:{jti}"
// Length prefix prevents second-preimage collisions if sessionID contains ':'.
// Applied at both issuance (POST /events/token step 4) and
// consumption (GET /events step 4).
sid = hmac.SHA256(cfg.BitcoinSessionSecret,
          fmt.Sprintf("%d:%s:%s", len(sessionID), sessionID, jti))
```

**IP claim binding:**
- IPv4 + `BTC_SSE_TOKEN_BIND_IP=true`: `/24` CIDR computed from client IP.
  e.g. client `192.168.1.55` → claim `192.168.1.0/24`.
- IPv6 or `BTC_SSE_TOKEN_BIND_IP=false`: no `ip` claim in token.
- Token with no `ip` claim: no IP check at consumption — any client IP is accepted.

---

## §5 — Mempool Tracker Internals

```go
// pendingMempool maps txid → all watched outputs in that transaction.
pendingMempool map[string]pendingEntry

type pendingEntry struct {
    // MERGE RULE: multiple vouts paying the same address are merged into one
    // watchedOutput with summed ValueSat. Ensures exactly one confirmed_tx
    // per (txid, address) pair.
    Outputs   []watchedOutput
    SeenAt    time.Time
    Outpoints []string  // "txid:vout_index" inputs — for RBF detection
}

type watchedOutput struct {
    Address  string
    ValueSat int64    // sum of all vouts to this address in this tx
    UserIDs  []string
}

// spentOutpoints: O(1) RBF detection.
// Always cleaned atomically with pendingMempool.
// Cap: BTC_PENDING_MEMPOOL_MAX_SIZE × 20.
// When cap is reached, new tx inputs are NOT indexed — RBF detection silently
// degrades for those transactions.
spentOutpoints map[string]string  // "txid:vout_index" → pendingMempool txid

// Both maps are protected by a single sync.RWMutex.
// READ lock: held during lookup only (pendingMempool scan in block handler step 1).
// WRITE lock: held during all mutations (add, evict, prune).
// READ lock is always released before any RPC call.
```

**Age-based pruning:** hourly goroutine evicts entries older than
`BTC_MEMPOOL_PENDING_MAX_AGE_DAYS`. `spentOutpoints` entries are removed atomically
on every eviction path (RBF, age pruning, block confirmation). The two maps are
always consistent — it is impossible for `spentOutpoints` to reference a txid that
no longer exists in `pendingMempool`.

---

## §6 — BlockEvent Handler — confirmed_tx Generation

```
On BlockEvent received:

1. READ LOCK
   Collect all txids from pendingMempool whose watched address is in the
   current user's watch list.
   RELEASE READ LOCK (before any RPC call — D-25).

2. Call GetBlockHeader(e.HashHex()) with its own context.WithTimeout(BTC_BLOCK_RPC_TIMEOUT_SECONDS).
   On failure: emit new_block with rpc_error:true, skip remaining steps.

3. Emit new_block{height, hash, network, rpc_error:false}.

4. If matches found in step 1:
   Call GetBlock(e.HashHex(), verbosity=1) with its own independent timeout.
   On failure: log WARNING, skip confirmed_tx for this block.
   PERMANENT LOSS: a timed-out GetBlock means confirmed_tx events for this
   block's txids are permanently lost for this instance. Clients must poll txstatus.

5. For each txid from step 1:
   WRITE LOCK
   Re-check that the entry still exists in pendingMempool.
   (May have been evicted between step 1's read-lock release and now — RBF or pruning.)
   If entry absent: skip silently — no event, no panic.
   If entry present:
     Emit confirmed_tx per address.
     Evict pendingMempool entry.
     Remove entry's Outpoints from spentOutpoints.
   RELEASE WRITE LOCK.
```

**Cross-field timeout constraint (D-38, T-105):**
`BTC_HANDLER_TIMEOUT_MS` must be greater than `2 × BTC_BLOCK_RPC_TIMEOUT_SECONDS × 1000 + 2000ms`.
Both RPC calls in the block handler (GetBlockHeader + GetBlock) each receive their
own independent timeout equal to `BTC_BLOCK_RPC_TIMEOUT_SECONDS`. The handler timeout
must accommodate both calls plus overhead.

---

## §7 — SSE Broker — Connection Management

The broker lives in `events/broker.go`. It owns the per-user channel map and
the fan-out logic. It has no knowledge of Bitcoin — it receives typed `Event`
structs and writes them to SSE connections.

```go
// Subscribe returns a channel for this user connection.
// Returns ErrCapReached if BTC_MAX_SSE_PROCESS is exceeded.
func (b *Broker) Subscribe(ctx context.Context, userID string) (<-chan Event, error)

// Unsubscribe removes the channel from the fan-out map.
// Called by doCleanup when the SSE connection closes.
func (b *Broker) Unsubscribe(userID string, ch <-chan Event)

// EmitToUser sends an event to all active channels for a given userID.
// Non-blocking: if a client's channel is full, the event is dropped for that client.
// Drop → dropped_zmq_messages_total{reason="sse_overflow"}.Inc()
func (b *Broker) EmitToUser(userID string, e Event)
```

The channel map is protected by a `sync.RWMutex`. Fan-out acquires a read lock
so multiple goroutines can emit simultaneously. Subscribe/Unsubscribe acquire
a write lock. Channel sends are non-blocking (`select { case ch <- e: default: drop }`).

---

## §8 — Per-User Connection Counter

```go
svc.connCounter = ratelimit.NewConnectionCounter(
    deps.RedisStore,
    ratelimit.DefaultBTCSSEConnKeyPrefix,  // constant = "btc:sse:conn:" — never a literal
                                           // MUST differ from "{btc:user:{id}}:addresses" (SET type)
                                           // to prevent WRONGTYPE in SCAN-based reconciliation.
    cfg.MaxSSEPerUser,
    2*time.Hour,  // safety TTL — auto-expires counter on process crash
)
```

**Heartbeat (CRITICAL #2 fix):** The TTL goroutine calls `svc.connCounter.Heartbeat(ctx, userID)`
every 2 minutes. Without this, a connection held for exactly 2 hours would cause
the counter key to expire, allowing an extra connection beyond the cap. Heartbeat
refreshes the TTL on the counter key while the connection is alive.

**OD-01 fix:** `AtomicDecrement` gains a `ttl time.Duration` parameter. The Lua
`PEXPIRE` fires when `count > 0 && ttl > 0`. `ConnectionCounter.Release` passes
`c.slotTTL`. Guarantees the counter key stays alive while any connection using it
is open.

---

## §9 — ZMQ Health Liveness Goroutine

Launched in `NewService()`. Tracked by `svc.wg`. Ticks every 30 seconds.

```go
svc.wg.Add(1)
go func() {
    defer svc.wg.Done()
    livenessTimer := time.NewTicker(30 * time.Second)
    defer livenessTimer.Stop()
    for {
        select {
        case <-livenessTimer.C:
            // M-05 fix: derive context from svc.ctx so appCancelFn() unblocks this call.
            rpcCtx, rpcCancel := context.WithTimeout(svc.ctx,
                time.Duration(cfg.BitcoinBlockRPCTimeoutSeconds)*time.Second)
            info, err := rpc.GetBlockchainInfo(rpcCtx)
            rpcCancel()
            if err != nil {
                if errors.Is(err, context.Canceled) { return }
                bitcoinLivenessRPCErrors.Inc()
                bitcoinRPCConnected.Set(0)  // H-02: flip on failure
                continue  // do NOT flip zmq_connected on RPC error alone
            }
            bitcoinRPCConnected.Set(1)
            // H-04 fix: lastSeenHash() is defined on Subscriber.
            // Returns "" before first block — comparison with BestBlockHash on
            // fresh startup is false ("" != "") — zmq_connected not incorrectly set to 0.
            if s.lastSeenHash() != "" && info.BestBlockHash != s.lastSeenHash() {
                log.Error().Msg("ZMQ hash mismatch — possible subscriber stall")
                bitcoinZMQConnected.Set(0)
            } else if !s.IsConnected() {
                log.Error().Msg("ZMQ idle timeout exceeded")
                bitcoinZMQConnected.Set(0)
            } else {
                bitcoinZMQConnected.Set(1)
            }
            // Update last-message-age gauge here so it increases over time
            // when ZMQ is disconnected, not just at message receipt.
            lastHashNano := s.lastHashTime.Load()
            if lastHashNano > 0 {
                ageSeconds := float64(time.Now().UnixNano()-lastHashNano) / 1e9
                bitcoinZMQLastMessageAge.Set(ageSeconds)
            }
        case <-svc.ctx.Done():
            return
        }
    }
}()
```

---

## §10 — Event Payload Shapes

```json
// mempool_tx / confirmed_tx — one event per unique watched address per tx
{"event":"mempool_tx","txid":"abc...","address":"tb1q...","value_sat":5000,"network":"testnet4"}
{"event":"confirmed_tx","txid":"abc...","address":"tb1q...","value_sat":5000,"network":"testnet4"}

// mempool_tx_replaced — one per address in replaced tx
{"event":"mempool_tx_replaced","old_txid":"abc...","new_txid":"def...",
 "address":"tb1q...","old_value_sat":5000,"new_value_sat":4800,"network":"testnet4"}

// new_block — height omitted + rpc_error:true on getblockheader failure
{"event":"new_block","height":126378,"hash":"0000...","network":"testnet4","rpc_error":false}

// stream_requires_reregistration
{"event":"stream_requires_reregistration","reason":"watch_list_expired","network":"testnet4"}

// ping
{"event":"ping","network":"testnet4"}
```

`value_sat`: always `rpc.BtcToSat(v)`. Never `float64(v) * 1e8`.

---

## §11 — Redis Key Reference

| Key | Type | TTL | Purpose |
|---|---|---|---|
| `btc:token:sid:{jti}` | String | `BTC_SSE_TOKEN_TTL` | Server-side session_id for SID verification |
| `btc:token:jti:{jti}` | String | `BTC_SSE_TOKEN_TTL` | JTI one-time use gate |
| `btc:sse:conn:{userID}` | String | 2h (kept alive by Heartbeat) | Per-user SSE connection counter |
| `btc:token:ip:{ip}` | String | 1 min | Token endpoint IP rate limiter bucket |
| `btc:events:ip:{ip}` | String | 1 min | Events endpoint IP rate limiter bucket |
| `btc:status:ip:{ip}` | String | 1 min | Status endpoint IP rate limiter bucket |

---

## §12 — Rate Limiters

| Limiter | KV prefix | Rate | Burst |
|---|---|---|---|
| `tokenLimiter` | `btc:token:ip:` | 5 req/min | 5 |
| `eventsLimiter` | `btc:events:ip:` | 5 req/min | 5 |
| `statusLimiter` | `btc:status:ip:` | 20 req/min | 20 |

All use `ratelimit.TrustedProxyRealIP` via upstream middleware (mounted in `routes.go`).

---

## §13 — Test Inventory

### Unit tests (no external deps) — mempool tracker

| ID | Test | Notes |
|---|---|---|
| T-15 | `TestPendingMempool_AddAndConfirm_SingleAddress` | |
| T-16 | `TestPendingMempool_RBFDetected` | |
| T-17 | `TestPendingMempool_AgeBasedPruning_SpentOutpointsCleanedAtomically` | |
| T-61 | `TestPendingMempool_MultiOutputTx` | multiple vouts, different addresses |
| T-62 | `TestPendingMempool_RBFChain` | A replaces B, C replaces A |
| T-78 | `TestPendingMempool_DuplicateVoutSameAddress_SingleEventSummedValue` | vout merge |
| T-81 | `TestConfirmedTx_GetBlockTimeout_EntriesNotEvicted_ConfirmedTxNotEmitted` | |
| T-85 | `TestPendingMempool_NeverConfirmedEntry_PrunedAfterMaxAge` | |
| T-92 | `TestNewValueSat_Zero_WhenReplacementDoesNotPayOriginal` | |
| T-103 | `TestSpentOutpoints_CapEnforced` | cap at MAX_SIZE × 20 |
| T-105 | `TestConfirmedTx_CrossFieldTimeout_DefaultsWork` | handler_ms > 2×rpc_timeout×1000+2000 |
| T-121 | `TestBlockEvent_HashHex_IsReversed` | ZMQ bytes → HashHex() == known RPC hex |
| T-122 | `TestTxEvent_HashHex_IsReversed` | |
| T-123 | `TestConfirmedTx_EntryEvictedByRBF_BetweenReadAndWriteLock_NoEmission` | `-race` flag |
| T-124 | `TestConfirmedTx_EntryEvictedByAgePruning_BetweenReadAndWriteLock_NoEmission` | `-race` flag |

### Unit tests — SSE token

| ID | Test | Notes |
|---|---|---|
| T-69 | `TestTokenIPBinding_IPv4_ClaimSet` | |
| T-70 | `TestTokenIPBinding_IPv6_NoClaimSet` | |
| T-71 | `TestTokenIPBinding_DisabledFlag_NoClaimSet` | |
| T-87 | `TestTokenSIDBinding_CorrectHMAC_Accepted` | |
| T-88 | `TestTokenSIDBinding_WrongHMAC_Rejected` | |
| T-141 | `TestTokenIPBinding_FalseWithIPv4_NoIPClaim` | BTC_SSE_TOKEN_BIND_IP=false + IPv4 |
| T-142 | `TestTokenIPBinding_NoIPClaim_DifferentIPv4_Succeeds` | no ip claim → no check |
| T-143 | `TestTokenIPBinding_IPv4Claim_DifferentSlash24_Fails` | 401 ip_mismatch |
| T-144 | `TestTokenIPBinding_IPv4Claim_SameSlash24_DifferentHost_Succeeds` | same /24, different host |
| T-145 | `TestTokenIPBinding_IPv4Claim_SubnetBoundary_DotZero_Succeeds` | |
| T-146 | `TestTokenIPBinding_IPv4Claim_SubnetBoundary_Dot255_Succeeds` | |
| T-147 | `TestSIDBinding_LengthPrefixedEncoding_Roundtrip` | H-01 fix regression guard |
| T-148 | `TestSIDBinding_OldColonSeparator_Fails` | old format rejected after H-01 fix |

### Unit tests — SSE handler / broker

| ID | Test | Notes |
|---|---|---|
| T-18 | `TestOriginValidation_AllowedOrigin_Accepted` | |
| T-19 | `TestOriginValidation_UnknownOrigin_Rejected` | |
| T-20 | `TestOriginValidation_MissingOrigin_Rejected` | no Origin header → 403 |
| T-77 | `TestSSECap_DecrRunsAfterContextCancellation` | slot released after ctx cancel |
| T-89 | `TestHandlerTimeout_BlockingHandlerCancelled` | |
| T-90 | `TestRealIP_TrustedProxy_Used` | |
| T-91 | `TestRealIP_UntrustedProxy_RemoteAddr` | |
| T-101 | `TestSafeInvoke_PanicInInnerGoroutine_ProcessSurvives` | |
| T-102 | `TestSSEEventWriteError_TriggersCleanup` | write error → doCleanup fires |
| T-104 | `TestSSEHandler_PanicInLoop_SlotReleased` | panic path: slot still released |
| T-136 | `TestSSECapExceeded_UsesCorrectAuditEvent` | EventBitcoinSSECapExceeded, not TokenConsumeFailure |

### Unit tests — connection counting + TTL

| ID | Test | Notes |
|---|---|---|
| T-125 | `TestService_Shutdown_DrainsDomainGoroutines` | wg.Wait() ≤15s |
| T-137 | `TestService_Shutdown_CalledBeforeGoroutineSchedules_NoPanic` | C-01 race |
| T-149 | `TestTTLGoroutine_ExitsOnSvcCtx_WithoutHTTPServerShutdown` | svc.cancel() without HTTP shutdown |
| T-159 | `TestTTLGoroutine_Heartbeat_KeepsCounterKeyAlive` | MAX conns + heartbeat → cap still enforced |
| T-160 | `TestTTLGoroutine_NoHeartbeat_CounterKeyExpires_CapBypassed` | documents vulnerability heartbeat fixes |
| T-161 | `TestTTLGoroutine_Heartbeat_KeyMissing_EmitsWarningMetric` | counter key expired mid-connection |

### Integration tests (require Redis)

| ID | Test | Notes |
|---|---|---|
| T-21 | `TestTokenIssuance_NothingWrittenToRedis` | session_id written to Redis at issuance |
| T-22 | `TestTokenConsumption_ValidToken_Returns200` | |
| T-23 | `TestTokenConsumption_AlreadyUsed_Returns401` | |
| T-24 | `TestTokenConsumption_Expired_Returns401` | |
| T-25 | `TestTokenConsumption_WrongAudience_Returns401` | |
| T-26 | `TestTokenConsumption_SIDMismatch_Returns401` | |
| T-27 | `TestTokenConsumption_IPMismatch_Returns401` | |
| T-28 | `TestTokenConsumption_RedisDown_Returns503` | |
| T-34 | `TestSSECap_AcquireSucceeds_UnderCap` | |
| T-35 | `TestSSECap_AcquireFails_AtCap` | |
| T-36 | `TestSSECap_ReleaseDecrements` | |
| T-37 | `TestSSECap_AcquireAfterRelease_Succeeds` | |
| T-38 | `TestSSECap_RedisDown_Returns503` | |
| T-65 | `TestUserCap_MultipleConnections_EnforcedPerUser` | |
| T-66 | `TestUserCap_DifferentUsers_IndependentCaps` | |
| T-67 | `TestUserCap_ReleasedSlot_AdmitsNextConnection` | |
| T-82 | `TestPendingMempool_MaxSizeEnforced` | |
| T-83 | `TestStartup_AllowedOriginsNullRejected` | |
| T-84 | `TestAudit_TokenConsumeFailure_RecordedWithReason` | |

### Startup validation tests

| ID | Test | Notes |
|---|---|---|
| T-49 | `TestStartup_ZMQBlockNonLoopback_Rejected` | |
| T-50 | `TestStartup_ZMQTxNonLoopback_Rejected` | |
| T-52 | `TestStartup_SSECeiling_Range` | BTC_MAX_SSE_PROCESS 10–10000 |
| T-53 | `TestStartup_TokenTTL_Range` | |
| T-54 | `TestStartup_MissingAllowedOrigins_Rejected` | |
| T-55 | `TestStartup_RedactionGate` | |
| T-74 | `TestStartup_MempoolPendingDays_Range` | |
| T-75 | `TestStartup_IdleTimeout_Range` | |
| T-76 | `TestStartup_HandlerTimeout_Range` | |
| T-80 | `TestStartup_RPCPortInvalid_Rejected` | |
| T-93 | `TestStartup_NetworkInvalid` | |
| T-94 | `TestStartup_SessionSecretTooShort` | |
| T-95 | `TestStartup_SigningSecretTooShort` | |
| T-96 | `TestStartup_SecretsIdentical_Rejected` | |
| T-97 | `TestStartup_CIDRInvalid_Rejected` | |
| T-98 | `TestStartup_OriginWildcard_Rejected` | |
| T-99 | `TestStartup_BlockRPCTimeoutRange` | |
| T-110 | `TestStartup_CrossFieldValidation_HandlerTimeoutTooSmall` | |
| T-111 | `TestStartup_CrossFieldValidation_DefaultsPass` | |
| T-112 | `TestStartup_FallbackAuditLog_UnwritableRejectsStart` | |
| T-113 | `TestStartup_MaxSSEProcess_Range` | |
| T-115 | `TestStartup_AllowedOrigins_TrailingSlashRejects` | |
| T-116 | `TestStartup_AllowedOrigins_HttpRejectedOnMainnet` | |
| T-117 | `TestStartup_ZMQPort_InvalidRejects` | |

### Cookie auth tests

| ID | Test | Notes |
|---|---|---|
| T-172 | `TestSSE_CookieAuth_NoCookie_Returns401` | GET /events with no btc_sse_jti cookie |
| T-173 | `TestSSE_CookieAuth_ValidCookie_Connects` | POST /token sets cookie; GET /events opens |
| T-174 | `TestSSE_CookieAuth_ExpiredCookie_Returns401` | expired JWT in cookie |

### Operational / chaos tests

| ID | Test | Notes |
|---|---|---|
| T-56 | `TestCombinedFailure_ZMQAndRedisSimultaneous` | |
| T-59 | `TestGoroutineLeak_MassDisconnect` | |
| T-60 | `TestLogRedaction_AppLogsNoToken` | cookie value must not appear in app logs |
| T-68 | `TestLogRedaction_NginxLogsNoToken` | cookie value must not appear in nginx logs |
| T-167 | `TestDoCleanup_AuditWriteFailure_FallbackLogged` | audit.Write fails → fallback metric + log |

### Tests forwarded from zmq-technical.md

These tests were originally specified in `zmq-technical.md` but require events
package infrastructure. Implement them here when this package is built.

| ID | Test | Notes |
|---|---|---|
| T-46 | `TestSubscriber_OverflowWritesRedis` | settlement_tx handler full → LPush to `btc:settlement:overflow`; Redis unavailable → ERROR log + metric |
| T-104 | `TestSSEHandler_PanicInLoop_SlotReleased` | panic in SSE event loop → doCleanup fires via sync.Once; slot released; audit written via context.Background() |
