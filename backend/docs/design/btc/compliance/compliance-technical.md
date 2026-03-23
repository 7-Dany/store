# Compliance — Technical Implementation

> **What this file is:** Implementation contracts for FATF Travel Rule recording,
> GDPR erasure job design, pseudonymisation patterns, and the complete test inventory.
>
> **Read first:** `compliance-feature.md` — behavioral contract and regulatory context.
> **Schema:** `sql/schema/010_btc_payouts.sql` (`fatf_travel_rule_records`,
> `gdpr_erasure_requests`, `sse_token_issuances`).
> **Schema (triggers):** `sql/schema/011_btc_functions.sql`
> (`fn_fatf_address_consistency`, `trg_fatf_address_consistency`).
> **Queries:** `sql/queries/btc.sql` (`InsertSSETokenIssuance`, `EraseSSETokenIssuances`).

---

## Table of Contents

1. [FATF Record — Insert Contract](#1--fatf-record--insert-contract)
2. [fn_fatf_address_consistency Trigger](#2--fn_fatf_address_consistency-trigger)
3. [GDPR Erasure Job Design](#3--gdpr-erasure-job-design)
4. [Pseudonymisation Patterns](#4--pseudonymisation-patterns)
5. [SSE Token IP Erasure](#5--sse-token-ip-erasure)
6. [Test Inventory](#6--test-inventory)

---

## §1 — FATF Record — Insert Contract

A `fatf_travel_rule_records` row must be inserted **before the payout record
transitions to `broadcast` status**. The sweep construction flow enforces this:

```
SweepService.constructAndBroadcast():
  1. Fetch payout records for this batch (status='constructing')
  2. For each payout record where net_satoshis × btc_rate > FATF_THRESHOLD:
     a. Check: SELECT EXISTS(
            SELECT 1 FROM fatf_travel_rule_records
            WHERE payout_record_id = $id
        )
     b. If not exists: INSERT fatf_travel_rule_records(...)
        This INSERT fires fn_fatf_address_consistency — will RAISE if address mismatch.
  3. walletcreatefundedpsbt → walletprocesspsbt → finalizepsbt
  4. DB: UPDATE payout_records SET status='broadcast', batch_txid=$txid
  5. sendrawtransaction
```

**Platform-mode payouts (no destination address):** `fn_fatf_address_consistency`
skips the check when `destination_address IS NULL` on the payout record. No FATF
record required for internal balance credits.

**FATF record fields:**
```go
type InsertFATFRecordParams struct {
    PayoutRecordID        uuid.UUID
    OriginatorVASPName    string    // platform legal name
    OriginatorAddress     string    // platform wallet address (the funding input)
    BeneficiaryID         uuid.UUID // vendor.id
    BeneficiaryName       string    // vendor legal name or display name
    BeneficiaryAddress    string    // destination address — MUST match payout_record
    TransferAmountSat     int64
    TransferAmountFiat    int64
    FiatCurrencyCode      string
    BtcRateAtTransfer     decimal.Decimal
    ReferenceID           string    // payout_record.id as string
}
```

---

## §2 — fn_fatf_address_consistency Trigger

Defined in `sql/schema/011_btc_functions.sql`. Fires `BEFORE INSERT` on
`fatf_travel_rule_records`.

**Logic:**
```sql
IF NEW.beneficiary_address IS NOT NULL THEN
    SELECT destination_address INTO v_dest
    FROM payout_records WHERE id = NEW.payout_record_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'fatf: payout_record % not found', NEW.payout_record_id;
    END IF;

    IF v_dest IS NOT NULL AND v_dest <> NEW.beneficiary_address THEN
        RAISE EXCEPTION
            'fatf: beneficiary_address % does not match payout destination %',
            NEW.beneficiary_address, v_dest;
    END IF;
END IF;
```

**Why DB-level enforcement:** a FATF filing error (wrong address recorded) is an
irrecoverable compliance failure after the sweep is broadcast. Application-layer
enforcement is insufficient because any code path that bypasses the check would
silently create an invalid record. The trigger makes address consistency structurally
impossible to violate.

---

## §3 — GDPR Erasure Job Design

The erasure job is triggered when a `gdpr_erasure_requests` row is created. It runs
as a single-threaded job (one erasure per user at a time) with crash-safe resumption
via `tables_processed`.

```go
func processErasureRequest(ctx context.Context, req GDPRErasureRequest) error {
    tables := []struct {
        name string
        fn   func(ctx context.Context, userID uuid.UUID) error
    }{
        {"sse_token_issuances", eraseSSETokens},
        {"users_display_name",  anonymiseUserDisplayName},
        {"users_email",         anonymiseUserEmail},
        // Add new tables here as they are introduced
    }

    for _, t := range tables {
        if slices.Contains(req.TablesProcessed, t.name) {
            continue // already done in a previous run
        }
        if err := t.fn(ctx, req.UserID); err != nil {
            return fmt.Errorf("erasure %s for user %s: %w", t.name, req.UserID, err)
        }
        // Append table name to tables_processed BEFORE processing the next table.
        // This is the crash-safety checkpoint.
        if err := q.AppendErasureTableProcessed(ctx, req.ID, t.name); err != nil {
            return err
        }
    }
    return q.CompleteErasureRequest(ctx, req.ID)
}
```

**`tables_processed` is an append-only array.** Each element is written in a
separate DB transaction after the corresponding table is processed. If the job
crashes between appending to `tables_processed` and processing the next table,
on restart the already-processed table is skipped.

**Idempotency within each step:** each erasure function is idempotent. Calling
`eraseSSETokens` twice for the same user is safe — the second call finds
`erased=TRUE` rows already and writes zero rows.

---

## §4 — Pseudonymisation Patterns

### HMAC actor labels

```go
// At financial audit event write time:
actorLabel := hmac.SHA256([]byte(cfg.AuditHMACSecret), []byte(user.Email))
// If OAuth-only account (no email):
actorLabel := hmac.SHA256([]byte(cfg.AuditHMACSecret), []byte(user.Username))
```

The `fn_fae_validate_actor_label` trigger in `011_btc_functions.sql` verifies:
```sql
v_expected := hmac(COALESCE(email, username)::bytea,
                   current_setting('app.audit_hmac_secret')::bytea, 'sha256');
IF encode(v_expected, 'hex') <> NEW.actor_label THEN
    RAISE EXCEPTION 'actor_label mismatch';
END IF;
```

The `app.audit_hmac_secret` session variable must be set before any INSERT into
`financial_audit_events`. Set it in the connection setup:
```sql
SET app.audit_hmac_secret = 'BTC_AUDIT_HMAC_SECRET_VALUE';
```

### IP pseudonymisation for SSE tokens

```go
// At token issuance:
dailyKey := getDailyRotationKey()  // fetched from secrets store; rotated every 24h
sourceIPHash := sha256.Sum256(append([]byte(clientIP), []byte("|"), dailyKey...))
jtiHash := hmac.SHA256([]byte(cfg.ServerSecret), []byte(jti))
```

**Daily rotation key:** stored in a secrets manager (not in the DB). The current
key is fetched at issuance time. When rotated, the old key is deleted. After
rotation, existing hashes become non-reversible even with the current secret.
Key rotation is scheduled daily at 00:00 UTC.

---

## §5 — SSE Token IP Erasure

Query: `EraseSSETokenIssuances(vendor_id)` — sets `erased=TRUE, source_ip_hash=NULL`
for all non-erased rows for a vendor. The `chk_sti_erased_coherent` constraint
enforces `source_ip_hash IS NULL` when `erased=TRUE`.

**What remains after erasure:**
- `jti_hash` — non-PII; needed for audit completeness
- `vendor_id` — non-PII; needed for linking records
- `network`, `issued_at`, `expires_at` — non-PII; operational data

The `jti_hash` is never reversible without `BTC_SERVER_SECRET`. As long as the
secret is rotated after a suspected compromise, erased records have no recoverable
identity information.

---

## §6 — Test Inventory

### Legend
- `[UNIT]` — pure unit test, no DB or network
- `[INTG]` — integration test requiring real PostgreSQL

### TI-25: FATF Travel Rule

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-25-01 | `TestFATF_RecordInserted_BeforeBroadcast` | INTG | FATF record exists before payout transitions to broadcast |
| TI-25-02 | `TestFATF_AddressConsistency_MatchingAddress_Accepted` | INTG | Matching beneficiary_address → INSERT succeeds |
| TI-25-03 | `TestFATF_AddressConsistency_MismatchedAddress_Rejected` | INTG | Non-matching address → RAISE EXCEPTION |
| TI-25-04 | `TestFATF_PlatformMode_NullDestination_NoRecord` | INTG | Platform-mode payout (null destination) → no FATF record required |
| TI-25-05 | `TestFATF_BelowThreshold_NoRecord` | INTG | net_satoshis × rate < threshold → no record inserted |
| TI-25-06 | `TestFATF_AboveThreshold_RecordInserted` | INTG | net_satoshis × rate ≥ threshold → record inserted |
| TI-25-07 | `TestFATF_BroadcastBlockedWithoutRecord` | INTG | Payout above threshold; no FATF row → broadcast transition fails |
| TI-25-08 | `TestFATF_PayoutNotFound_TriggerRaises` | INTG | INSERT with non-existent payout_record_id → RAISE |

### TI-26: GDPR Erasure

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-26-01 | `TestGDPR_ErasureJob_ProcessesTablesInOrder` | INTG | Tables processed in specified order |
| TI-26-02 | `TestGDPR_ErasureJob_CrashResume_SkipsCompleted` | INTG | tables_processed populated; crash; restart skips done tables |
| TI-26-03 | `TestGDPR_ErasureJob_Idempotent_DoublePprocess` | INTG | Running twice; second run is no-op |
| TI-26-04 | `TestGDPR_ErasureJob_Completed_AtSetOnDone` | INTG | All tables done; completed_at set |
| TI-26-05 | `TestGDPR_ErasureJob_25DayWarning_Fires` | INTG | requested_at > 25 days; WARNING alert |
| TI-26-06 | `TestGDPR_ErasureJob_30DayCritical_Fires` | INTG | requested_at > 30 days; CRITICAL alert |
| TI-26-07 | `TestGDPR_SSETokenErasure_NullifiesIPHash` | INTG | EraseSSETokenIssuances; source_ip_hash=NULL; erased=TRUE |
| TI-26-08 | `TestGDPR_SSETokenErasure_Idempotent` | INTG | Already erased rows; second call is no-op |
| TI-26-09 | `TestGDPR_FinancialAuditEvents_NotErased` | INTG | GDPR job does not touch financial_audit_events |
| TI-26-10 | `TestGDPR_FATFRecords_NotErased` | INTG | GDPR job does not touch fatf_travel_rule_records |

### TI-27: Pseudonymisation

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-27-01 | `TestHMAC_ActorLabel_ConsistentAcrossCalls` | UNIT | Same email + secret → same HMAC every time |
| TI-27-02 | `TestHMAC_ActorLabel_OAuthNoEmail_UsesUsername` | UNIT | Null email → COALESCE uses username |
| TI-27-03 | `TestHMAC_ActorLabel_Trigger_ValidLabel_Accepted` | INTG | Correct HMAC → INSERT succeeds |
| TI-27-04 | `TestHMAC_ActorLabel_Trigger_WrongLabel_Rejected` | INTG | Incorrect HMAC → RAISE EXCEPTION |
| TI-27-05 | `TestIPHash_DailyRotation_OldKeyMakesNonReversible` | UNIT | After key rotation, old hash not reproducible |
| TI-27-06 | `TestIPHash_SameIPSameKey_SameHash` | UNIT | Deterministic given same key |
| TI-27-07 | `TestIPHash_NullIP_NullHashStored` | UNIT | Missing IP → source_ip_hash=NULL; no error |
