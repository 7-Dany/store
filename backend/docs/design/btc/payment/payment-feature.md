# Payment Feature — Behavior & Edge Cases

> **What this file is:** A plain-language description of how the platform detects
> payments, handles confirmations, manages mempool drop detection, and enforces
> expiry rules. Read this to understand the feature contract before looking at any
> implementation detail.
>
> **Companion:** `payment-technical.md` — watchdog sequences, dual-trigger design,
> confirmation depth mechanics, test inventory.
> **Depends on:** `../invoice/invoice-feature.md` (invoice statuses, monitoring windows),
> `../zmq/zmq-feature.md` (ZMQ event pipeline).

---

## 1. How Payment Detection Works

The platform's ZMQ subscriber receives real-time notifications from Bitcoin Core when
new transactions appear in the mempool and when new blocks are mined. The settlement
handler matches incoming transactions against the DB-backed
`invoice_address_monitoring` table.

When a payment to a monitored address is detected:

1. An `invoice_payments` record is **always** written for every detected payment,
   regardless of invoice status (including terminal statuses). The INSERT uses
   `ON CONFLICT (txid, vout_index) DO NOTHING` for idempotency. This ensures there is
   always a DB record of every on-chain payment.
2. If the invoice is in a transitionable status (`pending`, `mempool_dropped`):
   - Invoice moves to `detected`
   - Expiry timer is frozen
   - `detected_txid` and `detected_at` are recorded on the invoice
   - Fiat equivalent at detection time is recorded (with currency code)
3. If the invoice is already in `detected` status and a new txid arrives for the same
   address: record the payment, then check `getmempoolentry(detected_txid)`.
   If the original txid is absent from the mempool, begin the two-cycle check
   (via `mempool_absent_since`). Do not update `detected_txid` until the original is
   confirmed absent (via `mempool_dropped → detected` re-detection).
   See `payment-technical.md §Mempool Drop Watchdog`.
4. If the invoice is in `underpaid` status: record the payment, re-sum all payments
   for this invoice, re-check tolerance. If the combined total now satisfies the
   invoiced amount within tolerance, invoke the settlement engine to transition
   `underpaid → settling → settled` via the atomic claim path.
5. If the invoice is in `settled`, `expired`, `cancelled`, or other terminal statuses:
   the `invoice_payments` record is written (step 1) with the appropriate flag set
   (`post_settlement`, or detected via status check), a financial audit event is
   written, and the appropriate alert fires.
6. The system waits for the required confirmation depth (per tier's snapshotted config).

---

## 2. Confirmation Depth

Settlement is triggered immediately when a confirmed invoice reaches its snapshotted
confirmation depth target. A polling safety net runs independently every 5 minutes.
Both paths share the same double-settlement prevention mechanism.

### Default confirmation depths

| Tier | Default confirmation depth |
|------|--------------------------|
| Free | 6 blocks (~60 min) |
| Growth | 3 blocks (~30 min) |
| Pro | 2 blocks (~20 min) |
| Enterprise | 1 block (~10 min) |

The **Enterprise 1-block confirmation** carries elevated reorg risk. Enterprise
tier agreements must disclose this risk explicitly.

---

## 3. Tolerance Band

Default: **±1%** platform-wide, configurable per tier. Snapshotted at invoice creation.

Within-tolerance payments: vendor receives the full received amount; platform fee
calculated on received amount.

---

## 4. Mempool Drop Handling

A mempool transaction can disappear without confirming for several reasons:
- Evicted due to fees falling below the node's minimum relay threshold
- Expired from the mempool after the node's eviction window (default: 14 days)
- Replaced by a conflicting transaction (fullrbf is default in Bitcoin Core v30+)
- Sibling eviction under TRUC (v3) policy

When a transaction disappears from the mempool, a watchdog detects the drop and
transitions the invoice to `mempool_dropped`. The expiry timer is unfrozen and the
effective expiry rules apply. The buyer is notified. The address continues to be
monitored for the standard 30-day post-expiry window.

The watchdog stores its two-cycle check state in `mempool_absent_since` on the invoice
record (DB-persisted, not in-memory). Both `detected_txid` and `mempool_absent_since`
must be present in the schema.

When an invoice transitions from `mempool_dropped` back to `detected` (re-detection
of a replacement payment), `mempool_absent_since` is explicitly cleared to NULL in the
same UPDATE statement to prevent the watchdog from immediately re-triggering the
two-cycle check on the new transaction.

### RBF and fullrbf
Bitcoin Core v30+ enables fullrbf by default. The watchdog treats all detected
transactions as potentially replaceable regardless of RBF signaling flags.

### TRUC (v3) sibling eviction
Appears to the watchdog as normal transaction absence and is handled identically.

---

## 5. Expiry During Mempool Drop

After an invoice transitions to `mempool_dropped`, the expiry timer is unfrozen.
The invoice is now subject to the standard effective expiry calculation, accounting
for any node outage periods. If the effective expiry elapses while the invoice is in
`mempool_dropped`, it transitions to `expired` and the 30-day monitoring window begins.

A new payment (replacement or otherwise) on the same address while in
`mempool_dropped` transitions the invoice back to `detected` and re-freezes the
expiry timer.

---

## 6. Cancellation During Mempool Drop

An invoice in `mempool_dropped` status may be cancelled by the buyer or admin,
subject to the constraint: there must be no `invoice_payments` records with a
confirmed status for this invoice. Since a drop means the transaction was never
confirmed, this constraint is normally satisfied. If a payment somehow confirmed
before the drop was detected, cancellation is blocked.

---

## 7. Blockchain Reorganization

When a block disconnection is detected, invoices whose first confirmed block was the
disconnected block are rolled back from `confirming` to `detected`. For invoices where
the sweep has already completed (`sweep_completed=true`), the status becomes
`reorg_admin_required`. See `../resilience/resilience-technical.md §Reorg Handling`.

---

## 8. Edge Cases

### Second txid to same address while invoice is `detected`
When ZMQ fires a `hashtx` event for a payment to an address whose invoice is already
in `detected` status, and the new txid differs from `detected_txid`:

1. Record the new payment in `invoice_payments` (step 1 idempotency rule always applies).
2. Call `getmempoolentry(detected_txid)`:
   - **If present:** the original tx is still in the mempool. The new payment may be
     a double-payment from a different wallet. Record with `double_payment=true` flag;
     fire "Double-payment detected" CRITICAL alert. Do NOT change invoice status.
   - **If absent:** begin the two-cycle check by writing `mempool_absent_since = NOW()`.
     The `mempool_dropped → detected` re-detection path will pick up the new txid
     from the next ZMQ `hashtx` event or backfill scan.

### Payment on address in `underpaid` status
The payment is recorded. All payments for the invoice are re-summed. If the combined
total is now within tolerance, the settlement engine is re-invoked using the **original
invoice snapshot** — the same fee rate, cap, and confirmation target that governed the
initial settlement attempt.

### Payment on address in terminal status
The payment is always recorded. The appropriate flag is set (`post_settlement` or
determined by status check). A financial audit event is written. The appropriate CRITICAL
alert fires. Admin review is required.
