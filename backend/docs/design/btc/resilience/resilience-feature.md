# Resilience Feature — Behavior & Edge Cases

> **What this file is:** A plain-language description of how the platform behaves
> when the Bitcoin node goes offline, how blockchain reorganizations are handled,
> and how the system recovers after an outage. Read this to understand the feature
> contract before looking at any implementation detail.
>
> **Companion:** `resilience-technical.md` — reorg rollback scope table, HandleRecovery
> flow, bitcoin_sync_state schema, post-recovery throttle, test inventory.
> **Depends on:** `../payment/payment-technical.md` (btc_outage_log schema),
> `../settlement/settlement-technical.md` (reorg rollback scope).

---

## 1. Bitcoin Node Offline — Degraded Mode

When the platform loses connection to the Bitcoin Core node:

### What continues working
- Rate cache refresh (rate feeds are independent of the Bitcoin node)

### What is SUSPENDED
- **Invoice creation** (requires `getnewaddress` and `getaddressinfo` RPC calls —
  both fail when the node is offline). Buyers receive "Bitcoin payments temporarily
  unavailable."
- Payment detection (ZMQ feed is unavailable)
- All new sweeps (wallet RPC unavailable)
- New payout construction

### What is automatically protected
- **Expiry timer pause:** when the node goes offline, a record is written to
  `btc_outage_log` (`{id, network, started_at, ended_at=NULL}`). When the node
  reconnects, the record is closed (`ended_at = NOW()`). The expiry cleanup job
  uses the precise formula in `../invoice/invoice-feature.md §5` to extend
  effective expiry for all affected invoices. No invoice expires during an outage
  window.

### Recovery
- ZMQ subscriber reconnects on exponential backoff.
- On reconnection, the settlement engine calls `HandleRecovery`, which triggers a
  block backfill scan from `last_processed_block_height` to the current chain tip.
- Sweep execution after recovery is throttled (see `resilience-technical.md §Post-Recovery
  Throttle`).
- Admin is alerted immediately when the node goes offline and when it recovers.

---

## 2. Blockchain Reorganization

When a block disconnection is detected, the platform rolls back any invoices whose
first confirmed block was the disconnected block:

- Invoices in `confirming`, `settling`, `settled`, `underpaid`, `overpaid`, or
  `settlement_failed` (with no sweep completed) → rolled back to `detected`.
- Invoices where the sweep has already completed (`sweep_completed=true`) →
  transition to `reorg_admin_required`.

For each affected invoice:
1. Rollback is applied in a DB transaction (see scope table in `resilience-technical.md §3`).
2. Associated payout records are rolled back in the same transaction.
3. Vendor and admin are alerted.
4. An immediate reconciliation run is triggered.

### `reorg_admin_required` — auto re-confirmation
When ZMQ detects that the original payment txid has re-confirmed in a new block, the
system performs a verification check before transitioning to `settled`:

1. Verify the sweep tx status (confirmed, in mempool, or dropped).
2. Update `first_confirmed_block_height` to the NEW block height.
3. Transition `reorg_admin_required → settled` atomically.

If the sweep tx is dropped (not in mempool, not confirmed), the payout records are
reset to `queued` for re-sweep, then the invoice transitions to `settled`.

A CRITICAL alert fires at entry to `reorg_admin_required`. An escalation CRITICAL
alert fires if unresolved after 72 hours. The escalation repeats weekly until resolved.

---

## 3. Post-Outage Block Backfill

When the Bitcoin node reconnects, the settlement engine must backfill missed blocks
to ensure correctness independent of ZMQ delivery. This covers payments that arrived
while the node was unreachable.

The backfill scans from `last_processed_block_height + 1` to the current chain tip.
Each payment found triggers `processPayment(invoiceID, txid, voutIndex, valueSat)`.
All `processPayment` calls are idempotent via `ON CONFLICT DO NOTHING` on
`invoice_payments`.

After backfill completes, post-recovery sweep throttling applies.

---

## 4. Chain Reset Detection

At startup and in `HandleRecovery`, the platform compares `last_processed_height`
with `getblockcount`. If `last_processed_height > getblockcount`, a chain reset or
node reindex has occurred. The platform resets `last_processed_height` to
`BTC_RECONCILIATION_START_HEIGHT`, logs an ERROR, and fires a CRITICAL alert:
"Chain height regression detected — possible testnet reset or node reindex."

---

## 5. Prune Window Validation (Startup)

At startup, the application calls `getblockchaininfo` and compares
`BTC_RECONCILIATION_START_HEIGHT` against the returned `pruneheight`. If
`start_height < pruneheight`, a CRITICAL alert fires and the application does not
accept connections until this is resolved. This check is part of the mainnet
pre-flight checklist.
