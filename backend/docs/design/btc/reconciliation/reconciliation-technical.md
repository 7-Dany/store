# Reconciliation — Technical Implementation

> **What this file is:** Implementation contracts for the reconciliation job,
> checkpoint design, formula query breakdown, sweep-hold activation, and the
> complete test inventory.
>
> **Read first:** `reconciliation-feature.md` — behavioral contract and edge cases.
> **Schema:** `sql/schema/009_btc.sql` (`reconciliation_job_state`,
> `reconciliation_run_history`, `bitcoin_sync_state`, `bitcoin_block_history`).
> **Queries:** `sql/queries/btc.sql` — see §2 for the full list.

---

## Table of Contents

1. [Job Scheduling and Advisory Lock](#1--job-scheduling-and-advisory-lock)
2. [Queries Used](#2--queries-used)
3. [reconcileSegment Flow](#3--reconcilesegment-flow)
4. [Formula Evaluation](#4--formula-evaluation)
5. [Sweep-Hold Activation and Clearance](#5--sweep-hold-activation-and-clearance)
6. [Job State and History](#6--job-state-and-history)
7. [Test Inventory](#7--test-inventory)

---

## §1 — Job Scheduling and Advisory Lock

The reconciliation job runs every **6 hours** via the platform job queue with
`unique_job = true` to prevent overlapping runs.

**Advisory lock:** at the start of each run, the job acquires:
```
pg_try_advisory_lock(hashtext('btc_reconciliation:' || network))
```
If the lock is unavailable (another instance holds it), the job logs INFO and exits
immediately. The scheduled run is not retried — the next scheduled cycle will proceed
normally.

**Why advisory locks:** the job does SELECT FOR UPDATE on `reconciliation_job_state`
for cursor reads, but advisory locks provide an additional guard for the
multi-statement segment processing loop where the cursor is advanced incrementally.

---

## §2 — Queries Used

| Query | Purpose |
|-------|---------|
| `GetReconciliationJobState` | Read cursor + last run metadata |
| `UpsertReconciliationJobState` | Update after each run (pass `last_successful_run_at = NOW()` on success, NULL on failure) |
| `InsertReconciliationRunHistory` | Open a history row at run start |
| `CloseReconciliationRunHistory` | Close history row with result + discrepancy |
| `GetBitcoinSyncState` | Read `last_processed_height` cursor |
| `UpdateBitcoinSyncState` | Advance cursor per checkpoint |
| `UpsertBlockHistory` | Record each processed block (or pruned placeholder) |
| `GetBlockHistoryRange` | Gap detection across a height range |
| `SumInflightInvoiceAmounts` | Formula term 1 |
| `SumInflightPayoutRecords` | Formula term 2 |
| `SumPlatformVendorBalances` | Formula term 3 |
| `GetPlatformConfig` | Read `treasury_reserve_satoshis` (formula term 4) + `sweep_hold_mode` |
| `SetSweepHold` | Activate emergency brake on discrepancy |
| `CloseStaleOutages` | Maintenance: close outage records older than 48 hours |

---

## §3 — reconcileSegment Flow

Each call processes `BTC_RECONCILIATION_CHECKPOINT_INTERVAL` blocks (default: 100)
in a single DB transaction with `SET LOCAL lock_timeout = '30s'`.

```
reconcileSegment(from_height, to_height):
  1. BEGIN TRANSACTION; SET LOCAL lock_timeout = '30s';
  2. SELECT FOR UPDATE on bitcoin_sync_state (prevents concurrent HandleRecovery from
     racing on the same cursor).
  3. For each height in [from_height, to_height]:
     a. rpc.GetBlockHash(height) → hash
     b. rpc.GetBlock(hash, verbosity=2) → full block
        - If IsPrunedBlockError: UpsertBlockHistory(height, pruned=true, hash=nil)
          Advance cursor. Log WARNING. Continue.
     c. For each transaction output in block:
        - Check output address against active invoice_address_monitoring
          (in-memory watch set first; GetActiveMonitoringByAddress as fallback)
        - If match: UpsertInvoicePayment (ON CONFLICT DO NOTHING — idempotent)
          Then run settlement check for that invoice
     d. UpsertBlockHistory(height, pruned=false, hash=hash)
  4. UpdateBitcoinSyncState(to_height)
  5. COMMIT
```

**Lock timeout rationale:** real-time ZMQ `HandleBlockEvent` also does status updates.
The 30s lock_timeout prevents the backfill segment from blocking real-time settlement
indefinitely. If the timeout fires, the segment rolls back and is retried at the
next `HandleRecovery` or scheduled run.

**Interaction with real-time ZMQ events:** `UpsertInvoicePayment` uses
`ON CONFLICT DO NOTHING`. The `TransitionInvoice*` optimistic-locking pattern
(WHERE status = expected) means duplicate processing is harmless — zero RowsAffected
means another path already processed this event.

---

## §4 — Formula Evaluation

After all segments are processed, the formula check runs:

```go
term1, _ := q.SumInflightInvoiceAmounts(ctx, network)
term2, _ := q.SumInflightPayoutRecords(ctx, network)
term3, _ := q.SumPlatformVendorBalances(ctx, network)
cfg,   _ := q.GetPlatformConfig(ctx, network)
term4    := cfg.TreasuryReserveSatoshis

formulaSum := term1 + term2 + term3 + term4

onChainUTXOs := rpc.GetWalletInfo(ctx).Balance // in satoshis via rpc.BtcToSat
discrepancy  := onChainUTXOs - formulaSum
```

**`SumPlatformVendorBalances`** joins `vendor_balances` to `vendor_wallet_config`
and filters `wallet_mode = 'platform'`. Hybrid balances are excluded by this join.
See query comment in `sql/queries/btc.sql`.

**UTXO value source:** `rpc.Client.GetWalletInfo` returns the wallet's total balance
including unconfirmed transactions. For reconciliation, `confirmed_balance` is the
correct field — unconfirmed inputs are still the platform's responsibility but are
not yet settled, which matches the formula terms. Use `WalletInfo.Balance` (the
spendable confirmed balance field) — verify against Bitcoin Core docs for the exact
field name at integration time.

---

## §5 — Sweep-Hold Activation and Clearance

**On discrepancy detection:**
```go
q.SetSweepHold(ctx, db.SetSweepHoldParams{
    Network: network,
    Reason:  fmt.Sprintf("Reconciliation discrepancy: %d sat", discrepancy),
})
```
`fn_ops_audit_platform_config` trigger in `011_btc_functions.sql` automatically
writes the hold activation to `ops_audit_log`. The calling admin's identity must
be in the session variables `app.current_actor_id` and `app.current_actor_label`
before this runs — for automated job activations, use a system actor identity.

**Clearance:** `ClearSweepHold` requires:
- Step-up authentication (TOTP) by an admin
- A written resolution reason stored in the audit trail
- `sweep_hold_mode = TRUE` in the WHERE clause (idempotent if already cleared)

The `fn_ops_audit_platform_config` trigger fires on clearance too, creating an
immutable record of who cleared the hold and when.

---

## §6 — Job State and History

`reconciliation_job_state` — one row per network; holds the cursor and last run metadata.

`reconciliation_run_history` — append-only; one row per run; never deleted.

**Run lifecycle:**
```
1. InsertReconciliationRunHistory(network, started_at=NOW()) → run_id
2. ... process segments ...
3. CloseReconciliationRunHistory(run_id, result, discrepancy_sat, note)
4. UpsertReconciliationJobState(network, result,
       discrepancy_sat,
       last_successful_run_at = NOW()  // only when result='ok'
                              = NULL   // on failure — COALESCE preserves previous
   )
```

**Staleness alert:** a separate monitoring job checks
`reconciliation_job_state.last_successful_run_at`. If it is older than 8 hours,
a CRITICAL alert fires: "Reconciliation job missed — last successful run was N hours ago."

---

## §7 — Test Inventory

### Legend
- `[UNIT]` — pure unit test, no DB or network
- `[INTG]` — integration test requiring real PostgreSQL
- `[E2E]` — end-to-end test requiring Bitcoin Core in regtest mode

### TI-23: Reconciliation Job

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-23-01 | `TestReconciliation_FormulaBalances_NoActivity` | INTG | Fresh state; all terms zero; discrepancy = 0 |
| TI-23-02 | `TestReconciliation_FormulaBalances_WithInFlightInvoices` | INTG | Inflight invoices counted; settled excluded |
| TI-23-03 | `TestReconciliation_FormulaBalances_WithPayoutRecords` | INTG | All four payout statuses included |
| TI-23-04 | `TestReconciliation_FormulaBalances_PlatformModeOnly_HybridExcluded` | INTG | Hybrid balance excluded; platform included |
| TI-23-05 | `TestReconciliation_FormulaBalances_TreasuryReserve_Included` | INTG | treasury_reserve_satoshis in formula |
| TI-23-06 | `TestReconciliation_TreasuryReserve_Skipped_CausesNegativeDiscrepancy` | INTG | Omitting IncrementTreasuryReserve → permanent negative discrepancy |
| TI-23-07 | `TestReconciliation_DiscrepancyDetected_SweepHoldActivated` | INTG | Mismatch → CRITICAL alert → sweep_hold_mode=TRUE |
| TI-23-08 | `TestReconciliation_SweepHold_BlocksNewConstruction` | INTG | Hold active; new sweep construction returns ErrSweepHold |
| TI-23-09 | `TestReconciliation_SweepHold_AlreadyBroadcast_Completes` | INTG | Broadcast before hold → confirmation still processes |
| TI-23-10 | `TestReconciliation_SweepHold_ClearedByAdmin_ResumesConstruction` | INTG | Admin clears hold with reason; sweeps resume |
| TI-23-11 | `TestReconciliation_SweepHold_Clear_RequiresStepUpAuth` | INTG | No TOTP → clearance rejected |
| TI-23-12 | `TestReconciliation_SweepHold_OpsAuditLog_Written` | INTG | fn_ops_audit_platform_config fires on set and clear |
| TI-23-13 | `TestReconciliation_JobMissed_Alert_After8Hours` | INTG | last_successful_run_at > 8h → CRITICAL alert |
| TI-23-14 | `TestReconciliation_RunHistory_OpenedAndClosed` | INTG | InsertReconciliationRunHistory then CloseReconciliationRunHistory |
| TI-23-15 | `TestReconciliation_LastSuccessfulRunAt_NotAdvanced_OnFailure` | INTG | COALESCE preserves previous successful timestamp on failure run |
| TI-23-16 | `TestReconciliation_ChainReset_CursorReset_CriticalAlert` | INTG | last_processed_height > chain_tip → cursor reset |
| TI-23-17 | `TestReconciliation_PruneWindowCheck_Startup_BlocksTraffic` | INTG | start_height < pruneheight → startup failure |
| TI-23-18 | `TestReconciliation_PrunedBlock_PlaceholderInserted_CursorAdvances` | INTG | Pruned block in backfill → UpsertBlockHistory(pruned=true); continue |
| TI-23-19 | `TestReconciliation_AdvisoryLock_PreventsDoubleRun` | INTG | Two concurrent jobs; second exits after lock fails |
| TI-23-20 | `TestReconciliation_Checkpoint_CrashResumesFromLastCheckpoint` | INTG | Crash mid-backfill; restart resumes from checkpoint not start |
| TI-23-21 | `TestReconciliation_LockTimeout_SegmentRollsBack_Retried` | INTG | lock_timeout fires; segment rolled back; retried next run |
| TI-23-22 | `TestReconciliation_IdempotentWithZMQEvents` | INTG | Block processed by both ZMQ and backfill; one row in invoice_payments |
| TI-23-23 | `TestReconciliation_HoldEscalation_4HoursUnresolved` | INTG | Hold active > 4h → escalation alert to platform owner |
| TI-23-24 | `TestReconciliation_StaleOutageCleaner_ClosesOldRecords` | INTG | Outage records > 48h closed by CloseStaleOutages |
| TI-23-25 | `TestReconciliation_ReorgAdminRequired_CountedInFormula` | INTG | reorg_admin_required invoice included in inflight term |
