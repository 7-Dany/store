# Settlement Feature — Behavior & Edge Cases

> **What this file is:** A plain-language description of the settlement engine — how
> confirmed payments are split between platform and vendor, how underpayments,
> overpayments, and hybrid mode are handled, and every resolution path for exceptional
> states. Read this to understand the feature contract before looking at any
> implementation detail.
>
> **Companion:** `settlement-technical.md` — atomicity contracts, state machines,
> error classification, locking patterns, test inventory.
> **Depends on:** `../payment/payment-feature.md` (confirmation trigger),
> `../vendor/vendor-feature.md` (tier config, wallet modes).

---

## 1. What Settlement Does

Settlement is the process of splitting a confirmed payment between the platform
(processing fee) and the vendor (net amount), and initiating the payout or balance
credit. It runs when an invoice reaches its required confirmation depth.

---

## 2. Settlement Steps

When an invoice reaches its required confirmation depth, the following sequence runs:

**Phase 1 — Pre-claim checks (run BEFORE acquiring `settling` status):**
1. SUM all `invoice_payments.value_sat` for this invoice's txid(s). Multi-output
   transactions to the same address are correctly summed.
2. Check tolerance: compare received amount against the snapshotted tolerance band.
   - If received amount < invoiced amount − tolerance band: transition
     `confirming → underpaid` directly (atomic WHERE status='confirming' UPDATE,
     RowsAffected check, no `settling` claim). Release. Done.
   - If received amount > invoiced amount + tolerance band AND both the snapshotted
     relative AND absolute overpayment thresholds are exceeded: transition
     `confirming → overpaid` directly. Release. Done.
   - Otherwise: within tolerance. Proceed to Phase 2.

**Phase 2 — Settlement atomic transaction:**
3. Acquire claim: atomic `confirming → settling` (WHERE status='confirming' AND id=$1;
   check RowsAffected == 1). Set `settling_source='confirming'`. If 0 rows: another
   worker got there first — exit cleanly.
4. Platform processing fee calculated on received amount from snapshotted rate.
5. Vendor net amount determined. If net = 0 satoshis: `ErrZeroNetAmount` →
   transition to `settlement_failed`; CRITICAL alert. Release.
6. Economic validation: net > fee floor (batch-amortized for batch vendors,
   single-output for Enterprise).
7. Invoice status set to `settled` (WHERE status='settling' AND id=$1;
   RowsAffected check — 0 rows triggers rollback).
8. Financial audit event written.
9. Depending on snapshotted wallet mode:
   - **Bridge mode:** payout record created (`queued` or `held`)
   - **Platform wallet mode:** vendor balance credited (SELECT FOR UPDATE on balance row)
   - **Hybrid mode:** vendor balance credited; auto-sweep threshold check runs

If any step in Phase 2 fails, the transaction rolls back entirely. Invoice returns to
`confirming`. The engine retries per the retry schedule in `settlement-technical.md §2`.

---

## 3. Two Fees, Always Tracked Separately

**Platform processing fee** — the percentage the platform deducts from vendor earnings.
Calculated at settlement time on the **received** satoshi amount (for within-tolerance
payments). Recorded as a permanent financial event.

**Bitcoin network miner fee** — paid to miners at sweep time from the platform's
treasury. Tracked separately from the processing fee for tier profitability analysis.

---

## 4. Underpayment

The invoice transitions from `confirming` to `underpaid` at settlement time when the
tolerance check runs in Phase 1 (before the `settling` claim is acquired).

**Underpaid re-settlement:** when a new payment arrives on an `underpaid` invoice and
the combined total is within tolerance, the settlement engine is re-invoked via the
same two-phase atomic claim mechanism. The `underpaid → settling` transition is
acquired first. The underpaid re-settlement uses the **original invoice snapshot** —
the same fee rate, cap, and confirmation target that governed the initial settlement
attempt.

**Resolution options for still-underpaid:**
- Buyer sends remaining amount to the same address → re-settlement triggers automatically
- `underpaid → refunded`: refund to the refund address collected at checkout.
  If no refund address was provided, funds are held for 30 days pending buyer
  contacting support.
- `underpaid → manually_closed`: admin writes off after 30 days unresolved.

**Monitoring alerts:** if an `underpaid` invoice remains unresolved for more than
7 days, a WARNING alert fires. After 30 days, a CRITICAL alert fires.

---

## 5. Overpayment

The invoice transitions from `confirming` to `overpaid` (direct, before `settling`
claim) when the received total exceeds the invoiced amount by **both** more than the
tier's snapshotted `overpayment_relative_threshold` (default: 10%) **and** more than
the tier's snapshotted `overpayment_absolute_threshold` (default: 10,000 satoshis).
Both thresholds are configurable per tier, snapshotted at invoice creation, and
enforced conjunctively — both must be exceeded for the overpaid path to trigger.

If either threshold is not exceeded: settled normally; vendor receives the full
received amount; fee calculated on received amount; excess recorded on the financial
audit event.

**`overpaid → settled` resolution:** admin applies the invoiced amount to the vendor
settlement. The vendor receives `invoiced_amount - platform_fee` (fee calculated on
`invoiced_amount`). The excess (`received_amount - invoiced_amount`) is returned to
the buyer on-chain. Two payout records are created in the same atomic transaction:
(1) vendor net payout on `invoiced_amount`, and (2) a buyer refund payout record for
the excess amount.

If no buyer refund address was provided at checkout, the excess refund payout record
is created in `held` status with `reason='no_refund_address'`. A WARNING alert fires
after 7 days and a CRITICAL alert after 30 days if unresolved.

`overpaid → manually_closed`: admin writes off with mandatory reason.

---

## 6. Hybrid Mode Settlement and Auto-Sweep

In hybrid mode, vendor earnings accumulate as an internal balance. When the balance
accumulated since the last sweep crosses the vendor's snapshotted
`auto_sweep_threshold`, all accumulated balance is automatically swept to the vendor's
external address.

**Balance semantics:**
- The vendor's internal `balance_satoshis` represents total accumulated earnings not
  yet queued for sweep.
- **Incremented** when a hybrid mode settlement credits net satoshis (Phase 2).
- **Decremented** when `held` payout records are promoted to `queued` (the threshold
  crossing event). At promotion time, the balance is reduced by the total of all
  promoted `held` records.
- NOT decremented at broadcast or confirmation — those transitions are already
  accounted for by the payout records moving through the state machine.

This means: after the first threshold crossing, the balance resets to near zero.
Subsequent settlements accumulate again from near zero. The auto-sweep trigger is:
`balance_after_increment >= auto_sweep_threshold` (where balance reflects only
earnings not yet queued for sweep).

**Auto-sweep threshold snapshot:** `auto_sweep_threshold` (satoshis) is snapshotted
on every invoice at creation time. The threshold that governs a settlement is from
the invoice snapshot — not the vendor's current configured threshold.

**Hybrid payout record model:** one payout record per settled invoice (same model as
bridge mode). Each settlement creates a payout record with status `held`. When the
accumulated balance crosses the threshold, all `held` payout records for that vendor
are promoted to `queued` together (SELECT FOR UPDATE on vendor balance row) and
included in the next sweep.

---

## 7. Admin Resolution of settlement_failed

Two actions (both require step-up authentication):
- **Retry settlement** — replays from the beginning. Retry counter resets to 0.
- **Mark as manual** — writes mandatory resolution note to financial audit trail.

A `settlement_failed` invoice by definition has neither a payout record nor a balance
credit. Admin-triggered retries reset the retry counter to 0. Replaying from the
beginning cannot double-pay.

---

## 8. Admin Resolution of reorg_admin_required

Three actions (all require step-up authentication):
- **Wait for re-confirmation** — if original payment tx re-confirms AND sweep tx is
  confirmed in the new chain, system automatically transitions to `settled`.
- **Request reversal from vendor** — recorded in audit trail; resolves to
  `manually_closed`.
- **Absorb as platform loss** — mandatory reason; invoice transitions to
  `manually_closed`.

A CRITICAL escalation alert fires if `reorg_admin_required` has not been resolved
after 72 hours. The alert repeats weekly until resolved.

See `../resilience/resilience-technical.md §8` for the full re-confirmation check
sequence before auto-transitioning to `settled`.

---

## 9. Platform Wallet Withdrawals

Address is validated (network-aware + RPC `getaddressinfo` ismine check to reject
platform-managed addresses).

**Processing rules:** below approval threshold → automatic; above → approval workflow.

**Minimum withdrawal:** minimum invoice amount + estimated single-output miner fee.

---

## 10. Internal Balance Debits (Subscription Fees)

Vendors in platform wallet mode may elect to pay platform subscription fees from
their Bitcoin balance.

When a subscription fee is due, the billing system submits a debit request. The
payment system converts the fiat amount to satoshis using the current cached rate
(floor rounding, consistent with invoice creation). If the cache is older than
`BTC_SUBSCRIPTION_DEBIT_MAX_RATE_AGE_SECONDS` (default: 120 seconds), the debit is
deferred. After 3 deferred attempts spanning more than 24 hours, the debit proceeds
using the most recent cached rate with an `ErrRateStale` flag on the financial audit
event. A WARNING alert fires when this occurs.

If the vendor's balance is sufficient, the debit executes atomically. If insufficient,
the debit is rejected with `ErrInsufficientBalance` — no partial debits. The vendor's
internal balance can never go below zero.
