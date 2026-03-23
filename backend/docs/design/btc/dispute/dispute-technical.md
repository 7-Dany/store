# Dispute — Technical Implementation

> **What this file is:** Implementation contracts for the dispute state machine,
> payout freeze/unfreeze mechanics, resolution paths, guard sequences, and the
> complete test inventory.
>
> **Read first:** `dispute-feature.md` — behavioral contract and edge cases.
> **Schema:** `sql/schema/010_btc_payouts.sql` (`dispute_records` table).
> **Depends on:** `../settlement/settlement-technical.md` (payout state machine),
> `../audit/audit-technical.md` (financial audit trail).

---

## Table of Contents

1. [Dispute State Machine](#1--dispute-state-machine)
2. [Payout Freeze and Unfreeze](#2--payout-freeze-and-unfreeze)
3. [Handler Guard Ordering — Open Dispute](#3--handler-guard-ordering--open-dispute)
4. [Handler Guard Ordering — Resolve Dispute](#4--handler-guard-ordering--resolve-dispute)
5. [Auto-Resolution: No Vendor Response](#5--auto-resolution-no-vendor-response)
6. [Test Inventory](#6--test-inventory)

---

## §1 — Dispute State Machine

### Permitted transitions

| From | To | Trigger |
|------|----|---------|
| (created) | `open` | Buyer, vendor, or admin opens dispute |
| `open` | `awaiting_vendor` | Admin requests vendor evidence |
| `open` | `awaiting_buyer` | Admin requests buyer clarification |
| `awaiting_vendor` | `open` | Vendor submits response |
| `awaiting_vendor` | `resolved_buyer` | Vendor fails to respond within SLA |
| `awaiting_buyer` | `open` | Buyer submits clarification |
| `awaiting_vendor` | `resolved_vendor` | Evidence supports vendor |
| `awaiting_vendor` | `resolved_buyer` | Evidence supports buyer |
| `open` | `resolved_vendor` | Admin resolves in vendor's favour |
| `open` | `resolved_buyer` | Admin resolves in buyer's favour |
| `open` | `withdrawn` | Buyer withdraws dispute |
| `open` | `escalated` | Admin escalates for legal review |
| `awaiting_vendor` | `escalated` | Admin escalates during vendor review |
| `awaiting_buyer` | `escalated` | Admin escalates during buyer review |
| `escalated` | `resolved_vendor` | Legal/external review concludes for vendor |
| `escalated` | `resolved_buyer` | Legal/external review concludes for buyer |
| `escalated` | `resolved_platform` | Platform absorbs loss after external review |
| `resolved_buyer` | `resolved_platform` | Refund infeasible (sweep already confirmed); platform absorbs |

All `resolved_*` and `withdrawn` are terminal statuses.
All terminal transitions require step-up authentication (TOTP).
All transitions are recorded in `financial_audit_events`.

---

## §2 — Payout Freeze and Unfreeze

**On dispute open (`open`):**
```sql
-- Freeze: move queued payout records to held with dispute_hold reason
UPDATE payout_records
SET status = 'held',
    hold_reason = 'dispute_hold',
    dispute_id = @dispute_id,
    updated_at = NOW()
WHERE invoice_id = @invoice_id
  AND vendor_id  = @vendor_id
  AND status     = 'queued';
-- Check RowsAffected to confirm freeze was applied
```

If `status = 'constructing'` at freeze time: the sweep job will check for
`dispute_id IS NOT NULL` at the broadcast boundary and abort, returning to `held`.
This is the same suspension check pattern as vendor suspension.

If `status IN ('broadcast', 'confirmed')`: the payout cannot be frozen. The dispute
is noted but payout proceeds. Resolution options are limited (see
`dispute-feature.md §Resolution Paths`).

**On terminal resolution (`resolved_vendor` or `withdrawn`):**
```sql
-- Unfreeze: promote held → queued for dispute-held records
UPDATE payout_records
SET status = 'queued',
    hold_reason = NULL,
    updated_at = NOW()
WHERE dispute_id = @dispute_id
  AND status     = 'held'
  AND hold_reason = 'dispute_hold';
```

**On `resolved_buyer`:**
- If payout records are `held`: cancel them. Initiate a refund to the buyer's
  refund address (create a new payout record type `refund`). If no refund address
  exists, a CRITICAL alert fires and admin must arrange manually.
- If payout records are already `broadcast` or `confirmed`: transition dispute to
  `resolved_platform`. Record the platform loss in `financial_audit_events`.

---

## §3 — Handler Guard Ordering — Open Dispute

`POST /api/v1/bitcoin/disputes`

```
1. Auth           token.UserIDFromContext → 401
2. Rate limit     disputeLimiter.Limit middleware (5 req/min per user)
3. Decode + validate
   - invoice_id required; dispute_reason required (min 20 chars); dispute_type required
4. Fetch invoice  GetInvoice(invoice_id) → 404 if not found
5. Ownership      req.actor must be buyer_id or vendor_id on the invoice
                  → 403 if neither
6. Status check   invoice.status must NOT be 'pending' or 'cancelled'
                  → 400 invoice_not_disputable
7. Window check   invoice.detected_at + BTC_DISPUTE_WINDOW_DAYS < NOW()
                  → 400 dispute_window_elapsed
8. Duplicate check SELECT EXISTS(SELECT 1 FROM dispute_records
                     WHERE invoice_id = $id AND status NOT IN ('withdrawn', 'resolved_vendor',
                     'resolved_buyer', 'resolved_platform'))
                  → 409 dispute_already_open
9. Write          BEGIN TX
                    INSERT dispute_records (status='open', ...)
                    UPDATE queued payout records → held with dispute_hold
                    INSERT financial_audit_events (dispute_opened)
                  COMMIT
10. Notify        vendor + admin notified
11. Response      201 Created
```

---

## §4 — Handler Guard Ordering — Resolve Dispute

`POST /api/v1/bitcoin/disputes/{dispute_id}/resolve`
Admin only; requires step-up authentication.

```
1. Auth           token.UserIDFromContext → 401; admin role check → 403
2. Step-up auth   TOTP verification → 403 if invalid
3. Rate limit
4. Fetch dispute  → 404 if not found; 409 if already terminal
5. Validate       resolution (resolved_vendor|resolved_buyer|resolved_platform)
                  resolution_reason required (min 50 chars for terminal resolutions)
6. Write          BEGIN TX
                    UPDATE dispute_records SET status = @resolution
                    If resolved_buyer:
                      Cancel held payout records → create refund payout record
                      OR if already broadcast/confirmed → transition to resolved_platform
                    If resolved_vendor or withdrawn:
                      Unfreeze held payout records → queued
                    INSERT financial_audit_events (dispute_resolved, resolution_reason)
                  COMMIT
7. Notify         both parties notified of resolution
8. Response       200 OK
```

---

## §5 — Auto-Resolution: No Vendor Response

A background job runs daily checking `dispute_records WHERE status = 'awaiting_vendor'
AND vendor_deadline < NOW()`. For each:
1. Transition `awaiting_vendor → resolved_buyer` automatically
2. Attempt refund (same logic as manual `resolved_buyer`)
3. Write `financial_audit_events` with `actor_type = 'system'` and reason
   `'Vendor failed to respond within 7-day SLA'`
4. Notify buyer (auto-resolved in their favour) and vendor

---

## §6 — Test Inventory

### Legend
- `[UNIT]` — pure unit test, no DB or network
- `[INTG]` — integration test requiring real PostgreSQL

### TI-29: Dispute Lifecycle

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-29-01 | `TestDispute_Open_QueuedPayouts_FrozenToHeld` | INTG | Open dispute; queued → held with dispute_hold |
| TI-29-02 | `TestDispute_Open_BroadcastPayout_NotFrozen` | INTG | Broadcast payout stays broadcast; dispute noted |
| TI-29-03 | `TestDispute_Open_DuplicateDispute_Returns409` | INTG | Second open dispute on same invoice → 409 |
| TI-29-04 | `TestDispute_Open_PendingInvoice_Returns400` | INTG | Invoice pending → not disputable |
| TI-29-05 | `TestDispute_Open_WindowElapsed_Returns400` | INTG | detected_at + 60 days < NOW() → 400 |
| TI-29-06 | `TestDispute_ResolveVendor_UnfreezesPayouts` | INTG | resolved_vendor → held → queued; dispute_id cleared |
| TI-29-07 | `TestDispute_ResolveVendor_RequiresStepUpAuth` | INTG | No TOTP → 403 |
| TI-29-08 | `TestDispute_ResolveBuyer_HeldPayout_RefundCreated` | INTG | resolved_buyer + held payout → refund record created |
| TI-29-09 | `TestDispute_ResolveBuyer_BroadcastPayout_TransitionsToPlatform` | INTG | resolved_buyer + broadcast → resolved_platform |
| TI-29-10 | `TestDispute_ResolveBuyer_NoRefundAddress_CriticalAlert` | INTG | No refund address → CRITICAL; admin must arrange |
| TI-29-11 | `TestDispute_Withdraw_UnfreezesPayouts` | INTG | Buyer withdraws → held → queued |
| TI-29-12 | `TestDispute_AutoResolve_VendorNoResponse_7Days` | INTG | awaiting_vendor + vendor_deadline elapsed → auto resolved_buyer |
| TI-29-13 | `TestDispute_AutoResolve_AuditEventActorSystem` | INTG | Auto-resolution writes actor_type='system' |
| TI-29-14 | `TestDispute_SweepBroadcast_DisputeHold_Aborted` | INTG | Payout in constructing; dispute opens; broadcast → aborted → held |
| TI-29-15 | `TestDispute_AllTransitions_Permitted_Succeed` | INTG | Every transition in §1 table can execute |
| TI-29-16 | `TestDispute_TerminalStatuses_NoFurtherTransitions` | INTG | withdrawn/resolved_* → no further automated transitions |
| TI-29-17 | `TestDispute_OwnershipCheck_BuyerCanOpen` | INTG | Buyer on invoice → 201 Created |
| TI-29-18 | `TestDispute_OwnershipCheck_VendorCanOpen` | INTG | Vendor on invoice → 201 Created |
| TI-29-19 | `TestDispute_OwnershipCheck_UnrelatedUser_Returns403` | INTG | User neither buyer nor vendor → 403 |
| TI-29-20 | `TestDispute_FinancialAuditEvent_WrittenPerTransition` | INTG | Every terminal transition writes a financial_audit_events row |
