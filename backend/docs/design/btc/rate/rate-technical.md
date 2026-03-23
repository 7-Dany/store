# Rate — Technical Implementation

> **What this file is:** Implementation contracts for rate source failure detection,
> deviation policy enforcement, stale debit handling, and the complete test inventory
> for this package.
>
> **Read first:** `rate-feature.md` — behavioral contract and edge cases.
> **Schema:** `sql/schema/009_btc.sql` (`btc_exchange_rate_log` table).
> **Queries:** `sql/queries/btc.sql` (`InsertExchangeRate`, `GetLatestExchangeRate`).

---

## Table of Contents

1. [Source "Unavailable" Definition](#1--source-unavailable-definition)
2. [Deviation Policy — Use Lower Price](#2--deviation-policy--use-lower-price)
3. [Cache Refresh and Suspension Logic](#3--cache-refresh-and-suspension-logic)
4. [Stale Debit Logic](#4--stale-debit-logic)
5. [Test Inventory](#5--test-inventory)

---

## §1 — Source "Unavailable" Definition

A rate source is considered unavailable if any of the following occur:
- TCP connection refused or DNS failure
- HTTP 4xx or 5xx response
- Response body is not valid JSON or does not contain the expected price field
- Response is not received within `BTC_RATE_SOURCE_TIMEOUT_MS` (default: 3000ms)

---

## §2 — Deviation Policy — Use Lower Price

When two sources both return a valid price but deviate by more than
`BTC_RATE_MAX_DEVIATION_PCT` (default: 5%):

1. Compute `deviation = abs(price_a - price_b) / min(price_a, price_b)`.
2. If `deviation > BTC_RATE_MAX_DEVIATION_PCT`:
   - Fire WARNING alert: "Rate source deviation exceeds threshold. Source A: X,
     Source B: Y, deviation: Z%."
   - Use `min(price_a, price_b)` for invoice creation.
   - **Do NOT suspend invoice creation.**

This path is only reached when both sources are reachable. The suspended path
(both unavailable) is a separate code path; deviation alone does not suspend.

---

## §3 — Cache Refresh, DB Logging, and Suspension Logic

Every successful rate fetch (from either source) writes a row to `btc_exchange_rate_log`
via `InsertExchangeRate`. This provides a complete time-series audit trail enabling
questions like "what was the BTC/USD rate at 14:23 on March 5?" and "which invoices
were created during the anomaly window?"

Anomaly detection: if the fetched rate deviates from the rolling 1-hour average by
more than `BTC_RATE_MAX_DEVIATION_PCT`, `anomaly_flag = TRUE` and `anomaly_reason`
are set on the inserted row. This also fires a WARNING alert.

Invoice creation uses `GetLatestExchangeRate` to read the most recent rate for the
`(network, fiat_currency)` pair. Index: `idx_ber_network_currency_time` covering
`(network, fiat_currency, fetched_at DESC)` — index-only for LIMIT 1.

```
Every BTC_RATE_REFRESH_INTERVAL_SECONDS (default: 60):
  1. Attempt primary source fetch (timeout: BTC_RATE_SOURCE_TIMEOUT_MS).
     - Success: update cache; INSERT row to btc_exchange_rate_log. Done.
     - Failure: mark primary unavailable. Attempt fallback.
  2. Attempt fallback source fetch (timeout: BTC_RATE_SOURCE_TIMEOUT_MS).
     - Success: update cache; INSERT row to btc_exchange_rate_log. Done.
     - Failure: mark both unavailable.

If both unavailable for > 5 minutes:
  - Suspend new invoice creation.
  - Fire CRITICAL alert: "Rate feed failure — both sources unreachable."
  - Notify vendors and admins.

On recovery (either source responds successfully):
  - Update cache.
  - Resume invoice creation automatically.
  - No restart required.
```

---

## §4 — Stale Debit Logic

At subscription debit time:

```
1. Check cache age: NOW() - cache.last_updated_at
2. If cache_age > BTC_SUBSCRIPTION_DEBIT_MAX_RATE_AGE_SECONDS (default: 120s):
   - Increment debit_defer_count and record debit_first_deferred_at (if first).
   - If NOT (debit_defer_count >= 3 AND NOW() - debit_first_deferred_at > 24h):
     - Defer. Return ErrRateStale to billing system.
   - Else (3 attempts over 24h exceeded):
     - Proceed with current cached rate.
     - Set ErrRateStale flag on financial audit event.
     - Fire WARNING alert with: rate_age, rate_used, last_known_valid_rate.

3. Convert fiat amount to satoshis using floor rounding (same as invoice creation).
4. Execute debit atomically (SELECT FOR UPDATE on balance row).
5. If balance insufficient → reject with ErrInsufficientBalance. No partial debits.
```

---

## §5 — Test Inventory

### Legend
- `[UNIT]` — pure unit test, no DB or network
- `[INTG]` — integration test requiring real PostgreSQL

### TI-13: Rate Feed

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-13-01 | `TestRate_PrimaryDown_FallsBackToSecondary` | INTG | Primary fails; secondary used; no suspension |
| TI-13-02 | `TestRate_BothDown_InvoiceCreationSuspended` | INTG | Both unavailable > 5min; suspended; CRITICAL alert |
| TI-13-03 | `TestRate_BothRecover_CreationResumesAutomatically` | INTG | Recovery; creation resumes without restart |
| TI-13-04 | `TestRate_Deviation_UsesLowerPrice_Warning_NoSuspension` | INTG | **H-05/Q-07**: deviation > 5%; lower price used; WARNING; creation continues |
| TI-13-05 | `TestRate_Deviation_BothUnreachable_Suspended_DespiteDeviation` | INTG | No valid response at all → suspended |
| TI-13-06 | `TestRate_SourceTimeout_MarkedUnavailable` | UNIT | Response > BTC_RATE_SOURCE_TIMEOUT_MS → unavailable |
| TI-13-07 | `TestRate_InvalidJSON_MarkedUnavailable` | UNIT | Malformed response body → unavailable |
| TI-13-08 | `TestRate_StaleDebit_DeferredTwice_ForcedOnThird` | INTG | 3 deferrals over 24h; ErrRateStale flag on audit event |
| TI-13-09 | `TestRate_StaleDebit_WarningAlert_Fires` | INTG | **H-09**: ErrRateStale debit → WARNING alert with rate details |
| TI-13-10 | `TestRate_DebitConversion_UsesFloorRounding` | UNIT | Fiat-to-sat for debit uses floor rounding |
