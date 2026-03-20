# Rate Feature — Behavior & Edge Cases

> **What this file is:** A plain-language description of how the platform maintains
> a BTC/fiat exchange rate cache, handles rate source failures and deviations, and
> applies rates for invoice creation and subscription debits. Read this to understand
> the feature contract before looking at any implementation detail.
>
> **Companion:** `rate-technical.md` — source unavailability definition, failure
> policy, deviation policy, stale debit logic, test inventory.
> **Used by:** `../invoice/invoice-feature.md` (invoice creation),
> `../settlement/settlement-feature.md` (subscription debits).

---

## 1. Fiat Currency

All fiat amounts and exchange rates are denominated in the currency configured by
`BTC_FIAT_CURRENCY` (e.g. `USD`). This value is required at startup and is validated
by `config.validate()`. Every DB record that stores a fiat amount includes the currency
code alongside the numeric value.

---

## 2. How Rates Are Managed

The platform maintains a locally cached BTC/fiat exchange rate. The cache is refreshed
every 60 seconds (configurable) from a primary rate source with at least one hot
failover source.

**Default rate sources:**
- Primary: CoinGecko API
- Fallback: Kraken public ticker API

The policy requires two sources minimum.

---

## 3. Price Deviation Policy

When the two sources return prices that deviate by more than `BTC_RATE_MAX_DEVIATION_PCT`
(default: 5%) from each other:

- A WARNING alert fires ("Rate source deviation exceeds threshold").
- The **lower** of the two prices is used for invoice creation. This is the
  conservative choice: the buyer pays a marginally higher BTC amount, which protects
  the vendor's fiat equivalent. Using the lower price never results in the vendor
  receiving less than the product price in fiat terms.
- Invoice creation is **not suspended** due to deviation alone. Suspension only occurs
  when both sources are simultaneously unavailable (no valid response at all).

This policy avoids suspending invoice creation during legitimate short-term volatility,
which tends to coincide with peak trading activity.

---

## 4. Failure Behavior

If both sources are unavailable for more than 5 minutes:
- New invoice creation is suspended
- Existing pending invoices are unaffected (amounts already locked)
- Vendors and admins are notified immediately
- Invoice creation resumes automatically when either source recovers

---

## 5. Rate Staleness for Subscription Debits

At debit time, the platform checks the cache age. If older than
`BTC_SUBSCRIPTION_DEBIT_MAX_RATE_AGE_SECONDS` (default: 120 seconds), the debit is
deferred.

After 3 deferred attempts spanning more than 24 hours, the debit proceeds using the
most recent cached rate with an `ErrRateStale` flag recorded on the financial audit
event — rather than deferring indefinitely. The billing system is notified of the flag.

When a debit executes with `ErrRateStale`, a WARNING alert fires recording the rate
age, the rate used, and the last known valid rate. This prevents silent over- or
under-billing during extended rate feed outages.
