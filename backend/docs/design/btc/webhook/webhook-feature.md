# Webhook Feature — Behavior & Edge Cases

> **What this file is:** A plain-language description of how the platform notifies
> vendors of payment events, the delivery guarantee model, retry policy, and every
> edge case the webhook system handles.
>
> **Companion:** `webhook-technical.md` — delivery worker design, retry backoff
> formula, dead letter review, guard sequences, test inventory.
> **Schema:** `sql/schema/010_btc_payouts.sql` (`webhook_deliveries`).
> **Queries:** `sql/queries/btc.sql` (`InsertWebhookDelivery`, `GetPendingWebhookDeliveries`,
> `MarkWebhookDelivered`, `MarkWebhookAttempted`).

> ⚑ **Feature flag:** `platform_config.webhooks_enabled` (default `FALSE`).
> When `FALSE`, no `webhook_deliveries` rows are written on any state change —
> vendors receive zero HTTP notifications. The delivery worker still runs but
> finds nothing to do. This is the safest flag to enable first: flip it to `TRUE`
> once the delivery infrastructure has been validated in production.

---

## What Webhooks Do

When a significant event occurs on an invoice or payout — payment detected, invoice
settled, payout confirmed, etc. — the platform sends an HTTP POST notification to the
vendor's configured webhook endpoint. This allows vendors to update their own systems
(mark an order as paid, trigger fulfilment, notify a buyer) in real time without
polling the API.

---

## Delivery Guarantee: At-Least-Once

The platform guarantees **at-least-once delivery**: every event will be delivered at
least once to the vendor's endpoint, given that the endpoint eventually becomes
reachable. This means:

- Vendors MUST make their webhook handlers idempotent. The same event may be
  delivered more than once (e.g., after a network timeout where the vendor received
  the delivery but the platform did not receive the 2xx response).
- Each delivery includes an `event_id` (the `webhook_deliveries.id` UUID) for
  deduplication. Vendors should record processed `event_id`s to detect duplicates.

**Not guaranteed:** delivery order. Under retry conditions, a later event (e.g.,
`invoice.settled`) could be delivered before an earlier one (e.g.,
`invoice.confirming`) if the earlier event's endpoint was temporarily unreachable.
Vendors must rely on the event payload's timestamps, not delivery order.

---

## The Transactional Outbox Pattern

The most critical property of the webhook system: **the delivery row is written in
the same database transaction as the state change that triggered it**. This is the
transactional outbox pattern.

For example, when an invoice transitions from `confirming` to `settled`:
1. `TransitionInvoiceToSettled` UPDATE — in transaction
2. `CreatePayoutRecord` INSERT — in transaction
3. `InsertFinancialAuditEvent` INSERT — in transaction
4. `InsertWebhookDelivery` INSERT — in transaction
5. COMMIT

If the process crashes after step 4 but before step 5, the transaction rolls back and
the webhook row is never written — but neither is the settlement. Atomicity is
preserved: either all four writes commit together, or none do.

If the process crashes after COMMIT, the delivery row exists in the DB and the
delivery worker will pick it up on the next poll cycle. The vendor will receive
the notification, even though the original request handler never responded.

---

## Events That Trigger Webhooks

| Event type | Trigger |
|------------|---------|
| `invoice.created` | New invoice created for this vendor's product |
| `invoice.detected` | Payment seen in mempool |
| `invoice.confirming` | First block confirmation received |
| `invoice.settled` | Settlement complete |
| `invoice.expired` | Invoice expired with no payment |
| `invoice.expired_with_payment` | Payment arrived after expiry |
| `invoice.underpaid` | Payment below minimum tolerance |
| `invoice.overpaid` | Payment above maximum tolerance |
| `invoice.mempool_dropped` | Payment disappeared from mempool |
| `invoice.cancelled` | Invoice cancelled by buyer or admin |
| `invoice.reorg_admin_required` | Blockchain reorganisation detected |
| `payout.queued` | Payout ready for sweep |
| `payout.broadcast` | Sweep transaction broadcast |
| `payout.confirmed` | Sweep confirmed at 3-block depth |
| `payout.failed` | Payout failed after all retries |

The set of events delivered to a specific vendor depends on their tier's webhook
configuration. Free tier vendors receive only `invoice.settled` and `payout.confirmed`.
Higher tiers receive the full event set. Event filtering is applied at insert time —
filtered events are never written to `webhook_deliveries`.

---

## Retry Policy

Failed deliveries are retried with exponential backoff. The retry schedule is:

| Attempt | Delay before retry |
|---------|--------------------|
| 1 (first failure) | 30 seconds |
| 2 | 2 minutes |
| 3 | 10 minutes |
| 4 | 30 minutes |
| 5 | 2 hours |
| 6 | 6 hours |
| 7+ | 24 hours (capped) |

After `max_attempts` (default: 10, configurable per vendor tier) the delivery is
marked `dead_lettered`. Dead lettered deliveries are never automatically retried.
Admin review is required.

**A delivery is considered failed when:**
- The endpoint returns any HTTP status outside 2xx
- The TCP connection is refused or times out (per `BTC_WEBHOOK_TIMEOUT_MS`)
- The endpoint returns a 2xx but the response body indicates an error (not used —
  the platform treats any 2xx as success regardless of body)

---

## What Constitutes a Successful Delivery

Any HTTP 2xx response from the vendor's endpoint within `BTC_WEBHOOK_TIMEOUT_MS`
(default: 10 seconds). The response body is ignored. The platform does not retry
after a 2xx even if the vendor later claims they did not process it — the vendor's
system is responsible for idempotent processing.

---

## Security: Webhook Signature

Every delivery includes a signature header so vendors can verify the payload
originated from the platform:

```
X-Platform-Signature: sha256=<HMAC-SHA256(payload_bytes, vendor_webhook_secret)>
```

The vendor's `webhook_secret` is generated at webhook configuration time and is
never stored in plaintext after initial display. Vendors are responsible for rotating
their secret if it is compromised. The platform stores only the HMAC of the secret.

Signature verification is strongly recommended but not enforced by the platform.
A vendor that ignores the signature bears the risk of processing spoofed webhooks.

---

## Delivery Worker Design

A background worker polls `webhook_deliveries WHERE status = 'pending' AND
next_retry_at <= NOW()` every `BTC_WEBHOOK_POLL_INTERVAL_MS` (default: 5 seconds).
The worker processes up to 100 deliveries per poll cycle (the `LIMIT` on
`GetPendingWebhookDeliveries`). Each delivery is processed concurrently up to
`BTC_WEBHOOK_WORKER_CONCURRENCY` (default: 20) goroutines.

**Thundering herd prevention:** the initial `next_retry_at` is NULL (eligible
immediately). After the first failure, `next_retry_at` is set with jitter
(±15% of the nominal delay) to spread retries across the poll window.

---

## Dead Letter Review

Dead lettered deliveries appear in the admin dashboard under
"Undelivered Webhooks". For each:
- Full event payload is available for inspection
- Last error message is recorded
- Admin can trigger a one-time manual retry (resets to `pending` with `next_retry_at = NOW()`)
- Admin can mark as abandoned (no retry; final state)

If a vendor's endpoint is repeatedly dead-lettering deliveries, the platform sends
an email alert to the vendor and a WARNING alert to admins. After 100 dead-lettered
deliveries from the same vendor within 7 days, the vendor's webhook is automatically
suspended and a CRITICAL alert fires.

---

## Vendor Webhook Configuration

Vendors configure their endpoint URL and optionally a secret in their dashboard.
The endpoint URL must be:
- HTTPS only (HTTP rejected, even on testnet4 development environments)
- Publicly reachable (platform cannot deliver to localhost or private IPs)
- Validated via a test ping at configuration time (the vendor's endpoint must return
  2xx for a `webhook.test` event before the URL is saved)

---

## Edge Cases

### Vendor deletes webhook endpoint mid-delivery
The delivery worker continues attempting delivery against the stored URL. Deliveries
against deleted endpoints eventually dead-letter. If the vendor reconfigures a new
URL while deliveries are pending, the pending deliveries target the old URL (they
were written with the endpoint captured at event time). New events after
reconfiguration use the new URL.

### Platform outage during delivery
Deliveries that failed during a platform outage will be retried when the delivery
worker restarts. The `next_retry_at` timestamp ensures they are not retried
immediately on restart — each delivery picks up at its correct position in the
backoff schedule.

### Large payload
Payloads are capped at 64KB. If an event payload would exceed this (unlikely — all
platform events are well under 1KB), the delivery is logged as an error and the
event is NOT written to `webhook_deliveries`. A CRITICAL alert fires.
