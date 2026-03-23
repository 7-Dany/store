# Reconciliation Feature — Behavior & Edge Cases

> **What this file is:** A plain-language description of what the reconciliation job
> does, how the formula works, what a discrepancy means, and every edge case it
> handles. Read this to understand the feature contract before looking at any
> implementation detail.
>
> **Companion:** `reconciliation-technical.md` — job scheduling, checkpoint design,
> formula query breakdown, sweep-hold activation, test inventory.
> **Depends on:** `../audit/audit-feature.md` (treasury reserve tracking),
> `../settlement/settlement-feature.md` (payout record statuses).

---

## What Reconciliation Does

The reconciliation job is the platform's accounting integrity check. It runs every
6 hours and answers one question: does the sum of all money the platform is
responsible for equal what is actually held in the Bitcoin wallet?

If the answer is no, something is wrong — a bug, a missed settlement, a failed sweep
record — and the platform stops all outgoing sweeps until a human investigates.

---

## The Formula

```
on_chain_UTXO_value
  = in_flight_invoice_satoshis
  + pre_confirmation_payout_obligations
  + platform_mode_vendor_balances
  + treasury_reserve_satoshis
```

Each term represents a category of funds the platform is custodying:

**`in_flight_invoice_satoshis`** — the sum of all `amount_sat` on invoices in
non-terminal states (`pending`, `detected`, `confirming`, `settling`, `underpaid`,
`mempool_dropped`). These are invoices where on-chain funds have been received (or
are expected) but settlement has not yet completed.

**`pre_confirmation_payout_obligations`** — the sum of `net_satoshis` on payout
records in `held`, `queued`, `constructing`, or `broadcast` status. These are
settled invoices where the vendor's money has not yet confirmed on-chain at the
destination.

**`platform_mode_vendor_balances`** — the sum of `balance_satoshis` in
`vendor_balances` for **platform-mode vendors only**. Hybrid-mode balances are
intentionally excluded: a hybrid vendor's balance is a running accumulator toward
the next sweep threshold, and its value is already fully captured in the `held`
payout records for that vendor. Counting both would double-count hybrid funds.

**`treasury_reserve_satoshis`** — accumulated miner fees retained from completed
sweeps. Every confirmed sweep transaction deducts miner fees from the wallet
UTXOs; the treasury reserve tracks this so the formula stays balanced. It is
incremented atomically with each `SetPayoutConfirmed` DB write. If any sweep
confirmation omits the `IncrementTreasuryReserve` call, every subsequent
reconciliation run will show a permanent negative discrepancy equal to the
missing fee amount.

---

## What a Discrepancy Means

A non-zero discrepancy (`on_chain - formula_sum ≠ 0`) means the DB accounting has
drifted from on-chain reality. This is always a serious event. Possible causes:

- A sweep confirmation wrote `SetPayoutConfirmed` but skipped `IncrementTreasuryReserve`
  (negative discrepancy equal to the miner fee)
- A payment was received on-chain but no `invoice_payments` record was written
  (positive discrepancy)
- A settlement marked an invoice settled but did not create a payout record or credit
  a balance (funds appear as settled but are unaccounted for in the formula)
- A DB bug or direct DB manipulation

A positive discrepancy (on-chain holds more than the formula accounts for) is unusual
but not impossible — e.g. an untracked transaction sent to a platform address from
outside the payment system.

A negative discrepancy (formula says we owe more than we hold) is more dangerous. It
means the platform may not be able to fulfil all outstanding payout obligations.

---

## Discrepancy Response

When any discrepancy is detected:

1. A CRITICAL alert fires immediately with the discrepancy amount in satoshis.
2. **Sweep-hold mode is activated** (`platform_config.sweep_hold_mode = TRUE`). All
   outgoing sweep construction and broadcast is blocked until an admin explicitly
   clears the hold.
3. The discrepancy is recorded in `reconciliation_job_state` and a history row in
   `reconciliation_run_history`.
4. If the hold is not cleared within 4 hours, an escalation alert fires to the
   platform owner.
5. Hold is cleared by admin action only — `ClearSweepHold` query with a mandatory
   written reason, requiring step-up authentication.

**Sweeps already broadcast at the time of hold activation complete normally** —
the hold blocks new construction, not in-flight broadcasts.

---

## Missed Reconciliation Job

If `last_successful_run_at` on `reconciliation_job_state` is more than 8 hours in
the past (checked by an independent monitoring job), a "Reconciliation job missed"
CRITICAL alert fires. This fires even if the discrepancy check would have passed —
it means the watchdog itself is broken.

The 8-hour threshold gives two full job cycles of grace (each is 6 hours) before
alerting, allowing for transient infrastructure failures without false positives.

---

## Block Backfill and Reconciliation

Reconciliation runs as a block-by-block cursor scan rather than a pure balance check.
The `bitcoin_sync_state` table tracks `last_processed_height` — the last block the
reconciliation job has fully processed. Each run advances this cursor to the current
chain tip, processing any unseen blocks along the way.

This design means reconciliation also serves as the block backfill for the settlement
engine after a node outage. When the node reconnects, the `RecoveryEvent` triggers
`HandleRecovery`, which advances the cursor from `last_processed_height` to the
current tip and processes any payments that arrived while the node was unreachable.

The cursor advances in checkpoints of `BTC_RECONCILIATION_CHECKPOINT_INTERVAL`
blocks per DB transaction (default: 100). A crash mid-backfill resumes from the last
checkpoint, not the beginning.

---

## Chain Reset Detection

At the start of each reconciliation run, `last_processed_height` is compared against
`getblockcount`. If `last_processed_height > getblockcount`, a chain reset or node
reindex has occurred. The cursor is reset to `BTC_RECONCILIATION_START_HEIGHT`, a
CRITICAL alert fires, and the job logs an ERROR. This covers testnet resets and
operator-initiated node reindexes.

---

## Prune Window Validation

At startup, `BTC_RECONCILIATION_START_HEIGHT` is compared against the node's
`pruneheight` (from `getblockchaininfo`). If `start_height < pruneheight`, the
application refuses to accept connections until this is resolved. The missing blocks
cannot be backfilled on a pruned node — they are gone permanently.

---

## Edge Cases

### Invoices in terminal states are excluded
Settled, expired, cancelled, manually closed, and refunded invoices are not in the
formula. Their funds have either been swept (accounted for in payout records) or
are not the platform's responsibility.

### `reorg_admin_required` invoices
These invoices are in a non-terminal state (not settled, sweep may have been
broadcast to the old chain). Their amount is counted in the formula via
`in_flight_invoice_satoshis` since no settlement is complete until the admin resolves
the reorg. The associated payout record may be in `broadcast` or rolled back to
`queued` depending on sweep tx status — either way it is counted.

### Concurrent reconciliation prevention
The job uses a PostgreSQL advisory lock (`pg_try_advisory_lock`) to prevent two
instances from running reconciliation simultaneously. If the lock is unavailable,
the job logs INFO and exits — it does not error. The next scheduled run will proceed
normally.

### Reconciliation during an active sweep
A sweep being constructed while reconciliation is running will have its payout
records in `constructing` status — correctly included in the
`pre_confirmation_payout_obligations` term. The formula is correct regardless of
whether the sweep is in progress.
