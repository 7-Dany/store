# KYC — Technical Implementation

> **What this file is:** Implementation contracts for the KYC state machine, provider
> webhook handling, payout gate integration, and the complete test inventory.
>
> **Read first:** `kyc-feature.md` — behavioral contract and edge cases.
> **Schema:** `sql/schema/010_btc_payouts.sql` (`kyc_submissions` table,
> `btc_kyc_status` ENUM on `payout_records`).

---

## Table of Contents

1. [KYC Submission State Machine](#1--kyc-submission-state-machine)
2. [Payout Gate Integration](#2--payout-gate-integration)
3. [Provider Webhook Handling](#3--provider-webhook-handling)
4. [Bulk Payout Status Update on Approval](#4--bulk-payout-status-update-on-approval)
5. [Test Inventory](#5--test-inventory)

---

## §1 — KYC Submission State Machine

```
submitted → under_review → approved
                        → rejected → (re-submitted as new row)
         → expired      (approval validity window elapsed)
```

| From | To | Trigger |
|------|----|---------|
| (created) | `submitted` | Platform initiates KYC flow; vendor notified |
| `submitted` | `under_review` | KYC provider webhook: review started |
| `under_review` | `approved` | KYC provider webhook: verification passed |
| `under_review` | `rejected` | KYC provider webhook: verification failed |
| `submitted` | `rejected` | Provider webhook: immediate rejection (e.g. document mismatch) |
| `approved` | `expired` | Background job: `approved_at + kyc_approval_validity_days < NOW()` |

`expired` is a terminal status for the submission row. A new submission is required.

---

## §2 — Payout Gate Integration

**At payout record creation (Phase 2 settlement):**
```go
kycStatus := db.BtcKycStatusNotRequired
threshold := invoice.VendorTierConfig.KycCheckRequiredAtThresholdSatoshis

if threshold != nil && payoutNetSatoshis >= *threshold {
    if !vendorHasApprovedKYC(ctx, vendorID) {
        kycStatus = db.BtcKycStatusRequired
        triggerKYCSubmission(ctx, vendorID) // create kyc_submissions row if not exists
    } else {
        kycStatus = db.BtcKycStatusApproved
    }
}

q.CreatePayoutRecord(ctx, db.CreatePayoutRecordParams{
    ...
    KycStatus: kycStatus,
})
```

**At held → queued promotion (fee floor re-evaluation job):**
```go
// Before promoting held records to queued:
if record.KycStatus == db.BtcKycStatusRequired ||
   record.KycStatus == db.BtcKycStatusPending {
    continue // skip this record; KYC not cleared
}
```

The promotion is blocked per-record, not per-vendor. A vendor with mixed KYC statuses
(some `approved`, some `required`) will have their `approved` records promoted while
`required` records remain held.

---

## §3 — Provider Webhook Handling

The KYC provider delivers webhooks to `POST /api/v1/admin/kyc/webhook`. This endpoint
is not vendor-facing — it is an admin-internal endpoint protected by the provider's
shared secret.

**Guard sequence:**
```
1. Verify X-KYC-Signature header using provider's shared secret
   → 401 on mismatch (timing-safe comparison required)
2. Parse event type and submission_id from payload
3. Fetch kyc_submissions row by provider_reference_id
   → 404 if not found
4. Transition submission status per event type
5. If 'approved': run bulk payout status update (§4)
6. If 'rejected': notify vendor + admin; check re-submission count
7. Audit: write financial_audit_event for the status change
8. Respond 200 immediately (provider retries on non-2xx)
```

**Provider webhook idempotency:** the provider may deliver the same webhook multiple
times (at-least-once delivery). The transition is guarded by
`WHERE status = $expected_status` (same optimistic locking pattern as invoice
transitions). A duplicate webhook that arrives after the status has already been
updated returns 0 RowsAffected — log INFO and respond 200.

---

## §4 — Bulk Payout Status Update on Approval

When a KYC submission transitions to `approved`, all payout records for that vendor
with `kyc_status IN ('required', 'pending')` must be bulk-updated:

```sql
UPDATE payout_records
SET kyc_status = 'approved',
    updated_at = NOW()
WHERE vendor_id  = @vendor_id
  AND kyc_status IN ('required', 'pending');
```

This runs in the same transaction as the `kyc_submissions` status update. After
commit, the held → queued promotion job picks up the newly approved records on its
next cycle.

---

## §5 — Test Inventory

### Legend
- `[UNIT]` — pure unit test, no DB or network
- `[INTG]` — integration test requiring real PostgreSQL

### TI-28: KYC

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-28-01 | `TestKYC_PayoutAboveThreshold_StatusRequired` | INTG | net_sat ≥ threshold; no approved KYC → kyc_status=required |
| TI-28-02 | `TestKYC_PayoutBelowThreshold_StatusNotRequired` | INTG | net_sat < threshold → kyc_status=not_required |
| TI-28-03 | `TestKYC_PayoutAboveThreshold_ApprovedKYC_StatusApproved` | INTG | Vendor has approved KYC → kyc_status=approved immediately |
| TI-28-04 | `TestKYC_NullThreshold_NeverTriggered` | INTG | kyc_check_required_at_threshold_satoshis IS NULL → never required |
| TI-28-05 | `TestKYC_HeldPromotion_Blocked_WhenRequired` | INTG | kyc_status=required → held record skipped at promotion |
| TI-28-06 | `TestKYC_HeldPromotion_Blocked_WhenPending` | INTG | kyc_status=pending → held record skipped at promotion |
| TI-28-07 | `TestKYC_HeldPromotion_Proceeds_WhenApproved` | INTG | kyc_status=approved → promotion proceeds normally |
| TI-28-08 | `TestKYC_ProviderWebhook_Approved_UpdatesSubmission` | INTG | Webhook approved → submission status=approved |
| TI-28-09 | `TestKYC_ProviderWebhook_Approved_BulkUpdatesPayoutRecords` | INTG | Approval → all required/pending payouts → approved |
| TI-28-10 | `TestKYC_ProviderWebhook_Rejected_NotifiesAdmin` | INTG | Rejected → vendor + admin notified; payouts still blocked |
| TI-28-11 | `TestKYC_ProviderWebhook_Idempotent_DuplicateDelivery` | INTG | Same webhook delivered twice → 0 RowsAffected on second; 200 OK |
| TI-28-12 | `TestKYC_ProviderWebhook_InvalidSignature_Returns401` | INTG | Signature mismatch → 401 |
| TI-28-13 | `TestKYC_Expiry_Background_Job_SetsExpired` | INTG | approved_at + validity < NOW() → status=expired |
| TI-28-14 | `TestKYC_Expiry_Warning_30Days` | INTG | 30 days before expiry → WARNING alert; vendor email |
| TI-28-15 | `TestKYC_Expiry_Critical_AtExpiry` | INTG | At expiry → CRITICAL alert |
| TI-28-16 | `TestKYC_Resubmission_CooldownEnforced` | INTG | Rejection + immediate re-submit → rejected; cooldown enforced |
| TI-28-17 | `TestKYC_Resubmission_3Rejections_AdminEscalation` | INTG | 3 rejections → admin CRITICAL alert; automatic re-submit blocked |
| TI-28-18 | `TestKYC_AdminOverride_MarksApprovedWithReason` | INTG | Admin override → kyc_status=approved; audit event; step-up required |
| TI-28-19 | `TestKYC_MixedStatuses_OnlyApprovedPromoted` | INTG | Vendor has required + approved records; only approved promoted |
| TI-28-20 | `TestKYC_BulkUpdate_SameTransactionAsSubmissionUpdate` | INTG | Bulk payout update and submission update in single DB tx |
