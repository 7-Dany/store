# Vendor Feature — Behavior & Edge Cases

> **What this file is:** A plain-language description of how tiers control vendor
> capabilities, how vendor accounts are suspended and deleted, the regulatory context
> for custodial features, and how step-up authentication protects critical admin
> actions. Read this to understand the feature contract before looking at any
> implementation detail.
>
> **Companion:** `vendor-technical.md` — tier config validation ranges, ismine
> check contract, TOTP step-up mechanism, KYC placeholder schema, config validation,
> test inventory.
> **Used by:** `../invoice/invoice-feature.md` (wallet mode permissions),
> `../settlement/settlement-feature.md` (fee rates, sweep schedules),
> `../sweep/sweep-feature.md` (fee caps, suspension check).

---

## 1. What Tiers Are

Tiers are named, owner-configurable presets controlling vendor access and payment
processing. The owner creates, renames, adjusts, and deactivates them.

---

## 2. What a Tier Controls

**Transaction rules:**

| Field | Min | Max | Default | Notes |
|-------|-----|-----|---------|-------|
| Processing fee rate | 0% | 50% | tier-specific | >50% requires explicit owner acknowledgment |
| Confirmation depth | 1 | 144 | tier-specific | 1=Enterprise; 144=~24h max |
| Miner fee cap | 1 sat/vbyte | 10,000 sat/vbyte | tier-specific | 0 not permitted |
| Sweep schedule | weekly/daily/realtime | — | Free=weekly | |
| Withdrawal approval threshold | 0 sat | 1,000,000,000 sat | tier-specific | 0 = all require approval |
| Invoice expiry window | 5 min | 1440 min | 30 min | |
| Payment tolerance band | 0% | 10% | 1% | |
| Minimum invoice amount | 1,000 sat | (none) | 10,000 sat | |
| Overpayment relative threshold | 1% | 100% | 10% | |
| Overpayment absolute threshold | 1,000 sat | (none) | 10,000 sat | |
| Expected batch size | 1 | 100 | 50 (Free), 20 (others) | Used in batch-amortized fee floor |

All ranges enforced at admin API layer and as DB CHECK constraints.

**Feature flags:** platform wallet mode access, webhooks level, API key access,
analytics dashboard access, multi-address payout support.

---

## 3. Deactivated Tiers

When a tier enters `deactivating`:

1. No new invoices can be created for vendors on that tier.
2. A pre-deactivation sweep is attempted for all `queued` payout records for vendors
   on that tier, using the tier's current fee cap.
3. **If the fee cap is exceeded and the sweep cannot run:** the admin is shown two
   escape hatches:
   - **Raise cap for this one-time deactivation sweep:** admin temporarily raises the
     fee cap, the sweep runs, and the original cap is restored.
   - **Proceed without pre-sweep:** admin acknowledges that remaining `queued` records
     will be swept by the destination tier's cap after vendor migration. A financial
     audit event is written recording the override.
   The deactivation is NOT blocked indefinitely by fee conditions.
4. Once all vendors are reassigned and the chosen resolution is applied, the tier
   transitions to `inactive`.

---

## 4. Config Snapshot at Invoice Creation

Every invoice permanently stores a snapshot of all tier config values and wallet mode,
including the `auto_sweep_threshold` for hybrid-mode vendors and both overpayment
thresholds. This snapshot is immutable regardless of subsequent tier or mode changes.

---

## 5. Vendor Account Lifecycle

### Suspension
- New invoices cannot be created; in-flight invoices complete normally.
- Payout records accumulate in `queued` but sweeps are held.
- Any sweep in `constructing` is checked at the broadcast boundary; if suspended,
  broadcast is aborted and records return to `queued`.

The admin sees three resolution options: release payout, freeze indefinitely, or
refund buyers on-chain.

### Deletion
Account deletion is blocked if any of the following are outstanding: pending invoices,
queued payout records, unresolved `settlement_failed` or `reorg_admin_required`
records, or a non-zero internal balance. Accounts are never hard-deleted. All records
are retained indefinitely.

---

## 6. Regulatory Context

### Platform wallet mode is custodial
Legal review is required before this mode is enabled for production vendors.

**`PLATFORM_WALLET_MODE_LEGAL_APPROVED` flag:** platform wallet mode is gated behind
an environment variable flag (default: `false`). When `false`, any attempt to enable
platform wallet mode on any tier is rejected at the admin API. This flag is set via a
deliberate ops action in the production deployment configuration — it is not available
as an admin UI toggle. Setting it to `true` must be preceded by a written legal
approval record in the admin financial audit trail.

### All users are buyers by default
No buyer role. All authenticated users can place orders. The vendor role adds seller
capabilities on top of this.

### KYC/AML placeholder
The payout/withdrawal record includes a `kyc_status` enum column (default:
`not_required`), and the tier config includes a
`kyc_check_required_at_threshold_satoshis` field (default: `NULL` = no check
required). Future KYC implementation sets non-null thresholds and implements the
`kyc_status` state machine without schema changes.

---

## 7. Step-Up Authentication

Step-up authentication is required for all critical admin actions: settlement retry,
`settlement_failed → manually_closed`, reorg admin resolution, failed payout re-queue
or manual payment, `held → manual_payout`, `held → failed`.

**Mechanism:** TOTP re-verification. An admin account must have TOTP enabled as a
prerequisite for performing any step-up-gated action — TOTP setup is enforced at role
assignment. The TOTP code is submitted alongside the admin action request in a single
atomic API call. If TOTP is not set up, the action is blocked with a prompt to complete
TOTP enrollment first.

**Session:** a step-up authentication is valid for 15 minutes. Multiple step-up-gated
actions can be performed within the 15-minute window without re-prompting. After the
window expires, the next action re-prompts for TOTP.

**Audit record:** each step-up authentication event is written to the financial audit
trail with: admin_id, auth_method (`totp`), timestamp, and the action initiated. This
record is immutable and separate from the authentication audit log.
