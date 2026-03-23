# Resilience — Technical Implementation

> **What this file is:** Implementation contracts for reorg rollback, HandleRecovery
> flow, bitcoin_sync_state schema, payout record rollback on reorg, post-recovery
> throttle, and the complete test inventory for this package.
>
> **Read first:** `resilience-feature.md` — behavioral contract and edge cases.
> **Depends on:** `../settlement/settlement-technical.md` (invoice state machine,
> payout state machine), `../payment/payment-technical.md` (btc_outage_log).

---

## Table of Contents

1. [bitcoin_sync_state Schema](#1--bitcoin_sync_state-schema)
2. [HandleRecovery Flow](#2--handlerecovery-flow)
3. [Reorg Rollback Scope](#3--reorg-rollback-scope)
4. [Payout Record Rollback on Reorg](#4--payout-record-rollback-on-reorg)
5. [reorg_admin_required Auto Re-confirmation](#5--reorg_admin_required-auto-re-confirmation)
6. [Post-Recovery Sweep Throttling](#6--post-recovery-sweep-throttling)
7. [Test Inventory](#7--test-inventory)

---

## §1 — bitcoin_sync_state

**Schema:** defined in `sql/schema/009_btc.sql`. `-1` is the sentinel for
fresh deployment (never processed any block).

**Queries** (`sql/queries/btc.sql`):
- `GetBitcoinSyncState` — read current cursor
- `UpdateBitcoinSyncState` — advance cursor per checkpoint

### Initialization
`last_processed_height` initializes to `BTC_RECONCILIATION_START_HEIGHT` on first run.
**This value must be configured before the first mainnet deployment.** A startup check
rejects `BTC_RECONCILIATION_START_HEIGHT = 0` on mainnet unless
`BTC_RECONCILIATION_ALLOW_GENESIS_SCAN=true`.

---

## §2 — HandleRecovery Flow

Triggered when the ZMQ subscriber reconnects after a node outage.

```
1. Read last_processed_height from bitcoin_sync_state (SELECT FOR UPDATE).
2. Call getblockcount for current chain tip. Perform chain-reset check.
   - If last_processed_height > getblockcount:
     Reset last_processed_height = BTC_RECONCILIATION_START_HEIGHT.
     Log ERROR. Fire CRITICAL alert "Chain height regression detected."
3. GUARD: if any payout records are currently in `constructing` status, defer
   HandleRecovery until the construction completes (or the stale watchdog returns
   them to `queued`). Log at INFO. This prevents backfill-triggered settlements
   from racing with an active batch construction.
4. Call reconcileSegment(last_processed_height + 1, current_tip) in chunks of
   BTC_RECONCILIATION_CHECKPOINT_INTERVAL (default: 100 blocks) per transaction.
5. For each block in segment:
   a. getblockhash(height) → hash
   b. getblock(hash, verbosity=2) → full block
      - isPrunedBlockError: log warning, insert placeholder in bitcoin_block_history,
        advance cursor, continue
   c. For each transaction: match outputs against active invoice_address_monitoring
      entries; call processPayment(invoiceID, txid, voutIndex, valueSat) for each
      match.
6. Update last_processed_height at end of each segment commit.
7. After backfill completes: apply post-recovery sweep throttling (§6).
```

### Lock timeout
Each `reconcileSegment` transaction sets `SET LOCAL lock_timeout = '30s'` to prevent
blocking real-time `HandleBlockEvent` indefinitely.

### Interaction with real-time ZMQ events
During backfill, real-time ZMQ events continue processing. The
`ON CONFLICT (txid, vout_index) DO NOTHING` insert on `invoice_payments` makes all
`processPayment` calls idempotent. The atomic claim mechanism prevents double-settlement
regardless of backfill/ZMQ ordering.

### Prune window validation
At startup, call `getblockchaininfo` and compare `BTC_RECONCILIATION_START_HEIGHT`
against the returned `pruneheight`. If `start_height < pruneheight`, fire a CRITICAL
alert and refuse to accept connections:

> "Reconciliation start height [X] is before the node's prune window [Y].
> Payments confirmed in blocks [X, Y) cannot be backfilled. Investigate whether
> any invoices were active during this period before going live."

---

## §3 — Reorg Rollback Scope

`rollbackSettlementFromHeight` handles all statuses that can exist when
`first_confirmed_block_height` is set. The SQL rollback query **must include all
statuses in this table**.

| Status | sweep_completed | Action |
|--------|----------------|--------|
| `confirming` | false | → `detected` |
| `settling` | false | → `detected` (regardless of settling_source — confirming block is gone) |
| `settling` | true | → `reorg_admin_required` |
| `settled` | false | → `detected` |
| `settled` | true | → `reorg_admin_required` |
| `underpaid` | false | → `detected` |
| `overpaid` | false | → `detected` |
| `settlement_failed` | false | → `detected` |
| `reorg_admin_required` | true | → `reorg_admin_required` (already correct; update `first_confirmed_block_height` to new height) |

The `reorg_admin_required` row handles the case where an invoice re-confirmed
(updating `first_confirmed_block_height` to a new block) and that new block is then
also reorged — the invoice stays in `reorg_admin_required` but its height is reset.

### Notes on specific transitions
- `confirming → detected` (reorg rollback) does not reset the expiry timer.
- `settling` status with `sweep_completed=false` → `detected` regardless of
  `settling_source`, because the confirming block is gone entirely.

---

## §4 — Payout Record Rollback on Reorg

When an invoice transitions to `reorg_admin_required`, the associated payout records
must be rolled back in the same DB transaction:

- If the payout record is in `confirmed` status AND the sweep txid is still in the
  mempool (verified via `getmempoolentry`): transition to `broadcast`.
- If the payout record is in `confirmed` or `broadcast` status AND the sweep txid is
  NOT in the mempool (dropped or never confirmed in the new chain): transition to
  `queued` so a new sweep can be constructed.
- Payout records in `held` or `queued` status are unaffected (no on-chain transaction
  has occurred for them yet).

This rollback runs as part of the `rollbackSettlementFromHeight` transaction. The
reconciliation job must be triggered immediately after any reorg detection to verify
balance consistency.

---

## §5 — reorg_admin_required Auto Re-confirmation

When ZMQ detects that the original payment txid has re-confirmed in a new block:

```
1. Verify sweep tx status:
   a. Call getmempoolentry(sweep_txid) OR walk confirmed blocks to verify sweep tx
      is confirmed in the current chain.
      - Sweep tx confirmed:
          → proceed to auto-transition (step 3).
      - Sweep tx in mempool (unconfirmed):
          → set sweep_completed=false; keep payout records in current status.
          → do NOT transition to `settled` yet; wait for sweep tx confirmation.
      - Sweep tx NOT in mempool AND NOT confirmed (dropped):
          → set sweep_completed=false; transition payout records to `queued` for re-sweep.
          → then transition invoice to `settled` (sweep will re-occur automatically).
2. Update first_confirmed_block_height to the NEW block height. This is mandatory:
   if this new block is also reorged, the rollback query must find the invoice by
   the new height.
3. Transition reorg_admin_required → settled atomically with the updates above.
```

**72-hour escalation:** a CRITICAL alert fires at entry to `reorg_admin_required`.
If unresolved after 72 hours, an escalation CRITICAL alert fires. The escalation
repeats weekly until resolved.

---

## §6 — Post-Recovery Sweep Throttling

After node reconnection, payout records that accumulated during downtime are eligible
for sweep. Throttled to `BTC_RECOVERY_SWEEP_RATE` (default: 5 sweeps per minute).
Throttle applies during the backfill scan (§2) and for
`BTC_RECOVERY_THROTTLE_WINDOW_MINUTES` after it completes.

---

## §7 — Test Inventory

### Legend
- `[UNIT]` — pure unit test, no DB or network
- `[INTG]` — integration test requiring real PostgreSQL
- `[E2E]` — end-to-end test requiring Bitcoin Core in regtest mode
- `[RACE]` — must be run with `-race` flag

### TI-10: Reorg Handling

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-10-01 | `TestReorg_Confirming_RollsBackToDetected` | INTG | Block disconnected; confirming → detected |
| TI-10-02 | `TestReorg_Settled_SweepNotComplete_RollsBackToDetected` | INTG | settled/sweep_completed=false → detected |
| TI-10-03 | `TestReorg_Settled_SweepComplete_TransitionsToReorgAdminRequired` | INTG | settled/sweep_completed=true → reorg_admin_required |
| TI-10-04 | `TestReorg_AllStatusesInRollbackTable_Handled` | INTG | Every status in rollback table covered; none missed |
| TI-10-05 | `TestReorg_StatusNotInTable_NotRolledBack` | INTG | Status not in table (e.g. expired) stays unchanged |
| TI-10-06 | `TestReorg_Enterprise1Block_SpecificTesting` | E2E | Enterprise; 1-block confirm; immediate reorg |
| TI-10-07 | `TestReorg_ReconfirmationUpdatesFirstConfirmedBlockHeight` | INTG | **C-06**: re-confirmation updates first_confirmed_block_height to new block |
| TI-10-08 | `TestReorg_AutoTransition_VerifiesSweepTxConfirmed` | INTG | **C-05**: ZMQ re-confirm; sweep tx verified before → settled |
| TI-10-09 | `TestReorg_AutoTransition_SweepTxDropped_ResetsSweepCompleted` | INTG | **C-05**: sweep tx dropped after reorg; sweep_completed=false; payout → queued |
| TI-10-10 | `TestReorg_DoubleReorg_HandledCorrectly` | E2E | Two sequential reorgs; state consistent throughout |
| TI-10-11 | `TestReorg_PayoutRecord_Confirmed_RolledBack_To_Broadcast` | INTG | **H-07**: payout confirmed; reorg; tx still in mempool → broadcast |
| TI-10-12 | `TestReorg_PayoutRecord_Confirmed_RolledBack_To_Queued` | INTG | **H-07**: payout confirmed; reorg; tx dropped → queued |
| TI-10-13 | `TestReorg_PayoutRollback_SameTransaction_As_InvoiceRollback` | INTG | **H-07**: both rollbacks in single DB tx; partial commit impossible |
| TI-10-14 | `TestReorg_VendorAndAdminNotified_PerAffectedInvoice` | INTG | Notification fired per affected invoice |
| TI-10-15 | `TestReorg_ReconciliationTriggered_Immediately_After_Reorg` | INTG | Reconciliation job triggered as part of rollback |
| TI-10-16 | `TestReorg_AdminRequired_72h_EscalationAlert` | INTG | **Q-10**: no admin action after 72h → CRITICAL escalation |
| TI-10-17 | `TestReorg_AdminRequired_WeeklyRepeat_UntilResolved` | INTG | **Q-10**: weekly repeat alert until manually_closed |
| TI-10-18 | `TestReorg_ReorgAdminRequired_ListedInRollbackTable` | UNIT | reorg_admin_required status handled by rollback SQL |

### TI-17: Operational Resilience

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-17-01 | `TestResilience_NodeOffline_InvoiceCreationSuspended` | INTG | RPC fails; 503 returned |
| TI-17-02 | `TestResilience_NodeOffline_OutageLogWritten` | INTG | btc_outage_log row; started_at set; ended_at=NULL |
| TI-17-03 | `TestResilience_NodeReconnect_OutageLogClosed` | INTG | Reconnect; ended_at set; HandleRecovery called |
| TI-17-04 | `TestResilience_MultipleInstances_NoDuplicateOutageLogs` | INTG RACE | Two instances; advisory lock prevents duplicate INSERT |
| TI-17-05 | `TestResilience_HandleRecovery_BackfillsMissedBlocks` | INTG | 100-block outage; backfill; processPayment idempotent |
| TI-17-06 | `TestResilience_HandleRecovery_IdempotentWithZMQ` | INTG | ZMQ and backfill both detect same payment; one row |
| TI-17-07 | `TestResilience_PostRecovery_SweepThrottled` | INTG | 5/min rate limit post-recovery |
| TI-17-08 | `TestResilience_StartHeight_PrunedBelow_CRITICAL_At_Startup` | INTG | **H-04**: start_height < pruneheight → CRITICAL at startup |
| TI-17-09 | `TestResilience_StartHeight_PrunedBelow_ApplicationDoesNotAcceptConnections` | INTG | **H-04**: app refuses to serve until check passes |
| TI-17-10 | `TestResilience_BackfillPrunedBlock_Placeholder_Inserted` | INTG | Pruned block during backfill → placeholder; cursor advances |
| TI-17-11 | `TestResilience_ChainReset_LastProcessedHeightReset` | INTG | **M-10**: last_processed > chain tip → reset + ERROR log |
| TI-17-12 | `TestResilience_LockTimeout_ReconcileSegment_30s` | INTG | lock_timeout='30s' prevents blocking HandleBlockEvent |
| TI-17-13 | `TestResilience_HandleRecovery_GuardAgainstConstructingRecords` | INTG | **H-08**: recovery deferred when payout records in constructing |
