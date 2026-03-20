# Vendor — Technical Implementation

> **What this file is:** Implementation contracts for tier config DB validation,
> the mandatory ismine check sequence, TOTP step-up mechanism, config validation
> rules, and the complete test inventory for this package.
>
> **Read first:** `vendor-feature.md` — behavioral contract and edge cases.

---

## Table of Contents

1. [Tier Config DB CHECK Constraints](#1--tier-config-db-check-constraints)
2. [Bridge Destination Address Validation — ismine Contract](#2--bridge-destination-address-validation--ismine-contract)
3. [TOTP Step-Up Mechanism](#3--totp-step-up-mechanism)
4. [Config Validation Rules](#4--config-validation-rules)
5. [Test Inventory](#5--test-inventory)

---

## §1 — Tier Config DB CHECK Constraints

All validation ranges from `vendor-feature.md §2` must be enforced at both the
admin API layer AND as DB CHECK constraints. DB-level enforcement ensures that
migrations, direct DB operations, or future code paths cannot silently violate
the invariants.

Key constraints:
```sql
CONSTRAINT chk_fee_rate       CHECK (processing_fee_rate >= 0 AND processing_fee_rate <= 50)
CONSTRAINT chk_confirm_depth  CHECK (confirmation_depth >= 1 AND confirmation_depth <= 144)
CONSTRAINT chk_miner_fee_cap  CHECK (miner_fee_cap_sat_vbyte >= 1 AND miner_fee_cap_sat_vbyte <= 10000)
CONSTRAINT chk_expiry_window  CHECK (invoice_expiry_minutes >= 5 AND invoice_expiry_minutes <= 1440)
CONSTRAINT chk_tolerance      CHECK (payment_tolerance_pct >= 0 AND payment_tolerance_pct <= 10)
CONSTRAINT chk_min_invoice    CHECK (minimum_invoice_sat >= 1000)
CONSTRAINT chk_ovpay_rel      CHECK (overpayment_relative_threshold_pct >= 1 AND overpayment_relative_threshold_pct <= 100)
CONSTRAINT chk_ovpay_abs      CHECK (overpayment_absolute_threshold_sat >= 1000)
CONSTRAINT chk_batch_size     CHECK (expected_batch_size >= 1 AND expected_batch_size <= 100)
```

`withdrawal_approval_threshold_sat` range: 0 to 1,000,000,000 sat.

---

## §2 — Bridge Destination Address Validation — ismine Contract

When a vendor submits a bridge destination address (or any withdrawal destination),
the system performs a **mandatory two-step check**. Both steps must pass. Neither
step alone is sufficient.

```
Step 1: DB check
  SELECT COUNT(*) FROM invoice_addresses WHERE address = $submitted_address
  + SELECT COUNT(*) FROM any other platform-managed address table
  If found: reject with "Cannot use a platform-managed address as a destination."

Step 2: RPC check (mandatory regardless of step 1 result)
  result = getaddressinfo($submitted_address)
  If result.ismine == true:
    reject with "Cannot use a platform-managed address as a destination."
```

**Why both are required:**
- The DB check is fast and catches the common case.
- The RPC check covers change addresses (wallet-managed, not in any DB table) and any
  future wallet addresses that haven't been issued as invoices yet.
- An attacker who observes a change output from a platform sweep transaction and
  submits it as their destination address would pass the DB check but fail the RPC
  check.

**Ordering:** DB check runs first (fast path). RPC check always runs second,
regardless of the DB result.

---

## §3 — TOTP Step-Up Mechanism

### Prerequisites
- Admin account must have TOTP configured before any step-up-gated action is
  permitted.
- TOTP setup is enforced at admin role assignment — an admin without TOTP cannot
  perform step-up-gated actions until enrollment is complete.

### Session flow
```
1. Admin submits action request including TOTP code in a single atomic API call.
2. Server verifies TOTP code against the admin's stored secret.
   - Invalid code: reject action with 403.
   - Valid code:
     a. Record step-up authentication event to financial audit trail:
        {admin_id, auth_method: "totp", timestamp, action_initiated}.
     b. Set step_up_valid_until = NOW() + 15 minutes on the admin's session.
     c. Proceed with the action.
3. For subsequent step-up-gated actions within 15 minutes:
   - Check step_up_valid_until > NOW().
   - If valid: proceed without re-prompting.
   - If expired: require TOTP re-verification (go to step 1).
```

### Audit record fields
Every step-up authentication event in the financial audit trail must include:
- `admin_id`: the authenticated admin's ID
- `auth_method`: always `"totp"` for step-up actions
- `timestamp`: exact UTC timestamp of the TOTP verification
- `action_initiated`: the specific action being authorized
- Immutable: this record is never updated or deleted

---

## §4 — Config Validation Rules

All validations run in `config.validate()` at startup. The application refuses to
start if any required config is absent or invalid.

| Rule | Condition | Error |
|------|-----------|-------|
| `BTC_FIAT_CURRENCY` required | Absent | Startup failure |
| `BTC_RECONCILIATION_START_HEIGHT` | = 0 on mainnet without genesis flag | Startup failure |
| `BTC_RPC_PORT` | Non-numeric value | Startup failure |
| Handler timeout cross-field | `handler_timeout_ms ≤ 2 × rpc_timeout_ms × 1000 + 2000` | Startup failure |
| Secrets distinct | `SESSION_SECRET == SSE_SIGNING_SECRET OR SESSION_SECRET == AUDIT_HMAC_SECRET` | Startup failure |
| CORS origins | Trailing slash or wildcard `*` | Startup failure |
| Overpayment thresholds | Outside valid ranges | Startup failure |

---

## §5 — Test Inventory

### Legend
- `[UNIT]` — pure unit test, no DB or network
- `[INTG]` — integration test requiring real PostgreSQL
- `[MANUAL]` — operational drill

### TI-18: Vendor Account Lifecycle and Wallet Validation

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-18-01 | `TestVendor_Suspension_NewInvoicesBlocked` | INTG | Suspended; invoice creation rejected |
| TI-18-02 | `TestVendor_Suspension_InFlightInvoicesComplete` | INTG | In-progress invoice at suspension; completes normally |
| TI-18-03 | `TestVendor_Suspension_QueuedPayoutsAccumulate_NotSwept` | INTG | Queued records stay queued |
| TI-18-04 | `TestVendor_Suspension_BroadcastBoundary_Aborted` | INTG | Broadcast check fires; records → queued |
| TI-18-05 | `TestVendor_Deletion_BlockedWithPendingInvoices` | INTG | Pending invoices; deletion rejected |
| TI-18-06 | `TestVendor_Deletion_BlockedWithQueuedPayouts` | INTG | Queued payouts; deletion rejected |
| TI-18-07 | `TestVendor_Deletion_BlockedWithNonZeroBalance` | INTG | Non-zero balance; deletion rejected |
| TI-18-08 | `TestVendor_TierDowngrade_InFlightUnaffected` | INTG | Tier downgrade; existing invoices keep snapshot |
| TI-18-09 | `TestVendor_ModeDowngrade_NewInvoicesBlocked_ExistingComplete` | INTG | Mode not permitted; new blocked; existing complete |
| TI-18-10 | `TestVendor_BridgeAddress_IsmineFalse_Accepted` | INTG | **C-04**: RPC returns ismine=false; address accepted |
| TI-18-11 | `TestVendor_BridgeAddress_IsmineTrue_Rejected` | INTG | **C-04**: RPC returns ismine=true → "Cannot use platform-managed address" |
| TI-18-12 | `TestVendor_BridgeAddress_ChangeAddress_IsmineTrue_Rejected` | INTG | **C-04**: change address from prior sweep; ismine=true → rejected |
| TI-18-13 | `TestVendor_BridgeAddress_DBCheck_InvoiceAddress_Rejected` | INTG | DB check: address matches invoice_addresses → rejected |
| TI-18-14 | `TestVendor_BridgeAddress_RPCCheck_RunsEvenIfDBCheckPasses` | INTG | **C-04**: both checks mandatory; RPC runs even if DB clean |
| TI-18-15 | `TestVendor_WithdrawalAddress_IsmineTrue_Rejected` | INTG | Platform address submitted as withdrawal destination; rejected |

### TI-20: Config Validation

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-20-01 | `TestConfig_BitcoinFiatCurrency_Required_WhenEnabled` | UNIT | **N-06**: BTC_FIAT_CURRENCY absent → error |
| TI-20-02 | `TestConfig_BitcoinFiatCurrency_Default_USD` | UNIT | Missing = defaults to "USD" |
| TI-20-03 | `TestConfig_ReconciliationStartHeight_MainnetZero_HardError` | UNIT | mainnet + height=0 + no genesis flag → error |
| TI-20-04 | `TestConfig_CrossFieldHandlerTimeout_Enforced` | UNIT | handler_ms ≤ 2×rpc_timeout×1000+2000 → error |
| TI-20-05 | `TestConfig_AllSecrets_Distinct` | UNIT | SESSION == SSE_SIGNING == AUDIT_HMAC → errors |
| TI-20-06 | `TestConfig_RPCPort_NonNumeric_Error` | UNIT | BTC_RPC_PORT=abc → error |
| TI-20-07 | `TestConfig_AllowedOrigins_TrailingSlash_Error` | UNIT | origin="https://x.com/" → error |
| TI-20-08 | `TestConfig_AllowedOrigins_Wildcard_Error` | UNIT | origin="*" → error |
| TI-20-09 | `TestConfig_OverpaymentThresholds_ValidatedAtConfigLoad` | UNIT | relative/absolute threshold ranges validated |

### TI-21: Security

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-21-01 | `TestSSE_JTI_OneTimeConsume_SecondCallReturns0` | INTG | Lua script; first call=1; second call=0 |
| TI-21-02 | `TestSSE_JTI_TTLExpiry_AllowsFreshToken` | INTG | After TTL; new JTI succeeds |
| TI-21-03 | `TestSSE_IPBinding_DifferentSubnet_Rejected` | UNIT | Bound to 10.0.0.x; from 10.0.1.x → rejected |
| TI-21-04 | `TestSSE_IPBinding_SameSubnet_Accepted` | UNIT | Bound to 10.0.0.1; from 10.0.0.99 → accepted |
| TI-21-05 | `TestSSE_SidMismatch_Rejected` | INTG | Session terminated; sid check fails |
| TI-21-06 | `TestSSE_TokenInURL_LogFilteringRule_Documented` | MANUAL | Log contains no `?token=` value in access logs |
| TI-21-07 | `TestFinancialAudit_ImmutabilityUnderConcurrentWrites` | INTG RACE | Concurrent audit writes; none overwrite each other |
| TI-21-08 | `TestBridgeAddress_BothChecks_DBAndRPC_MandatoryOrder` | INTG | **C-04**: DB check then RPC check; neither skips |
