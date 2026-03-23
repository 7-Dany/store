# Webhook — Technical Implementation

> **What this file is:** Implementation contracts for the webhook delivery worker,
> retry backoff formula, dead letter review, outbox pattern enforcement, and the
> complete test inventory.
>
> **Read first:** `webhook-feature.md` — behavioral contract and edge cases.
> **Schema:** `sql/schema/010_btc_payouts.sql` (`webhook_deliveries` table,
> `idx_wd_pending` partial index `WHERE status = 'pending'`).
> **Queries:** `sql/queries/btc.sql` (`InsertWebhookDelivery`, `GetPendingWebhookDeliveries`,
> `MarkWebhookDelivered`, `MarkWebhookAttempted`).

---

## Table of Contents

1. [Outbox Insert Pattern](#1--outbox-insert-pattern)
2. [Delivery Worker Loop](#2--delivery-worker-loop)
3. [Retry Backoff Formula](#3--retry-backoff-formula)
4. [Webhook Signature Computation](#4--webhook-signature-computation)
5. [Dead Letter Management](#5--dead-letter-management)
6. [Test Inventory](#6--test-inventory)

---

## §1 — Outbox Insert Pattern

`InsertWebhookDelivery` MUST be called inside the same DB transaction as the state
change that triggered the event. The call site pattern:

```go
// Inside a DB transaction (tx):
if err := q.WithTx(tx).TransitionInvoiceToSettled(ctx, ...); err != nil { return err }
if err := q.WithTx(tx).CreatePayoutRecord(ctx, ...); err != nil { return err }
if err := q.WithTx(tx).InsertFinancialAuditEvent(ctx, ...); err != nil { return err }
if err := q.WithTx(tx).InsertWebhookDelivery(ctx, db.InsertWebhookDeliveryParams{
    VendorID:   invoice.VendorID,
    EventType:  "invoice.settled",
    Payload:    marshalPayload(invoice, payout),
    InvoiceID:  pgtype.UUID{Bytes: invoice.ID, Valid: true},
    MaxAttempts: vendorMaxAttempts,
}); err != nil { return err }
// tx.Commit()
```

**Never call `InsertWebhookDelivery` outside a transaction.** If the outer
transaction rolls back, the delivery row is also rolled back — which is exactly
correct. Writing the delivery row in a separate transaction would create the
possibility of a delivery row existing for a state change that never committed,
causing phantom notifications.

---

## §2 — Delivery Worker Loop

```
Every BTC_WEBHOOK_POLL_INTERVAL_MS (default: 5s):
  1. rows = GetPendingWebhookDeliveries(limit=100)
     (status='pending' AND (next_retry_at IS NULL OR next_retry_at <= NOW())
      ORDER BY next_retry_at ASC NULLS FIRST)

  2. For each row (up to BTC_WEBHOOK_WORKER_CONCURRENCY=20 concurrent):
     a. Build HTTP POST request:
        - URL: vendor's configured endpoint (fetched from vendor config)
        - Body: row.payload (JSON)
        - Headers:
            Content-Type: application/json
            X-Platform-Signature: sha256=<HMAC-SHA256(payload, vendor_secret_hmac)>
            X-Platform-Event-ID: <row.id>
            X-Platform-Event-Type: <row.event_type>
        - Timeout: BTC_WEBHOOK_TIMEOUT_MS (default: 10000ms)

     b. Execute request. Evaluate response:
        - 2xx: MarkWebhookDelivered(row.id)
        - non-2xx or error:
            new_attempt_count = row.attempt_count + 1
            if new_attempt_count >= row.max_attempts:
                MarkWebhookAttempted(row.id, status='dead_lettered',
                    last_error=<error>, next_retry_at=nil)
                emit DeadLetteredWebhookEvent to admin dashboard
            else:
                next_retry_at = NOW() + backoffWithJitter(new_attempt_count)
                MarkWebhookAttempted(row.id, status='pending',
                    last_error=<error>, next_retry_at=next_retry_at)
```

**Concurrency control:** the worker uses a semaphore of size
`BTC_WEBHOOK_WORKER_CONCURRENCY`. Each goroutine acquires a slot before issuing the
HTTP request and releases it when done (success or failure). This prevents the
worker from spawning unbounded goroutines under a large poll batch.

---

## §3 — Retry Backoff Formula

```go
func backoffWithJitter(attempt int) time.Duration {
    base := []time.Duration{
        30 * time.Second,     // attempt 1
        2 * time.Minute,      // attempt 2
        10 * time.Minute,     // attempt 3
        30 * time.Minute,     // attempt 4
        2 * time.Hour,        // attempt 5
        6 * time.Hour,        // attempt 6
        24 * time.Hour,       // attempt 7+
    }
    idx := attempt - 1
    if idx >= len(base) {
        idx = len(base) - 1
    }
    nominal := base[idx]
    // ±15% jitter to spread retries across the poll window
    jitterRange := int64(float64(nominal) * 0.15)
    jitter := time.Duration(rand.Int63n(jitterRange*2) - jitterRange)
    return nominal + jitter
}
```

---

## §4 — Webhook Signature Computation

The signature uses HMAC-SHA256 over the raw request body bytes:

```go
mac := hmac.New(sha256.New, vendorWebhookSigningKey)
mac.Write(payloadBytes)
sig := hex.EncodeToString(mac.Sum(nil))
header := "sha256=" + sig
```

`vendorWebhookSigningKey` is the raw secret bytes — not the HMAC stored in the DB.
The application decrypts it from the vendor config at delivery time using the
platform `TOKEN_ENCRYPTION_KEY` (AES-256-GCM via `crypto.Encryptor`).

**Vendor-side verification (Go example):**
```go
expected := computeSignature(body, secret)
received := r.Header.Get("X-Platform-Signature")
if !hmac.Equal([]byte(expected), []byte(received)) {
    // reject
}
```

**Timing-safe comparison is required.** Plain string equality leaks timing
information that could allow a timing oracle attack on the secret.

---

## §5 — Dead Letter Management

Dead lettered deliveries have `status = 'dead_lettered'`. The admin dashboard
surfaces these with the full payload, the last error, and the attempt history.

**Admin actions:**
- **Retry once:** reset to `status='pending'`, `next_retry_at=NOW()`, `attempt_count=0`.
  Logs an admin audit event with the admin's identity.
- **Abandon:** set `status='abandoned'`. Terminal. No further delivery attempts.
  Logs an admin audit event.

**Automated vendor suspension:** a background job counts dead-lettered deliveries
per vendor per 7-day rolling window. When the count exceeds 100, the vendor's webhook
is suspended (`vendor_webhook_config.suspended=TRUE`), and a CRITICAL alert fires.
New events are not delivered to a suspended webhook endpoint; delivery rows are
still written but with `status='suspended_skip'` (no worker picks them up).

The vendor is emailed and must re-enable the webhook from their dashboard after
fixing their endpoint.

---

## §6 — Test Inventory

### Legend
- `[UNIT]` — pure unit test, no DB or network
- `[INTG]` — integration test requiring real PostgreSQL
- `[RACE]` — must be run with `-race` flag

### TI-24: Webhook Delivery

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-24-01 | `TestWebhook_OutboxPattern_WrittenInSameTx` | INTG | InsertWebhookDelivery in same tx as state change; rollback removes both |
| TI-24-02 | `TestWebhook_OutboxPattern_TxRollback_NoDeliveryRow` | INTG | Outer tx rolls back; no webhook_deliveries row exists |
| TI-24-03 | `TestWebhook_Delivery_2xx_MarksDelivered` | INTG | 2xx response → MarkWebhookDelivered; status=delivered |
| TI-24-04 | `TestWebhook_Delivery_5xx_RetriesWithBackoff` | INTG | 500 response → status stays pending; next_retry_at set |
| TI-24-05 | `TestWebhook_Delivery_Timeout_RetriesWithBackoff` | INTG | Timeout → status pending; backoff applied |
| TI-24-06 | `TestWebhook_Delivery_ConnectionRefused_RetriesWithBackoff` | INTG | Connection refused → retry |
| TI-24-07 | `TestWebhook_RetryBackoff_ScheduleCorrect` | UNIT | 7-step schedule matches spec; 24h cap after step 6 |
| TI-24-08 | `TestWebhook_RetryBackoff_JitterApplied` | UNIT | ±15% jitter; no two consecutive retries have identical delay |
| TI-24-09 | `TestWebhook_MaxAttempts_DeadLettered` | INTG | attempt_count reaches max_attempts → dead_lettered |
| TI-24-10 | `TestWebhook_DeadLettered_AdminRetry_ResetsCounter` | INTG | Admin retries dead letter; attempt_count=0; pending |
| TI-24-11 | `TestWebhook_DeadLettered_AdminAbandon_Terminal` | INTG | Admin abandons; status=abandoned; no further retries |
| TI-24-12 | `TestWebhook_Signature_HMACSHA256_CorrectFormat` | UNIT | sha256=<hex> header; timing-safe comparison |
| TI-24-13 | `TestWebhook_Signature_WrongSecret_NotVerified` | UNIT | Different secret → different signature; mismatch |
| TI-24-14 | `TestWebhook_Worker_Concurrency_Semaphore_Respected` | INTG RACE | 100 pending; max 20 concurrent HTTP calls |
| TI-24-15 | `TestWebhook_Worker_PollOrdering_NullFirst` | INTG | next_retry_at IS NULL delivered before older timestamps |
| TI-24-16 | `TestWebhook_EventFiltering_FreeTier_OnlySettledAndConfirmed` | INTG | Free tier vendor; only two event types written |
| TI-24-17 | `TestWebhook_Idempotency_DuplicateDelivery_EventID` | INTG | Same event_id delivered twice; vendor can detect duplicate |
| TI-24-18 | `TestWebhook_VendorSuspension_100DeadLetters_7Days` | INTG | 100 dead letters in 7 days → webhook suspended; CRITICAL alert |
| TI-24-19 | `TestWebhook_PayloadCap_64KB_ExceededEventNotWritten_CriticalAlert` | UNIT | Oversized payload → not written; CRITICAL alert |
| TI-24-20 | `TestWebhook_EndpointURL_HTTPSOnly_HTTPRejected` | UNIT | HTTP endpoint URL → rejected at configuration time |
| TI-24-21 | `TestWebhook_TestPing_Must2xx_BeforeSaving` | INTG | Test ping fires at configuration; non-2xx → URL not saved |
