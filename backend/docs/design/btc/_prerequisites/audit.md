# Prerequisites — audit Extensions

> **Package:** `internal/audit/`
> **Files affected:** `audit.go`, `audit_test.go`
>
> **Status:** Must be merged before any bitcoin domain code is written.
> **Depends on:** All platform prerequisites merged (kvstore, ratelimit, token, rbac).
> **Blocks:** All bitcoin domain handlers that call `audit.Write`.

---

## Overview

Thirteen new `EventType` constants are added for the bitcoin payment domain. All three
locations that must stay in sync (RULES.md §3.14) are updated together:

1. The `const` block in `audit.go`
2. `AllEvents()` in `audit.go`
3. `TestEventConstants_ExactValues` cases in `audit_test.go`

A PR that updates only one or two of the three will fail `TestEventConstants_ExactValues`
in CI — this is intentional enforcement.

---

## §1 — New Constants

Add to the `const (...)` block after the last existing constant:

```go
// ── Bitcoin payment domain ─────────────────────────────────────────────────

// EventBitcoinAddressWatched: at least one new address successfully registered
// (added_count > 0). Re-registration of existing addresses is silent.
EventBitcoinAddressWatched EventType = "bitcoin_address_watched"

// EventBitcoinTxDetected: watched address appeared in a new mempool or confirmed tx.
EventBitcoinTxDetected EventType = "bitcoin_tx_detected"

// EventBitcoinSSETokenIssued: POST /bitcoin/events/token successfully created a
// one-time SSE token. Metadata: userID, sha256(jti), exp, sourceIP.
EventBitcoinSSETokenIssued EventType = "bitcoin_sse_token_issued"

// EventBitcoinSSETokenConsumeFailure: GET /bitcoin/events rejected a token at
// the token-validation layer. Metadata: reason (already_used | ip_mismatch |
// sid_mismatch | expired), partial sha256(jti), sourceIP.
// IMPORTANT: capacity-limit rejections use EventBitcoinSSECapExceeded, NOT this
// event. Mixing them corrupts security analytics.
EventBitcoinSSETokenConsumeFailure EventType = "bitcoin_sse_token_consume_failure"

// EventBitcoinSSECapExceeded: GET /bitcoin/events rejected because a capacity
// ceiling was reached. Metadata: reason (user_cap | process_cap), userID, sourceIP.
// Separate from EventBitcoinSSETokenConsumeFailure so capacity events do not
// trigger replay-detection alerts.
EventBitcoinSSECapExceeded EventType = "bitcoin_sse_cap_exceeded"

// EventBitcoinSSEConnected: SSE stream successfully established.
// Metadata: userID, sourceIP.
EventBitcoinSSEConnected EventType = "bitcoin_sse_connected"

// EventBitcoinSSEDisconnected: SSE stream closed for any reason (client disconnect,
// write error, ping failure, ctx cancellation). Written via doCleanup() using
// context.Background() — never the cancelled handler context.
// Metadata: userID, sourceIP, durationMs.
EventBitcoinSSEDisconnected EventType = "bitcoin_sse_disconnected"

// EventBitcoinRedisFallback: a Bitcoin domain Redis operation failed and the
// system entered degraded mode. Written to stdout JSON AND BTC_FALLBACK_AUDIT_LOG.
// Metadata: operation, error summary.
EventBitcoinRedisFallback EventType = "bitcoin_redis_fallback"

// EventBitcoinInvoiceReorgAdminRequired: a blockchain reorg affected an invoice
// whose funds have already been swept. Admin must verify whether the sweep tx was
// also reversed. Metadata: invoice_id, previous_status.
EventBitcoinInvoiceReorgAdminRequired EventType = "bitcoin_invoice_reorg_admin_required"

// EventBitcoinWatchLimitExceeded: POST /watch rejected because the per-user address
// cap or 7-day registration window was reached. Metadata: userID, sourceIP,
// reason (count_cap | registration_window_expired).
EventBitcoinWatchLimitExceeded EventType = "bitcoin_watch_limit_exceeded"

// EventBitcoinWatchRateLimitHit: POST /watch rejected by IP rate limiter.
// Metadata: sourceIP.
EventBitcoinWatchRateLimitHit EventType = "bitcoin_watch_rate_limit_hit"

// EventBitcoinWatchInvalidAddress: POST /watch rejected because an address failed
// validateAndNormalise. Metadata: userID, sourceIP, address_count,
// invalid_address_hmac (HMAC-SHA256(BTC_AUDIT_HMAC_KEY, invalidAddr) — cross-event
// correlation without retaining raw address PII).
// IMPORTANT: separate from EventBitcoinWatchLimitExceeded — do not conflate
// format-validation failures with cap-limit hits in security analytics.
EventBitcoinWatchInvalidAddress EventType = "bitcoin_watch_invalid_address"

// EventBitcoinSSEAuditWriteFailure: written to fallback log only when audit.Write
// fails inside doCleanup for EventBitcoinSSEDisconnected. Enables detection of
// audit trail gaps via bitcoin_audit_write_failures_total metric and fallback log.
EventBitcoinSSEAuditWriteFailure EventType = "bitcoin_sse_audit_write_failure"
```

---

## §2 — AllEvents() Additions

Add to the `return []EventType{...}` slice after `EventOwnerTransferCancelled`:

```go
EventBitcoinAddressWatched,
EventBitcoinTxDetected,
EventBitcoinSSETokenIssued,
EventBitcoinSSETokenConsumeFailure,
EventBitcoinSSECapExceeded,
EventBitcoinSSEConnected,
EventBitcoinSSEDisconnected,
EventBitcoinRedisFallback,
EventBitcoinInvoiceReorgAdminRequired,
EventBitcoinWatchLimitExceeded,
EventBitcoinWatchRateLimitHit,
EventBitcoinWatchInvalidAddress,
EventBitcoinSSEAuditWriteFailure,
```

---

## §3 — TestEventConstants_ExactValues Additions

Add to the test cases table after the last existing entry:

```go
{audit.EventBitcoinAddressWatched,            "bitcoin_address_watched"},
{audit.EventBitcoinTxDetected,                "bitcoin_tx_detected"},
{audit.EventBitcoinSSETokenIssued,            "bitcoin_sse_token_issued"},
{audit.EventBitcoinSSETokenConsumeFailure,    "bitcoin_sse_token_consume_failure"},
{audit.EventBitcoinSSECapExceeded,            "bitcoin_sse_cap_exceeded"},
{audit.EventBitcoinSSEConnected,              "bitcoin_sse_connected"},
{audit.EventBitcoinSSEDisconnected,           "bitcoin_sse_disconnected"},
{audit.EventBitcoinRedisFallback,             "bitcoin_redis_fallback"},
{audit.EventBitcoinInvoiceReorgAdminRequired, "bitcoin_invoice_reorg_admin_required"},
{audit.EventBitcoinWatchLimitExceeded,        "bitcoin_watch_limit_exceeded"},
{audit.EventBitcoinWatchRateLimitHit,         "bitcoin_watch_rate_limit_hit"},
{audit.EventBitcoinWatchInvalidAddress,       "bitcoin_watch_invalid_address"},
{audit.EventBitcoinSSEAuditWriteFailure,      "bitcoin_sse_audit_write_failure"},
```

---

## §4 — Test Improvement: Map-Based Failure Message

Replace the existing `require.Len` check in `TestEventConstants_ExactValues` with a
map-based comparison so the failure message names the missing constant rather than
printing a count mismatch (`"expected N, got N+1"`):

```go
// Build lookup map from test cases
caseMap := make(map[audit.EventType]string, len(cases))
for _, c := range cases { caseMap[c.eventType] = c.expectedString }

// Every constant in AllEvents() must have a test case
for _, ev := range audit.AllEvents() {
    expected, ok := caseMap[ev]
    require.True(t, ok,
        "audit constant %q is in AllEvents() but missing from test cases — add it to TestEventConstants_ExactValues", ev)
    require.Equal(t, string(expected), string(ev))
}

// Every test case must be in AllEvents()
allSet := make(map[audit.EventType]struct{}, len(audit.AllEvents()))
for _, ev := range audit.AllEvents() { allSet[ev] = struct{}{} }
for _, c := range cases {
    _, ok := allSet[c.eventType]
    require.True(t, ok,
        "test case for %q is not in AllEvents() — remove it or add to AllEvents()", c.eventType)
}
```

---

## §5 — Event Usage Reference

| Event | Where emitted |
|---|---|
| `EventBitcoinAddressWatched` | `watch/handler.go` — POST /watch success (added_count > 0) |
| `EventBitcoinTxDetected` | `events/service.go` — ZMQ tx handler |
| `EventBitcoinSSETokenIssued` | `events/handler.go` — POST /events/token step 5 |
| `EventBitcoinSSETokenConsumeFailure` | `events/handler.go` — GET /events steps 4, 7 |
| `EventBitcoinSSECapExceeded` | `events/handler.go` — GET /events steps 6, 8, 9 |
| `EventBitcoinSSEConnected` | `events/handler.go` — GET /events step 12 |
| `EventBitcoinSSEDisconnected` | `events/handler.go` — GET /events `doCleanup()` via `context.Background()` |
| `EventBitcoinRedisFallback` | `events/service.go` — Redis error handler |
| `EventBitcoinInvoiceReorgAdminRequired` | `events/service.go` (Stage 2) — `rollbackSettlementFromHeight` |
| `EventBitcoinWatchLimitExceeded` | `watch/handler.go` — POST /watch cap/window rejection |
| `EventBitcoinWatchRateLimitHit` | `watch/handler.go` — POST /watch rate limit step 3 |
| `EventBitcoinWatchInvalidAddress` | `watch/handler.go` — POST /watch validation step 5 |
| `EventBitcoinSSEAuditWriteFailure` | `events/handler.go` — `doCleanup()` fallback log |

**Audit field conventions:**
- `jti` is always stored as `sha256(jti)` — never the raw JTI value.
- `invalid_address` is stored as `HMAC-SHA256(BTC_AUDIT_HMAC_KEY, rawAddress)` — never raw.
- `sourceIP` is always the true client IP after `TrustedProxyRealIP` middleware.
