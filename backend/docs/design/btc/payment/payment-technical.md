# Payment — Technical Implementation

> **What this file is:** Implementation contracts for payment detection, mempool drop
> watchdog, confirmation trigger design, and the complete test inventory for this package.
>
> **Read first:** `payment-feature.md` — behavioral contract and edge cases.
> **Depends on:** `../invoice/invoice-technical.md` (invoice_payments schema, monitoring table),
> `../settlement/settlement-technical.md` (settlement engine entry point).

---

## Table of Contents

1. [Mempool Drop Watchdog](#1--mempool-drop-watchdog)
2. [Confirmation Trigger Design](#2--confirmation-trigger-design)
3. [Block Height Source](#3--block-height-source)
4. [btc_outage_log Schema](#4--btc_outage_log-schema)
5. [Test Inventory](#5--test-inventory)

---

## §1 — Mempool Drop Watchdog

### Problem
An invoice in `detected` status has its expiry timer frozen. If the detected
transaction disappears from the mempool, the invoice would be stuck indefinitely
unless actively detected.

### Polling interval (G-I1 resolved)
The watchdog polls every **`BTC_MEMPOOL_WATCHDOG_INTERVAL_SECONDS`** (default: 60
seconds). This interval is a trade-off:
- Too slow (>5 min): invoices stay stuck for a long time after a legitimate drop.
- Too fast (<30s): hammers the node with `getmempoolentry` calls at a rate proportional
  to the number of active `detected` invoices.

**Scaling note:** the watchdog queries ALL invoices in `detected` status in one pass.
If there are N invoices in `detected` status, the watchdog makes N `getmempoolentry`
RPC calls per polling cycle. At 1000 concurrent detected invoices and a 60-second
interval, this is ~17 RPC calls/second — within Bitcoin Core's default capacity but
should be monitored as volume grows. If the watchdog run time exceeds the polling
interval, the next run starts immediately (no overlapping runs — the job uses a
`unique_job = true` constraint).

### Two-cycle confirmation rule
A watchdog job polls invoices in `detected` status by calling
`getmempoolentry(detected_txid)` against the node.

- **First check absent:** write current timestamp to `mempool_absent_since` column
  (DB-persisted, not in-memory). Invoice stays in `detected`.
- **Second check absent (and `mempool_absent_since` is set):** transition to
  `mempool_dropped`.

**RPC failure handling:** if `getmempoolentry` fails (node unreachable, timeout, or
RPC error), the watchdog **must not** treat this as "transaction absent."
`mempool_absent_since` is not advanced. Failure is logged. The "Bitcoin node offline"
alert fires independently. This prevents false `mempool_dropped` transitions during
node outages.

### `mempool_absent_since` clear rule
When an invoice transitions from `mempool_dropped` to `detected` (replacement
transaction re-detected), the same UPDATE that changes the status and updates
`detected_txid` **MUST** also set `mempool_absent_since = NULL`. This prevents the
watchdog from immediately re-triggering the two-cycle check on the new transaction.

```sql
-- Correct mempool_dropped → detected transition:
UPDATE invoices
SET status = 'detected',
    detected_txid = $new_txid,
    mempool_absent_since = NULL,
    updated_at = NOW()
WHERE id = $id AND status = 'mempool_dropped';
-- Check RowsAffected == 1
```

### RBF re-detection handling (settlement path only)
When ZMQ fires a `hashtx` event for a payment to an address whose invoice is already
in `detected` status, and the new txid differs from `detected_txid`:

1. Record the new payment in `invoice_payments` (idempotency rule always applies).
2. Call `getmempoolentry(detected_txid)`:
   - **Present:** original tx still in mempool. New payment may be double-payment.
     Record with `double_payment=true` flag; fire "Double-payment detected" CRITICAL
     alert. Do NOT change invoice status.
   - **Absent:** begin two-cycle check by writing `mempool_absent_since = NOW()`.

When the invoice is in `mempool_dropped` status and a new `hashtx` event arrives for
the same address, the handler transitions `mempool_dropped → detected` with the new
txid and clears `mempool_absent_since = NULL`. The transition is triggered by the
ZMQ event handler, not the watchdog.

> **Note:** `spentOutpoints` is the in-memory map maintained by the Stage 0 SSE/display
> handler. It is display-only, ephemeral, and per-instance. The settlement path never
> reads `spentOutpoints`. All RBF detection in the settlement path uses only
> `getmempoolentry` and DB-persisted state.

---

## §2 — Confirmation Trigger Design

Settlement is triggered by two independent paths:

### Primary: ZMQ block notification
When a new block is mined, ZMQ fires `hashblock`. The settlement worker checks all
`confirming` invoices whose snapshotted confirmation depth has been reached.

### Secondary: Polling safety net
A separate job polls every 5 minutes for `confirming` invoices that have reached
their depth target but have not yet been settled.

### Double-settlement prevention
Both paths share the same atomic `confirming → settling` claim mechanism (see
`../settlement/settlement-technical.md §Settlement Atomicity`). Only one worker can
claim a given invoice.

### ZMQ ordering edge case (G-N9)
In a multi-instance deployment, it is possible (though rare) for the `hashblock`
event to be processed before the `hashtx` event for the same payment. If this
happens, the `hashblock` handler finds the invoice still in `pending` status (not
yet `detected` or `confirming`) and has nothing to settle. The invoice transitions
normally via the `hashtx` event, enters `confirming`, and the 5-minute polling safety
net triggers settlement. This is correct behavior — the safety net specifically exists
for this ordering scenario.

---

## §3 — Block Height Source

`current_chain_height` is obtained via `getblockcount` RPC at settlement evaluation
time — not from the ZMQ notification payload and not from a cached value. Recomputed
live on each poll or ZMQ event.

Confirmation depth calculation:
```
current_chain_height - invoice.first_confirmed_block_height
```

`first_confirmed_block_height` is set on the invoice **in the same DB transaction as
the `detected → confirming` status transition** — never deferred. See
`invoice-technical.md §5` for the column definition.

---

## §4 — btc_outage_log Schema

The outage log tracks periods when the Bitcoin Core node was unreachable. It is used
by the expiry cleanup job to compute effective expiry times (see
`../invoice/invoice-feature.md §5 Expiry Rules`). The reconnect event that closes an
outage log entry is also the event that triggers `HandleRecovery` in the resilience
package.

```sql
CREATE TABLE btc_outage_log (
    id         BIGSERIAL PRIMARY KEY,
    network    TEXT        NOT NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at   TIMESTAMPTZ,
    CONSTRAINT chk_outage_times CHECK (ended_at IS NULL OR ended_at > started_at)
);
CREATE INDEX idx_btc_outage_open ON btc_outage_log (network)
    WHERE ended_at IS NULL;
CREATE INDEX idx_btc_outage_range ON btc_outage_log (network, started_at, ended_at);
```

### Write protocol
- **On node disconnect:** INSERT a new row with `ended_at = NULL`. Use a PostgreSQL
  advisory lock to prevent multiple application instances from inserting duplicate
  open outage records simultaneously.
- **On node reconnect:** `UPDATE btc_outage_log SET ended_at = NOW() WHERE id = $id
  AND ended_at IS NULL`. The `AND ended_at IS NULL` clause makes this idempotent.
- **On application startup:** if an open outage record exists from a previous process,
  close it with `ended_at = NOW()`.

### Advisory lock for duplicate INSERT prevention (G-M1 resolved)
The advisory lock that prevents duplicate open outage records uses:
```
pg_try_advisory_lock(hashtext('btc_outage_log:' || $network))
```
`hashtext` is a PostgreSQL built-in that converts a string to a 32-bit integer. The
lock key is stable for a given network string. The lock type is **session-level** (not
transaction-level): it is acquired when the ZMQ subscriber detects node disconnect and
released when the reconnect event closes the outage record (or on application shutdown).

If an application instance holds the advisory lock and crashes, PostgreSQL automatically
releases the session-level lock when the connection closes. This means the 48-hour
stale record maintenance job (which closes orphaned open records) is the cleanup path
for the outage log row, while PostgreSQL handles the lock cleanup automatically.

**Lock acquisition failure:** if `pg_try_advisory_lock` returns false (another instance
already holds the lock and has an open outage record), the current instance does NOT
insert a new row. It logs "Outage already recorded by another instance" at INFO and
proceeds without the lock. The outage is already recorded; duplicate records are not
needed.

### Stale record maintenance
A periodic job (every 6 hours) scans for open outage records older than 48 hours.
Each is closed with `ended_at = MIN(NOW(), started_at + INTERVAL '48 hours')` and a
WARNING alert fires: "Stale outage record closed — application instance may have been
terminated unexpectedly."

---

## §5 — Test Inventory

### Legend
- `[UNIT]` — pure unit test, no DB or network
- `[INTG]` — integration test requiring real PostgreSQL
- `[RACE]` — must be run with `-race` flag

### TI-2: Payment Detection

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-2-01 | `TestPaymentDetection_MempoolDetected_PendingToDetected` | INTG | ZMQ hashtx; pending → detected; expiry frozen |
| TI-2-02 | `TestPaymentDetection_Idempotent_DuplicateTxid` | INTG | Same txid fired twice; ON CONFLICT DO NOTHING; one row |
| TI-2-03 | `TestPaymentDetection_MultipleOutputsSameAddress_Summed` | INTG | Two vouts to same address; value_sat summed |
| TI-2-04 | `TestPaymentDetection_DoublePayment_SecondTxid_Flag` | INTG | Second distinct txid; double_payment=true; CRITICAL alert |
| TI-2-05 | `TestPaymentDetection_PostSettlement_Flag_And_AuditEvent` | INTG | Post-settled payment; flag + financial audit event written |
| TI-2-06 | `TestPaymentDetection_ExpiredInvoice_MonitoringWindow_LatePayment` | INTG | Payment in 30d window → expired_with_payment; three notifications |
| TI-2-07 | `TestPaymentDetection_CancelledInvoice_MonitoringWindow_LatePayment` | INTG | Payment on cancelled address → cancelled_with_payment |
| TI-2-08 | `TestPaymentDetection_AfterMonitoringWindow_AlertFires` | INTG | Payment after 30d window; unknown tx alert; no status change |
| TI-2-09 | `TestPaymentDetection_UnknownAddress_Ignored` | INTG | ZMQ event for address not in monitoring table; no panic |
| TI-2-10 | `TestPaymentDetection_DetectedInvoice_NewTxid_OriginalPresent_DoublePay` | INTG | Second txid; first still in mempool; double_payment flag |
| TI-2-11 | `TestPaymentDetection_DetectedInvoice_NewTxid_OriginalAbsent_BeginTwoCycle` | INTG | Second txid; first absent; mempool_absent_since set |
| TI-2-12 | `TestPaymentDetection_MempoolDropped_NewHashtx_TransitionsToDetected` | INTG | **G-N9-adjacent**: ZMQ hashtx fires while invoice in mempool_dropped; transitions to detected with new txid |

### TI-3: Confirmation and Block Handling

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-3-01 | `TestConfirmation_ReachesDepthTarget_TriggersSettlement` | INTG | Block arrives; depth = tier target; settlement triggered |
| TI-3-02 | `TestConfirmation_BelowDepthTarget_NoSettlement` | INTG | depth < target; stays confirming |
| TI-3-03 | `TestConfirmation_PollingFallback_Catches_MissedZMQ` | INTG | 5-min poller catches missed ZMQ block |
| TI-3-04 | `TestConfirmation_DualTrigger_OnlyOneSettles` | INTG RACE | ZMQ and poller both fire; only one claims settling |
| TI-3-05 | `TestConfirmation_CurrentChainHeight_FromRPC_NotCache` | UNIT | getblockcount called live at settlement time |
| TI-3-06 | `TestConfirmation_Enterprise1Block_SettlesImmediately` | E2E | Enterprise; depth=1; first block settles |
| TI-3-07 | `TestConfirmation_RecordFirstConfirmedBlockHeight_AtConfirmingTransition` | INTG | **G-I2**: first_confirmed_block_height set atomically with detected→confirming |
| TI-3-08 | `TestConfirmation_SweepCompletedFlag_SetAtBroadcast` | INTG | sweep_completed=true when payout record → broadcast |
| TI-3-09 | `TestConfirmation_HashtxBeforeHashblock_PollerSettles` | INTG | **G-N9**: hashtx processed; invoice pending→detected; hashblock fires with invoice still pending; poller eventually settles |

### TI-4: Expiry

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-4-01 | `TestExpiry_PendingInvoice_ExpiresAfterWindow` | INTG | Effective expiry elapsed; pending → expired |
| TI-4-02 | `TestExpiry_DetectedInvoice_TimerFrozen` | INTG | detected; time passes; stays detected |
| TI-4-03 | `TestExpiry_MempoolDropped_TimerUnfrozen` | INTG | mempool_dropped; expiry resumes; eventually expires |
| TI-4-04 | `TestExpiry_OutageCompensation_SingleOutage` | INTG | Single outage; expiry extended by exact overlap |
| TI-4-05 | `TestExpiry_OutageCompensation_MultipleOutages` | INTG | Multiple outages; SUM formula correct |
| TI-4-06 | `TestExpiry_OutageCompensation_OutageBeginsBeforeInvoice` | INTG | Outage before invoice; formula clips correctly |
| TI-4-07 | `TestExpiry_OutageCompensation_OpenOutageRecord_CoalesceNow` | INTG | Unclosed record; COALESCE(ended_at, NOW()) |
| TI-4-08 | `TestExpiry_NoExpiryDuringActiveOutage` | INTG | Outage active; no invoice expires |
| TI-4-09 | `TestExpiry_CleanupJob_DoesNotRetireNullMonitorUntil` | UNIT | monitor_until IS NULL → not retired |
| TI-4-10 | `TestExpiry_StaleOutageLog_ClosedByMaintenance_Warning` | INTG | Outage record > 48h auto-closed; WARNING alert |

### TI-5: Mempool Drop Watchdog

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-5-01 | `TestMempoolWatchdog_TransactionPresent_NoAction` | INTG | Present; no state change |
| TI-5-02 | `TestMempoolWatchdog_FirstAbsence_SetsMempoolAbsentSince` | INTG | First absent; absent_since set; stays detected |
| TI-5-03 | `TestMempoolWatchdog_SecondAbsence_TransitionsToDropped` | INTG | Second absent; → mempool_dropped |
| TI-5-04 | `TestMempoolWatchdog_RPCFailure_DoesNotFalseFire` | INTG | RPC error; absent_since NOT advanced; no transition |
| TI-5-05 | `TestMempoolWatchdog_TransactionReappears_ClearsMempoolAbsentSince` | INTG | Absent then present; absent_since cleared |
| TI-5-06 | `TestMempoolWatchdog_NodeOffline_NoFalseDrops` | INTG | Node offline; no mempool_dropped transitions |
| TI-5-07 | `TestMempoolWatchdog_MempoolDroppedToDetected_ClearsAbsentSince` | INTG | **M-01**: new payment on dropped → detected; absent_since = NULL in same UPDATE |
| TI-5-08 | `TestMempoolDropped_Cancellation_NoConfirmedPayment_Allowed` | INTG | mempool_dropped → cancelled when no confirmed payments |
| TI-5-09 | `TestMempoolDropped_Cancellation_WithPriorConfirmedPayment_Rejected` | INTG | mempool_dropped → cancelled blocked if confirmed payment exists |
| TI-5-10 | `TestMempoolWatchdog_PollingInterval_IsConfigurable` | UNIT | **G-I1**: BTC_MEMPOOL_WATCHDOG_INTERVAL_SECONDS configures polling rate |
| TI-5-11 | `TestMempoolWatchdog_HighVolume_NoOverlap` | INTG | **G-I1**: with 1000 detected invoices; job does not overlap; unique_job=true enforced |

### TI-6: Outage Log Advisory Lock

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-6-01 | `TestOutageLog_FirstInstance_InsertsRow` | INTG RACE | First instance acquires advisory lock; inserts row |
| TI-6-02 | `TestOutageLog_SecondInstance_SkipsInsert` | INTG RACE | **G-M1**: second instance sees lock held; does NOT insert duplicate; logs INFO |
| TI-6-03 | `TestOutageLog_AdvisoryLockKey_DerivedFromNetwork` | UNIT | hashtext('btc_outage_log:mainnet') is stable and documented |
| TI-6-04 | `TestOutageLog_LockReleasedOnReconnect` | INTG | Advisory lock released when ended_at set; next disconnect acquires fresh lock |

### Tests forwarded from zmq-technical.md

This test was originally specified in `zmq-technical.md` but requires a live ZMQ
server to force a disconnect/reconnect cycle. Implement it as an integration test
here when the payment package's ZMQ recovery handler is wired.

| ID | Test | Notes |
|---|---|---|
| T-45 | `TestSubscriber_ReconnectEmitsRecoveryEvent` | Disconnect the ZMQ socket; verify RecoveryEvent fires on reconnect before the first post-reconnect BlockEvent; verify `LastSeenSequence` equals the last sequence seen before disconnect |
