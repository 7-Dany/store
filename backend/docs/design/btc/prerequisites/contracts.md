# Bitcoin — Cross-Package Contracts

> **What this file is:** The formal interface boundaries between Bitcoin packages.
> Each section defines what one package promises to another — method signatures,
> schema ownership, event protocols. When two packages interact, the contract here
> is the source of truth, not the individual package docs.
>
> **Why this exists:** Cross-package boundary bugs (orphaned records on rollback,
> missing ismine checks on buyer addresses, unspecified persistence for debit state)
> are invisible when reading a single package. This file makes boundaries explicit
> so they can be audited as a unit.

---

## Contract 1 — `invoice` → `zmq`: Address Registration

**Provider:** `invoice` package (writes to watch set)
**Consumer:** `zmq` subscriber (reads watch set)

### Method
```
watchService.RegisterImmediate(ctx context.Context, address string, network string)
```

**Guarantees:**
- The address is added to the ZMQ subscriber's active in-memory watch set **before** the invoice creation response is returned to the buyer.
- This is in addition to (not instead of) the `invoice_address_monitoring` DB record. The DB record is the persistent source of truth; this call is the low-latency fast path.
- If the DB write subsequently fails (after `RegisterImmediate` has been called), the address may be in the watch set without a DB record. The ZMQ subscriber's 5-minute DB reload will eventually produce a consistent state (it will reload only addresses that have DB records). The net effect is a brief false-positive watch window — no payments will be missed or double-counted.

**Failure mode:** If `RegisterImmediate` is not called (e.g. only the DB write path is used), a payment arriving in the seconds between invoice creation and the next 5-minute reload will be missed entirely. This is a financial loss scenario.

**Schema dependency:** The ZMQ subscriber's 5-minute reload and startup load query `invoice_address_monitoring` — see `schemas.md`.

---

## Contract 2 — `payment` → `invoice`: Outage Log Schema

**Provider:** `payment` package (owns and writes `btc_outage_log`)
**Consumer:** `invoice` package (reads outage intervals for expiry formula)

### Schema
DDL is in `payment/payment-technical.md §4`. `invoice` must never write to this table.

### Protocol
- `invoice`'s expiry cleanup job reads `btc_outage_log` to compute `effective_expires_at` using the formula in `invoice/invoice-feature.md §5`.
- The formula uses `COALESCE(ended_at, NOW())` to handle open (in-progress) outages.
- `invoice` must never assume the outage log is empty — it must run the full formula on every expiry check.

**Breaking change risk:** Any change to `btc_outage_log` column names or the semantics of `ended_at = NULL` must be coordinated with the invoice expiry formula. The `network` column must match the invoice's network for the join to be correct.

---

## Contract 3 — `payment` → `resilience`: Recovery Trigger

**Provider:** `payment` package (owns node reconnection detection)
**Consumer:** `resilience` package (`HandleRecovery`)

### Protocol
- When the Bitcoin node reconnects (ZMQ subscriber re-establishes connection), `payment` closes the open `btc_outage_log` record (`ended_at = NOW()`) **and** triggers `resilience.HandleRecovery()`.
- These two actions must be coupled: closing the outage log and triggering recovery are part of the same reconnection event. A reconnect that closes the outage log but does not trigger recovery will leave `last_processed_height` stale. A reconnect that triggers recovery without closing the outage log will leave the outage period open, incorrectly extending invoice expiry indefinitely.
- `HandleRecovery` reads `btc_outage_log` indirectly (via `btc_outage_log.started_at` to know which blocks to scan from). It does NOT directly query the outage log — it uses `bitcoin_sync_state.last_processed_height` as its cursor.

---

## Contract 4 — `settlement` → `sweep`: SweepService Interface

**Provider:** `sweep` package (implements SweepService)
**Consumer:** `settlement` package (calls SweepService; never constructs transactions directly)

### Interface
```
constructAndBroadcast(payoutRecords[], confirmationTarget, feeCapSatVbyte) → TxId
estimateFee(confirmationTarget) → sat/vbyte
checkMempool(txid) → present | absent | rpc_error
checkAddressOwnership(address) → ismine bool | rpc_error
```

Full specification in `sweep/sweep-technical.md §1`.

### Hard invariants that `settlement` depends on:
1. **DB before network:** `SweepService` commits the `constructing → broadcast` DB update (with txid) before calling `sendrawtransaction`. If the DB commit fails, `sendrawtransaction` is not called. `settlement` relies on this to be recoverable from any failure mode.
2. **Signed PSBTs never logged:** `SweepService` never writes a signed PSBT to any log. `settlement` must not pass signed PSBTs to any non-`SweepService` function.
3. **`checkAddressOwnership` covers change addresses:** The ismine check uses `getaddressinfo` (RPC) not a DB lookup. This is required because change addresses are wallet-managed and not in the DB. `settlement` must call `checkAddressOwnership` for both vendor bridge addresses and buyer refund addresses — not just vendor addresses. See Gap B.

---

## Contract 5 — `resilience` → `settlement`: Rollback

**Provider:** `resilience` package (triggers rollback on reorg)
**Consumer:** `settlement` package (state machine owns invoice and payout transitions)

### Method
```
rollbackSettlementFromHeight(height int)
```

Full rollback scope table in `resilience/resilience-technical.md §3`.

### Hard invariants:
1. **Invoice + payout in same transaction:** When `rollbackSettlementFromHeight` transitions an invoice to `reorg_admin_required`, the associated payout record rollback must occur in the **same DB transaction**. A partial rollback (invoice rolled back, payout not yet) produces an inconsistent state that the reconciliation formula cannot resolve.

2. **`settled/sweep_completed=false → detected` also rolls back payout records:** The rollback scope table shows `settled/sweep_completed=false → detected`. Phase 2 of settlement atomically creates a `queued` or `held` payout record in the same transaction that commits the `settling → settled` transition. If a reorg then rolls the invoice back to `detected`, that payout record is now orphaned — it references a `detected` invoice and is counted by the reconciliation formula as an on-chain liability, double-counting funds. **`rollbackSettlementFromHeight` must delete (or return to `queued`) any `held` or `queued` payout records whose parent invoice is being rolled back to `detected`.** This is the fix for Gap A from the audit analysis.

3. **RowsAffected check required:** Every status UPDATE in the rollback must check `RowsAffected() == 1`. A zero result means another worker has concurrently changed the status — the rollback must treat this as an error, not success.

4. **Expiry timer not reset:** `confirming → detected` via reorg does not reset the invoice's expiry timer. The original `expires_at` and any outage-compensation still apply.

---

## Contract 6 — All Packages → `audit`: Financial Audit Event Write Protocol

**Provider:** `audit` package (owns `financial_audit_events` table)
**Consumer:** `settlement`, `sweep`, `resilience`, `vendor` (all write financial events)

### Rules
1. Every financial event is written as a new append-only row. No existing row is ever modified.
2. Admin override resolutions are written as new rows referencing the original event's `id` — the original row is never updated.
3. Every row that stores a fiat amount must include the `BTC_FIAT_CURRENCY` currency code alongside the numeric value.
4. Step-up authentication events are written with: `admin_id`, `auth_method: "totp"`, `timestamp`, `action_initiated`.
5. The DB user has `INSERT` and `SELECT` only. `UPDATE` and `DELETE` are not granted. A trigger also rejects `UPDATE`/`DELETE` from any DB user including superusers.

**Required fields on every financial audit event:**
- `timestamp` (UTC)
- `actor` (user ID, admin ID, or `"system"` for automated events)
- `satoshi_amounts_before` / `satoshi_amounts_after`
- `fiat_equivalent` + `fiat_currency_code`
- `invoice_id` or `payout_record_id` (or both)
- For admin overrides: `admin_id`, `reason` (mandatory written text), `step_up_authenticated_at`, `references_event_id`

---

## Contract 7 — `invoice` → `payment`: Monitoring Table Ownership

**Provider:** `invoice` package (owns and writes `invoice_address_monitoring`)
**Consumer:** `payment` package (ZMQ subscriber reads active addresses)

### Protocol
- `invoice` creates a row in `invoice_address_monitoring` when an invoice is created. This row has `status = 'active'` and `monitor_until = NULL` (actively monitored).
- `invoice` updates `monitor_until` (in the same transaction as the invoice status transition to a terminal state) when an invoice reaches a terminal status.
- `invoice`'s expiry cleanup job sets `status = 'retired'` when `monitor_until < NOW()`.
- `payment`'s ZMQ subscriber reads all `status = 'active'` rows at startup, on reconnection, and every 5 minutes as a safety net. It never writes to this table.
- `resilience`'s backfill scan also reads `status = 'active'` rows to match block outputs — same read-only relationship.

**Breaking change risk:** Any change to the `status` values, the `monitor_until` NULL semantics, or the partial index definition `WHERE status = 'active'` must be coordinated with the ZMQ subscriber's query and the backfill scan's query.

---

## Contract 8 — `rate` → `settlement`: Stale Debit State Persistence

**Provider:** `rate` package (owns stale debit deferral logic)
**Consumer:** `settlement` package (calls rate for subscription debit conversion)

### Required persistent state
The stale debit logic in `rate/rate-technical.md §4` references `debit_defer_count`
and `debit_first_deferred_at` by name. These values **must be persisted** (not
in-memory) to survive application restarts. If they are in-memory, the "3 deferrals
spanning more than 24 hours" forced-proceed logic can never trigger across restarts
— the safety valve that prevents indefinite deferral silently doesn't work.

**Required:** `debit_defer_count` (INTEGER) and `debit_first_deferred_at` (TIMESTAMPTZ)
must be stored on the subscription debit request record (or an equivalent persistent
store). The schema for this record must be defined and included in `schemas.md`.
This is Gap C from the audit analysis.

---

## Contract 9 — `vendor` → All: Step-Up Authentication Session Storage

**Provider:** `vendor` package (owns TOTP step-up mechanism)
**Consumer:** All packages that gate admin actions behind step-up auth

### Required persistent state
The step-up session (`step_up_valid_until = NOW() + 15 minutes`) must be stored in
**Redis or a DB column** — not in-memory on the application instance.

In a multi-instance deployment, if an admin authenticates step-up on instance A,
their next admin action may route to instance B. If session state is in-memory,
instance B will re-prompt for TOTP even though the 15-minute window has not elapsed.
This degrades UX and is inconsistent with the documented behavior.

**Required:** `step_up_valid_until` must be stored in a keyed Redis entry
(`btc:stepup:{admin_id}`) with a 15-minute TTL, or as a column on the admin session
record. The choice must be documented and the implementation must match.
This is Gap E from the audit analysis.

---

## Contract 10 — `invoice` → Buyer Refund Address: ismine Check

**Note:** This is a missing contract that must be added to the implementation.

Vendor bridge addresses have a mandatory two-step check (DB check + RPC `getaddressinfo`
ismine check) before saving. Buyer-provided refund addresses currently only have
network-aware format validation — no ismine check.

**Required contract:** Before storing a buyer refund address, the same ismine check
applied to vendor bridge addresses must run:
```
result = getaddressinfo($submitted_refund_address)
if result.ismine == true:
    reject with "Cannot use a platform-managed address as a refund destination."
```

Without this check, a buyer who submits a platform-managed address (e.g., observing
a change output from a prior sweep) as their refund address will have a refund issued
to the platform's own wallet, with no automatic credit to the buyer.
This is Gap B from the audit analysis.
