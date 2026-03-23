# Bitcoin ZMQ — Resolved Context

**Section:** New domain — `internal/platform/bitcoin/` + `internal/domain/bitcoin/`
**Status:** Stage 0 approved; Stage 2 schema complete

---

## Resolved paths

- ZMQ platform: `internal/platform/bitcoin/zmq/` (`subscriber.go`, `event.go`, `conn.go`, `recorder.go`)
- RPC platform:  `internal/platform/bitcoin/rpc/` (`client.go`, `types.go`, `recorder.go`)
- Domain root:   `internal/domain/bitcoin/`
- Shared:        `internal/domain/bitcoin/shared/recorder.go` (`BitcoinRecorder` interface)
- Feature packages (to build): `watch/`, `events/`, `txstatus/`, `invoice/`, `payment/`, `settlement/`, `sweep/`, `rate/`, `resilience/`, `vendor/`, `audit/`, `reconciliation/`, `webhook/`, `compliance/`, `kyc/`, `dispute/`, `wallet-backup/`
- Config:        `internal/config/config.go`
- Deps:          `internal/app/deps.go` (`BitcoinZMQ`, `BitcoinRPC`, `BitcoinNetwork`, `KVStore`)
- Schema:        `sql/schema/009_btc.sql`, `sql/schema/010_btc_payouts.sql`, `sql/schema/011_btc_functions.sql`
- Queries:       `sql/queries/btc.sql` (production), `sql/queries_test/btc_test.sql` (test-only)
- bitcoin.conf:  `D:\bitcoin\data\bitcoin.conf` (zmqpubhashblock, zmqpubhashtx)

---

## Key decisions — current (supersedes stale entries below)

| ID | Decision | Notes |
|----|----------|-------|
| D-02 | Pure-Go ZMTP 3.1 implementation in `zmq/conn.go` | No CGo, no third-party ZMQ library |
| D-03 | `hashblock` + `hashtx` topics only | Not raw* |
| D-04 | Handler registration pattern for platform→domain fan-out | `RegisterBlockHandler`, `RegisterSettlementTxHandler`, `RegisterDisplayTxHandler`, `RegisterRecoveryHandler` |
| **D-05** | **Address matching uses `rpc.GetTransaction` (wallet-native, no txindex)** | ~~Originally `getrawtransaction`~~ — corrected; `GetTransaction` covers all wallet-tracked transactions without requiring `txindex=1` |
| D-06 | **Settlement watch: DB-backed `invoice_address_monitoring`; in-memory cache loaded at startup and on every `RecoveryEvent`** | ~~Originally in-memory sync.Map per userID~~ — superseded. Display watch (SSE) still uses Redis SET via `POST /watch`. See `watch/watch-feature.md §Two separate watch systems`. |
| D-07 | SSE (not WebSocket) for event stream | |
| **D-08** | **SSE auth via HttpOnly cookie (`btc_sse_jti`)** | ~~Originally `?token=` query string~~ — superseded. Token issuances recorded in `sse_token_issuances` table for GDPR erasure. See `events/events-technical.md §1`. |
| D-10 | `internal/domain/bitcoin/` domain — never imports other domains | |
| D-12 | Feature opt-in via `BTC_ENABLED=false` default | |
| D-24 | All invoice addresses are P2WPKH bech32 (`getnewaddress "invoice" "bech32"`) | Label "invoice" mandatory for Scenario D wallet recovery |

---

## ZMQ subscriber — how it works

The subscriber (`zmq.Subscriber` interface, `zmq/subscriber.go`) is a pure platform concern.
It has zero domain imports. Domain packages register handlers before `Run()` is called.

```go
// Wiring (in server.New or bitcoin domain assembler):
watchSvc.Load(ctx)                                              // 1. Load DB watch set before Run()
sub.RegisterSettlementTxHandler(payment.Handler.Handle)        // 2. Register handlers
sub.RegisterBlockHandler(settlement.Handler.Handle)
sub.RegisterRecoveryHandler(recovery.Handler.Handle)
go sub.Run(ctx)                                                 // 3. Start
defer sub.Shutdown()
```

On `RecoveryEvent` (reconnect or sequence gap), the recovery handler must:
1. `GetOpenOutage` → `CloseOutageRecord` (close the outage window in DB)
2. `LoadActiveMonitoringByNetwork` → rebuild the in-memory watch set
3. Backfill missed blocks via `rpc.GetBlockCount` / `rpc.GetBlockHash` loop

---

## Audit events

| Constant | Value |
|----------|-------|
| `EventBitcoinSSETokenIssued` | `"bitcoin_sse_token_issued"` |
| `EventBitcoinSSETokenConsumeFailure` | `"bitcoin_sse_token_consume_failure"` |
| `EventBitcoinSSEConnected` | `"bitcoin_sse_connected"` |
| `EventBitcoinSSEDisconnected` | `"bitcoin_sse_disconnected"` |
| `EventBitcoinSSECapExceeded` | `"bitcoin_sse_cap_exceeded"` |
| `EventBitcoinAddressWatched` | `"bitcoin_address_watched"` |
| `EventBitcoinTxDetected` | `"bitcoin_tx_detected"` |
| `EventBitcoinWatchRateLimitHit` | `"bitcoin_watch_rate_limit_hit"` |
| `EventBitcoinWatchLimitExceeded` | `"bitcoin_watch_limit_exceeded"` |
| `EventBitcoinWatchInvalidAddress` | `"bitcoin_watch_invalid_address"` |

---

## Sentinel errors

Defined in `internal/domain/bitcoin/shared/`:

- `ErrZMQNotRunning`
- `ErrInvalidAddress`
- `ErrUnsupportedNetwork`
- `ErrStatusPreconditionFailed` — invoice or payout status UPDATE returned 0 RowsAffected
- `ErrInsufficientBalance` — SQLSTATE 23514 from `btc_debit_balance` stored procedure
- `ErrDuplicatePayout` — SQLSTATE 23505 on `payout_records.uq_pr_invoice_id`
- `ErrRPCUnavailable` — non-404 RPC error from Bitcoin Core
- `ErrRateStale` — exchange rate cache age exceeded threshold for subscription debit

---

## Rate-limit Redis key prefixes

| Prefix | Endpoint | Limit |
|--------|----------|-------|
| `btc:watch:ip:` | `POST /bitcoin/watch` | 10/min IP |
| `btc:token:ip:` | `POST /bitcoin/events/token` | 5/min IP |
| `btc:events:ip:` | `GET /bitcoin/events` | 5/min IP |
| `btc:status:ip:` | `GET /bitcoin/status` | 20/min IP |
| `btc:txstatus:ip:` | `GET /bitcoin/tx/*/status` | 20/min IP |
| `btc:sse:conn:` | Per-user SSE connection counter | |
| `btc:global:watch_count` | Cross-instance watch count estimate | advisory |

---

## Stage 2 implementation order

### Stage 2a — `invoice/`: detection only, nothing moves
- Invoice creation: `getnewaddress "invoice" "bech32"` + `getaddressinfo` (two-step RPC)
- Address registered in `invoice_address_monitoring` (DB) + in-memory watch set (immediate)
- Queries: `CreateInvoice`, `CreateInvoiceAddress`, `CreateInvoiceAddressMonitoring`, `LoadActiveMonitoringByNetwork`
- **Financial risk: Zero**

### Stage 2b — `settlement/`: accounting only, no on-chain transaction
- Atomic settlement with Phase 1 pre-claim checks
- Queries: all `TransitionInvoice*`, `CreatePayoutRecord`, `CreditVendorBalance`, `DebitVendorBalance`, `InsertFinancialAuditEvent`
- Ships when `bitcoin_balance_drift_satoshis = 0` for ≥ 1 week on testnet4

### Stage 2c — `sweep/`: real Bitcoin moves
- Full PSBT flow; DB update (`constructing → broadcast`) MUST commit before `sendrawtransaction`
- Queries: `ClaimPayoutForConstruction`, `SetPayoutBroadcast`, `SetPayoutConfirmed`, `IncrementTreasuryReserve`
- Default in beta: `BTC_AUTO_SWEEP_ENABLED=false`
- **Financial risk: Real Bitcoin leaves the platform**

---

## Integration with existing platform systems

| System | How the Bitcoin system uses it |
|--------|-------------------------------|
| **RBAC roles** | Tier assignment (via `fn_sync_vendor_tier_role` trigger) changes the vendor's RBAC role |
| **Approval workflow** | Withdrawals above threshold enter the existing approval pipeline |
| **Billing system** | Subscription fee debit requests; rate-staleness deferral response |
| **Job queue** | Settlement, sweep, reconciliation, UTXO consolidation, expiry cleanup, mempool watchdog, stale-constructing watchdog, wallet backup, held-aging monitor, fee-floor re-evaluation |
| **ZMQ infrastructure** | Settlement engine receives `BlockEvent`, `TxEvent`, `RecoveryEvent` via handler registration |
| **Financial audit trail** | `financial_audit_events` table — immutable, indefinite retention, GDPR-compliant via HMAC pseudonymisation |
| **Product listing system** | Enforces bridge-mode address requirement |
| **Bitcoin Core wallet (RPC)** | `rpc.Client` interface in `internal/platform/bitcoin/rpc/client.go` |

---

## Package Map

Each package has a `{name}-feature.md` (behavior, rules, edge cases) and a
`{name}-technical.md` (schemas, queries, state machines, implementation contracts,
test inventory). Read the feature file first.

| Package | Feature doc | Technical doc | Primary content |
|---------|-------------|---------------|-----------------|
| `invoice/` | invoice-feature.md | invoice-technical.md | Vendor wallet modes, invoice creation, address lifecycle, expiry rules |
| `payment/` | payment-feature.md | payment-technical.md | Payment detection, confirmation depths, mempool drop watchdog, outage log |
| `settlement/` | settlement-feature.md | settlement-technical.md | Settlement phases, underpay/overpay/hybrid, invoice state machine, payout state machine |
| `sweep/` | sweep-feature.md | sweep-technical.md | Fee system, sweep models, PSBT broadcast sequence, RBF, batch integrity |
| `rate/` | rate-feature.md | rate-technical.md | BTC/fiat rate cache, deviation policy, failure behavior, btc_exchange_rate_log |
| `resilience/` | resilience-feature.md | resilience-technical.md | Degraded mode, reorg rollback, post-outage backfill, HandleRecovery |
| `vendor/` | vendor-feature.md | vendor-technical.md | Tier config, vendor lifecycle, regulatory context, TOTP step-up auth |
| `audit/` | audit-feature.md | audit-technical.md | Financial audit trail DB enforcement, immutability triggers, actor label HMAC |
| `reconciliation/` | reconciliation-feature.md | reconciliation-technical.md | Reconciliation formula, sweep-hold activation, checkpoint design, block cursor |
| `webhook/` | webhook-feature.md | webhook-technical.md | Transactional outbox, delivery worker, retry backoff, dead letter management |
| `compliance/` | compliance-feature.md | compliance-technical.md | FATF Travel Rule recording, GDPR erasure job, pseudonymisation patterns |
| `kyc/` | kyc-feature.md | kyc-technical.md | KYC state machine, payout gate, provider webhook handling |
| `dispute/` | dispute-feature.md | dispute-technical.md | Dispute lifecycle, payout freeze/unfreeze, resolution paths, auto-resolution |
| `wallet-backup/` | wallet-backup-feature.md | wallet-backup-technical.md | Backup layers, recovery scenarios (A/B/C/D), keypool cursor advance, pre-mainnet checklist |

Packages from Stage 0 (platform layer — no domain imports):

| Package | Docs | Notes |
|---------|------|-------|
| `zmq/` | `internal/platform/bitcoin/zmq/` | ZMTP subscriber, event types, recorder interface |
| `rpc/` | `internal/platform/bitcoin/rpc/` | JSON-RPC client, types, all RPC method wrappers |

Display-layer packages (Stage 0 HTTP features):

| Package | Feature doc | Technical doc |
|---------|-------------|---------------|
| `watch/` | watch-feature.md | watch-technical.md |
| `events/` | events-feature.md | events-technical.md |
| `txstatus/` | txstatus-feature.md | txstatus-technical.md |

---

## Open items (blocking mainnet)

| # | Item | Blocks |
|----|------|--------|
| O-01 | Jurisdiction determination for platform wallet mode | Platform wallet mode launch |
| O-02 | Legal ToS review for platform wallet mode | Platform wallet mode launch |
| O-03 | KYC provider selection and API contract | KYC feature launch |
| O-04 | FATF threshold jurisdiction configuration (USD/EUR/other) | Compliance launch |

---

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

---

# Prerequisites — RBAC Additions

> **Package:** `internal/platform/rbac/`
> **Files affected:** `checker.go`
>
> **Status:** Must be merged before any bitcoin domain code is written.
> **Depends on:** Nothing — constant additions only.
> **Blocks:** Nothing in Stage 0 (not yet enforced). Stage 2 routes will reference
>   these constants when access control is wired.

---

## Overview

Stage 0 does not gate bitcoin endpoints behind RBAC — any authenticated user can
call `/watch`, `/events/token`, `/events`, and `/status`. These constants are added
pre-emptively so that when Stage 2 introduces access control, developers reference
typed constants rather than raw string literals that are easy to mistype and
impossible to grep reliably.

No logic changes. No changes to `checker.go` beyond adding the constants.

---

## New Constants

Add to the existing `const` block in `checker.go` alongside existing `Perm*` constants:

```go
// Bitcoin payment domain permissions.
// Stage 0: not yet enforced — all authenticated users may access bitcoin endpoints.
// Stage 2+: apply rbac.Require(rbac.PermBitcoinWatch) to POST /watch and GET /events.
const (
    PermBitcoinWatch  = "bitcoin:watch"   // register addresses for SSE notification
    PermBitcoinStatus = "bitcoin:status"  // read ZMQ subscriber health (GET /status)
    PermBitcoinManage = "bitcoin:manage"  // admin: adjust watch limits, flush caches
)
```

---

## Stage 2 Pre-Launch Checklist

When `rbac.Require(PermBitcoin*)` is wired on any route in Stage 2:

1. Add the corresponding rows to the DB permissions seed migration
   (`db/migrations/` or `db/seed/permissions.sql`) before the route is wired.
2. Add an integration test (or startup check) asserting that if
   `rbac.Require(PermBitcoinWatch)` is wired, the `bitcoin:watch` row exists in
   the DB permissions table. A missing seed migration causes a silent global 403
   on all bitcoin endpoints in any environment that skips the migration.
3. Add "rbac-seed-migration-verified" to the Stage 2 pre-launch hard-blocker checklist.

---

## No Test Inventory

No new logic — pure constant additions. Existing `rbac` tests remain unchanged.
The Stage 2 DB assertion test is added in the Stage 2 implementation, not here.
