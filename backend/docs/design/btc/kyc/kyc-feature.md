# KYC Feature — Behavior & Edge Cases

> **What this file is:** A plain-language description of how the platform handles
> Know Your Customer (KYC) checks for high-value payouts, the KYC state machine, and
> how KYC status gates the payout lifecycle. The KYC provider integration is treated
> as a pluggable external service.
>
> **Companion:** `kyc-technical.md` — KYC state machine, webhook handling, payout
> gate integration, retry policy, test inventory.
> **Schema:** `sql/schema/010_btc_payouts.sql` (`kyc_submissions`,
> `payout_records.kyc_status`).

> ⚑ **Feature flag:** `platform_config.kyc_enabled` (default `FALSE`).
> This entire feature is dormant until that flag is flipped to `TRUE` by the owner.
> When `FALSE`, every payout record is created with `kyc_status = 'not_required'`
> and the KYC submission flow never triggers, regardless of tier thresholds.
> Flip it when the KYC provider integration is production-ready.

---

## What KYC Does

KYC is the process of verifying that the platform knows who a vendor is before
sending them significant amounts of money. It is triggered by the payout system
when a vendor's accumulated payout amount crosses a configured threshold.

**KYC is a gate on payouts, not on invoice creation or settlement.** A vendor can
receive payments from buyers and accumulate balance without passing KYC. KYC is
only required at the point of withdrawal or sweep.

---

## When KYC Is Required

KYC is triggered when:
1. A `payout_record` is created with `net_satoshis × btc_rate ≥ kyc_threshold`
   (threshold from the vendor's tier config: `kyc_check_required_at_threshold_satoshis`)
2. The vendor has not yet passed KYC (no `kyc_submissions` row with
   `status = 'approved'` linked to this vendor)

When KYC is required, the payout record is created with `kyc_status = 'required'`
rather than the normal `kyc_status = 'not_required'`. A `held` payout record with
`kyc_status = 'required'` will NOT be promoted to `queued` until KYC passes.

If `kyc_check_required_at_threshold_satoshis IS NULL` on the tier (the default), KYC
is never triggered for vendors on that tier.

---

## KYC Statuses on Payout Records

| Status | Meaning |
|--------|---------|
| `not_required` | No KYC check needed for this payout |
| `required` | KYC check required; payout blocked until passed |
| `pending` | KYC submission exists; awaiting provider decision |
| `approved` | KYC passed; payout may proceed |
| `rejected` | KYC failed; admin action required |

These are the `btc_kyc_status` ENUM values on `payout_records.kyc_status`.

---

## KYC Submission Lifecycle

When KYC is triggered for a vendor:

1. A `kyc_submissions` row is created with `status = 'submitted'`.
2. The vendor is notified that identity verification is required before their
   withdrawal can proceed. They are given a link to the KYC provider's verification
   flow.
3. The KYC provider sends a webhook when the verification is complete
   (`approved` or `rejected`).
4. On `approved`:
   - `kyc_submissions.status` → `approved`
   - All `payout_records` for this vendor with `kyc_status = 'required'` or
     `kyc_status = 'pending'` are updated to `kyc_status = 'approved'`
   - The payout lifecycle proceeds normally (held → queued → ...)
5. On `rejected`:
   - `kyc_submissions.status` → `rejected`
   - All pending payout records remain blocked
   - Admin is notified; vendor is notified with the rejection reason
   - Admin can override or request re-submission

---

## KYC Expiry

An approved KYC submission is valid for `kyc_approval_validity_days` (default: 365
days, configurable per tier). After expiry:
- New payouts above the threshold are created with `kyc_status = 'required'`
- Existing `approved` payouts are not retroactively blocked
- A WARNING alert fires 30 days before expiry and a CRITICAL alert fires at expiry
- The vendor is prompted to re-complete verification

KYC expiry does not block in-flight payouts that are already in `broadcast` or
`confirmed` status.

---

## Re-submission

After a rejection, the vendor may re-submit. A new `kyc_submissions` row is created
(the old row is retained as history). The `kyc_resubmission_cooldown_hours` config
(default: 24) prevents rapid re-submission abuse.

After 3 rejected submissions, the vendor's account is escalated to CRITICAL admin
review. Automatic re-submission is blocked until an admin manually unlocks it.

---

## Vendor Notification

- **On KYC required:** vendor receives an email and an in-app notification with a
  link to start verification. The notification includes the amount threshold that
  triggered the check.
- **On approval:** vendor receives an email confirmation; payout proceeds.
- **On rejection:** vendor receives the provider's rejection reason (if available);
  admin is notified.
- **On expiry approaching (30 days):** vendor receives a WARNING email.

---

## Admin Overrides

Admins can:
- **Override KYC requirement:** mark a payout record as `kyc_status = 'approved'`
  without a corresponding `kyc_submissions` row. Requires step-up authentication
  and a mandatory written reason in the audit trail. Use for exceptional circumstances
  only (e.g., existing verified customer migrating from a legacy system).
- **Force re-submission:** reset a `rejected` submission to allow the vendor to
  re-submit immediately, bypassing the cooldown.
- **Escalate:** mark a vendor as requiring manual review; blocks all future payouts
  until resolved regardless of KYC status.

All admin actions are recorded in `financial_audit_events`.

---

## What KYC Does NOT Determine

- Invoice creation — buyers can pay invoices regardless of vendor KYC status
- Settlement — invoices are settled to internal balances regardless of KYC
- Platform fee collection — fees are collected at settlement time regardless of KYC

KYC is purely a payout gate. A vendor could have a non-zero internal balance and
receive new payments indefinitely while KYC is pending — the funds just cannot be
swept until KYC passes.
