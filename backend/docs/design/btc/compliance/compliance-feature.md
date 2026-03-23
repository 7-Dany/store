# Compliance Feature — Behavior & Edge Cases

> **What this file is:** A plain-language description of the platform's compliance
> obligations: FATF Travel Rule recording for high-value payouts, GDPR personal data
> erasure, and the data retention / pseudonymisation strategy. Read this before
> implementing any compliance-related feature.
>
> **Companion:** `compliance-technical.md` — FATF trigger contract, GDPR erasure
> job design, pseudonymisation patterns, test inventory.
> **Schema:** `sql/schema/010_btc_payouts.sql` (`fatf_travel_rule_records`,
> `gdpr_erasure_requests`, `sse_token_issuances`).
> **Queries:** `sql/queries/btc.sql` (`InsertSSETokenIssuance`, `EraseSSETokenIssuances`).

> ⚑ **Feature flags** — two separate flags cover this file:
>
> `platform_config.fatf_enabled` (default `FALSE`) — gates the FATF Travel Rule
> section. When `FALSE`, no `fatf_travel_rule_records` row is required before
> broadcasting a sweep, and the `fn_fatf_address_consistency` trigger is bypassed.
> Do not enable until the platform is VASP-registered and a Travel Rule protocol
> (TRISA / OpenVASP) is integrated.
>
> `platform_config.gdpr_erasure_job_enabled` (default `FALSE`) — gates the erasure
> background job. When `FALSE`, erasure requests are still accepted and recorded
> (legally required), but the job that actually nullifies PII does not run.
> Enable only after the job has been validated end-to-end in staging.

---

## 1. FATF Travel Rule

### What it is

The Financial Action Task Force (FATF) Travel Rule requires Virtual Asset Service
Providers (VASPs) to collect and transmit identity information alongside cryptocurrency
transfers above a threshold value. This platform is a VASP.

The rule applies to **outgoing transfers** (payouts from the platform to vendor
destination addresses). It does not apply to incoming payments from buyers — the
platform cannot compel buyers to identify themselves as part of payment.

### Threshold

The threshold is jurisdiction-dependent. Conservative defaults:
- **Global / unspecified:** USD 1,000 equivalent at payout time
- **EU (AMLD/MiCA):** EUR 1,000 equivalent
- **US (FinCEN):** USD 3,000 equivalent

The platform uses `BTC_FATF_THRESHOLD_FIAT` and `BTC_FATF_THRESHOLD_CURRENCY` config
values, with a default of USD 1,000. This is intentionally conservative — when in
doubt, record.

### What must be recorded

For each qualifying payout, a `fatf_travel_rule_records` row must exist **before the
sweep transaction is broadcast**. The `fn_fatf_address_consistency` trigger in
`011_btc_functions.sql` enforces this at the DB level: any attempt to broadcast a
payout (transition to `broadcast`) without a corresponding FATF record causes the
broadcast to fail.

Required fields per record:
- Originator identity (platform / vendor VASP information)
- Originator address (the sending wallet — platform wallet)
- Beneficiary identity (vendor name, ID, and if available: legal entity info)
- Beneficiary address (the vendor's destination address — must match
  `payout_records.destination_address`; enforced by `fn_fatf_address_consistency`)
- Transfer amount in satoshis and fiat equivalent
- Transaction reference (payout record ID)

### Address consistency enforcement

`fn_fatf_address_consistency` fires as a `BEFORE INSERT` trigger on
`fatf_travel_rule_records`. It rejects any INSERT where `beneficiary_address`
does not match `payout_records.destination_address` for the referenced payout.
This prevents a filing error where the recorded address differs from the actual
on-chain destination.

Platform-mode payouts have no destination address (funds stay internal). These are
skipped by the trigger — no FATF record is required for internal balance credits.

### FATF record retention

FATF records must be retained for at least 5 years from the transaction date
(FATF Recommendation 12). Records are never deleted via the application. They are
subject to GDPR constraints: any PII fields (personal names, addresses) must be
pseudonymised or anonymised at the 5-year mark if the regulatory retention window
allows.

---

## 2. GDPR — Right to Erasure (Article 17)

### What GDPR requires

Any authenticated user who is a natural person (EU/UK resident) can request that
the platform erase their personal data. This right applies to:
- Display name, username, email address
- IP addresses and device identifiers
- Any profile data

This right does **not** apply to data the platform is legally required to retain:
- Financial records (invoices, payments, payout records) — retained for tax purposes
- FATF Travel Rule records — retained for 5 years
- Financial audit events — immutable and permanently retained (they document
  financial transactions, not personal identity)

### What can actually be erased

| Data | Erasable? | Notes |
|------|-----------|-------|
| `users.email`, `users.display_name` | Yes | Replaced with anonymised value |
| `sse_token_issuances.source_ip_hash` | Yes | Nullified; `erased=TRUE` set |
| `users.username` | Partial | Must remain unique; replaced with opaque ID |
| Invoice amounts, addresses, txids | No | Financial records — retained |
| `financial_audit_events.actor_label` | No — never stored as PII | HMAC pseudonymisation means nothing to erase |
| `fatf_travel_rule_records` | No | Regulatory retention requirement |
| `payout_records` | No | Financial records |

### Pseudonymisation strategy

To minimise the need for erasure, the platform uses pseudonymisation by default for
any data that could be PII:

**`financial_audit_events.actor_label`:** stores `HMAC-SHA256(email, server_secret)`
rather than raw email. This means the audit trail links actions to an identity via
a non-reversible token. A GDPR erasure request does not need to touch
`financial_audit_events` at all — there is nothing to erase.

**`sse_token_issuances.source_ip_hash`:** stores
`SHA256(ip || daily_rotation_key)` rather than raw IP. The daily rotation key is
deleted after rotation, making the hash non-reversible within 24 hours. On GDPR
erasure, `source_ip_hash` is nullified and `erased=TRUE` is set.

**`btc_zmq_dead_letter` / `ops_audit_log`:** these contain no PII by design.
Addresses, txids, and amounts are not personal data. The `actor_label` in
`ops_audit_log` uses the same HMAC pattern as `financial_audit_events`.

### GDPR erasure request lifecycle

When a GDPR erasure request is received:

1. A `gdpr_erasure_requests` row is created with `status='pending'` and
   `tables_processed = []`.
2. The erasure job processes each table sequentially, appending each completed
   table name to `tables_processed` after each step.
3. If the job crashes mid-erasure, on restart it reads `tables_processed` and
   skips already-completed tables. This makes the job crash-safe and idempotent.
4. When all tables are processed, `status='completed'` and `completed_at` is set.

**Tables processed in order:**
1. `sse_token_issuances` — nullify `source_ip_hash`, set `erased=TRUE`
2. `users` — anonymise display_name, replace email with opaque hash
3. Any future PII-bearing tables added to the system

**Hard limit:** erasure must complete within 30 days of the request (GDPR Article 17).
If `completed_at` is NULL and `requested_at < NOW() - INTERVAL '25 days'`, a WARNING
alert fires. At 30 days, a CRITICAL alert fires.

### What cannot be erased and why

Vendors in platform wallet mode have financial obligations recorded on
`invoices`, `payout_records`, and `financial_audit_events`. These records are
legally required for:
- Tax reporting (7 years in most EU jurisdictions)
- FATF Travel Rule compliance (5 years)
- Dispute resolution

The platform's privacy policy must disclose this retention to vendors at registration
time. Vendors who want their data erased must first withdraw all balances, ensure all
pending invoices are settled or cancelled, and agree that financial records are
retained under legal obligation.

---

## 3. Data Minimisation

The platform follows data minimisation principles — collect only what is necessary:

- **Buyer information:** the platform collects only what is necessary for a payment.
  No name, address, or identity is required from a buyer for a standard payment. The
  only optional PII collected from buyers is a refund address (a Bitcoin address,
  not a name).
- **Vendor information:** wallet configuration (destination address, mode) is
  business data, not personal data. The vendor's identity is captured in the RBAC
  system, not the Bitcoin payment system.
- **IP addresses:** stored only in `sse_token_issuances` under pseudonymisation. Not
  stored in any other Bitcoin payment system table.
- **Rate data:** `btc_exchange_rate_log` contains no PII.
- **ZMQ dead letters:** contain only Bitcoin data (txids, amounts). No PII.
