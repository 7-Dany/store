# Events Feature — Behavior & Edge Cases

> **What this file is:** A plain-language description of what POST /events/token,
> GET /events, and GET /status do, every rule they enforce, and every edge case
> they handle. Read this to understand the feature contract before looking at any
> implementation detail.
>
> **Companion:** `events-technical.md` — guard sequences, mempool tracker internals,
> SSE broker, token auth implementation, test inventory.

---

## Overview

Three endpoints compose the events feature:

- `POST /api/v1/bitcoin/events/token` — issues a short-lived one-time auth token
  for the SSE connection. Must be called before opening the stream.
- `GET /api/v1/bitcoin/events` — the SSE stream itself. Display-only, best-effort.
- `GET /api/v1/bitcoin/status` — reports ZMQ subscriber health and the latest known
  block. Used for monitoring and client-side health checks.

---

## Why a separate token endpoint exists

The browser `EventSource` API cannot send custom request headers. The standard
approach of passing a JWT in an `Authorization` header is therefore impossible.
Two alternatives exist: put the token in the URL query string, or put it in a
cookie. The query string approach leaks the token through browser history, proxy
logs, Referer headers, and screenshots. This system uses a cookie instead.

`POST /events/token` authenticates the user via their regular Bearer JWT, then
issues a short-lived one-time JWT stored in an HttpOnly Secure SameSite=Strict
cookie named `btc_sse_jti`. The browser automatically sends this cookie on every
request to `/api/v1/bitcoin/events`, including `EventSource` reconnects, with no
application-layer intervention required.

---

## Token security properties

The cookie approach provides these guarantees:
- The token never appears in any URL — no browser history leakage.
- `HttpOnly` — inaccessible to JavaScript; Service Workers and XSS cannot read it.
- `Secure` — transmitted over TLS only.
- `SameSite=Strict` — no cross-site request can carry the cookie.
- `Path=/api/v1/bitcoin/events` — cookie scoped to SSE endpoint only.
- The token is one-time use: once consumed by GET /events it is invalidated in Redis
  and cannot be reused.
- The token carries a `sid` claim: an HMAC of the user's session ID and the token's
  JTI. This prevents token forgery even if an attacker steals the signing secret.
- IPv4 clients get an additional `/24` subnet binding: a token issued to 192.168.1.x
  can only be consumed from within 192.168.1.0/24. IPv6 clients rely on JTI one-time
  use and short TTL for replay protection (IPv6 addresses change too frequently for
  subnet binding to be practical).

**Forgery vs replay distinction:** The `sid` claim prevents forgery (an attacker
crafting a valid token from scratch). Replay protection comes from JTI one-time use
combined with the short TTL (`BTC_SSE_TOKEN_TTL`, default 60 seconds). These are
separate concerns handled by separate mechanisms.

**The `session_id` is never in the JWT.** At issuance, the session ID is stored
server-side in Redis under `btc:token:sid:{jti}` with TTL equal to `BTC_SSE_TOKEN_TTL`.
At consumption, the server reads and atomically deletes this key with `GetDel`, then
recomputes the HMAC to verify `sid`. The session ID never travels over the wire in
any form, eliminating the session-correlation risk that would exist if it were a JWT
claim.

---

## Token issuance DB record (GDPR)

Every token issuance is recorded in the `sse_token_issuances` table with:
- `jti_hash = HMAC-SHA256(jti, server_secret)` — non-reversible without the secret
- `source_ip_hash = SHA256(ip || daily_rotation_key)` — non-reversible after the
  key rotates (rotates every 24 hours; old key is deleted)

**Why this table and not `financial_audit_events`:**
`financial_audit_events` is immutable — DELETE and UPDATE are blocked by DB triggers.
IP addresses are PII subject to GDPR Article 17 erasure requests. Storing raw IPs
in an immutable table would make GDPR compliance structurally impossible.
`sse_token_issuances` supports erasure: on a GDPR request, `source_ip_hash` is
nullified and `erased = TRUE` is set. The pseudonymised `jti_hash` remains (it is
non-identifying without the server secret) to preserve the audit trail.

The daily rotation key means the IP hash becomes non-reversible within 24 hours
regardless of any erasure action, satisfying GDPR pseudonymisation requirements.

**What remains after erasure:** the `jti_hash`, `vendor_id`, `network`, `issued_at`,
and `expires_at` columns are retained. These are not personal data.

---

## SSE stream — what it is and what it guarantees

The SSE stream is explicitly **display-only and best-effort**. It exists to give
the frontend real-time visibility into Bitcoin transactions involving watched
addresses. It must never be used as a source of truth for payment settlement.

**Important distinction from settlement:** The SSE stream watches a Redis-backed
user address set that vendors register via `POST /watch`. This is entirely separate
from the DB-backed `invoice_address_monitoring` table that the settlement engine
uses to track invoice payment addresses. The two watch systems share no state —
a payment detected by the settlement engine via `invoice_address_monitoring` may
or may not appear on the SSE stream, depending on whether the vendor has separately
registered that address via `POST /watch`.

Guarantees the stream makes:
- Events are delivered in order within a single connection.
- A `ping` event fires every 30 seconds to keep the connection alive through proxies.

Guarantees the stream does NOT make:
- No delivery guarantee across disconnects. A `confirmed_tx` event missed during a
  connection drop is gone permanently for that client.
- No replay buffer. There are no event IDs and the server has no history.
- No guarantee of receiving `confirmed_tx` after a process restart. The
  `pendingMempool` map is in-process memory; it is empty after a restart.

---

## Event types

**`mempool_tx`** — fired when a watched address appears in a new mempool transaction.
Carries the txid, address, value in satoshis, and network. One event per unique
watched address per transaction (a tx paying the same address from two outputs
generates one event with the summed value, not two separate events).

**`confirmed_tx`** — fired when a previously emitted `mempool_tx` transaction is
included in a confirmed block. ZMQ does not re-emit `hashtx` for confirmed
transactions, so the server correlates `BlockEvent` data against an in-memory
`pendingMempool` map to generate this event. One event per (txid, address) pair.

**`mempool_tx_replaced`** — fired when a transaction previously emitted as
`mempool_tx` is replaced via RBF. Carries both the old and new txid, the address,
the old and new values in satoshis. `new_value_sat: 0` means the replacement
transaction does not pay the original address.

**`new_block`** — fired when any new block is mined, regardless of whether any
watched addresses are involved. Carries the block height and hash. Used by the
frontend to drive block confirmation count updates. If the RPC call to get the
block header fails, the event is still emitted but with `rpc_error: true` and no
height.

**`stream_requires_reregistration`** — fired when the server detects that the
user's watch set key has expired in Redis (TTL goroutine detected a missing key).
The client must re-POST to `/watch` and ensure the SSE connection stays open to
keep the new watch set alive.

**`ping`** — keepalive sent every 30 seconds to prevent proxy timeouts.

---

## How confirmed_tx is generated

ZMQ sends a `hashblock` signal when a new block is mined. The server receives this
as a `BlockEvent`. The `events` service's block handler:

1. Acquires a read lock on `pendingMempool`. Collects all txids that appear in the
   user's current watch set. Releases the read lock before any RPC call.
2. Calls `GetBlockHeader` to get the block height (for the `new_block` event).
3. Emits `new_block`.
4. If any matching txids were found, calls `GetBlock(hash, verbosity=1)` to get
   the list of txids in the block. If this RPC call fails or times out, `confirmed_tx`
   events for this block are permanently lost for this process instance — clients
   must poll the txstatus endpoint to reconcile.
5. For each matching txid, acquires a write lock, re-checks that the entry still
   exists in `pendingMempool` (it may have been evicted between step 1 and now by
   an RBF replacement or age-based pruning), emits `confirmed_tx` per address only
   if the entry still exists, evicts the entry, removes associated `spentOutpoints`
   entries, releases the write lock.

The re-check in step 5 is critical: without it, a race between the read-lock release
in step 1 and the write-lock acquisition in step 5 could cause emission of a
`confirmed_tx` for an entry that was evicted by RBF in between — producing a
spurious event.

---

## RBF (Replace-By-Fee) handling

When a new `hashtx` ZMQ event arrives, the service checks whether any of the new
transaction's inputs appear in the `spentOutpoints` secondary index. This index maps
`txid:vout_index` → the pending txid that originally spent that outpoint.

If a match is found: the old `pendingMempool` entry is evicted, its `spentOutpoints`
entries are removed atomically, and `mempool_tx_replaced` is emitted for each watched
address in the old transaction. If the replacement transaction also pays a watched
address, a new `pendingMempool` entry is added and `mempool_tx` is emitted.

If no match is found: the transaction is treated as a new unrelated transaction.

The `spentOutpoints` index has a hard cap: `BTC_PENDING_MEMPOOL_MAX_SIZE × 20`. When
this cap is reached, new transaction inputs are not indexed. RBF detection silently
degrades for those transactions — a spurious `mempool_tx` may appear instead of
`mempool_tx_replaced`. This is accepted for the display-only path. The cap exists
to bound memory usage; a multiplier of 20 approximates average inputs per transaction,
but CoinJoin and batched sweep transactions routinely exceed 100 inputs.

---

## pendingMempool cap

The `pendingMempool` map is capped at `BTC_PENDING_MEMPOOL_MAX_SIZE` entries
(default 10,000). When the cap is reached:

- New mempool transactions are still fan-out visible (the `mempool_tx` event is still
  emitted to connected SSE clients via the display channel).
- But the transaction is NOT added to `pendingMempool` — it will never produce a
  `confirmed_tx` event.
- The transaction's inputs are also NOT indexed in `spentOutpoints` — RBF detection
  is lost for this transaction.
- A WARNING is logged and `bitcoin_pending_mempool_overflow_total` is incremented.

This cap prevents memory exhaustion under load. At 10,000 entries and mempool
activity of ~7 tx/s on mainnet, the cap represents roughly 24 minutes of history —
well above the typical block interval.

Entries are age-pruned hourly: any entry older than `BTC_MEMPOOL_PENDING_MAX_AGE_DAYS`
is evicted. `spentOutpoints` entries are always removed atomically with their
`pendingMempool` entry — the two maps are always consistent.

---

## In-process state and restart behavior

`pendingMempool` and `spentOutpoints` are process-local and ephemeral. On restart:

- All pending mempool entries are lost.
- A block confirming a transaction that was in the mempool before restart will not
  produce a `confirmed_tx` event — the server has no record of having seen that
  transaction.
- `new_block` events will still fire for new blocks.
- Clients must poll the txstatus endpoint after reconnect to reconcile any pending
  transactions tracked client-side.

---

## Horizontal scaling behavior

Each application instance maintains its own independent `pendingMempool`. Under
horizontal load balancing:

- The instance that receives the `hashtx` ZMQ event for a transaction may differ from
  the instance serving the user's SSE connection when the block containing that
  transaction arrives.
- `confirmed_tx` events are therefore emitted on some instances and not others for
  the same transaction and the same user session.

This is accepted for the display-only, best-effort contract. The frontend must rely
on REST polling via the txstatus endpoint after any reconnect. Settlement is
unaffected because it uses RPC-based block scanning, not `pendingMempool`.

---

## SSE connection authentication guard sequence

All 13 steps of the GET /events guard run in strict order. Notable behaviors:

**Origin check before JWT parse:** The origin validation runs before the JWT is
parsed to avoid timing-based information leakage. If a non-browser client with no
Origin header reaches the JWT parse step, any parse failure would reveal to the
client that JWT validation is the next step in the auth chain. The 403 fires first.

**Capacity check before JTI consumption:** The per-user SSE connection cap is
pre-checked (read-only) before the one-time JTI token is consumed. If the cap is
hit and the JTI were consumed first, the client would be forced to re-issue a token
(consuming from the 5/min token budget) without ever getting a connection. Pre-checking
preserves the JTI so the client can retry the same token when a slot frees up.

**JTI is consumed exactly once:** A Lua script atomically checks and sets the JTI
key in Redis. If the key already exists, the token is rejected as already-used. This
is the authoritative replay gate. The `GetDel` on the session-id key in step 4 is
defense-in-depth.

**Cleanup guarantee via sync.Once + defer:** The handler sets up a `doCleanup`
function that releases the connection counter slot and unsubscribes from the SSE
channel. This runs exactly once via `sync.Once`, regardless of whether the handler
exits normally, due to context cancellation, or due to a panic. The `acquired` and
`subscribed` boolean flags ensure `doCleanup` does not attempt to release a slot
or unsubscribe from a channel that was never acquired in the first place (e.g. if
the handler exits at step 6 before reaching step 7).

---

## Per-user SSE connection cap

A user may hold at most `BTC_MAX_SSE_PER_USER` simultaneous SSE connections (default
configurable). This cap is enforced via an atomic Redis counter keyed by userID. The
counter has a safety TTL of 2 hours — if a process crashes without calling Release,
the counter eventually self-corrects. The TTL goroutine calls `Heartbeat` every 2
minutes to prevent the counter key from expiring while a long-lived connection is
open (CRITICAL #2 fix — without this, a stable connection held for exactly 2 hours
would allow an extra connection beyond the cap on the next acquire attempt).

---

## Reconnect behavior

On reconnect the frontend must:

1. Open a new `EventSource` — the browser automatically sends the `btc_sse_jti`
   cookie. If the cookie has expired (connection was down for more than
   `BTC_SSE_TOKEN_TTL` seconds, default 60s), the server returns 401. The application
   must then call `POST /events/token` to obtain a fresh cookie and reopen the stream.
2. Re-POST to `/watch` to re-register watched addresses (the server's in-memory watch
   cache may have expired).
3. For each txid tracked client-side as "mempool_tx received but confirmed_tx not yet
   delivered": call the txstatus endpoint to reconcile.

If the connection drops and reconnects within 60 seconds, the existing cookie is still
valid and the reconnect happens automatically with no application-layer intervention.

---

## GET /status behavior

Reports per-instance health, not cluster-wide health:

- `zmq_connected`: true if a ZMQ message was received within `idleTimeout` AND the
  last dial succeeded.
- `latest_block`: the most recently observed block hash and height.
- `watching_count`: number of unique userIDs whose watch list this specific instance
  has cached in memory. This differs across instances.
- `global_watching_count_estimate`: eventually-consistent count from the Redis atomic
  counter. For capacity monitoring only — do not use for per-user decisions.

---

## What is explicitly out of scope

- The SSE stream is never a valid input to settlement logic. Any payment confirmation
  derived from SSE events without independent RPC verification is a funds-loss risk.
- The `new_block` event does not carry full block data. It is a signal, not a receipt.
- The stream provides no guarantee that `confirmed_tx` will be emitted for every
  transaction that was observed in the mempool. Clients must poll txstatus for
  financial reconciliation.
- The SSE watch list (`POST /watch`) is completely separate from invoice address
  monitoring (`invoice_address_monitoring` table). A vendor seeing payment events on
  their SSE stream does not mean those events have been processed by the settlement
  engine, and vice versa.
