# Dispute Feature — Behavior & Edge Cases

> **What this file is:** A plain-language description of how the platform handles
> payment disputes — when a buyer disputes a charge, what the resolution paths are,
> and how disputes interact with the invoice and payout lifecycle.
>
> **Companion:** `dispute-technical.md` — state machine, resolution paths, DB
> integration, guard sequences, test inventory.
> **Schema:** `sql/schema/010_btc_payouts.sql` (`dispute_records`).

> ⚑ **Feature flag:** `platform_config.disputes_enabled` (default `FALSE`).
> When `FALSE`, the dispute creation endpoint returns a "feature not yet available"
> response. No `dispute_records` rows are written, no payout records can be frozen,
> and no SLA alert timers fire. Do not enable until an admin playbook exists for
> handling open disputes, since active disputes can freeze vendor funds and trigger
> CRITICAL alerts if left unresolved past the 30-day SLA.

---

## What Disputes Are

A dispute is a formal challenge raised by a buyer (or, in limited circumstances,
a vendor or admin) against a completed or in-progress invoice. Unlike traditional
payment disputes in credit card systems, Bitcoin payments are irreversible on-chain.
A Bitcoin dispute therefore cannot reverse the on-chain transaction — it can only
determine the disposition of the funds that the platform holds or controls.

Disputes are support tickets with financial consequences.

---

## When a Dispute Can Be Raised

A buyer can open a dispute on an invoice that is:
- In any non-`pending` status (i.e., a payment was made)
- Within `BTC_DISPUTE_WINDOW_DAYS` of the payment detection date (default: 60 days)

Buyers cannot open disputes on `pending` invoices (no payment made) or after the
dispute window has elapsed.

Vendors can open disputes on invoices where:
- The invoice is in `settled` status but the payout has not yet been swept
- The vendor believes the settlement amount was calculated incorrectly

Admins can open disputes on any invoice for investigation purposes.

---

## Dispute Statuses

| Status | Meaning |
|--------|---------|
| `open` | Dispute raised; under review |
| `awaiting_vendor` | Platform has requested information from the vendor |
| `awaiting_buyer` | Platform has requested information from the buyer |
| `resolved_buyer` | Resolved in buyer's favour; refund issued or queued |
| `resolved_vendor` | Resolved in vendor's favour; invoice proceeds normally |
| `resolved_platform` | Resolved with platform absorbing a loss |
| `withdrawn` | Buyer withdrew the dispute; invoice proceeds normally |
| `escalated` | Dispute requires legal or external review |

---

## Dispute Effect on Invoice and Payouts

**While a dispute is `open` or `awaiting_*`:**
- Invoices in `settled` status: any associated payout records are **frozen** — they
  cannot be swept while the dispute is active. Existing `queued` payout records are
  moved to `held` with `reason = 'dispute_hold'`.
- The invoice itself does not change status during dispute.
- New invoices from the same vendor/buyer pair are NOT blocked.

**On resolution:**
- `resolved_buyer`: payout records are cancelled or rolled back. Funds are refunded
  to the buyer (if the platform holds them — i.e., payout has not yet been swept).
  If the sweep has already confirmed, the platform records a loss and escalates to
  `resolved_platform` with mandatory reason.
- `resolved_vendor`: payout records are unfrozen; sweep proceeds normally.
- `withdrawn`: payout records are unfrozen; sweep proceeds normally.

---

## Resolution Paths

### Buyer dispute: non-delivery
The buyer claims they paid but did not receive the product or service.

This is a **vendor-side fulfilment dispute**, not a payment dispute. The payment is
confirmed on-chain. The platform's role is to hold the payout while the vendor
provides proof of delivery.

Resolution timeline:
- Day 0: dispute opened; vendor has 7 days to respond
- Day 7: if no vendor response → auto-resolve in buyer's favour (refund issued if
  funds not yet swept)
- If vendor responds with evidence: admin reviews and decides

### Buyer dispute: wrong amount
The buyer claims the amount charged differed from the quoted price.

The platform verifies against the invoice snapshot (which records the exact
BTC/fiat rate at creation). If the invoice was created correctly, the dispute is
resolved in the vendor's favour. If the invoice was created with an incorrect
rate (a platform bug), the platform absorbs the difference.

### Buyer dispute: double payment
A buyer accidentally sent payment twice to the same address. The second payment is
already recorded as `double_payment=true` in `invoice_payments`. Admin allocates the
second payment and may issue a refund.

### Vendor dispute: settlement calculation error
Vendor believes the fee deducted was incorrect. Admin verifies against the snapshotted
fee rate on the invoice. Disputes based on tier config changes after invoice creation
are always resolved in the vendor's favour — the snapshot governs.

---

## Dispute SLA

| Stage | SLA |
|-------|-----|
| First response to buyer | 2 business days |
| First contact with vendor for evidence | 3 business days |
| Vendor response deadline | 7 days |
| Full resolution | 30 days |

Breaching the SLA fires CRITICAL alerts at each threshold.

---

## Disputes and Financial Audit Trail

Every dispute status transition is recorded in `financial_audit_events`. Any
financial disposition (refund issued, funds released to vendor, platform loss
absorbed) is recorded as a financial audit event with mandatory reason. All dispute
admin actions require step-up authentication.

---

## What Disputes Cannot Do

- Reverse an already-confirmed on-chain sweep. Once funds have left the platform
  wallet and confirmed, there is no technical mechanism to recover them. The only
  resolution is to arrange a new on-chain transaction from the vendor (if they
  cooperate) or absorb as a platform loss.
- Block new invoices. A dispute on one invoice does not prevent the vendor or buyer
  from creating new invoices.
- Access private keys or sign transactions on behalf of vendors.
