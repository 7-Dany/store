# Audit Feature — Behavior & Edge Cases

> **What this file is:** A plain-language description of the financial audit trail
> and the reconciliation system. Read this to understand the feature contract before
> looking at any implementation detail.
>
> **Companion:** `audit-technical.md` — audit table DB enforcement, reconciliation
> formula, job scheduling, treasury reserve tracking, test inventory.
> **Monitoring events:** The complete inventory of business-level monitoring events
> (alerts, severities, triggers) lives in
> `../../monitoring/bitcoin-monitoring.md §11`.

---

## 1. Financial Audit Trail

Every financial event is written as an immutable, append-only record containing:
timestamp, actor, satoshi amounts before/after, fiat equivalent with currency code,
originating invoice or payout reference, and for admin overrides: admin identity,
mandatory written reason, step-up authentication timestamp.

Retained indefinitely. Separate from the authentication audit log.

---

## 2. Monitoring Events

The complete inventory of business-level monitoring events — every alert the system
fires, its trigger condition, and its severity — is maintained in the canonical
monitoring design document:

> **See:** `../../monitoring/bitcoin-monitoring.md §11 — Financial Monitoring Events`

That section is the single source of truth for alert definitions. Package docs
(including this one) describe what triggers an alert as part of their behavioral
contracts, but the authoritative severity classification and complete event list live
in the monitoring document.

---

## 3. Reconciliation

A scheduled job runs every **6 hours** checking:

```
on_chain_UTXO_value
  = SUM(vendor_internal_balances)
  + SUM(payout_records.net_satoshis
        WHERE status IN ('held','queued','constructing','broadcast'))
  + SUM(invoice_payments.value_sat
        WHERE invoice.status IN (
          'confirming', 'settling', 'settlement_failed',
          'underpaid', 'overpaid', 'reorg_admin_required',
          'expired_with_payment', 'cancelled_with_payment'))
  + platform_treasury_reserve
```

`platform_treasury_reserve` is the accumulated miner fee earnings retained by the
platform from completed sweeps. It is tracked as a dedicated balance column
(`treasury_reserve_satoshis`) on the `platform_config` table, incremented when sweep
transactions confirm (the difference between gross payout and net vendor payout equals
the fee paid to miners), and decremented when treasury funds are withdrawn or used for
UTXO consolidation.

**Additional checks:**
- Every settled invoice has a corresponding payout record or balance credit
- No payout record exists where the parent invoice is in a status earlier than `settled`
- No sweep transaction in `confirmed` status lacks a corresponding blockchain
  confirmation

### Reconciliation job failure monitoring
If the `last_successful_run_at` timestamp for the reconciliation job is more than
8 hours in the past, the "Reconciliation job missed" CRITICAL alert fires
independently of job output.

### Discrepancy response
When a discrepancy is found: CRITICAL alert fires, platform enters **sweep-hold mode**.
Sweeps resume only after admin resolution with written reason recorded to audit trail.
Hold > 4 hours triggers escalation to platform owner.
