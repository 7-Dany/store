# Invoice Feature — Behavior & Edge Cases

> **What this file is:** A plain-language description of vendor wallet configuration,
> invoice creation, address lifecycle, expiry rules, and every edge case the invoice
> subsystem handles. Read this to understand the feature contract before touching
> any implementation detail.
>
> **Companion:** `invoice-technical.md` — RPC sequences, schema, test inventory.
> **Depends on:** `internal/platform/bitcoin/zmq/subscriber.go` (ZMQ watch registration),
> `../vendor/vendor-feature.md` (tier config, wallet mode permissions).

---

## 1. Vendor Wallet Configuration

### What it is
Every vendor chooses how they receive Bitcoin earnings. This is their "wallet
configuration" — it controls what happens to their money after a sale settles.

Wallet configuration is not available to all users by default. The platform owner
configures tiers, optionally links roles to those tiers, and assigns permissions
to each tier. The set of wallet modes a vendor may configure is determined entirely
by their tier and the roles attached to it. A user who has not been granted a vendor
role cannot access wallet configuration at all.

### Role-gated onboarding
Wallet configuration is triggered by role assignment, not by account creation.
When a user is granted a vendor role (or any role linked to a tier with payment
permissions), the platform prompts them to complete their wallet configuration
before they can publish any listings. Until wallet configuration is complete, the
vendor's account is active but no products can go live.

The available wallet modes presented during this setup are limited to those permitted
by the vendor's tier. If a tier does not permit platform wallet mode, the vendor never
sees that option — it simply does not exist for them.

### Three modes

**Bridge mode** — the vendor provides an external Bitcoin address they own (on a
hardware wallet, exchange, or any wallet they control). The platform receives payment,
takes its fee, and automatically forwards the remainder to that address. The vendor's
money never sits on the platform for long.

**Platform wallet mode** — the vendor's earnings stay on the platform as an internal
balance. They withdraw whenever they want, use balance to pay their subscription fees,
or leave it sitting. The platform holds their Bitcoin indefinitely — this is a
custodial relationship. This mode is only available if the vendor's tier explicitly
permits it **and** the platform-wide `PLATFORM_WALLET_MODE_LEGAL_APPROVED` flag is
`true` (see `../vendor/vendor-feature.md §Regulatory`).

**Hybrid mode** — a vendor sets an auto-sweep threshold (satoshi amount) and an
external destination address. Earnings accumulate as an internal balance. Once the
balance accumulated since the last sweep crosses the threshold, all accumulated balance
is automatically swept to their external address. This mode is only available if the
vendor's tier permits both platform wallet access and bridge mode. The auto-sweep
threshold and destination address are snapshotted on every invoice at creation time
alongside all other tier config values.

### Rules
- **Wallet mode availability is determined by the vendor's tier.** The owner
  configures which modes each tier permits. A vendor can only select from the modes
  their tier allows. This is enforced by the payment system, not by UI alone.
- Vendors complete their wallet configuration after being granted a vendor role.
  They can change their mode at any time, subject to the modes their current tier
  permits. **If a vendor's tier is downgraded and their active mode is no longer
  permitted, their mode is frozen** — existing in-flight invoices complete normally,
  but new invoices are blocked until the vendor reconfigures to a permitted mode.
  To unfreeze, the vendor must explicitly select a new wallet mode that is permitted
  by their current tier. This requires a vendor-side configuration action; there is
  no automatic unfreeze.
- **The wallet mode AND destination address governing a settlement are always those
  active when the invoice was created.** Both are snapshotted on the invoice. A mode
  change or destination address change applies only to invoices created after the
  change is saved. In-flight invoices are never affected. This is non-negotiable for
  financial correctness — it prevents a vendor from redirecting in-flight sweep funds
  to a new address by changing their configuration mid-flight.
- Bridge mode vendors must configure a valid destination address **before** any of
  their products can be published. No address = no active listings = no invoices.
  This is enforced by the product listing system, not the payment system.
- **A bridge mode destination address must not be any address managed by this
  platform.** Before saving a destination address, the system performs a two-step check:
  1. **DB check:** the address must not appear in `invoice_addresses` or any other
     platform-managed address table.
  2. **RPC check:** `getaddressinfo(submitted_address)` is called. If `"ismine": true`,
     the address is owned by the platform wallet and is rejected with: "Cannot use a
     platform-managed address as a destination." This RPC check covers change addresses
     and any future wallet addresses not tracked in the DB.
  Both checks must pass. The DB check runs first (fast path); the RPC check is
  mandatory regardless of the DB result.
- **A buyer-provided refund address is also subject to the ismine check.** Before
  storing a buyer refund address, `getaddressinfo` is called. If `ismine: true`, the
  refund address is rejected. This prevents a buyer from directing a refund to the
  platform's own wallet. See `../prerequisites/contracts.md Contract 10`.
- If a bridge mode vendor removes their destination address, all their active product
  listings are immediately unpublished. Listings restore automatically when a new valid
  address is saved.
- There is no escrow or holding mechanism for bridge mode vendors without an address.
  The constraint is enforced upstream to prevent this situation entirely.
- The platform cannot verify that a vendor's destination address belongs to them.
  Validation is format-only and RPC-only (network-aware ismine check). The vendor
  explicitly confirms the address before it is saved.
- Platform wallet mode is a custodial service. Legal review of terms of service is
  required before this mode is enabled for production vendors.

---

## 2. Invoice System

### What it is
An invoice is the record that links a buyer's purchase intent to a specific Bitcoin
address and amount. Every invoice is unique and tied to one specific product purchase
from one specific vendor.

### Creation
When a buyer selects Bitcoin at checkout:

1. The platform verifies that the product is still active and the vendor has a valid
   wallet configuration (bridge-mode address or permitted platform wallet mode). Both
   checks occur atomically before address generation to prevent TOCTOU races with
   product unpublishing or address removal. If the product listing system is unavailable,
   invoice creation fails with 503.
2. The platform fetches the current BTC/fiat rate from its local cache.
3. The product's fiat price is converted to satoshis and locked on the invoice.
   **Rounding rule:** the fiat-to-satoshi conversion always uses **floor (truncation
   toward zero)**. Example: 0.00001234567 BTC → 1234 satoshis (not 1235). This
   direction is non-negotiable for financial correctness and is tested explicitly.
4. A unique P2WPKH address is generated via a **two-step RPC process**:
   - Step 4a: `getnewaddress "invoice" "bech32"` → returns the address string
   - Step 4b: `getaddressinfo(address)` → reads the `hdkeypath` field to extract
     the HD derivation index (e.g., the leaf index from `m/84'/0'/0'/0/5200`)
   Both calls must succeed (`rpc.Client.GetNewAddress` then `rpc.Client.GetAddressInfo`).
   If either fails, the invoice is not created, the buyer receives a 503
   "Bitcoin payments temporarily unavailable," and a `KeypoolOrRPCError` critical alert fires.
   The address string and HD derivation index are both stored on the invoice address
   record. Bitcoin Core tracks the address internally.
5. The new address is registered with the ZMQ subscriber's active watch set **after**
   the DB write succeeds — not before. See `invoice-technical.md §2` for the ordering
   requirement and its rationale.
6. The vendor's current tier config values and wallet configuration — fee rate,
   confirmation target, tolerance band, minimum amount, expiry window, wallet mode,
   **bridge/hybrid destination address**, auto-sweep threshold (hybrid mode only), and
   both overpayment thresholds — are snapshotted and stored on the invoice. This
   snapshot is immutable.
7. A 30-minute expiry timer starts (configurable per tier).
8. The buyer is shown the address and amount.

If the BTC/fiat rate feed is unavailable at invoice creation time, creation is
rejected immediately. The buyer sees that Bitcoin payments are temporarily unavailable
and is offered other payment methods.

Invoice creation is SUSPENDED when the node is offline (both `getnewaddress` and
`getaddressinfo` are RPC calls).

### Address type
All invoice addresses are **P2WPKH native segwit (bech32)** — addresses beginning
with `bc1q` on mainnet or `tb1q` on testnet4. This is the most fee-efficient standard
address type (~68 vbytes per input), is broadly supported by all modern wallets, and
is compatible with the ZMQ subscriber's all-lowercase address normalisation. The
address type is fixed platform-wide and is not owner-configurable in V1.
`getnewaddress` is always called as `getnewaddress "invoice" "bech32"`.

### Amount rules
- All amounts are stored as integer satoshis. No decimals anywhere.
- Fiat-to-satoshi conversion uses **floor (truncate toward zero)**.
- The fiat equivalent is recorded at three timestamps: creation, first payment
  detection, and settlement. All three records include the fiat currency code
  (`BTC_FIAT_CURRENCY` config, e.g. `USD`). The **settlement timestamp fiat
  equivalent is the authoritative value for tax and accounting purposes.**
- A minimum invoice amount is enforced per tier. Default: 10,000 satoshis.
  Free tier minimum may be higher (e.g. 50,000 satoshis) due to less efficient
  weekly batching. Invoices below the minimum are rejected at checkout.

### Invoice creation rate limiting
A buyer may not have more than 20 pending invoices simultaneously across all vendors.
After an invoice expires or is cancelled, a **60-second cooldown** applies before the
buyer can create another. This prevents address space exhaustion and monitoring resource
abuse. The rate limit is enforced at the API layer and is configurable by the
platform owner.

### Tier config snapshot
The invoice permanently records the financial rules and wallet configuration active
when it was created: fee rate, confirmation target, tolerance band, expiry window,
minimum amount, wallet mode, bridge/hybrid destination address, overpayment absolute
threshold, overpayment relative threshold, and (for hybrid mode) the auto-sweep
threshold. This snapshot is immutable.

---

## 3. Invoice Statuses

| Status | Meaning | Requires resolution? |
|--------|---------|---------------------|
| `pending` | Created, awaiting payment | No |
| `detected` | Payment seen in mempool, expiry frozen | No |
| `mempool_dropped` | Payment seen in mempool but disappeared before confirming | No — expires automatically |
| `confirming` | Block confirmed, awaiting required depth | No |
| `settling` | Settlement worker has claimed this invoice (transient) | No |
| `settled` | Settlement complete, fees recorded, payout queued | No |
| `settlement_failed` | Atomic settlement failed after max retries | Yes — admin |
| `reorg_admin_required` | Invoice was settled+swept; underlying block reorganized | Yes — admin |
| `expired` | Window elapsed, no payment seen | No |
| `expired_with_payment` | Expired, but late payment arrived | Yes — admin + support |
| `cancelled` | Cancelled before any payment | No |
| `cancelled_with_payment` | Cancelled, but payment arrived during monitoring window | Yes — admin + support |
| `underpaid` | Payment below minimum tolerance | Yes — buyer or admin |
| `overpaid` | Payment above maximum tolerance | Yes — admin |
| `refunded` | Refund issued and confirmed on-chain | No |
| `manually_closed` | Admin wrote off the invoice after investigation | No |

The full invoice state machine — all permitted transitions with triggers — is in
`../settlement/settlement-technical.md §Invoice State Machine`.

---

## 4. Cancellation Constraints
Cancellation is permitted when an invoice is in `pending` **or `mempool_dropped`**
status. The `mempool_dropped` case requires that there are no `invoice_payments`
records with a confirmed status (the drop means no confirmed payment exists).
Invoices in `detected`, `confirming`, `settled`, or any terminal status cannot be
cancelled. Once a payment is in-flight (detected in the mempool with no drop
confirmed), the funds cannot be reversed by the vendor.

---

## 5. Expiry Rules

- An invoice expires when its **effective expiry time** elapses with no payment detected.
- **Effective expiry accounts for node outages.** When the Bitcoin node goes offline,
  a `btc_outage_log` record is written with `started_at`. When the node reconnects,
  the record is closed with `ended_at`. The expiry cleanup job computes effective
  expiry precisely as:

  ```sql
  effective_expires_at = original_expires_at + COALESCE((
    SELECT SUM(
      LEAST(COALESCE(ended_at, NOW()), original_expires_at)
      - GREATEST(started_at, invoice.created_at)
    )
    FROM btc_outage_log
    WHERE started_at < original_expires_at
      AND COALESCE(ended_at, NOW()) > invoice.created_at
  ), INTERVAL '0')
  ```

  This formula: clips each outage to the overlap with the invoice's pending window
  only; excludes outage time before the invoice was created; handles still-open
  outages (no `ended_at`) correctly. No invoice expires during an outage window.

- **The expiry cleanup job evaluates both `pending` and `mempool_dropped` invoices**
  against their effective expiry time. `mempool_dropped` invoices have their expiry
  timers unfrozen after the drop and must be checked for expiry exactly like
  `pending` invoices.

- **Critical:** once a payment is detected in the mempool (`detected` status), the
  expiry timer is frozen permanently. The invoice cannot expire while a payment is
  in-flight.

---

## 6. Address Lifecycle and Monitoring Windows

Each address is used for exactly one invoice, then enters a monitoring window.
Monitoring state is tracked in a persistent `invoice_address_monitoring` table
(DB-backed, not in-memory) so it survives process restarts and is consistent across
horizontal replicas. This table is the authoritative list of addresses the ZMQ
subscriber must watch.

| Invoice terminal state | monitor_until |
|------------------------|---------------|
| `expired` | expired_at + 30 days |
| `cancelled` | cancelled_at + 30 days |
| `settled` | settled_at + 30 days |
| `refunded` | refunded_at + 30 days |
| `manually_closed` | closed_at + 30 days |
| `reorg_admin_required` | **Open-ended** — monitoring remains `active` until the invoice leaves this status. `monitor_until` is set only when the invoice transitions out of this status. |

Invoices in non-terminal statuses (`pending`, `detected`, `confirming`, `settling`,
`underpaid`, `overpaid`, `settlement_failed`) remain in `active` monitoring with
`monitor_until = NULL` until they reach a terminal state.

After the full monitoring window elapses with no further payment activity, the address
is retired: the monitoring record is marked `retired`, the ZMQ subscriber removes it
from its watch set, and Bitcoin Core's wallet retains its own record of the address
forever.

---

## 7. Edge Cases

### Late payment on expired invoice
If a payment arrives on an expired invoice address (within the 30-day monitoring
window), the invoice moves to `expired_with_payment`. Three parties are notified via
the platform's notification system (push + email + admin dashboard alert):
- Platform admin: CRITICAL alert
- Vendor: dashboard notification
- Buyer: email notification

Resolution options (all recorded as new audit trail rows):
- `expired_with_payment` → `refunded`: refund buyer on-chain
- `expired_with_payment` → `settled`: admin creates a **new** invoice with the correct
  amount for the buyer. Both parties must consent. The original `expired_with_payment`
  invoice transitions to `manually_closed` with an audit trail reference to the new
  invoice ID.
- `expired_with_payment` → `manually_closed`: admin writes off with mandatory reason

### Late payment on cancelled invoice
Identical flow, transitions to `cancelled_with_payment`. Same resolution options.

**`cancelled_with_payment → settled` resolution:** admin creates a **new** invoice for
the buyer with the correct amount and status. The original `cancelled_with_payment`
invoice transitions to `manually_closed` with a mandatory audit trail entry referencing
the new invoice ID. This preserves a clean audit trail — cancelled invoices never
appear as settled in financial records.

### Post-settlement payment (second payment on settled address)
If a second payment arrives on an already-settled address (within the 30-day
post-settlement monitoring window):
- A "Post-settlement payment detected" CRITICAL alert fires.
- The payment is recorded in `invoice_payments` with a `post_settlement` flag.
- **A financial audit event is written** (immutable, append-only) recording the txid,
  value_sat, and invoice reference.
- Admin review required.
- Resolution: `settled → refunded` (admin issues on-chain refund to buyer) or
  `settled → manually_closed` (with mandatory reason).

`settled → refunded` is a permitted transition specifically for the post-settlement
payment refund path. This transition requires step-up authentication.

### Payment after monitoring window
Treated as an unknown incoming transaction — CRITICAL event requiring full manual
investigation.

### Buyer refund address
The checkout flow requests a Bitcoin refund address from the buyer before showing them
the payment address. The field is optional but strongly encouraged, with clear
messaging: "Required if your payment needs to be returned."

The address is validated as:
1. Valid Bitcoin address (network-aware format check)
2. **Not a platform-managed address** (`getaddressinfo` ismine check — see
   `../prerequisites/contracts.md Contract 10`)

Refund addresses are PII and subject to the platform's data retention policy.

### Concurrent invoices
There is no limit on the number of concurrent active invoices per vendor. Multiple
buyers can purchase from the same vendor simultaneously — each gets a unique address.
The rate limit is per buyer, not per vendor.

### Multi-output payments to same address
A single transaction may send multiple outputs to the same invoice address. The
platform sums all `value_sat` entries in `invoice_payments` where
`invoice_id = X AND txid = Y` before comparing against the invoiced amount. This
applies to the underpayment and overpayment checks alike.

### Double-payment (second transaction to same address)
If the same invoice address receives a payment from a second, distinct txid,
the second payment is recorded in `invoice_payments` with a `double_payment` flag.
A "Double-payment detected" CRITICAL alert fires. Admin review required.

Note: the double_payment row represents real on-chain value received by the platform.
It is not automatically earmarked for refund — admin must determine the disposition.
The reconciliation formula includes these rows in the sum for in-flight invoices since
the platform holds those funds. See `../audit/audit-feature.md §3`.
