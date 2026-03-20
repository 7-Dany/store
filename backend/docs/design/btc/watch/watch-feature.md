# Watch Feature — Behavior & Edge Cases

> **What this file is:** A plain-language description of what POST /api/v1/bitcoin/watch
> does, every rule it enforces, and every edge case it handles. Read this to understand
> the feature contract before looking at any implementation detail.
>
> **Companion:** `watch-technical.md` — Lua script, Go wiring, test inventory.
> **Source of truth for HTTP contract:** `../1-zmq-system.md §2`.

---

## What this feature does

A user submits a list of Bitcoin addresses. The server registers them so that if
any of those addresses appear in a mempool transaction or a confirmed block, the
user gets a real-time SSE notification. The watch list has a TTL — it is only
kept alive as long as the user maintains an active SSE connection. This endpoint
is purely the registration step; the actual notification delivery happens through
the events feature.

---

## Address validation and normalisation

Every address in the request goes through `validateAndNormalise` before any
storage operation runs. This function does three things in order:

1. Trims whitespace from both ends of the string.
2. Lowercases the entire string.
3. Validates the address format against `BTC_NETWORK` (testnet4 or mainnet).

The lowercase step is not cosmetic — it is a correctness requirement. Bitcoin Core
always emits addresses in lowercase in its RPC responses. The ZMQ handler compares
incoming tx outputs against the stored watch set using exact string matching. If a
user submits `TB1QEXAMPLE` (uppercase bech32), it would be stored as a different
Redis set member from `tb1qexample` and would never match any ZMQ event. The
normalisation step prevents this silent failure.

Supported address types: P2PKH, P2SH, bech32 segwit v0 (P2WPKH/P2WSH), and
bech32m P2TR (Taproot). The network check ensures a testnet4 address is rejected
on mainnet and vice versa — a mismatch returns `400 invalid_address`.

If any single address in the batch fails validation, the entire request is rejected
with `400 invalid_address`. There is no partial success. The user must fix and
resubmit the whole batch.

---

## Network mismatch

The request body contains a `network` field that must exactly match `BTC_NETWORK`
configured on the server. A mismatch returns `400 network_mismatch` before any
address validation runs. This guard prevents a user on testnet4 from accidentally
watching mainnet addresses, which would never match anything and silently do nothing.

---

## Per-user cap enforcement

Two separate caps apply, enforced atomically inside a single Lua script:

**Count cap:** a user may have at most `BTC_WATCH_ADDRESS_LIMIT` addresses in their
watch set at any given time. If adding the submitted addresses would push the total
over this limit, the entire batch is rejected with `400 watch_limit_exceeded`. Any
addresses that were speculatively added during the Lua script are rolled back before
the error is returned — the watch set is left unchanged.

**7-day registration window:** the first time a user ever calls this endpoint, the
server records a `registered_at` timestamp. This key is set once and never refreshed.
Once 7 days have elapsed from first registration, all subsequent POST /watch calls
are rejected with `400 watch_limit_exceeded` and reason `registration_window_expired`,
regardless of how many addresses the user currently has. The window cannot be reset
by the user — it requires admin intervention.

The `registered_at` key is given a 30-day cleanup TTL after the 7-day window expires
(with a minimum 1-day grace period). This prevents stale keys from accumulating in
Redis indefinitely for users who registered and then stopped using the service.

---

## Re-registration semantics

Submitting an address that is already in the user's watch set is not an error. The
Redis `SADD` operation is idempotent — adding an existing member is a no-op. The
response `watching` array will echo the submitted address, but `added_count` inside
the service will be zero for that address.

The key distinction is what happens when `added_count == 0` for the entire batch
(all submitted addresses were already registered): the server does nothing beyond
responding. Specifically, it does NOT increment the global watch count, does NOT
publish a cache invalidation event, and does NOT write an audit event. This prevents
thundering-herd cache invalidation storms during post-outage reconnects when all
clients simultaneously re-register addresses that are already in their watch sets.

An important subtlety: re-registering existing addresses does NOT refresh the watch
set TTL. Only adding at least one new address triggers an `EXPIRE` inside the Lua
script. The TTL is kept alive by the active SSE connection's TTL goroutine, not by
repeated POST /watch calls.

---

## Duplicate addresses within a single request

Submitting the same address twice in one request is handled transparently. `SADD`
is idempotent, so the second occurrence of the same address simply doesn't increment
the count. The response `watching` array echoes the original request including
duplicates. Clients should deduplicate before sending for clarity, but duplicates
will not cause errors.

---

## The `watching` response field

The `watching` array contains exactly the addresses from the current request that
are now active (whether newly added or already registered). It does NOT represent
the user's complete watch list. It does NOT distinguish between newly added and
pre-existing addresses. Clients must track their own full watch list — they cannot
reconstruct it from this response field.

---

## TTL behavior

The watch set TTL is 30 minutes, set atomically inside the Lua script when at
least one new address is added. The `last_active` key for the user is always
refreshed (even on re-registration of existing addresses) using Redis server time.

The watch set is sustained only while an active SSE connection is open. When a
user connects to GET /events, a per-connection TTL goroutine ticks every 2 minutes
and calls `RefreshTTL` on the watch set key and the `last_active` key, resetting
both to 30 minutes. When the SSE connection closes, the goroutine exits and the
watch set is left to expire naturally.

If a user calls POST /watch but never establishes a GET /events connection, the
watch set will expire after 30 minutes with no notification.

If the watch set expires while an SSE connection is open (e.g. during a Redis
outage that lasts longer than 30 minutes), the TTL goroutine detects the missing
key on its next tick and emits `stream_requires_reregistration` to the client.
The client must then re-POST to /watch and keep its SSE connection open.

---

## Cross-instance behavior

The watch list is stored in Redis, not in-process. Under horizontal load balancing:

- Any instance can serve a POST /watch call.
- Each instance maintains an in-memory cache of watch lists (keyed by userID) with
  a `BTC_CACHE_TTL` staleness window (default 5 minutes).
- When a POST /watch call adds at least one new address (`added_count > 0`), the
  server publishes a cache invalidation event on `btc:watch:invalidate:{userID}`.
  All instances subscribed to this channel will evict their local cache for that
  user on the next event. Until they do, they may use a stale cache for up to
  `BTC_CACHE_TTL`.
- This staleness is accepted for the display-only SSE path. Settlement correctness
  is unaffected because settlement uses a separate DB-backed address registry.

---

## Redis unavailability

If Redis is unavailable when POST /watch is called, the server returns
`503 service_unavailable` with a `Retry-After: 5` header. No partial state is
written — the Lua script either runs atomically or doesn't run at all.

During Redis recovery, the watch set TTL goroutine for active SSE connections will
log errors but continue retrying on each 2-minute tick. If Redis remains down for
more than 30 minutes, watch set keys will expire and clients will receive
`stream_requires_reregistration`.

---

## Audit trail

When at least one new address is successfully added (`added_count > 0`), the server
writes an `EventBitcoinAddressWatched` audit event. Re-registering existing addresses
produces no audit event.

When the rate limit is hit, `EventBitcoinWatchRateLimitHit` is written with the
source IP.

When a per-user cap is exceeded, `EventBitcoinWatchLimitExceeded` is written with
the userID, source IP, and the reason (`count_cap` or `registration_window_expired`).

When an invalid address is submitted, `EventBitcoinWatchInvalidAddress` is written
with the `invalid_address_hmac` field (HMAC of the address, not the raw value) so
the event is useful for abuse detection without storing raw PII.

---

## Rate limiting

IP-based rate limit: 10 requests per minute, burst of 10. The limit is applied
before authentication, using `ratelimit.TrustedProxyRealIP` to extract the true
client IP from proxy headers. Hitting the limit returns `429 rate_limit_exceeded`
and increments `bitcoin_watch_rejected_total{reason="rate_limit"}`.

---

## What is intentionally not supported

- There is no endpoint to list a user's current watch addresses.
- There is no endpoint to remove individual addresses from the watch set.
- There is no way for a user to reset the 7-day registration window.
- The TTL is not extended by re-registering existing addresses.
- Watch set expiry during an outage cannot be avoided — it is an accepted trade-off
  for simplicity.
