# Sweep Feature — Behavior & Edge Cases

> **What this file is:** A plain-language description of the fee system, payout
> accumulation, the two sweep models, RBF handling, and every edge case the sweep
> subsystem handles. Read this to understand the feature contract before looking at
> any implementation detail.
>
> **Companion:** `sweep-technical.md` — SweepService interface, PSBT broadcast
> sequence, fee estimation math, batch integrity, stuck sweep detection, UTXO
> consolidation guards, test inventory.
> **Depends on:** `../settlement/settlement-feature.md` (payout record creation),
> `../vendor/vendor-feature.md` (tier sweep schedule, fee caps).

---

## 1. Two Fees, Always Tracked Separately

**Platform processing fee** — the percentage the platform deducts from vendor earnings.
Calculated at settlement time on the received satoshi amount (for within-tolerance
payments). Recorded as a permanent financial event.

**Bitcoin network miner fee** — paid to miners at sweep time from the platform's
treasury. Tracked separately from the processing fee for tier profitability analysis.

---

## 2. Fee Estimation

Before constructing any sweep transaction, the platform estimates the miner fee based
on the tier's snapshotted confirmation target and current mempool conditions. A 10%
safety buffer is applied. The buffered rate is compared against the tier's live miner
fee cap.

---

## 3. Miner Fee Caps (Defaults)

| Tier | Default miner fee cap |
|------|----------------------|
| Free | 50 sat/vbyte |
| Growth | 100 sat/vbyte |
| Pro | 200 sat/vbyte |
| Enterprise | 500 sat/vbyte |

Allowed range: 1–10,000 sat/vbyte. A cap of 0 is not permitted.

---

## 4. Economic Validation and Fee Floor

Before creating a payout record at settlement time, the settlement engine checks that
the vendor's net amount exceeds the estimated miner fee floor.

**For batch sweep vendors (Free, Growth, Pro-batch):** the floor uses the
batch-amortized fee estimate. See `sweep-technical.md §Fee Estimation` for the formula.

**For Enterprise (realtime):** the single-output fee estimate is used.

If net amount is below the floor → payout record is `held`.
If net amount is zero → settlement is rejected (`settlement_failed`; CRITICAL alert).

A second economic validation runs immediately before signing at sweep construction time.

**Enterprise held-due-to-fee-spike alert:** if an Enterprise settlement produces
a `held` payout record, a WARNING alert fires to admin and vendor.

---

## 5. Two Sweep Models

**Realtime sweep** — each settled invoice triggers its own transaction.
**Batch sweep** — settled invoices accumulate; single transaction with multiple outputs.

### Sweep schedule by tier

| Tier | Sweep model |
|------|-------------|
| Free | Weekly batch |
| Growth | Daily batch |
| Pro | Daily batch or realtime (owner-configured) |
| Enterprise | Realtime |

**Maximum batch output count:** capped at **100 outputs** per transaction. If more
records are queued, they are split into sequential batches of ≤ 100.

**Sweep confirmation depth:** all outgoing sweep transactions require **3 confirmations**
before payout records are marked `confirmed`. Fixed platform-level constant,
independent of the incoming invoice confirmation tier.

---

## 6. Payout Record Statuses

| Status | Meaning |
|--------|---------|
| `held` | Net amount below the miner fee floor; accumulating with future settlements |
| `queued` | Accumulated total cleared the floor; waiting for the next sweep window |
| `constructing` | Batch is being built; this record is assigned to a specific batch |
| `broadcast` | The sweep transaction has been sent to the network; awaiting 3 confirmations |
| `confirmed` | The sweep output is confirmed on-chain at 3-block depth |
| `failed` | Sweep failed permanently after all retries; requires admin action |
| `refunded` | Payout was reversed; funds returned to the buyer |
| `manual_payout` | Admin marked as manually paid outside the sweep system; terminal |

---

## 7. Payout Record Resolution Paths

### `constructing` stale record watchdog
Payout records in `constructing` status older than 10 minutes are returned to `queued`
and a "Stale constructing payout record" WARNING alert fires.

### `held` payout record resolution (all require step-up authentication)
`held` records are automatically promoted to `queued` when the accumulated total
clears the fee floor (via background re-evaluation job). Admin override paths:
- `held → manual_payout`: admin records out-of-band payment; mandatory txid and
  written reason in audit trail; terminal state. Fires a CRITICAL audit event.
- `held → failed`: admin declares the record permanently unresolvable (e.g., vendor
  unresponsive, account deleted). Fires CRITICAL alert; vendor notified.
  Further resolution: `failed → refunded` or `failed → manual_payout`.

**Monitoring:** if any `held` payout record is older than 7 days, a WARNING alert
fires. If older than 30 days, a CRITICAL alert fires.

### `failed` payout record resolution (all require step-up authentication)
- `failed → queued`: re-queue with fresh fee estimate
- `failed → manual_payout`: admin records manual txid in audit trail (terminal)
- `failed → refunded`: funds returned to buyer on-chain

---

## 8. Suspension Check at Sweep Broadcast

Before broadcasting, `SweepService` checks vendor suspension status. If suspended,
broadcast is aborted, records return to `queued`, admin is notified.

---

## 9. Stuck Sweeps and RBF

A sweep transaction is considered stuck when unconfirmed past **2× its target
confirmation window**.

- **Case 1 — current estimate > original fee used:** RBF triggered automatically.
  Replacement uses the current estimate. `sendrawtransaction` called with
  `maxfeerate = tier_cap` to prevent fee estimation bugs from overpaying. Admin alerted.
  Both txids recorded on payout records.
- **Case 2 — current estimate ≤ original fee:** wait one additional window. Then
  escalate to manual admin alert.
- **Case 3 — stuck longer than 48 hours:** RBF triggered automatically regardless of
  fee comparison. Long-stuck escalation alert fires to platform owner.

If the original transaction confirms before the replacement is broadcast, the original
is treated as canonical. The pending RBF operation is abandoned.

---

## 10. Platform Wallet Withdrawals

Address is validated (network-aware + RPC `getaddressinfo` ismine check to reject
platform-managed addresses).

**Processing rules:** below approval threshold → automatic; above → approval workflow.

**Minimum withdrawal:** minimum invoice amount + estimated single-output miner fee.

---

## 11. UTXO Consolidation

A background job can consolidate platform wallet UTXOs (many small UTXOs → fewer
larger ones) to reduce future miner fees. The job is disabled by default and only
activates once the owner has configured a fee threshold. It is governed by a set of
guards that must all pass before it runs — see `sweep-technical.md §UTXO Consolidation
Guards`.
