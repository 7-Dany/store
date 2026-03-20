# Bitcoin — Schema Index

> **What this file is:** A single-page index of every DB table owned by the
> Bitcoin payment system. Use this when writing migrations, auditing cross-package
> dependencies, or verifying reconciliation formula coverage.
>
> **Convention:** "Owner" is the package whose technical doc contains the canonical
> DDL. No table may be defined in two places. If a package reads a table it does
> not own, it must reference the owning package's doc.

---

## Table Index

| Table | Owner package | DDL location | Notes |
|-------|---------------|--------------|-------|
| `invoices` | `invoice` | `invoice/invoice-technical.md §5` | Required columns listed; base schema assumed from implementation |
| `invoice_addresses` | `invoice` | `invoice/invoice-technical.md §1, §6` | Stores address string + `hd_derivation_index`; uniqueness constraint on `(address, network)` |
| `invoice_address_monitoring` | `invoice` | `invoice/invoice-technical.md §3` | Authoritative ZMQ watch list; `monitor_until` NULL = actively monitored |
| `invoice_payments` | `invoice` | `invoice/invoice-technical.md §4` | `UNIQUE (txid, vout_index)` idempotency constraint; `double_payment` and `post_settlement` flags |
| `btc_outage_log` | `payment` | `payment/payment-technical.md §4` | Written on node disconnect/reconnect; read by invoice expiry formula and resilience `HandleRecovery` |
| `bitcoin_sync_state` | `resilience` | `resilience/resilience-technical.md §1` | Tracks `last_processed_height` per network; sentinel -1 = never processed |
| `bitcoin_block_history` | `resilience` | ⚠️ **UNDEFINED** — referenced in `resilience-technical.md §2` step 5b but never given a schema | Stores placeholder rows for pruned blocks during backfill; schema must be defined before Stage 2a |
| `vendor_balances` | `settlement` | `settlement/settlement-technical.md §5, §6` | `CHECK (balance_satoshis >= 0)` constraint; `SELECT FOR UPDATE` required before every read-modify-write |
| `payout_records` | `settlement` | `settlement/settlement-technical.md §4` | `BEFORE INSERT` trigger rejects records when parent invoice `status != 'settled'` |
| `financial_audit_events` | `audit` | `audit/audit-technical.md §1` | Immutable append-only; DB user has INSERT+SELECT only; UPDATE/DELETE rejected by trigger |
| `reconciliation_job_state` | `audit` | `audit/audit-technical.md §4` | Stores `last_successful_run_at`; monitored independently to fire "Reconciliation job missed" alert |
| `platform_config` | `audit` | `audit/audit-technical.md §3` | Contains `treasury_reserve_satoshis` column; required for reconciliation formula to balance |
| `wallet_backup_success` | `wallet-backup` | `wallet-backup/wallet-backup-technical.md §1` | Written only after backup file is copied to storage; alert triggers on this timestamp, not job run time |

---

## Cross-Table Read Dependencies

Tables that are read by packages other than their owner. These are the seams most
likely to produce bugs when either side changes.

| Table | Owner | Read by | Why |
|-------|-------|---------|-----|
| `btc_outage_log` | `payment` | `invoice` | Expiry formula needs outage intervals to compute `effective_expires_at` |
| `btc_outage_log` | `payment` | `resilience` | `HandleRecovery` is triggered by the same reconnect event that closes the outage record |
| `invoice_address_monitoring` | `invoice` | `payment` (ZMQ subscriber) | ZMQ watch set is loaded from this table at startup, reconnect, and every 5 minutes |
| `invoice_address_monitoring` | `invoice` | `resilience` (backfill) | `reconcileSegment` matches block outputs against active monitoring records |
| `invoices` | `invoice` | `resilience` | `rollbackSettlementFromHeight` reads and updates invoice status |
| `invoices` | `invoice` | `settlement` | All settlement phase transitions write to this table |
| `payout_records` | `settlement` | `resilience` | `rollbackSettlementFromHeight` rolls back payout records in the same transaction |
| `payout_records` | `settlement` | `audit` | Reconciliation formula reads `net_satoshis WHERE status IN (held, queued, constructing, broadcast)` |
| `invoice_payments` | `invoice` | `settlement` | Phase 1 sums `value_sat` before tolerance check |
| `invoice_payments` | `invoice` | `audit` | Reconciliation formula reads `value_sat` for in-flight invoices |
| `vendor_balances` | `settlement` | `audit` | Reconciliation formula reads `SUM(balance_satoshis)` |
| `platform_config` | `audit` | `audit` | Reconciliation formula reads `treasury_reserve_satoshis` |

---

## Flags and Known Gaps

**`bitcoin_block_history` — UNDEFINED SCHEMA**
Referenced in `resilience-technical.md §2` step 5b ("insert placeholder in
`bitcoin_block_history`") but no DDL, column list, or purpose description exists
anywhere in the design docs. This must be defined before Stage 2a implementation.
Minimum required: know what columns it has, whether it's ever queried (and by what),
and whether it can be replaced with a simpler cursor-advance-and-log approach.
See gaps analysis — Gap F.

**`invoice_addresses` — DDL not fully specified**
The table is referenced (address string, `hd_derivation_index`, uniqueness constraint)
but the full DDL is never written out as a CREATE TABLE statement. The schema design
pass must produce the complete DDL including any FK references, index coverage for
label integrity checks, and the `hd_derivation_index` column type (INTEGER vs BIGINT).
