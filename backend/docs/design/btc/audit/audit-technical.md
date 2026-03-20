# Audit — Technical Implementation

> **What this file is:** Implementation contracts for financial audit trail DB
> enforcement, the reconciliation formula, treasury reserve tracking, job scheduling,
> and the complete test inventory for this package.
>
> **Read first:** `audit-feature.md` — behavioral contract and monitoring event
> inventory.

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
The application DB user has `INSERT` and `SELECT` on the audit table. `UPDATE` and
`DELETE` are not granted.

### Database trigger layer
An additional trigger rejects any `UPDATE` or `DELETE` on the audit table regardless
of which DB user initiates it (including privileged migration users):

```sql
CREATE OR REPLACE FUNCTION audit_immutability_guard()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'financial_audit_events is immutable: % not permitted', TG_OP;
END;
$$;

CREATE TRIGGER trg_audit_no_update
    BEFORE UPDATE ON financial_audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_immutability_guard();

CREATE TRIGGER trg_audit_no_delete
    BEFORE DELETE ON financial_audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_immutability_guard();
```

Both layers together ensure immutability: the permission layer prevents accidental
application code updates, and the trigger layer prevents any DB-level override.

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

```sql
-- The reconciliation invariant:
SELECT
  (SELECT COALESCE(SUM(balance_satoshis), 0) FROM vendor_balances) +
  (SELECT COALESCE(SUM(net_satoshis), 0) FROM payout_records
   WHERE status IN ('held','queued','constructing','broadcast')) +
  (SELECT COALESCE(SUM(ip.value_sat), 0)
   FROM invoice_payments ip
   JOIN invoices i ON ip.invoice_id = i.id
   WHERE i.status IN (
     'confirming', 'settling', 'settlement_failed',
     'underpaid', 'overpaid', 'reorg_admin_required',
     'expired_with_payment', 'cancelled_with_payment'
   )) +
  (SELECT treasury_reserve_satoshis FROM platform_config)
AS expected_on_chain_satoshis
```

### treasury_reserve_satoshis
The `treasury_reserve_satoshis` column on `platform_config` tracks accumulated miner
fee earnings retained by the platform from completed sweeps.

**Incremented:** when a sweep transaction confirms at 3-block depth. The increment
equals `(gross_payout_satoshis - SUM(vendor_net_satoshis))` — the difference is the
miner fee that was deducted from the on-chain UTXOs.

**Decremented:** when treasury funds are withdrawn or used for UTXO consolidation.

**Why it's needed:** without this term, the formula would not balance after sweeps
have occurred — the on-chain UTXO value would be lower than the sum of vendor
balances and payout records because miner fees have already been spent.

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
