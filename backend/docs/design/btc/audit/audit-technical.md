# Audit — Technical Implementation

> **What this file is:** Implementation contracts for financial audit trail DB
> enforcement, the reconciliation formula, treasury reserve tracking, job scheduling,
> and the complete test inventory for this package.
>
> **Read first:** `audit-feature.md` — behavioral contract and monitoring event
> inventory.
> **Schema:** `sql/schema/010_btc_payouts.sql` (`financial_audit_events`),
> `sql/schema/011_btc_functions.sql` (immutability triggers + actor label validation).
> **Queries:** `sql/queries/btc.sql` (`InsertFinancialAuditEvent`, `GetAuditEventsForInvoice`, `GetAuditEventsForPayout`).

---

## Table of Contents

1. [Financial Audit Trail DB Enforcement](#1--financial-audit-trail-db-enforcement)
2. [Resolution Record Pattern](#2--resolution-record-pattern)
3. [Reconciliation Formula and Treasury Reserve](#3--reconciliation-formula-and-treasury-reserve)
4. [Reconciliation Job Scheduling](#4--reconciliation-job-scheduling)
5. [Test Inventory](#5--test-inventory)

---

## §1 — Financial Audit Trail DB Enforcement

### Database permission layer
`btc_app_role` has `INSERT` and `SELECT` only on `financial_audit_events`.
`UPDATE` and `DELETE` are not granted (see grants section of `011_btc_functions.sql`).

### Database trigger layer
Two row-level triggers and one statement-level trigger defined in
`sql/schema/011_btc_functions.sql`:

| Trigger | Function | Fires on |
|---------|----------|----------|
| `trg_fae_no_update` | `fn_btc_audit_immutable` | BEFORE UPDATE — rejects unconditionally |
| `trg_fae_no_delete` | `fn_btc_audit_immutable` | BEFORE DELETE — rejects unconditionally |
| `trg_fae_no_truncate` | `fn_btc_audit_no_truncate` | BEFORE TRUNCATE — closes the gap left by row-level triggers |
| `trg_fae_validate_actor` | `fn_fae_validate_actor_label` | BEFORE INSERT — verifies `actor_label == COALESCE(email, username)` for the given `actor_id`; skips for `actor_type = 'system'` |

The permission layer prevents accidental application updates. The trigger layer
prevents any DB-level override including from privileged migration users.

**GDPR note on `actor_label`:** store `HMAC-SHA256(email, server_secret)` rather
than raw email. `financial_audit_events` is immutable — raw PII cannot be erased for
GDPR Article 17 requests. The `fn_fae_validate_actor_label` trigger checks
`COALESCE(email, username)` to support OAuth-only accounts with no email.

---

## §2 — Resolution Record Pattern

Admin resolutions are written as **new audit rows** referencing the original event's
ID. The original row is never modified.

Required fields on every admin override audit event:
- `admin_id`: identity of the admin performing the action
- `action_type`: the specific override action (e.g., `"settlement_manual_close"`,
  `"reorg_resolved_platform_loss"`)
- `reason`: mandatory written reason provided by the admin
- `references_event_id`: ID of the original audit event being resolved
- `step_up_authenticated_at`: timestamp of the TOTP verification that authorized
  this action
- `timestamp`: exact UTC timestamp of this resolution record

---

## §3 — Reconciliation Formula and Treasury Reserve

The three formula terms each have a dedicated query in `sql/queries/btc.sql`:

| Term | Query | Notes |
|------|-------|-------|
| In-flight invoice satoshis | `SumInflightInvoiceAmounts` | statuses: pending, detected, confirming, settling, underpaid, mempool_dropped |
| Pre-confirmation payout obligations | `SumInflightPayoutRecords` | statuses: held, queued, constructing, broadcast |
| Platform-mode vendor balances | `SumPlatformVendorBalances` | JOIN to vendor_wallet_config; **hybrid-mode balances excluded** (they are threshold accumulators, not value-bearing) |
| Treasury reserve | `GetPlatformConfig` → `treasury_reserve_satoshis` | |

The formula:
```
on_chain_UTXO_value
  = SumInflightInvoiceAmounts(network)
  + SumInflightPayoutRecords(network)
  + SumPlatformVendorBalances(network)
  + treasury_reserve_satoshis
```

**Important:** `SumPlatformVendorBalances` intentionally excludes hybrid-mode balances.
Hybrid balances are threshold accumulators — their value is fully represented in
`held`/`queued` payout records. Including both would double-count hybrid funds.
See `vendor_balances` table comment in `009_btc.sql`.

### treasury_reserve_satoshis
Tracks accumulated miner fee earnings from completed sweeps.

**Incremented:** via `IncrementTreasuryReserve` query, **in the same transaction as
`SetPayoutConfirmed`**. Omitting this causes a permanent negative discrepancy.

**Decremented:** on treasury withdrawal or UTXO consolidation (admin operation).

---

## §4 — Reconciliation Job Scheduling

- Runs every **6 hours** on a scheduled job.
- Records `last_successful_run_at` timestamp on the `reconciliation_job_state` table
  (or equivalent) on each successful completion.
- If `last_successful_run_at` is more than 8 hours in the past (checked by an
  independent monitoring job), fire CRITICAL alert: "Reconciliation job missed."
- On discrepancy: fire CRITICAL alert, activate sweep-hold mode, notify admin.
- Sweep-hold mode: all outgoing sweep construction and broadcast is blocked until
  admin explicitly resolves the hold with a written reason recorded to the audit trail.
- If hold > 4 hours without admin resolution: escalate to platform owner.

---

## §5 — Test Inventory

### Legend
- `[UNIT]` — pure unit test, no DB or network
- `[INTG]` — integration test requiring real PostgreSQL
- `[RACE]` — must be run with `-race` flag

### TI-14: Financial Audit Trail

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-14-01 | `TestAudit_NoUpdate_AppUser` | INTG | App DB user cannot UPDATE audit table |
| TI-14-02 | `TestAudit_NoDelete_AppUser` | INTG | App DB user cannot DELETE from audit table |
| TI-14-03 | `TestAudit_TriggerRejectsUpdate_PrivilegedUser` | INTG | Trigger fires; rejects UPDATE even from superuser |
| TI-14-04 | `TestAudit_TriggerRejectsDelete_PrivilegedUser` | INTG | Trigger fires; rejects DELETE even from superuser |
| TI-14-05 | `TestAudit_ResolutionRecord_NewRow_ReferencesOriginal` | INTG | Admin override: new row; original unchanged |
| TI-14-06 | `TestAudit_EverySettlementHasAuditRow` | INTG | Post-settlement: all settled invoices have audit rows |
| TI-14-07 | `TestAudit_AdminOverride_HasIdentity_Reason_StepupTimestamp` | INTG | Step-up auth fields in admin override rows |
| TI-14-08 | `TestAudit_PostSettlementPayment_FinancialAuditEventWritten` | INTG | **M-08**: post_settlement payment writes financial audit event |
| TI-14-09 | `TestAudit_FiatCurrencyCode_PresentOnAllRecords` | INTG | Every fiat record includes BTC_FIAT_CURRENCY code |
| TI-14-10 | `TestAudit_HybridAutoSweep_AuditEventHasBalanceBeforeAfter` | INTG | Auto-sweep event records balance_before and balance_after |
| TI-14-11 | `TestAudit_StepUpAuth_TOTP_RecordedInAuditTrail` | INTG | **Q-04**: TOTP re-verification recorded per admin action |
| TI-14-12 | `TestAudit_StepUpAuth_MissingTOTP_ActionBlocked` | INTG | **Q-04**: admin without TOTP cannot perform step-up action |
| TI-14-13 | `TestAudit_StepUpSession_Valid15Minutes` | INTG | **Q-04**: second action within 15min; no re-prompt |
| TI-14-14 | `TestAudit_StepUpSession_Expired_RepromptsAfter15Min` | INTG | **Q-04**: action after 15min window; TOTP re-prompt |

### TI-15: Reconciliation

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-15-01 | `TestReconciliation_Formula_NoDiscrepancy` | INTG | UTXO = balances + queued + in-flight + treasury |
| TI-15-02 | `TestReconciliation_TreasuryReserve_IncrementedAtSweepConfirmation` | INTG | **M-03**: treasury_reserve_satoshis incremented when sweep confirms |
| TI-15-03 | `TestReconciliation_TreasuryReserve_DecrementedOnWithdrawal` | INTG | **M-03**: treasury decremented on withdrawal |
| TI-15-04 | `TestReconciliation_DiscrepancyDetected_SweepHoldActivated` | INTG | Mismatch found; CRITICAL alert; sweep-hold mode |
| TI-15-05 | `TestReconciliation_SweepHold_ResumesAfterAdminResolution` | INTG | Admin resolves; sweeps resume |
| TI-15-06 | `TestReconciliation_JobMissed_Alert_After8Hours` | INTG | last_successful_run_at > 8h → CRITICAL alert |
| TI-15-07 | `TestReconciliation_EverySettledInvoice_HasPayoutOrBalance` | INTG | No settled invoice lacks payout or balance credit |
| TI-15-08 | `TestReconciliation_NoPayoutRecord_ForPreSettledInvoice` | INTG | No payout where parent not settled |
| TI-15-09 | `TestReconciliation_Runs6Hourly_OnSchedule` | INTG | Job scheduling verified |
| TI-15-10 | `TestReconciliation_HybridBalance_PostDecrement_Correct` | INTG | **C-02**: balance decremented at queued promotion; formula still balances |
