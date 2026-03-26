# Sweep — Technical Implementation

> **What this file is:** Implementation contracts for the SweepService interface,
> the full PSBT broadcast sequence, fee estimation mechanics, batch integrity,
> stuck sweep detection, UTXO consolidation guards, and the complete test inventory
> for this package.
>
> **Read first:** `sweep-feature.md` — behavioral contract and edge cases.
> **Depends on:** `../settlement/settlement-technical.md` (payout state machine,
> broadcast ordering invariant).

---

## Table of Contents

1. [SweepService Interface Contract](#1--sweepservice-interface-contract)
2. [Bitcoin Core RPC Calls Owned by SweepService](#2--bitcoin-core-rpc-calls-owned-by-sweepservice)
3. [Sweep Broadcast Sequence](#3--sweep-broadcast-sequence)
4. [Fee Estimation and Economic Validation](#4--fee-estimation-and-economic-validation)
5. [Batch Sweep Integrity](#5--batch-sweep-integrity)
6. [Change Address Management](#6--change-address-management)
7. [Stuck Sweep Detection and RBF Trigger](#7--stuck-sweep-detection-and-rbf-trigger)
8. [UTXO Consolidation Job Guards](#8--utxo-consolidation-job-guards)
9. [Test Inventory](#9--test-inventory)

---

## §1 — SweepService Interface Contract

The settlement engine never constructs or signs Bitcoin transactions directly.
All sweep construction and signing is delegated to a `SweepService` — the only
component that calls Bitcoin Core's wallet RPCs.

```
constructAndBroadcast(payoutRecords[], confirmationTarget, feeCapSatVbyte) → TxId
estimateFee(confirmationTarget) → sat/vbyte
checkMempool(txid) → present | absent | rpc_error
checkAddressOwnership(address) → ismine bool | rpc_error
```

---

## §2 — Bitcoin Core RPC Calls Owned by SweepService

| RPC | Purpose | Notes |
|-----|---------|-------|
| `getnewaddress "invoice" "bech32"` | Derive a fresh P2WPKH address; label="invoice" mandatory for Scenario D recovery | Returns address string only — see getaddressinfo |
| `getaddressinfo <address>` | (1) Read `hdkeypath` to extract HD derivation index; (2) Check `ismine` for bridge destination validation | Required second step after getnewaddress; also used for change address ownership check |
| `walletcreatefundedpsbt` | Construct a funded PSBT; Core selects UTXOs and change address automatically | Failure with insufficient funds → CRITICAL alert "Platform wallet insufficient UTXOs" |
| `walletprocesspsbt` | Sign the constructed PSBT | Returns base64-encoded signed PSBT — NOT a broadcastable hex |
| `finalizepsbt` | Convert signed PSBT to broadcastable raw hex | Required between walletprocesspsbt and sendrawtransaction; assert `"complete": true` |
| `sendrawtransaction <hex> <maxfeerate>` | Broadcast the finalized transaction | `maxfeerate` MUST be set to tier_cap in BTC/kvbyte; called AFTER DB status update commits |
| `estimatesmartfee` | Estimate the fee rate for a target block count | |
| `getmempoolentry` | Check whether a specific transaction is in the mempool | |
| `getblockcount` | Get the current chain tip height | Authoritative source for current_chain_height at settlement time |
| `getblockhash` | Get the block hash at a specific height | Used by reconciliation loop and backfill scan |
| `getblock` | Get full block data (verbosity=2) | Used for backfill scan and tx verification; does NOT require txindex |
| `backupwallet` | Trigger a wallet backup to a specified path | Path is on the Bitcoin Core host's filesystem — see `4-wallet-backup.md` for topology notes |

> **No `txindex` required.** The production node uses `prune=10000`. `txindex` is
> incompatible with pruning. All settlement-path transaction verification uses
> `getblock` at the known `first_confirmed_block_height`. The display-only
> `GET /bitcoin/tx/{txid}/status` endpoint uses `rpc.Client.GetTransaction`
> (wallet-native) which only covers transactions the wallet has seen — this is the
> correct scope. There is no `pruned` status; unknown transactions return `not_found`.

> **Signed PSBTs are sensitive.** A fully signed PSBT is a broadcastable Bitcoin
> transaction. It must never be written to any log at any level. Pass it directly
> from `walletprocesspsbt → finalizepsbt → (compute txid) → (DB update) →
> sendrawtransaction` without storing to disk or DB.

---

## §3 — Sweep Broadcast Sequence

```
1. walletcreatefundedpsbt(outputs, options)   → psbt_base64_unsigned
2. walletprocesspsbt(psbt_base64_unsigned)    → {psbt: psbt_base64_signed, complete: bool}
3. Assert complete == true; if false: return ErrPSBTIncomplete
4. finalizepsbt(psbt_base64_signed)           → {hex: raw_hex, complete: bool}
5. Assert complete == true; if false: return ErrPSBTNotFinalized
5a. Compute txid = ReverseHex(SHA256d(DecodeHex(raw_hex)))
    (Bitcoin txid = double-SHA256 of serialized tx, byte-reversed for display)
5b. DB transaction:
      UPDATE payout_records SET status='broadcast', txid=$txid, broadcast_at=NOW()
      WHERE status='constructing'
        AND batch_id=$batch_id   ← F-07: REQUIRED — prevents reclaimed-worker overwrite
    Assert RowsAffected > 0; commit.
    If RowsAffected == 0: watchdog reclaimed records — abort, do NOT broadcast.
    If commit fails: return error — do NOT broadcast.
6. sendrawtransaction(raw_hex, maxfeerate)    → txid (verify matches computed txid)
   where maxfeerate = tier_cap_sat_per_vbyte × 0.00001  (sat/vbyte → BTC/kvbyte)
   On clean RPC rejection: payout stays 'broadcast'; watchdog handles RBF.
   On timeout/connection error (F-02 ambiguous):
     UPDATE payout_records SET rpc_ambiguous=TRUE WHERE batch_id=$batch_id
     Do NOT treat as "broadcast failed" — TX may have reached the network.
```

**F-07 — batch_id in WHERE clause:** Without `AND batch_id=$my_batch_id` on step 5b,
a reclaimed-and-reassigned worker can overwrite another worker's `batch_txid`. The
vendor's payout record shows `broadcast` under a txid that may not include their
output. Always include `batch_id` in the WHERE and treat 0 RowsAffected as a clean
"reclaimed" abort — never broadcast after 0 RowsAffected.

**F-02 — rpc_ambiguous on timeout:** A timeout from `sendrawtransaction` does not
mean the TX was rejected. Bitcoin Core may have accepted it before the connection
dropped. Setting `rpc_ambiguous=TRUE` instructs the stuck-sweep watchdog to call
`getrawtransaction` before constructing any RBF replacement:
- If `getrawtransaction` returns the TX → it is on the network; wait for confirmation.
- If `getrawtransaction` returns "not found" → clear `rpc_ambiguous`, proceed with RBF.

---

## §4 — Fee Estimation and Economic Validation

### Fee estimation
`SweepService` calls `estimatesmartfee <blocks> ECONOMICAL`. Returned rate × 1.10
(10% safety buffer). Compared against tier's live fee cap.

- Buffered rate ≤ cap: sweep proceeds
- Buffered rate > cap: sweep queued; admin alerted

### Economic validation — first pass (at settlement time)

**Batch vendors (Free, Growth, Pro-batch):** batch-amortized floor:
```
floor = (estimatesmartfee(target) × 1.10 × typical_batch_vbytes) / expected_vendors_in_batch
```
Where `typical_batch_vbytes` uses a **conservative 1-input-per-output assumption**
(100 outputs × ~31 vbytes/output + 100 inputs × ~68 vbytes/input + 10 vbytes
overhead ≈ 10,000 vbytes). This errs toward over-estimating the floor.
The assumed input count is configurable via `BTC_FLOOR_INPUT_ASSUMPTION`
(default: 1 per output).

`expected_vendors_in_batch` is a tier config value (default: 50 for Free, 20 for
Growth/Pro-batch). Refreshed every `BTC_FLOOR_REFRESH_INTERVAL_MINUTES` (default: 30).

**Enterprise (realtime):** single-output fee floor (1 input + 1 output ≈ 141 vbytes).

If net < floor → `held`. If net = 0 → `ErrZeroNetAmount`.

### Economic validation — second pass (at sweep construction time)
Immediately before signing, `SweepService` performs a second fee estimate. If fees
have risen and the sweep is no longer economical, sweep is deferred and payout records
return to `held`.

### Insufficient platform wallet UTXOs
If `walletcreatefundedpsbt` returns an error indicating insufficient funds (Bitcoin
Core error code -6 or -4), a dedicated CRITICAL alert fires: "Platform wallet has
insufficient UTXOs to fund pending sweeps." This triggers an immediate reconciliation
run.

### Fee floor re-evaluation background job
A background job periodically re-evaluates all `held` payout records against the
current fee floor. When conditions allow, records are promoted to `queued`. **This
job must acquire SELECT FOR UPDATE on the vendor balance row** before reading held
totals and promoting records, to prevent a race with concurrent Phase 2 settlements.

---

## §5 — Batch Sweep Integrity

### Batch construction
All queued payout records for a given vendor are consolidated into a single output
in the transaction. Each payout record references the batch txid and its output index.

**Maximum batch output count: 100.** If queued records exceed 100 outputs, they are
split into multiple sequential batch transactions of ≤ 100 each, processed one at a
time.

### Batch reconstruction guarantee
Failed broadcasts or unconfirmed-past-threshold transactions: all records in the batch
return to `queued`. Batch is reconstructed as a completely new transaction with a new
txid.

### RBF on a batch transaction
Both original txid and replacement txid are recorded on each payout record. If the
original confirms before the replacement is broadcast, the original is treated as
canonical.

### Atomic batch status update
When a batch transaction is confirmed at 3-block depth, all payout records referencing
that batch txid must transition `broadcast → confirmed` in a single database transaction.

---

## §6 — Change Address Management

Change addresses are managed entirely by Bitcoin Core's wallet. `SweepService` calls
`walletcreatefundedpsbt`; Core automatically selects UTXOs, computes change, derives a
fresh change address, and adds the change output. The application never derives or
tracks change addresses independently.

**Platform address ownership check:** when a vendor submits a bridge destination
address or withdrawal address, `getaddressinfo(address)` is called. If `ismine: true`,
the submission is rejected. This covers change addresses (which are wallet-managed and
not in the DB) as well as invoice addresses (which are also in the DB for double-check).
Both checks run; neither is sufficient alone.

---

## §7 — Stuck Sweep Detection and RBF Trigger

A sweep transaction is considered stuck when unconfirmed past **2× its target
confirmation window**.

```
current_estimate = estimatesmartfee(target) × 1.10
```

**F-02 — rpc_ambiguous pre-check (runs before all cases below):** Before evaluating
whether to RBF a stuck broadcast, check `payout_records.rpc_ambiguous`:
- If `rpc_ambiguous=TRUE`: call `getrawtransaction($batch_txid)`.
  - Found in mempool/chain → clear `rpc_ambiguous=FALSE`, continue normal monitoring.
  - Not found → TX was never broadcast; set `rpc_ambiguous=FALSE`, proceed to RBF.
- If `rpc_ambiguous=FALSE`: proceed to normal stuck detection below.

**Case 1 — current estimate > original fee used:** RBF triggered automatically.
Replacement uses `current_estimate`. `sendrawtransaction` called with
`maxfeerate = tier_cap` to prevent fee estimation bugs from overpaying. Admin alerted.
Both txids recorded on payout records.

**Case 2 — current estimate ≤ original fee:** wait one additional window. Then
escalate to manual admin alert.

**Case 3 — stuck longer than 48 hours:** RBF triggered automatically regardless of
fee comparison. Long-stuck escalation alert fires to platform owner.

If the original transaction confirms before the replacement is broadcast, the original
is treated as canonical. The pending RBF operation is abandoned.

---

## §8 — UTXO Consolidation Job Guards

| Guard | Condition to proceed |
|-------|---------------------|
| Fee window | Current time is within the owner-configured low-fee window |
| Fee threshold | Current network fee estimate is below the owner-configured threshold |
| Reconciliation hold | No reconciliation hold is active |
| Sweep in progress | No sweep transaction is currently being constructed |
| Node availability | Bitcoin Core node is reachable |
| Concurrent instance | No other consolidation job is currently running (advisory DB lock) |

The job is disabled by default and only activates once the owner has configured a fee
threshold. The job acquires a PostgreSQL advisory lock
(`pg_try_advisory_lock(BTC_CONSOLIDATION_LOCK_KEY)`) at startup and exits immediately
if the lock is already held. The job queue must be configured with `unique_job = true`
for this job type.

---

## §9 — Test Inventory

### Legend
- `[UNIT]` — pure unit test, no DB or network
- `[INTG]` — integration test requiring real PostgreSQL
- `[RACE]` — must be run with `-race` flag

### TI-11: Payout Record and Sweep

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-11-01 | `TestPayout_Held_To_Queued_WhenFloorCleared` | INTG | Accumulated clears floor; held → queued |
| TI-11-02 | `TestPayout_Queued_To_Constructing_SweepJob` | INTG | Sweep job claims queued records |
| TI-11-03 | `TestPayout_Constructing_StaleWatchdog_ReturnsToQueued` | INTG | > 10min in constructing; watchdog → queued; WARNING |
| TI-11-04 | `TestPayout_DBUpdateBeforeBroadcast_Invariant` | INTG | **C-03/H-02**: constructing→broadcast DB commits BEFORE sendrawtransaction |
| TI-11-04b | `TestPayout_F07_BatchIdInWhereClause_ReclamedWorkerAborts` | INTG | **F-07**: broadcast WHERE includes batch_id; reclaimed worker gets 0 rows, aborts |
| TI-11-04c | `TestPayout_F07_ReclamedReassignedRecord_OriginalWorkerCannotOverwrite` | INTG RACE | **F-07**: record reclaimed+reassigned; original worker UPDATE affects 0 rows |
| TI-11-05 | `TestPayout_DBUpdateReturnsZeroRows_BroadcastAborted` | INTG | **C-03**: 0 rows → no broadcast called |
| TI-11-06 | `TestPayout_DBCommitFails_BroadcastAborted` | INTG | **C-03**: DB commit error → no broadcast called |
| TI-11-06b | `TestPayout_F02_SendRawTimeout_SetsRpcAmbiguous` | INTG | **F-02**: sendrawtransaction timeout → rpc_ambiguous=TRUE; not treated as rejection |
| TI-11-06c | `TestPayout_F02_RpcAmbiguous_Watchdog_CallsGetRawTransaction` | INTG | **F-02**: watchdog sees rpc_ambiguous=TRUE → calls getrawtransaction before RBF |
| TI-11-06d | `TestPayout_F02_RpcAmbiguous_TxFound_ClearsFlag_NoRBF` | INTG | **F-02**: getrawtransaction returns TX → rpc_ambiguous cleared; no RBF |
| TI-11-07 | `TestPayout_TxidComputed_Before_Broadcast` | UNIT | **C-03**: txid derived from raw hex before sendrawtransaction |
| TI-11-08 | `TestPayout_Broadcast_To_Confirmed_AtThreeBlocks` | INTG | 3-block depth; broadcast → confirmed |
| TI-11-09 | `TestPayout_BatchAtomicConfirmation_AllRecordsInOneTx` | INTG | Batch confirmed; all records in same DB tx |
| TI-11-10 | `TestPayout_BatchSplit_At101Outputs` | INTG | 101 queued → 2 batches (100+1) |
| TI-11-11 | `TestPayout_BroadcastDropped_ReturnsToQueued` | INTG | Tx drops from mempool; broadcast → queued; reconstructed |
| TI-11-12 | `TestPayout_RBF_TriggeredAt2xWindow` | INTG | Unconfirmed past 2×; auto-RBF; both txids recorded |
| TI-11-13 | `TestPayout_RBF_OriginalConfirmsBeforeReplacement` | INTG | Original confirms during RBF; original canonical |
| TI-11-14 | `TestPayout_MaxfeerateGuard_PreventsBuggyBroadcast` | INTG | sendrawtransaction with maxfeerate=tier_cap |
| TI-11-15 | `TestPayout_VendorSuspended_AtBroadcastBoundary_Aborted` | INTG | Suspension check fires; records → queued |
| TI-11-16 | `TestPayout_Failed_AdminRequeue_StepUpRequired` | INTG | failed → queued; TOTP verified; audit trail |
| TI-11-17 | `TestPayout_Failed_ManualPayout_TxidRecorded` | INTG | failed → manual_payout; txid mandatory |
| TI-11-18 | `TestPayout_Held_AdminManualPayout_Resolution` | INTG | **H-01**: held → manual_payout admin path; txid + reason required |
| TI-11-19 | `TestPayout_Held_AdminFailed_Resolution` | INTG | **H-01**: held → failed admin path; CRITICAL alert |
| TI-11-20 | `TestPayout_HeldAging_7DayWarning` | INTG | **N-02**: 7 days in held → WARNING |
| TI-11-21 | `TestPayout_HeldAging_30DayCritical` | INTG | **N-02**: 30 days in held → CRITICAL |
| TI-11-22 | `TestPayout_PSBT_WalletprocessAssertsComplete` | INTG | complete=false → ErrPSBTIncomplete; not retried |
| TI-11-23 | `TestPayout_PSBT_FinalizepsbtAssertsComplete` | INTG | complete=false → ErrPSBTNotFinalized |
| TI-11-24 | `TestPayout_SignedPSBT_NeverLogged` | UNIT | No log call receives signed PSBT bytes |
| TI-11-25 | `TestPayout_InsufficientWalletUTXOs_CRITICAL_Alert` | INTG | **H-06**: walletcreatefundedpsbt insufficient funds → CRITICAL alert |

### TI-12: Batch Sweep and Fee Estimation

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-12-01 | `TestFee_EstimatesmartfeeWith10PctBuffer` | UNIT | estimated × 1.10 |
| TI-12-02 | `TestFee_CapExceeded_SweepQueued_AlertFires` | INTG | Buffered rate > cap; queued; WARNING |
| TI-12-03 | `TestFee_SecondEconomicValidation_AtSweepTime` | INTG | Fee spike between settlement and sweep; defers |
| TI-12-04 | `TestFee_BatchAmortizedFloor_Calculation` | UNIT | floor = (estimate × 1.1 × vbytes) / expected_vendors |
| TI-12-05 | `TestFee_EnterpriseFloor_SingleOutput_141vbytes` | UNIT | 141 vbytes for enterprise floor |
| TI-12-06 | `TestFee_MaxfeerateConversion_SatVbyte_To_BtcKvbyte` | UNIT | sat/vbyte × 0.00001 = BTC/kvbyte |
| TI-12-07 | `TestFee_EnterpriseHeld_AlertFires` | INTG | Enterprise settlement → held → WARNING |
| TI-12-08 | `TestFee_FloorReeval_Job_SelectForUpdate` | INTG | **M-07**: fee floor re-eval acquires SELECT FOR UPDATE |
