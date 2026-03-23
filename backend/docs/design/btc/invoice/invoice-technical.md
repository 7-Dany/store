# Invoice — Technical Implementation

> **What this file is:** Implementation contracts for invoice creation, address
> allocation, monitoring table schema, and the complete test inventory for this package.
>
> **Read first:** `invoice-feature.md` — behavioral contract and edge cases.
> **Depends on:** `internal/platform/bitcoin/zmq/subscriber.go` (`RegisterImmediate` / `RecoveryEvent`),
> `sql/queries/btc.sql` (all DB queries), `../vendor/vendor-technical.md` (tier config, ismine validation).

---

## Table of Contents

1. [Address Generation — Two-Step RPC Sequence](#1--address-generation--two-step-rpc-sequence)
2. [Immediate ZMQ Registration](#2--immediate-zmq-registration)
3. [Invoice Address Monitoring Schema](#3--invoice-address-monitoring-schema)
4. [invoice_payments Schema](#4--invoice_payments-schema)
5. [invoices Table — Required Columns](#5--invoices-table--required-columns)
6. [Address Uniqueness Constraint](#6--address-uniqueness-constraint)
7. [Keypool Monitoring](#7--keypool-monitoring)
8. [Test Inventory](#8--test-inventory)

---

## §1 — Address Generation — Two-Step RPC Sequence

Invoice creation requires two RPC calls in sequence. Both must succeed or the invoice
is not created.

```
Step 4a: getnewaddress "invoice" "bech32"
         → address string (bc1q... / tb1q...)

Step 4b: getaddressinfo(address)
         → read hdkeypath field
         → extract leaf index from path e.g. m/84'/0'/0'/0/5200 → index 5200
```

Both the address string and HD derivation index are stored on the invoice address
record. The derivation index is required for:
- Scenario C wallet recovery (computing the correct import range)
- Keypool cursor advance during Scenario B recovery

If either RPC call fails, the invoice is not created. The buyer receives a 503
"Bitcoin payments temporarily unavailable." A `KeypoolOrRPCError` CRITICAL alert fires.

**Address type invariant:** `getnewaddress` is **always** called with `"bech32"` as
the address type. This is a hard invariant — never call it with legacy or p2sh-segwit
type. All-lowercase bech32 addresses are required for the ZMQ subscriber's address
normalisation to work correctly.

**Label invariant:** the label argument is **always** `"invoice"`. This label is used
in Scenario D recovery (`listlabeladdresses("invoice")`) to enumerate all
platform-managed addresses from the Bitcoin Core wallet. Using any other label silently
breaks recovery. See `wallet-backup/wallet-backup-feature.md §Scenario D`.

---

## §2 — Immediate ZMQ Registration

After steps 4a and 4b succeed, and before the invoice creation response is returned
to the buyer, the new address must be added to the ZMQ subscriber's active watch set
via a **direct in-process notification** (channel message or method call). The periodic
5-minute DB reload is a safety net only — not the primary registration path.

This prevents the race condition where a payment arrives in the seconds between invoice
creation and the next 5-minute reload.

```go
// Correct ordering:
// 1. RPC calls succeed (address + derivation index known)
// 2. DB write: INSERT invoice + invoice_address_monitoring rows (atomically)
// 3. If DB write succeeds: call watchService.RegisterImmediate(ctx, address, network)
// 4. Return success to buyer
watchService.RegisterImmediate(ctx, address, network)
```

**Ordering requirement:** `RegisterImmediate` MUST be called AFTER the DB write
succeeds, not before. If called before and the DB write fails:
- The address is in the watch set but has no `invoice_address_monitoring` record.
- Any ZMQ event for that address would find no matching invoice record — the "unknown
  address" handler fires, and the payment is treated as an anomaly.
- The 5-minute reload would not add the address back (no DB record exists).
- This is a permanent false-positive watch entry until the next application restart.

**If RegisterImmediate fails** (channel full, subscriber not running): log the error
but do NOT fail the invoice creation. The DB record exists; the 5-minute reload is
the safety net and will pick up the address. A missed immediate registration means at
most a 5-minute window where ZMQ events are not matched.

---

## §3 — Invoice Address Monitoring

**Schema:** defined in `sql/schema/009_btc.sql` (`invoice_address_monitoring` table,
`uq_iam_one_active_per_invoice`, `uq_iam_active_address_network` unique partial indexes,
`fn_iam_address_consistency` trigger in `011_btc_functions.sql`).

**Queries** (`sql/queries/btc.sql`):

| Query | When called |
|-------|-------------|
| `CreateInvoiceAddressMonitoring` | After `CreateInvoiceAddress` commits; before `RegisterImmediate()` |
| `LoadActiveMonitoringByNetwork` | Startup + every `RecoveryEvent` |
| `GetActiveMonitoringByAddress` | Per-hashtx DB fallback (hot path is in-memory) |
| `SetMonitoringWindow` | Same TX as terminal invoice status transition |
| `RetireExpiredMonitoringRecords` | Expiry cleanup job |

### Monitoring window update rules
When an invoice transitions to a terminal status, `SetMonitoringWindow` is called in the
same DB transaction as the status update:

| Terminal status | monitor_until |
|-----------------|---------------|
| `expired` | `expired_at + 30 days` |
| `cancelled` | `cancelled_at + 30 days` |
| `settled` | `settled_at + 30 days` |
| `refunded` | `refunded_at + 30 days` |
| `manually_closed` | `closed_at + 30 days` |
| `reorg_admin_required` | **NULL** — remains `active`. Set only when transitioning out. |

### ZMQ reload
The recovery handler (registered via `sub.RegisterRecoveryHandler`) calls
`LoadActiveMonitoringByNetwork` to rebuild the in-memory watch set:
- On startup (before `sub.Run()`)
- On every `RecoveryEvent` (reconnect or sequence gap)
- On the 5-minute safety net timer

New addresses are registered immediately via `RegisterImmediate()` after
`CreateInvoiceAddressMonitoring` commits — the 5-minute reload is a safety net only.

### Archival note (G-N4)
Retired records are never deleted. The partial index `WHERE status = 'active'` keeps
active-path queries fast. For high-volume production, implement time-based partitioning
or a periodic archival job.

---

## §4 — invoice_payments

**Schema:** defined in `sql/schema/009_btc.sql` (`invoice_payments` table,
`uq_inv_payment_txid_vout` unique constraint, `idx_ip_invoice_id` covering index
with `INCLUDE (value_sat, txid, vout_index, detected_at)` for index-only Phase 1 SUM).

**Queries** (`sql/queries/btc.sql`):
- `UpsertInvoicePayment` — always `ON CONFLICT (txid, vout_index) DO NOTHING`; idempotent
- `SumInvoicePayments` — Phase 1 sum: `SUM(value_sat) WHERE invoice_id = $id AND txid = $txid`

**Key column notes:**
- `invoice_id`: FK to invoices; required by all settlement and reconciliation queries
- `double_payment`: true when a second distinct txid pays the same address while active
- `post_settlement`: true when payment arrived after invoice was already settled
- All inserts use `ON CONFLICT (txid, vout_index) DO NOTHING` for idempotency

---

## §5 — invoices Table — Key Columns

**Schema:** defined in `sql/schema/009_btc.sql`. **Queries:** `sql/queries/btc.sql`
(`CreateInvoice`, `GetInvoice`, `GetInvoiceWithLock`, all `TransitionInvoice*` queries).

The full column list is in the schema file. Key columns with implementation notes:

Additional columns required beyond the obvious fields:

| Column | Type | Notes |
|--------|------|-------|
| `vendor_id` | `TEXT NOT NULL` | FK to the vendor whose product was purchased |
| `tier_id` | `TEXT NOT NULL` | FK to the tier at invoice creation time (for audit) |
| `wallet_mode` | `TEXT NOT NULL` | Snapshot of wallet mode: `'bridge'`, `'platform'`, `'hybrid'` |
| `bridge_destination_address` | `TEXT` | **Snapshotted** bridge/hybrid destination address at invoice creation time. NULL for platform wallet mode. This is the address where sweep proceeds when the invoice settles — the vendor's current address is irrelevant after creation. See G-C2 below. |
| `sweep_completed` | `BOOLEAN NOT NULL DEFAULT false` | Set to `true` when the associated payout record first transitions to `broadcast`. This is the pivot for reorg rollback logic. Set by the `sweep` package in the same transaction as `constructing → broadcast`. |
| `first_confirmed_block_height` | `INTEGER` | Block height at which the invoice's payment first received its initial confirmation. **Must be set atomically with the `detected → confirming` transition** — not deferred. Updated when `reorg_admin_required` re-confirms. See G-I2. |
| `settling_source` | `TEXT` | `'confirming'` or `'underpaid'` when status is `settling`. Also preserved in `settlement_failed` status so admin retry knows the original predecessor. NULL only when status is not `settling` or `settlement_failed`. See G-C3. |
| `detected_txid` | `TEXT` | txid currently being watched |
| `detected_at` | `TIMESTAMPTZ` | When the payment was first seen |
| `mempool_absent_since` | `TIMESTAMPTZ` | Set on first absent watchdog check; NULL when tx present or invoice not in `detected` status; explicitly cleared to NULL on `mempool_dropped → detected` re-transition |
| `retry_count` | `INTEGER NOT NULL DEFAULT 0` | Settlement retry counter. Reset to 0 on admin-triggered retry. See G-I6. |
| `expires_at` | `TIMESTAMPTZ NOT NULL` | Original expiry timestamp (before outage compensation). Used by expiry formula. |
| `fiat_equivalent_created_at` | `BIGINT` | Fiat value at creation (informational) |
| `fiat_currency_code` | `TEXT NOT NULL` | Currency code (e.g. `'USD'`). Must match `BTC_FIAT_CURRENCY`. |
| `network` | `TEXT NOT NULL` | `'mainnet'` or `'testnet4'` |

> **Gap G-C2 (resolved):** `bridge_destination_address` must be snapshotted on the
> invoice at creation time. The invoice snapshot governs settlement — if a vendor
> changes their bridge address after invoice creation, in-flight invoices use the
> original address. This prevents the wrong party from receiving a sweep. Hybrid mode
> vendors use the same column for their external sweep destination.

> **Gap G-C3 (resolved):** `settling_source` is preserved in `settlement_failed`
> status (not set to NULL). When an invoice transitions from `settling` to
> `settlement_failed`, `settling_source` retains its value (`'confirming'` or
> `'underpaid'`). Admin retry of `settlement_failed` reads this column to set
> `settling_source` correctly on the new `settling` claim. The column description
> "NULL when not settling" is corrected: it should be "NULL for all statuses except
> `settling` and `settlement_failed`."

> **Gap G-I2 (resolved):** `first_confirmed_block_height` is set in the same DB
> transaction as the `detected → confirming` status transition. It is never set
> asynchronously or deferred. The confirmation depth calculation
> `current_chain_height - first_confirmed_block_height` is only correct if this
> column reflects the block height of the first confirmation.

---

## §6 — Address Uniqueness Constraint

```sql
CONSTRAINT uq_invoice_addresses_address_network UNIQUE (address, network)
```

Required on the invoice addresses table to prevent address reuse. If Bitcoin Core
issues the same address twice (which should never happen with a properly functioning
keypool), the second invoice creation will fail at the DB level rather than silently
creating a duplicate.

On constraint violation: the invoice creation returns a 503 and the `KeypoolOrRPCError`
CRITICAL alert fires. Bitcoin Core should be investigated. Do NOT retry automatically —
the duplicate address would cause double-settlement risk.

---

## §7 — Keypool Monitoring

The keypool is the pool of pre-derived addresses Bitcoin Core maintains for fast
issuance via `getnewaddress`. If it is exhausted, address generation fails.

| Threshold | Action |
|-----------|--------|
| `keypoolsize < 100` | `Keypool low` WARNING alert fires; `keypoolrefill` is called automatically |
| `keypoolsize < 10` | CRITICAL alert fires |

Set `keypool=10000` in `bitcoin.conf`.

```bash
bitcoin-cli -datadir=/var/lib/bitcoin getwalletinfo | jq .keypoolsize
```

---

## §8 — Test Inventory

### Legend
- `[UNIT]` — pure unit test, no DB or network
- `[INTG]` — integration test requiring real PostgreSQL
- `[E2E]` — end-to-end test requiring Bitcoin Core in regtest mode
- `[RACE]` — must be run with `-race` flag

### TI-1: Invoice Creation

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-1-01 | `TestCreateInvoice_HappyPath_BridgeMode` | INTG | Full two-step RPC, address registered with ZMQ, snapshot stored |
| TI-1-02 | `TestCreateInvoice_HappyPath_PlatformWalletMode` | INTG | Balance credit path; legal flag required |
| TI-1-03 | `TestCreateInvoice_HappyPath_HybridMode` | INTG | Snapshot includes auto_sweep_threshold and bridge_destination_address |
| TI-1-04 | `TestCreateInvoice_RateFeedUnavailable_Returns503` | INTG | Both rate sources down; creation suspended |
| TI-1-05 | `TestCreateInvoice_NodeOffline_Returns503` | INTG | Node offline; creation suspended; btc_outage_log written |
| TI-1-06 | `TestCreateInvoice_VendorNoAddress_BridgeMode_Rejected` | INTG | Bridge mode vendor without address; 503 |
| TI-1-07 | `TestCreateInvoice_VendorTierModeNotPermitted_Rejected` | INTG | Tier doesn't allow platform wallet mode |
| TI-1-08 | `TestCreateInvoice_BelowMinimumAmount_Rejected` | UNIT | Amount below tier minimum satoshi |
| TI-1-09 | `TestCreateInvoice_BuyerConcurrentInvoiceLimit_Rejected` | INTG | Buyer at 20 pending; 21st rejected |
| TI-1-10 | `TestCreateInvoice_BuyerCooldown_After_Expiry` | INTG | 60-second cooldown enforced post-expiry |
| TI-1-11 | `TestCreateInvoice_Step4a_Fails_NoInvoiceCreated` | INTG | getnewaddress fails; 503; alert fires |
| TI-1-12 | `TestCreateInvoice_Step4b_Fails_NoInvoiceCreated` | INTG | getaddressinfo fails; invoice not written |
| TI-1-13 | `TestCreateInvoice_AddressRegisteredWithZMQ_AfterDBWrite` | INTG | **G-ordering**: RegisterImmediate called after DB write; address in watch set |
| TI-1-14 | `TestCreateInvoice_DBWriteFails_RegisterImmediateNotCalled` | INTG | **G-ordering**: DB write fails; RegisterImmediate not called; no phantom watch entry |
| TI-1-15 | `TestCreateInvoice_TierSnapshotImmutable_AfterTierChange` | INTG | Tier changes; in-flight invoice keeps original snapshot |
| TI-1-16 | `TestCreateInvoice_ModeSnapshotImmutable_AfterModeChange` | INTG | Vendor changes wallet mode; in-flight unaffected |
| TI-1-17 | `TestCreateInvoice_BridgeAddressSnapshotted_AfterVendorChangesAddress` | INTG | **G-C2**: vendor changes bridge address; in-flight invoice still uses original snapshotted address |
| TI-1-18 | `TestCreateInvoice_TOCTOU_VendorAddressRemovedDuringCreation` | INTG | Address removed between check and generation |
| TI-1-19 | `TestCreateInvoice_PlatformWalletMode_LegalFlagFalse_Rejected` | INTG | PLATFORM_WALLET_MODE_LEGAL_APPROVED=false |
| TI-1-20 | `TestCreateInvoice_AddressType_AlwaysP2WPKH_Bech32` | INTG | getnewaddress called with "bech32"; bc1q / tb1q prefix |
| TI-1-21 | `TestCreateInvoice_FiatToSatoshi_FloorRounding` | UNIT | 0.000012345678 BTC → 1234 sat (not 1235); floor rule |
| TI-1-22 | `TestCreateInvoice_FiatToSatoshi_ExactAmount_NoRounding` | UNIT | Exact integer conversion; no rounding applied |
| TI-1-23 | `TestCreateInvoice_SnapshotIncludesOverpaymentThresholds` | INTG | Both overpayment thresholds stored on invoice snapshot |
| TI-1-24 | `TestCreateInvoice_BuyerRefundAddress_IsmineTrue_Rejected` | INTG | **G-B (contracts.md Contract 10)**: buyer submits platform-managed address as refund address → rejected |
| TI-1-25 | `TestCreateInvoice_FirstConfirmedBlockHeight_SetAtConfirming` | INTG | **G-I2**: first_confirmed_block_height set atomically with detected→confirming transition |
| TI-1-26 | `TestCreateInvoice_AddressConflict_Returns503_NotRetried` | INTG | **G-§6**: duplicate address constraint violation → 503; CRITICAL alert; no automatic retry |

### TI-16: Address Monitoring

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-16-01 | `TestMonitoring_NewInvoice_ImmediatelyRegistered` | INTG | New address in ZMQ active set after DB write returns |
| TI-16-02 | `TestMonitoring_5MinReload_PicksUpNewAddresses` | INTG | Periodic reload safety net |
| TI-16-03 | `TestMonitoring_WindowByStatus_Expired` | INTG | expired_at + 30 days = monitor_until |
| TI-16-04 | `TestMonitoring_WindowByStatus_Settled` | INTG | settled_at + 30 days = monitor_until |
| TI-16-05 | `TestMonitoring_ReorgAdminRequired_OpenEnded_NullMonitorUntil` | INTG | reorg_admin_required → monitor_until stays NULL |
| TI-16-06 | `TestMonitoring_Retirement_AfterWindowElapsed` | INTG | monitor_until < NOW(); cleanup sets retired |
| TI-16-07 | `TestMonitoring_AddressUniquenessConstraint_Prevents_Reuse` | INTG | Second invoice at same address; constraint violation |
| TI-16-08 | `TestMonitoring_StartupLoad_AllNonRetiredAddresses_InWatchSet` | INTG | ZMQ loads all active on startup |
| TI-16-09 | `TestMonitoring_ReconnectLoad_ReloadsFromDB` | INTG | ZMQ reconnect; watch set reloaded |
