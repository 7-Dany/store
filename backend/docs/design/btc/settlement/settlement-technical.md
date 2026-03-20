# Settlement — Technical Implementation

> **What this file is:** Implementation contracts for settlement atomicity, locking
> patterns, the invoice state machine, the payout record state machine, error
> classification, and the complete test inventory for this package.
>
> **Read first:** `settlement-feature.md` — behavioral contract and edge cases.
> **Depends on:** `../sweep/sweep-technical.md` (SweepService interface),
> `../vendor/vendor-technical.md` (balance constraint enforcement).

---

## Table of Contents

1. [Settlement Atomicity and Locking](#1--settlement-atomicity-and-locking)
2. [Error Classification](#2--error-classification)
3. [Invoice State Machine](#3--invoice-state-machine)
4. [Payout Record State Machine](#4--payout-record-state-machine)
5. [Internal Balance Constraint](#5--internal-balance-constraint)
6. [Hybrid Mode — Phase 2 Flow](#6--hybrid-mode--phase-2-flow)
7. [Test Inventory](#7--test-inventory)

---

## §1 — Settlement Atomicity and Locking

Settlement is the highest-risk write in the system. The following guarantees are
non-negotiable and must be verified at schema design time.

### Rows-affected check — mandatory on every status UPDATE

Every `UPDATE invoices SET status = $new WHERE status = $expected AND id = $id` call
**must** check `result.RowsAffected()` immediately after execution. If
`RowsAffected() == 0`, the update must be treated as a hard error and the enclosing
transaction must roll back. An UPDATE returning 0 rows in Go's `database/sql` does
NOT return an error by default; this check must be explicitly coded.

```go
// Required pattern for every settlement status UPDATE:
result, err := tx.ExecContext(ctx,
    `UPDATE invoices SET status=$1, updated_at=NOW(), settling_source=$2
     WHERE id=$3 AND status=$4`,
    newStatus, settlingSource, invoiceID, expectedStatus)
if err != nil {
    return fmt.Errorf("status update: %w", err)
}
if n, _ := result.RowsAffected(); n == 0 {
    return ErrStatusPreconditionFailed  // triggers rollback
}
```

This pattern is required at EVERY state transition: settlement, reorg rollback,
watchdog, payout sweep worker.

### Payout record DB integrity

No payout record may be created unless the parent invoice is in `settled` status.
Enforced both at the application layer (within Phase 2) and as a DB-level trigger:
`BEFORE INSERT ON payout_records` checks that the referenced invoice has
`status = 'settled'`.

### Double-settlement prevention

The `confirming → settling` claim write uses `WHERE status = 'confirming'` so only
one worker can succeed. The `underpaid → settling` claim write uses
`WHERE status = 'underpaid'` for the same guarantee. The `settling` status is a
transient claim token — never user-facing.

A watchdog returns stale `settling` invoices (older than 5 minutes) to their
predecessor status. The watchdog reads `settling_source` to determine the target:
- `settling_source = 'confirming'` → return to `confirming`
- `settling_source = 'underpaid'` → return to `underpaid`

### SELECT FOR UPDATE on vendor held total

Within Phase 2, before reading the vendor's accumulated `held` total and creating
the new payout record, acquire a row-level lock:

```sql
-- Inside the Phase 2 atomic transaction:
SELECT accumulated_held_satoshis FROM vendor_balances
WHERE vendor_id = $1
FOR UPDATE;
-- Then: read, add new settlement, check floor, create payout record
```

This prevents concurrent settlements for the same vendor from both creating `held`
records when the combined total would clear the floor.

### Broadcast ordering invariant — DB before network

The `constructing → broadcast` DB status update (with txid written) **must commit and
be verified before `sendrawtransaction` is called**. This is a hard invariant:

```
Step A: finalizepsbt → raw_hex
Step B: Compute txid from raw_hex (deterministic double-SHA256 of serialized tx)
Step C: DB transaction:
          UPDATE payout_records SET status='broadcast', txid=$txid, broadcast_at=NOW()
          WHERE status='constructing' AND batch_id=$batch
          -- assert RowsAffected > 0; commit
        If RowsAffected == 0: watchdog already reclaimed these records.
          Abort — do NOT call sendrawtransaction.
        If DB commit fails: return error — do NOT call sendrawtransaction.
Step D: sendrawtransaction(raw_hex, maxfeerate)
```

If `sendrawtransaction` subsequently fails after a successful DB commit, the records
are in `broadcast` status with a txid that never made it to the network. The
stuck-sweep watchdog (see `../sweep/sweep-technical.md §Stuck Sweep`) will detect
this and trigger RBF or escalation. This is recoverable. Broadcasting before the DB
update is NOT recoverable if the DB update then fails.

---

## §2 — Error Classification

### Business logic errors (never retried)

| Error | Description |
|-------|-------------|
| `ErrInsufficientBalance` | PostgreSQL `check_violation` (SQLSTATE 23514) on balance column |
| `ErrZeroNetAmount` | Vendor net amount computed as 0 satoshis after fee deduction |
| `ErrStatusPreconditionFailed` | Status UPDATE affected 0 rows — concurrent state change |
| `ErrPSBTIncomplete` | `walletprocesspsbt` returned `complete=false` |
| `ErrPSBTNotFinalized` | `finalizepsbt` returned `complete=false` |

Business logic errors are returned immediately without retry. Logged at WARN level
and written to the financial audit trail.

### Transient errors
Database write conflict, short read timeout, deadlock on non-critical row.

| Attempt | Wait before retry |
|---------|------------------|
| 1st | 30 seconds |
| 2nd | 2 minutes |
| 3rd | 5 minutes |

### Infrastructure errors
Bitcoin Core node unreachable, wallet RPC unavailable, DB connection pool exhausted,
or any unclassified error.

| Attempt | Wait before retry |
|---------|------------------|
| 1st | 5 minutes |
| 2nd | 15 minutes |
| 3rd | 1 hour |
| 4th | 4 hours |

After 5 total failures across any error type → `settlement_failed` + CRITICAL alert.

---

## §3 — Invoice State Machine

Only the transitions listed here are permitted. Any write producing an unlisted
transition must be rejected at the application layer.

### Permitted transitions

| From | To | Trigger |
|------|----|---------|
| `pending` | `detected` | ZMQ mempool notification for the invoice address |
| `pending` | `expired` | Effective expiry time elapses (outage-adjusted) with no payment |
| `pending` | `cancelled` | Buyer or admin cancellation |
| `detected` | `confirming` | Block confirmation received for the detected txid |
| `detected` | `mempool_dropped` | Watchdog confirms transaction absent from mempool (two-cycle check) |
| `confirming` | `settling` | Settlement worker claims the invoice (Phase 2, tolerance check already passed); `settling_source='confirming'` |
| `confirming` | `detected` | Block disconnection (reorg) removes the confirming block |
| `confirming` | `underpaid` | Phase 1: received amount < invoiced − tolerance (direct, no `settling` claim) |
| `confirming` | `overpaid` | Phase 1: received amount > invoiced + tolerance (direct, no `settling` claim) |
| `settling` | `settled` | Atomic Phase 2 transaction commits (RowsAffected check passes) |
| `settling` | `confirming` | Stale claim watchdog returns invoice when `settling_source='confirming'`; OR RowsAffected == 0 on `settling → settled` UPDATE (concurrent reorg) |
| `settling` | `underpaid` | Stale claim watchdog returns invoice when `settling_source='underpaid'` |
| `settling` | `settlement_failed` | Max retries exhausted |
| `mempool_dropped` | `expired` | Effective expiry elapses after drop confirmed |
| `mempool_dropped` | `detected` | Transaction rebroadcast and re-detected; `detected_txid` updated; `mempool_absent_since` explicitly cleared to NULL |
| `mempool_dropped` | `cancelled` | Buyer or admin cancellation; guard: no confirmed payment exists |
| `expired` | `expired_with_payment` | Payment detected on expired address within 30-day monitoring window |
| `cancelled` | `cancelled_with_payment` | Payment detected on cancelled address within 30-day monitoring window |
| `settled` | `reorg_admin_required` | Block reorg detected AND `sweep_completed = true` |
| `settled` | `refunded` | Admin issues on-chain refund for post-settlement payment (step-up auth required) |
| `underpaid` | `settling` | Combined total (all payments) now within tolerance; settlement engine re-invoked; `settling_source='underpaid'` |
| `underpaid` | `refunded` | Refund issued and confirmed on-chain |
| `underpaid` | `manually_closed` | Admin writes off after 30 days; mandatory reason recorded |
| `overpaid` | `settled` | Admin applies invoiced amount; two payout records created atomically (vendor net + buyer excess refund) |
| `overpaid` | `manually_closed` | Admin writes off; mandatory reason recorded |
| `expired_with_payment` | `refunded` | Refund issued and confirmed on-chain |
| `expired_with_payment` | `settled` | Admin applies to new invoice with both parties' consent |
| `expired_with_payment` | `manually_closed` | Admin writes off; mandatory reason recorded |
| `cancelled_with_payment` | `refunded` | Refund issued and confirmed on-chain |
| `cancelled_with_payment` | `manually_closed` | Admin creates new invoice; original transitions to manually_closed with reference to new invoice ID |
| `settlement_failed` | `settling` | Admin triggers retry (step-up auth required); retry counter resets to 0; `settling_source` set to original predecessor status |
| `settlement_failed` | `manually_closed` | Admin marks manually resolved (step-up auth required) |
| `reorg_admin_required` | `settled` | Original payment tx AND sweep tx both re-confirmed on-chain after reorg (ZMQ detection + sweep tx verification) |
| `reorg_admin_required` | `manually_closed` | Admin resolves (step-up auth required) |

Terminal statuses with no further automated transitions: `refunded`, `cancelled`
(unless payment arrives), `expired` (unless payment arrives), `manually_closed`.
`settled` can transition to `reorg_admin_required` or `refunded` (post-settlement
payment admin refund).

### Notes on specific transitions
- `pending → detected` freezes the expiry timer.
- `confirming → detected` (reorg rollback) does not reset the expiry timer.
- `settling` is a transient claim status — never user-facing.
- `settling_source` column determines which status `settling` returns to when:
  (a) stale claim watchdog fires, or (b) RowsAffected == 0 on `settling → settled`.
- `confirming → underpaid` and `confirming → overpaid` bypass `settling` — they run
  in Phase 1, before any claim is acquired.
- `reorg_admin_required` is only reachable when `sweep_completed = true`. Invoices
  where no sweep has broadcast revert to `detected`.
- `mempool_dropped → cancelled` requires that no `invoice_payments` records with
  confirmed status exist for this invoice.
- `cancelled_with_payment → manually_closed` (not `settled`) — a new invoice is
  created for the resolution; the original cancelled invoice is never marked settled.

### Reorg rollback scope
`rollbackSettlementFromHeight` handles all statuses that can exist when
`first_confirmed_block_height` is set:

| Status | sweep_completed | Action |
|--------|----------------|--------|
| `confirming` | false | → `detected` |
| `settling` | false | → `detected` (regardless of settling_source — the confirming block is gone) |
| `settling` | true | → `reorg_admin_required` |
| `settled` | false | → `detected` |
| `settled` | true | → `reorg_admin_required` |
| `underpaid` | false | → `detected` |
| `overpaid` | false | → `detected` |
| `settlement_failed` | false | → `detected` |
| `reorg_admin_required` | true | → `reorg_admin_required` (already in correct status; update `first_confirmed_block_height` to new height) |

The SQL rollback query **must include all statuses in this table**. The
`reorg_admin_required` row handles the case where an invoice re-confirmed (updating
`first_confirmed_block_height` to a new block) and that new block is then also
reorged — the invoice stays in `reorg_admin_required` but its
`first_confirmed_block_height` is reset.

---

## §4 — Payout Record State Machine

| From | To | Trigger |
|------|----|---------|
| (created) | `held` | Net amount below fee floor at settlement time |
| (created) | `queued` | Net amount cleared the floor at settlement time |
| `held` | `queued` | Accumulated total for vendor clears the floor (SELECT FOR UPDATE on vendor balance row) |
| `held` | `manual_payout` | Admin declares out-of-band payment; mandatory txid + reason in audit trail; step-up auth required |
| `held` | `failed` | Admin declares unresolvable; step-up auth required; CRITICAL alert fires |
| `queued` | `constructing` | Sweep job assigns record to an active batch |
| `constructing` | `broadcast` | DB update commits with txid (step 5b of broadcast sequence); then sendrawtransaction called |
| `constructing` | `queued` | Batch construction/broadcast failed; OR vendor suspended at broadcast time; OR stale watchdog returns record |
| `broadcast` | `confirmed` | Batch transaction confirmed at 3-block depth (atomic across all records in batch) |
| `broadcast` | `queued` | Transaction dropped from mempool; OR payout record rolled back by reorg handling |
| `confirmed` | `refunded` | Refund issued and confirmed on-chain (post-settlement payment refund) |
| `confirmed` | `queued` | Payout record rolled back by reorg handling when sweep tx dropped |
| `queued` | `failed` | Max sweep retries exhausted |
| `failed` | `queued` | Admin re-queues with fresh fee estimate (step-up auth required) |
| `failed` | `refunded` | Admin refunds buyer on-chain (step-up auth required) |
| `failed` | `manual_payout` | Admin marks as manually paid; txid recorded in audit trail (step-up auth required); terminal |

**Constructing stale watchdog:** records in `constructing` status older than
`BTC_CONSTRUCTING_WATCHDOG_THRESHOLD` (default: 10 minutes) are returned to `queued`
by a watchdog job. WARNING alert fires.

**Held aging:** 7 days in `held` → WARNING alert. 30 days in `held` → CRITICAL alert.

---

## §5 — Internal Balance Constraint

A vendor's internal balance can never go below zero. Enforced by a
`CHECK (balance_satoshis >= 0)` constraint. Any violation produces a `check_violation`
(SQLSTATE 23514) classified as `ErrInsufficientBalance` — never retried.

Within every settlement or debit operation, the balance row is locked with
`SELECT FOR UPDATE` before reading the current value.

---

## §6 — Hybrid Mode — Phase 2 Flow

```
1. Acquire SELECT FOR UPDATE on vendor balance row.
2. Increment vendor's balance_satoshis by the net amount.
3. Auto-sweep check: if balance_satoshis >= snapshotted_auto_sweep_threshold:
   a. DECREMENT balance_satoshis by the total of all current `held` payout records
      for this vendor (balance resets to near zero — ready to accumulate for the
      next cycle).
   b. Set all `held` payout records for this vendor to `queued` (bulk UPDATE
      WHERE vendor_id=$1 AND status='held').
   c. Create new payout record for this settlement's net amount with status `queued`.
   d. Write financial audit event: "Hybrid auto-sweep triggered; balance_before=X;
      balance_after=Y; records_promoted=N."
4. If balance_satoshis < threshold:
   - Create new payout record with status `held`.
   - Do NOT decrement balance (it stays as the running accumulated total).
```

**Economic floor for hybrid auto-sweep:** if at promotion time the sum of the
vendor's `held` payout records does not exceed the fee floor (fee spike), the records
remain `held` and the vendor is notified. The fee-floor re-evaluation background job
periodically re-evaluates all `held` records and promotes when conditions allow.
This job acquires SELECT FOR UPDATE on the vendor balance row before acting.

---

## §7 — Test Inventory

### Legend
- `[UNIT]` — pure unit test, no DB or network
- `[INTG]` — integration test requiring real PostgreSQL
- `[RACE]` — must be run with `-race` flag

### TI-6: Settlement Engine

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-6-01 | `TestSettlement_HappyPath_BridgeMode_CreatesPayoutRecord` | INTG | Phase 1+2; payout record in queued |
| TI-6-02 | `TestSettlement_HappyPath_PlatformWallet_CreditsBalance` | INTG | Balance credited; SELECT FOR UPDATE used |
| TI-6-03 | `TestSettlement_HappyPath_HybridMode_BelowThreshold_Held` | INTG | Below threshold; payout in held |
| TI-6-04 | `TestSettlement_HappyPath_HybridMode_CrossesThreshold_Queued` | INTG | **C-02 fix**: threshold crossed; balance decremented; records queued |
| TI-6-05 | `TestSettlement_HybridMode_PostThreshold_SmallSettlements_Accumulate` | INTG | **C-02 fix**: after first sweep, subsequent settlements accumulate again |
| TI-6-06 | `TestSettlement_ConcurrentWorkers_OnlyOneSettles` | INTG RACE | Two workers race; one gets RowsAffected=0 |
| TI-6-07 | `TestSettlement_RowsAffectedZero_RollsBack` | INTG | settling→settled UPDATE returns 0 rows; tx rolled back |
| TI-6-08 | `TestSettlement_ZeroNetAmount_TransitionsToFailed` | INTG | Fee = full amount; ErrZeroNetAmount; CRITICAL alert |
| TI-6-09 | `TestSettlement_EconomicValidation_BelowFloor_HeldPayout` | INTG | Net < floor; payout in held |
| TI-6-10 | `TestSettlement_Phase1_Underpayment_SkipsSettlingClaim` | INTG | confirming→underpaid direct; no settling claim |
| TI-6-11 | `TestSettlement_Phase1_Overpayment_BothThresholds_SkipsSettlingClaim` | INTG | Both thresholds exceeded; confirming→overpaid direct |
| TI-6-12 | `TestSettlement_Phase1_Overpayment_OnlyRelative_SettledNormally` | INTG | Only relative exceeded; settled normally |
| TI-6-13 | `TestSettlement_Phase1_Overpayment_OnlyAbsolute_SettledNormally` | INTG | Only absolute exceeded; settled normally |
| TI-6-14 | `TestSettlement_Phase1_WithinTolerance_ProceedsToPhase2` | INTG | 1% underpay within tolerance; proceeds |
| TI-6-15 | `TestSettlement_PayoutRecordCreated_WithinSameTransaction` | INTG | Status and payout in same DB tx; partial commit impossible |
| TI-6-16 | `TestSettlement_DBTrigger_RejectsPayout_ForNonSettledInvoice` | INTG | Payout INSERT on non-settled invoice; trigger rejects |
| TI-6-17 | `TestSettlement_CheckViolation_InsufficientBalance_NotRetried` | INTG | SQLSTATE 23514; ErrInsufficientBalance; no retry |
| TI-6-18 | `TestSettlement_RetryLogic_TransientError_ExponentialBackoff` | UNIT | 30s, 2min, 5min; 5 failures → settlement_failed |
| TI-6-19 | `TestSettlement_AdminRetry_ResetsRetryCounter` | INTG | settlement_failed → admin retry → counter resets to 0 |
| TI-6-20 | `TestSettlement_AdminManualClose_MandatoryReason` | INTG | settlement_failed → manually_closed; reason in audit trail |
| TI-6-21 | `TestSettlement_MultiOutputTx_Summed_ForTolerance` | INTG | Single tx, multiple vouts; all summed before tolerance |
| TI-6-22 | `TestSettlement_SelectForUpdate_VendorBalanceRow` | INTG | Phase 2 holds balance row lock |
| TI-6-23 | `TestSettlement_StaleSettlingClaim_Confirming_ReturnedToConfirming` | INTG | settling > 5min with source='confirming' → confirming |
| TI-6-24 | `TestSettlement_StaleSettlingClaim_Underpaid_ReturnedToUnderpaid` | INTG | settling > 5min with source='underpaid' → underpaid |
| TI-6-25 | `TestSettlement_SettlingSource_SetCorrectly_Confirming` | INTG | settling_source='confirming' when from confirming |
| TI-6-26 | `TestSettlement_SettlingSource_SetCorrectly_Underpaid` | INTG | settling_source='underpaid' when from underpaid |

### TI-7: Underpayment Resolution

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-7-01 | `TestUnderpaid_SecondPayment_CombinedWithinTolerance_Settles` | INTG | Combined total within tolerance; re-settlement triggers |
| TI-7-02 | `TestUnderpaid_ConcurrentSecondPayments_OnlyOneSettles` | INTG RACE | **C-01 fix**: two concurrent payments; atomic underpaid→settling claim; one wins |
| TI-7-03 | `TestUnderpaid_SettlingClaim_RowsAffected_PreventsDoubleSettle` | INTG | **C-01 fix**: second worker gets RowsAffected=0 on underpaid→settling |
| TI-7-04 | `TestUnderpaid_SecondPayment_StillBelowThreshold_StaysUnderpaid` | INTG | Still below; stays underpaid |
| TI-7-05 | `TestUnderpaid_Refund_ToRefundAddress` | INTG | underpaid → refunded; refund address required |
| TI-7-06 | `TestUnderpaid_Refund_NoRefundAddress_HeldPending` | INTG | No refund address; funds held; support notification |
| TI-7-07 | `TestUnderpaid_ManualClose_After30Days` | INTG | 30-day CRITICAL alert; admin manually_closes |
| TI-7-08 | `TestUnderpaid_7DayWarningAlert_Fires` | INTG | 7 days unresolved → WARNING |
| TI-7-09 | `TestUnderpaid_ResettlementUsesOriginalSnapshot` | INTG | **M-04**: fee rate at re-settlement uses original snapshot |
| TI-7-10 | `TestUnderpaid_ReorgDuringSettling_ReturnsToUnderpaid` | INTG | Reorg while in settling (source=underpaid) → underpaid |

### TI-8: Overpayment Resolution

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-8-01 | `TestOverpaid_WithinAbsoluteThreshold_SettledNormally` | INTG | Relative exceeded but absolute not; settled |
| TI-8-02 | `TestOverpaid_WithinRelativeThreshold_SettledNormally` | INTG | Absolute exceeded but relative not; settled |
| TI-8-03 | `TestOverpaid_AboveBothThresholds_TransitionsToOverpaid` | INTG | Both exceeded → overpaid |
| TI-8-04 | `TestOverpaid_AdminApplies_InvoicedAmount_TwoPayoutRecords` | INTG | **Q-02/M-09 fix**: vendor gets invoiced_amount net; buyer refund payout created |
| TI-8-05 | `TestOverpaid_FeeCalculatedOn_InvoicedAmount_NotReceived` | INTG | **Q-02**: fee = invoiced_amount × rate; not received_amount |
| TI-8-06 | `TestOverpaid_NoRefundAddress_ExcessHeld_Warning7d_Critical30d` | INTG | **Q-09**: no refund address; excess payout in held; alerts at 7 and 30 days |
| TI-8-07 | `TestOverpaid_ManualClose_MandatoryReason` | INTG | overpaid → manually_closed |
| TI-8-08 | `TestOverpaid_SnapshotThresholds_UsedNotLive` | INTG | Live tier thresholds change; invoice uses snapshotted values |
| TI-8-09 | `TestOverpaid_TwoPayoutRecords_CreatedAtomically` | INTG | Vendor net + buyer refund in single DB transaction |

### TI-9: Hybrid Mode

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-9-01 | `TestHybrid_BelowThreshold_CreatesHeld` | INTG | Settlement below threshold; held record |
| TI-9-02 | `TestHybrid_ThresholdCrossed_PromotesHeld_BalanceDecremented` | INTG | **C-02 fix**: threshold crossed; held→queued; balance decremented at promotion |
| TI-9-03 | `TestHybrid_PostThreshold_SmallSettlement_Accumulates_NotImmediate` | INTG | **C-02 fix**: next settlement after sweep; accumulates; doesn't immediately sweep |
| TI-9-04 | `TestHybrid_ConcurrentSettlements_SelectForUpdate_Serialized` | INTG RACE | Two concurrent hybrid settlements; balance correct |
| TI-9-05 | `TestHybrid_AutoSweepAuditEvent_Written` | INTG | "Hybrid auto-sweep triggered" audit event; balance_before/after recorded |
| TI-9-06 | `TestHybrid_ThresholdSnapshot_UsedNotCurrentThreshold` | INTG | Vendor changes threshold; in-flight uses snapshot |
| TI-9-07 | `TestHybrid_FeeFloor_BackgroundJob_SelectForUpdate` | INTG | **M-07**: background job acquires SELECT FOR UPDATE before promotion |
| TI-9-08 | `TestHybrid_FeeFloor_BackgroundJob_ConcurrentSettlement_NoRace` | INTG RACE | Background promotion and live settlement race; balance correct |

### TI-19: State Machine Completeness

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-19-01 | `TestStateMachine_AllPermittedTransitions_Succeed` | INTG | Every row in §3 can execute |
| TI-19-02 | `TestStateMachine_AllUnlistedTransitions_Rejected` | INTG | Every unlisted transition fails / 0 RowsAffected |
| TI-19-03 | `TestStateMachine_PayoutRecord_AllPermittedTransitions_Succeed` | INTG | Every row in §4 can execute |
| TI-19-04 | `TestStateMachine_RowsAffected_Pattern_AllStatusUpdates` | UNIT | Every status UPDATE checks RowsAffected (code review test) |
| TI-19-05 | `TestStateMachine_NoPayoutRecord_ForConfirmingInvoice` | INTG | DB trigger prevents payout for non-settled invoice |
| TI-19-06 | `TestStateMachine_TerminalStatuses_NoAutomatedTransitions` | INTG | refunded, manually_closed; no automated transitions |
| TI-19-07 | `TestStateMachine_SettlingSource_AlwaysSet_OnSettlingEntry` | INTG | settling_source is never NULL when status='settling' |
| TI-19-08 | `TestStateMachine_SweepCompleted_SetAtBroadcast_NotBefore` | INTG | sweep_completed=true only at constructing→broadcast |
| TI-19-09 | `TestStateMachine_CancelledWithPayment_ResolvesTo_ManuallyClosedNotSettled` | INTG | **Q-06**: original stays/becomes manually_closed; new invoice created |
