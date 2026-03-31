/* ============================================================
   sql/queries/btc.sql
   Bitcoin payment system queries for sqlc code generation.

   Sections (in execution-frequency order):
     ZMQ hot path              — address monitoring lookups on every hashtx
     Invoice addresses         — HD keypool address allocation
     Invoice payments          — append-only on-chain payment records
     Invoice lifecycle         — state machine transitions and worker scans
     Invoice address monitoring lifecycle
     Payout records            — vendor payout lifecycle
     Vendor balance procedures — credit/debit via stored procedures
     Vendor wallet config      — per-vendor wallet settings
     Platform config           — global operational switches
     Reconciliation            — job state and run history
     Block history             — processed-block cursor log
     Outage log                — node downtime records for expiry compensation
     Exchange rate log         — BTC/fiat rate feed
     ZMQ dead letter           — unmatched ZMQ events
     Financial audit events    — immutable financial audit trail
     Webhook deliveries        — vendor notification outbox
     SSE token issuances       — SSE connection token records

   Concurrency invariant for invoice status transitions:
     Every TransitionInvoice* call uses :execrows. The caller MUST assert
     RowsAffected() == 1. Zero rows means a concurrent worker changed the
     status first. Return ErrStatusPreconditionFailed and roll back.
     Never silently continue.

   Balance mutation invariant:
     Never call UPDATE directly on vendor_balances. Direct UPDATE is revoked
     from btc_app_role at the DB level. Always use CreditVendorBalance /
     DebitVendorBalance which call the btc_credit_balance / btc_debit_balance
     stored procedures that acquire the row-level lock internally.

   Depends on: 009_btc.sql, 010_btc_payouts.sql, 011_btc_functions.sql
   ============================================================ */


/* ════════════════════════════════════════════════════════════
   ZMQ HOT PATH — invoice_address_monitoring
   Called on every hashtx and hashblock event. Must be fast.
   ════════════════════════════════════════════════════════════ */

-- name: GetActiveMonitoringByAddress :one
-- ZMQ hashtx handler: resolve (address, network) → invoice_id.
-- This is on the critical path of every received Bitcoin transaction.
-- The unique partial index uq_iam_active_address_network guarantees at most
-- one active row per (address, network), so this always returns 0 or 1 rows.
-- Index: idx_iam_active ON invoice_address_monitoring(address, network)
--        WHERE status = 'active'
SELECT
    iam.id,
    iam.invoice_id,
    iam.address,
    iam.network,
    iam.monitor_until,
    iam.status
FROM invoice_address_monitoring iam
WHERE iam.address = @address
  AND iam.network = @network
  AND iam.status  = 'active';


-- name: LoadActiveMonitoringByNetwork :many
-- ZMQ startup / reconnect: rebuild the in-memory watch set for a network.
-- Called on every startup and after every reconnect so the subscriber
-- knows which addresses to watch before it starts dispatching events.
-- Index: idx_iam_reload ON invoice_address_monitoring(network, invoice_id)
--        WHERE status = 'active'
SELECT
    iam.invoice_id,
    iam.address,
    iam.network,
    iam.monitor_until
FROM invoice_address_monitoring iam
WHERE iam.network = @network
  AND iam.status  = 'active'
ORDER BY iam.invoice_id;


/* ════════════════════════════════════════════════════════════
   INVOICE ADDRESSES
   ════════════════════════════════════════════════════════════ */

-- name: CreateInvoiceAddress :one
-- Inserts the invoice address row immediately after getnewaddress returns.
-- On 23505 (uq_invoice_address_network): duplicate address from the keypool —
-- KeypoolOrRPCError CRITICAL alert. Do NOT retry automatically.
-- On 23505 (uq_invoice_address_per_invoice): invoice already has an address — bug.
INSERT INTO invoice_addresses (
    invoice_id,
    address,
    network,
    hd_derivation_index,
    label
)
VALUES (
    @invoice_id::uuid,
    @address,
    @network,
    @hd_derivation_index::bigint,
    'invoice'
)
RETURNING id, invoice_id, address, network, hd_derivation_index, label, created_at;


-- name: GetInvoiceAddress :one
-- Fetch the address allocated to an invoice.
-- Uses the unique index created by uq_invoice_address_per_invoice on invoice_id.
SELECT
    id,
    invoice_id,
    address,
    network,
    hd_derivation_index,
    label,
    created_at
FROM invoice_addresses
WHERE invoice_id = @invoice_id::uuid;


-- name: GetMaxHDDerivationIndex :one
-- Wallet recovery Scenario B: keypool cursor advance.
-- Returns MAX(hd_derivation_index) for the given network so the application
-- can compute the next keypool refill target as MAX * 1.2 with a minimum buffer.
-- Index: idx_ia_max_derivation ON invoice_addresses(network, hd_derivation_index DESC)
-- Returns NULL on a fresh deployment before any addresses have been allocated.
SELECT MAX(hd_derivation_index) AS max_derivation_index
FROM invoice_addresses
WHERE network = @network;


/* ════════════════════════════════════════════════════════════
   INVOICE PAYMENTS
   ════════════════════════════════════════════════════════════ */

-- name: UpsertInvoicePayment :one
-- Append-only payment record. Always ON CONFLICT DO NOTHING for idempotency —
-- ZMQ events may deliver the same txid multiple times on reconnect.
-- Returns NULL when the row already exists (conflict); callers discard NULLs.
-- Conflict target: UNIQUE constraint uq_inv_payment_txid_vout on (txid, vout_index).
INSERT INTO invoice_payments (
    invoice_id,
    txid,
    vout_index,
    value_sat,
    double_payment,
    post_settlement
)
VALUES (
    @invoice_id::uuid,
    @txid,
    @vout_index::integer,
    @value_sat::bigint,
    @double_payment::boolean,
    @post_settlement::boolean
)
ON CONFLICT (txid, vout_index) DO NOTHING
RETURNING id, invoice_id, txid, vout_index, value_sat, detected_at, double_payment, post_settlement;


-- name: SumInvoicePayments :one
-- Settlement Phase 1: sum all received satoshis for the given invoice and txid.
-- A single TX may send multiple vouts to the same address — this SUM covers all.
-- Index: idx_ip_invoice_id with INCLUDE (value_sat, txid) enables index-only scan.
SELECT COALESCE(SUM(value_sat), 0)::bigint AS total_received_sat
FROM invoice_payments
WHERE invoice_id = @invoice_id::uuid
  AND txid       = @txid;


/* ════════════════════════════════════════════════════════════
   INVOICE LIFECYCLE
   ════════════════════════════════════════════════════════════ */

-- name: CreateInvoice :one
-- Creates a new invoice with a full tier/wallet snapshot baked in at creation.
-- The application MUST resolve every snapshot column as
--   COALESCE(vendor_tier_overrides.field, btc_tier_config.field)
-- before calling this query. The settlement engine reads ONLY from the snapshot
-- columns — live tier/config changes have zero effect on in-flight invoices.
INSERT INTO invoices (
    vendor_id,
    buyer_id,
    tier_id,
    network,
    status,
    amount_sat,
    fiat_amount,
    fiat_currency_code,
    btc_rate_at_creation,
    wallet_mode,
    bridge_destination_address,
    auto_sweep_threshold_sat,
    processing_fee_rate,
    confirmation_depth,
    miner_fee_cap_sat_vbyte,
    payment_tolerance_pct,
    minimum_invoice_sat,
    overpayment_relative_threshold_pct,
    overpayment_absolute_threshold_sat,
    expected_batch_size,
    invoice_expiry_minutes,
    expires_at,
    buyer_refund_address
)
VALUES (
    @vendor_id::uuid,
    @buyer_id::uuid,
    @tier_id::uuid,
    @network,
    'pending',
    @amount_sat::bigint,
    @fiat_amount::bigint,
    @fiat_currency_code,
    @btc_rate_at_creation::numeric,
    @wallet_mode::btc_wallet_mode,
    sqlc.narg('bridge_destination_address'),
    sqlc.narg('auto_sweep_threshold_sat')::bigint,
    @processing_fee_rate::numeric,
    @confirmation_depth::integer,
    @miner_fee_cap_sat_vbyte::integer,
    @payment_tolerance_pct::numeric,
    @minimum_invoice_sat::bigint,
    @overpayment_relative_threshold_pct::numeric,
    @overpayment_absolute_threshold_sat::bigint,
    @expected_batch_size::integer,
    @invoice_expiry_minutes::integer,
    @expires_at::timestamptz,
    sqlc.narg('buyer_refund_address')
)
RETURNING *;


-- name: GetInvoice :one
-- Fetch a single invoice by ID.
SELECT * FROM invoices WHERE id = @invoice_id::uuid;


-- name: GetInvoiceWithLock :one
-- Fetch an invoice with an exclusive row-level lock for settlement or status
-- transitions that require a read-modify-write within a transaction.
SELECT * FROM invoices WHERE id = @invoice_id::uuid FOR UPDATE;


-- name: TransitionInvoiceToDetected :execrows
-- pending → detected: payment seen in mempool. Freezes the expiry timer.
-- Sets detected_txid and detected_at atomically.
-- Caller MUST assert RowsAffected() == 1.
UPDATE invoices
SET
    status                  = 'detected',
    detected_txid           = @txid,
    detected_at             = NOW(),
    fiat_equiv_at_detection = sqlc.narg('fiat_equiv_at_detection')::bigint,
    updated_at              = NOW()
WHERE id     = @invoice_id::uuid
  AND status = 'pending';


-- name: TransitionInvoiceToConfirming :execrows
-- detected → confirming: first block confirmation received.
-- Sets first_confirmed_block_height ATOMICALLY — this is required by the reorg
-- rollback query which filters on this column.
-- Caller MUST assert RowsAffected() == 1.
UPDATE invoices
SET
    status                       = 'confirming',
    first_confirmed_block_height = @first_confirmed_block_height::bigint,
    updated_at                   = NOW()
WHERE id     = @invoice_id::uuid
  AND status = 'detected';


-- name: TransitionInvoiceToSettling :execrows
-- confirming|underpaid → settling: settlement worker claims this invoice.
-- Sets settling_source so admin retry uses the correct code path.
-- Caller MUST assert RowsAffected() == 1.
UPDATE invoices
SET
    status          = 'settling',
    settling_source = @settling_source::btc_settling_source,
    updated_at      = NOW()
WHERE id     = @invoice_id::uuid
  AND status = @expected_status::btc_invoice_status;


-- name: TransitionInvoiceToSettled :execrows
-- settling → settled: settlement complete, payout queued or balance credited.
-- Caller MUST assert RowsAffected() == 1.
UPDATE invoices
SET
    status                   = 'settled',
    sweep_completed          = @sweep_completed::boolean,
    fiat_equiv_at_settlement = sqlc.narg('fiat_equiv_at_settlement')::bigint,
    settled_at               = NOW(),
    updated_at               = NOW()
WHERE id     = @invoice_id::uuid
  AND status = 'settling';


-- name: TransitionInvoiceToSettlementFailed :execrows
-- settling → settlement_failed: all retries exhausted. Increments retry_count.
-- Caller MUST assert RowsAffected() == 1.
UPDATE invoices
SET
    status      = 'settlement_failed',
    retry_count = retry_count + 1,
    updated_at  = NOW()
WHERE id     = @invoice_id::uuid
  AND status = 'settling';


-- name: TransitionInvoiceToExpired :execrows
-- pending|mempool_dropped → expired. Sets expired_at.
-- Caller MUST assert RowsAffected() == 1.
UPDATE invoices
SET
    status     = 'expired',
    expired_at = NOW(),
    updated_at = NOW()
WHERE id     = @invoice_id::uuid
  AND status = @expected_status::btc_invoice_status;


-- name: TransitionInvoiceToMempoolDropped :execrows
-- detected → mempool_dropped: payment disappeared from mempool.
-- Sets mempool_absent_since on the first absent check.
-- Caller MUST assert RowsAffected() == 1.
UPDATE invoices
SET
    status               = 'mempool_dropped',
    mempool_absent_since = NOW(),
    updated_at           = NOW()
WHERE id     = @invoice_id::uuid
  AND status = 'detected';


-- name: TransitionMempoolDroppedToDetected :execrows
-- mempool_dropped → detected: payment re-appeared in the mempool.
-- Clears mempool_absent_since per the two-cycle watchdog design.
-- Caller MUST assert RowsAffected() == 1.
UPDATE invoices
SET
    status               = 'detected',
    mempool_absent_since = NULL,
    updated_at           = NOW()
WHERE id     = @invoice_id::uuid
  AND status = 'mempool_dropped';


-- name: MarkInvoiceSweepCompleted :execrows
-- Set sweep_completed = TRUE atomically with the constructing → broadcast
-- payout transition. The reorg rollback logic pivots on this field:
--   TRUE  → reorg_admin_required (sweep may already be broadcast)
--   FALSE → roll back to detected (safe to re-settle)
-- Called in the same transaction as SetPayoutBroadcast.
-- Caller MUST assert RowsAffected() == 1.
UPDATE invoices
SET
    sweep_completed = TRUE,
    updated_at      = NOW()
WHERE id              = @invoice_id::uuid
  AND sweep_completed = FALSE;


-- name: ResetStaleSettlingInvoice :execrows
-- Reset a stale 'settling' invoice back to its predecessor status
-- (confirming or underpaid, as recorded in settling_source).
-- Called by the stale-settling watchdog after the 5-minute threshold.
-- Caller MUST assert RowsAffected() == 1.
UPDATE invoices
SET
    status     = settling_source::text::btc_invoice_status,
    updated_at = NOW()
WHERE id     = @invoice_id::uuid
  AND status = 'settling';


-- name: GetConfirmingInvoices :many
-- Settlement worker: return invoices that have reached their required
-- confirmation depth and are ready to settle.
-- Caller pre-computes @max_confirmed_height = current_chain_height - confirmation_depth
-- for each tier; pass the lowest (most conservative) value to get all eligible invoices,
-- then filter per-tier in the application if needed.
-- Index: idx_inv_confirming ON invoices(network, first_confirmed_block_height)
--        WHERE status = 'confirming'
SELECT * FROM invoices
WHERE network                      = @network
  AND status                       = 'confirming'
  AND first_confirmed_block_height <= @max_confirmed_height::bigint;


-- name: GetDetectedInvoices :many
-- Mempool-drop watchdog: return all detected invoices for a network so the
-- watchdog can call getmempoolentry on each txid to check if it is still live.
-- Index: idx_inv_detected ON invoices(network, detected_at)
--        WHERE status = 'detected'
SELECT id, detected_txid, detected_at, expires_at, network
FROM invoices
WHERE network = @network
  AND status  = 'detected';


-- name: GetExpiryCandidates :many
-- Expiry cleanup job: return invoices whose unadjusted expires_at has passed.
-- IMPORTANT: this returns a SUPERSET. The job must apply the outage compensation
-- formula (GetOutageOverlapForInvoice) before marking any invoice expired.
-- Never mark an invoice expired based on expires_at alone.
-- Index: idx_inv_expiry_candidates ON invoices(network, expires_at)
--        WHERE status IN ('pending', 'mempool_dropped')
SELECT id, status, expires_at, created_at, network
FROM invoices
WHERE network    = @network
  AND expires_at < NOW()
  AND status     IN ('pending', 'mempool_dropped');


-- name: GetStaleSettlingInvoices :many
-- Stale settling-claim watchdog: return invoices stuck in 'settling' longer
-- than the configured threshold (default 5 minutes).
-- Caller pre-computes @stale_before = NOW() - threshold duration.
-- Index: idx_inv_stale_settling ON invoices(network, updated_at)
--        WHERE status = 'settling'
SELECT id, settling_source, updated_at
FROM invoices
WHERE network    = @network
  AND status     = 'settling'
  AND updated_at < @stale_before::timestamptz;


-- name: GetUnderpaidInvoices :many
-- Underpaid re-settlement: a new payment has arrived and may top up an
-- underpaid invoice. Returns all underpaid invoices for payment matching.
-- Index: idx_inv_underpaid ON invoices(network) WHERE status = 'underpaid'
SELECT id, amount_sat, payment_tolerance_pct, network
FROM invoices
WHERE network = @network
  AND status  = 'underpaid';


-- name: GetInvoicesAtBlockHeight :many
-- Reorg rollback: return all invoices first-confirmed at the disconnected
-- block height. The settlement engine rolls them back or escalates to
-- reorg_admin_required based on sweep_completed.
-- Index: idx_inv_first_confirmed_height ON invoices(network, first_confirmed_block_height)
--        WHERE first_confirmed_block_height IS NOT NULL
SELECT id, status, sweep_completed, first_confirmed_block_height
FROM invoices
WHERE network                      = @network
  AND first_confirmed_block_height = @disconnected_block_height::bigint;


/* ════════════════════════════════════════════════════════════
   INVOICE ADDRESS MONITORING LIFECYCLE
   ════════════════════════════════════════════════════════════ */

-- name: CreateInvoiceAddressMonitoring :one
-- Register an address in the ZMQ watch list.
-- MUST be called AFTER CreateInvoiceAddress commits so the
-- fn_iam_address_consistency trigger can verify the address/network match.
-- After this INSERT commits, call RegisterImmediate() on the ZMQ subscriber.
-- The 5-minute periodic reload is a safety net only — never rely on it for
-- newly created invoices.
INSERT INTO invoice_address_monitoring (
    invoice_id,
    address,
    network,
    monitor_until,
    status
)
VALUES (
    @invoice_id::uuid,
    @address,
    @network,
    NULL,
    'active'
)
RETURNING id, invoice_id, address, network, monitor_until, status, created_at;


-- name: SetMonitoringWindow :execrows
-- Set the monitor_until timestamp when an invoice reaches a terminal status.
-- MUST be called in the SAME transaction as the terminal status transition.
-- monitor_until = terminal_at + 30 days.
-- Caller MUST assert RowsAffected() == 1.
UPDATE invoice_address_monitoring
SET
    monitor_until = @monitor_until::timestamptz,
    updated_at    = NOW()
WHERE invoice_id = @invoice_id::uuid
  AND status     = 'active';


-- name: RetireExpiredMonitoringRecords :many
-- Expiry cleanup job: retire active monitoring records whose window has elapsed.
-- Retired rows are NEVER deleted — they form the permanent monitoring audit trail.
-- The partial index on status = 'active' keeps all queries fast regardless of
-- how many retired rows accumulate.
-- Index: idx_iam_expiry_cleanup ON invoice_address_monitoring(monitor_until)
--        WHERE status = 'active' AND monitor_until IS NOT NULL
UPDATE invoice_address_monitoring
SET
    status     = 'retired',
    updated_at = NOW()
WHERE status        = 'active'
  AND monitor_until IS NOT NULL
  AND monitor_until < NOW()
RETURNING invoice_id;


/* ════════════════════════════════════════════════════════════
   PAYOUT RECORDS
   ════════════════════════════════════════════════════════════ */

-- name: CreatePayoutRecord :one
-- Create a payout record for a settled invoice.
-- Three BEFORE INSERT triggers fire: fn_btc_payout_guard (checks invoice.status
-- = 'settled'), fn_pr_vendor_consistency (checks vendor_id match),
-- fn_pr_destination_consistency (checks destination_address match).
-- UNIQUE (invoice_id) is the race-safe double-payout guard — on 23505
-- roll back and return ErrDuplicatePayout.
INSERT INTO payout_records (
    invoice_id,
    vendor_id,
    network,
    status,
    net_satoshis,
    platform_fee_satoshis,
    wallet_mode,
    destination_address,
    kyc_status,
    fee_breakdown
)
VALUES (
    @invoice_id::uuid,
    @vendor_id::uuid,
    @network,
    'held',
    @net_satoshis::bigint,
    @platform_fee_satoshis::bigint,
    @wallet_mode::btc_wallet_mode,
    sqlc.narg('destination_address'),
    @kyc_status::btc_kyc_status,
    sqlc.narg('fee_breakdown')::jsonb
)
RETURNING *;


-- name: GetPayoutRecord :one
SELECT * FROM payout_records WHERE id = @payout_id::uuid;


-- name: GetPayoutRecordByInvoice :one
-- Fetch the payout for a settled invoice. Uses the implicit unique index
-- created by UNIQUE constraint uq_pr_invoice_id on invoice_id.
SELECT * FROM payout_records WHERE invoice_id = @invoice_id::uuid;


-- name: PromoteHeldToQueued :execrows
-- held → queued: net_satoshis has cleared the miner fee floor.
-- fn_pr_status_guard trigger enforces this is a permitted transition.
-- Caller MUST assert RowsAffected() == 1.
UPDATE payout_records
SET
    status     = 'queued',
    updated_at = NOW()
WHERE id     = @payout_id::uuid
  AND status = 'held';


-- name: ClaimPayoutForConstruction :execrows
-- queued → constructing: sweep job claims this payout for batch construction.
-- Sets batch_id. Caller MUST assert RowsAffected() == 1.
-- If RowsAffected() == 0 a concurrent worker already claimed it — skip.
UPDATE payout_records
SET
    status     = 'constructing',
    batch_id   = @batch_id::uuid,
    updated_at = NOW()
WHERE id     = @payout_id::uuid
  AND status = 'queued';


-- name: SetPayoutBroadcast :execrows
-- constructing → broadcast. Sets batch_txid, vout_index_in_batch, fee fields.
-- CRITICAL: this UPDATE MUST commit BEFORE sendrawtransaction is called.
-- See BROADCAST ORDERING INVARIANT in payout_records table comment.
-- If RowsAffected() == 0 the watchdog reclaimed the record — abort broadcast.
UPDATE payout_records
SET
    status              = 'broadcast',
    batch_txid          = @batch_txid,
    vout_index_in_batch = @vout_index_in_batch::integer,
    fee_rate_sat_vbyte  = @fee_rate_sat_vbyte::numeric,
    miner_fee_satoshis  = @miner_fee_satoshis::bigint,
    broadcast_at        = NOW(),
    updated_at          = NOW()
WHERE id     = @payout_id::uuid
  AND status = 'constructing';


-- name: SetPayoutConfirmed :execrows
-- broadcast → confirmed. Sets confirmed_at.
-- MUST be called in the SAME transaction as IncrementTreasuryReserve to keep
-- the reconciliation formula balanced.
-- Caller MUST assert RowsAffected() == 1.
UPDATE payout_records
SET
    status       = 'confirmed',
    confirmed_at = NOW(),
    updated_at   = NOW()
WHERE id     = @payout_id::uuid
  AND status = 'broadcast';


-- name: SetPayoutRBF :execrows
-- Record an RBF fee bump on a broadcast payout. Saves the original txid to
-- original_txid and replaces batch_txid with the replacement txid.
-- Status remains 'broadcast' — the confirmation handler detects the new txid.
UPDATE payout_records
SET
    original_txid      = batch_txid,
    batch_txid         = @replacement_txid,
    fee_rate_sat_vbyte = @new_fee_rate_sat_vbyte::numeric,
    updated_at         = NOW()
WHERE id     = @payout_id::uuid
  AND status = 'broadcast';


-- name: MarkPayoutFailed :execrows
-- Any pre-terminal state → failed. Used when all retries are exhausted.
-- fn_pr_status_guard enforces the transition is permitted from the current state.
-- Caller MUST assert RowsAffected() == 1.
UPDATE payout_records
SET
    status     = 'failed',
    updated_at = NOW()
WHERE id     = @payout_id::uuid
  AND status = @expected_status::btc_payout_status;


-- name: ResolvePayoutManually :execrows
-- failed → manual_payout: admin declares an out-of-band payment was made.
-- fn_pr_status_guard enforces failed → manual_payout is a permitted transition.
-- Caller MUST assert RowsAffected() == 1.
UPDATE payout_records
SET
    status              = 'manual_payout',
    resolution_reason   = @resolution_reason,
    resolution_admin_id = @resolution_admin_id::uuid,
    updated_at          = NOW()
WHERE id     = @payout_id::uuid
  AND status = 'failed';


-- name: GetQueuedPayoutsForVendor :many
-- Sweep job: return all queued payouts for a specific vendor on a network,
-- ordered by creation time (oldest first).
-- Index: idx_pr_queued_vendor ON payout_records(network, vendor_id)
--        WHERE status = 'queued'
SELECT id, net_satoshis, destination_address, wallet_mode, fee_breakdown
FROM payout_records
WHERE network   = @network
  AND vendor_id = @vendor_id::uuid
  AND status    = 'queued'
ORDER BY created_at ASC;


-- name: GetQueuedPayoutsAboveThreshold :many
-- Approval workflow: find queued payouts at or above the withdrawal approval
-- threshold, highest value first. The application then checks each against
-- the vendor's tier threshold.
-- Index: idx_pr_queued_net_satoshis ON payout_records(network, net_satoshis DESC)
--        WHERE status = 'queued'
SELECT id, vendor_id, net_satoshis, destination_address, created_at
FROM payout_records
WHERE network      = @network
  AND status       = 'queued'
  AND net_satoshis >= @threshold_sat::bigint
ORDER BY net_satoshis DESC;


-- name: GetPayoutsByBatchTxid :many
-- Confirmation handler and RBF: find all payout records sharing a sweep TX.
-- Used to mark all outputs confirmed when the sweep TX gets sufficient depth.
-- Index: idx_pr_batch_txid ON payout_records(batch_txid)
--        WHERE batch_txid IS NOT NULL
SELECT id, vendor_id, invoice_id, net_satoshis, vout_index_in_batch, status
FROM payout_records
WHERE batch_txid = @batch_txid;


-- name: GetStaleConstructingPayouts :many
-- Stuck-sweep watchdog: return payout records stuck in 'constructing' longer
-- than the configured threshold (default 10 min). The watchdog reclaims them
-- back to 'queued' so the sweep job can retry.
-- Caller pre-computes @stale_before = NOW() - threshold duration.
-- Index: idx_pr_stale_constructing ON payout_records(network, updated_at)
--        WHERE status = 'constructing'
SELECT id, batch_id, updated_at
FROM payout_records
WHERE network    = @network
  AND status     = 'constructing'
  AND updated_at < @stale_before::timestamptz;


-- name: GetHeldAgingPayouts :many
-- Held-aging monitor: return held payouts older than the given age threshold.
-- Caller pre-computes @older_than = NOW() - age duration
-- (e.g. NOW() - 7*24h for WARNING, NOW() - 30*24h for CRITICAL).
-- Index: idx_pr_held_aging ON payout_records(network, created_at)
--        WHERE status = 'held'
SELECT id, vendor_id, net_satoshis, created_at
FROM payout_records
WHERE network    = @network
  AND status     = 'held'
  AND created_at < @older_than::timestamptz;


/* ════════════════════════════════════════════════════════════
   VENDOR BALANCE — STORED PROCEDURES
   Direct UPDATE on vendor_balances is revoked from btc_app_role.
   Always use these two queries to call the stored procedures.
   The procedures acquire the row-level lock internally — callers
   never need SELECT FOR UPDATE before calling them.
   ════════════════════════════════════════════════════════════ */

-- name: CreditVendorBalance :one
-- Credit the vendor's internal balance atomically via btc_credit_balance.
-- The stored procedure writes the immutable financial_audit_events row itself in
-- the same transaction, so there is no unaudited mutation path.
-- Raises an exception if no vendor_balances row exists for (vendor_id, network).
SELECT btc_credit_balance(
    @vendor_id::uuid,
    @network,
    @amount_sat::bigint,
    @actor_type,
    sqlc.narg('actor_id')::uuid,
    @actor_label,
    @invoice_id::uuid,
    sqlc.narg('payout_record_id')::uuid,
    sqlc.narg('references_event_id')::bigint,
    sqlc.narg('fiat_equivalent')::bigint,
    sqlc.narg('fiat_currency_code'),
    @rate_stale::boolean,
    @metadata::jsonb,
    @event_type
) AS new_balance_sat;


-- name: DebitVendorBalance :one
-- Debit the vendor's internal balance atomically via btc_debit_balance.
-- The stored procedure writes the immutable financial_audit_events row itself in
-- the same transaction, so there is no unaudited mutation path.
-- Raises SQLSTATE 23514 (ErrInsufficientBalance) when balance < amount.
-- Never retry on 23514 — it is a deterministic error.
SELECT btc_debit_balance(
    @vendor_id::uuid,
    @network,
    @amount_sat::bigint,
    @actor_type,
    sqlc.narg('actor_id')::uuid,
    @actor_label,
    sqlc.narg('invoice_id')::uuid,
    sqlc.narg('payout_record_id')::uuid,
    sqlc.narg('references_event_id')::bigint,
    sqlc.narg('fiat_equivalent')::bigint,
    sqlc.narg('fiat_currency_code'),
    @rate_stale::boolean,
    @metadata::jsonb,
    @event_type
) AS new_balance_sat;


-- name: GetVendorBalance :one
-- Read the current vendor balance. SELECT only — never UPDATE directly.
SELECT balance_satoshis, updated_at
FROM vendor_balances
WHERE vendor_id = @vendor_id::uuid
  AND network   = @network;


/* ════════════════════════════════════════════════════════════
   VENDOR WALLET CONFIG
   ════════════════════════════════════════════════════════════ */

-- name: GetVendorWalletConfig :one
-- Fetch the full wallet config for a vendor. Used at invoice creation and
-- settlement to read wallet_mode, bridge_destination_address, tier_id, etc.
SELECT *
FROM vendor_wallet_config
WHERE vendor_id = @vendor_id::uuid
  AND network   = @network;


-- name: GetActiveVendorTierOverride :one
-- Fetch the effective tier override for a vendor if one exists and has not
-- expired. Used at invoice creation to resolve the COALESCE snapshot.
-- NOTE: do NOT use a partial index with NOW() — indexes cannot have STABLE
-- function predicates. This uses a plain index and filters in the query.
-- Index: idx_vto_vendor_network ON vendor_tier_overrides(vendor_id, network)
SELECT *
FROM vendor_tier_overrides
WHERE vendor_id  = @vendor_id::uuid
  AND network    = @network
  AND (expires_at IS NULL OR expires_at > NOW());


/* ════════════════════════════════════════════════════════════
   PLATFORM CONFIG
   ════════════════════════════════════════════════════════════ */

-- name: GetPlatformConfig :one
-- Read the operational config for a network. Called by the sweep job to check
-- sweep_hold_mode before constructing any batch, and by the reconciliation job
-- to read reconciliation_start_height and treasury_reserve_satoshis.
SELECT *
FROM platform_config
WHERE network = @network;


-- name: SetSweepHold :exec
-- Activate the sweep hold emergency brake. Requires a written reason.
-- fn_ops_audit_platform_config trigger writes to ops_audit_log automatically.
-- Set app.current_actor_id and app.current_actor_label session variables before
-- executing so the ops audit entry captures who activated the hold.
UPDATE platform_config
SET
    sweep_hold_mode         = TRUE,
    sweep_hold_reason       = @reason,
    sweep_hold_activated_at = NOW(),
    updated_at              = NOW()
WHERE network = @network;


-- name: ClearSweepHold :execrows
-- Clear the sweep hold after admin investigation. Caller is responsible for
-- recording step-up authentication before executing.
-- fn_ops_audit_platform_config trigger writes to ops_audit_log automatically.
-- Returns 0 if the hold was already cleared (idempotent on re-run).
UPDATE platform_config
SET
    sweep_hold_mode         = FALSE,
    sweep_hold_reason       = NULL,
    sweep_hold_activated_at = NULL,
    updated_at              = NOW()
WHERE network         = @network
  AND sweep_hold_mode = TRUE;


-- name: IncrementTreasuryReserve :one
-- Increment the platform treasury reserve by the miner fees retained from a
-- confirmed sweep batch. The stored procedure writes the immutable
-- financial_audit_events row itself in the same transaction.
-- MUST be called in the SAME transaction as SetPayoutConfirmed so the
-- reconciliation formula remains balanced.
SELECT btc_credit_treasury_reserve(
    @network,
    @fee_amount_sat::bigint,
    @actor_type,
    sqlc.narg('actor_id')::uuid,
    @actor_label,
    sqlc.narg('payout_record_id')::uuid,
    sqlc.narg('references_event_id')::bigint,
    sqlc.narg('fiat_equivalent')::bigint,
    sqlc.narg('fiat_currency_code'),
    @rate_stale::boolean,
    @metadata::jsonb,
    @event_type
) AS new_treasury_reserve_sat;


-- name: DecrementTreasuryReserve :one
-- Decrement the platform treasury reserve through the same audited stored
-- procedure path used for increments. Intended for admin treasury withdrawals
-- or UTXO consolidation expenses.
SELECT btc_debit_treasury_reserve(
    @network,
    @amount_sat::bigint,
    @actor_type,
    sqlc.narg('actor_id')::uuid,
    @actor_label,
    sqlc.narg('payout_record_id')::uuid,
    sqlc.narg('references_event_id')::bigint,
    sqlc.narg('fiat_equivalent')::bigint,
    sqlc.narg('fiat_currency_code'),
    @rate_stale::boolean,
    @metadata::jsonb,
    @event_type
) AS new_treasury_reserve_sat;


/* ════════════════════════════════════════════════════════════
   RECONCILIATION
   ════════════════════════════════════════════════════════════ */

-- name: GetReconciliationJobState :one
SELECT * FROM reconciliation_job_state WHERE network = @network;


-- name: UpsertReconciliationJobState :exec
-- Update reconciliation state after each run.
-- Pass last_successful_run_at = NOW() on result = 'ok'.
-- Pass last_successful_run_at = NULL on failure — the COALESCE preserves
-- the previous successful timestamp so the staleness alert fires correctly.
INSERT INTO reconciliation_job_state (
    network,
    last_run_at,
    last_run_result,
    last_discrepancy_sat,
    last_successful_run_at
)
VALUES (
    @network,
    NOW(),
    @result::btc_reconciliation_result,
    sqlc.narg('discrepancy_sat')::bigint,
    sqlc.narg('last_successful_run_at')::timestamptz
)
ON CONFLICT (network) DO UPDATE SET
    last_run_at            = NOW(),
    last_run_result        = EXCLUDED.last_run_result,
    last_discrepancy_sat   = EXCLUDED.last_discrepancy_sat,
    last_successful_run_at = COALESCE(
                                 EXCLUDED.last_successful_run_at,
                                 reconciliation_job_state.last_successful_run_at
                             ),
    updated_at             = NOW();


-- name: InsertReconciliationRunHistory :one
-- Append a history record at the start of each run. Call CloseReconciliationRunHistory
-- at the end to fill finished_at and result.
INSERT INTO reconciliation_run_history (network, started_at)
VALUES (@network, NOW())
RETURNING id, network, started_at;


-- name: CloseReconciliationRunHistory :exec
-- Close an in-progress reconciliation run with its final result and discrepancy.
UPDATE reconciliation_run_history
SET
    finished_at     = NOW(),
    result          = @result::btc_reconciliation_result,
    discrepancy_sat = sqlc.narg('discrepancy_sat')::bigint,
    note            = sqlc.narg('note')
WHERE id = @run_id::bigint;


-- name: GetBitcoinSyncState :one
SELECT * FROM bitcoin_sync_state WHERE network = @network;


-- name: UpdateBitcoinSyncState :exec
-- Advance the block-height cursor inside a reconcileSegment transaction.
-- Updated every BTC_RECONCILIATION_CHECKPOINT_INTERVAL blocks so a crash
-- mid-backfill resumes from the checkpoint rather than the beginning.
UPDATE bitcoin_sync_state
SET
    last_processed_height = @height::bigint,
    updated_at            = NOW()
WHERE network = @network;


-- name: SumInflightInvoiceAmounts :one
-- Reconciliation formula term: sum of satoshis locked in non-terminal invoice states.
-- Index: idx_inv_network_status_inflight with INCLUDE (amount_sat, created_at)
-- enables an index-only scan — no heap fetch required.
SELECT COALESCE(SUM(amount_sat), 0)::bigint AS total_inflight_sat
FROM invoices
WHERE network = @network
  AND status  IN ('pending', 'detected', 'confirming', 'settling',
                  'underpaid', 'mempool_dropped');


-- name: SumPlatformVendorBalances :one
-- Reconciliation formula term: sum of all platform-mode vendor balances.
-- Hybrid-mode balances are intentionally EXCLUDED — they are threshold
-- accumulators only, not value-bearing. Their value is fully captured in
-- held/queued payout_records. Including both would double-count hybrid funds.
SELECT COALESCE(SUM(vb.balance_satoshis), 0)::bigint AS total_platform_balance_sat
FROM vendor_balances vb
JOIN vendor_wallet_config vwc
  ON vwc.vendor_id = vb.vendor_id
 AND vwc.network   = vb.network
WHERE vb.network      = @network
  AND vwc.wallet_mode = 'platform';


-- name: SumInflightPayoutRecords :one
-- Reconciliation formula term: sum of pre-confirmation payout obligations.
-- Includes held, queued, constructing, broadcast — all states where on-chain
-- funds are still owed to vendors but not yet confirmed.
SELECT COALESCE(SUM(net_satoshis), 0)::bigint AS total_inflight_payout_sat
FROM payout_records
WHERE network = @network
  AND status  IN ('held', 'queued', 'constructing', 'broadcast');


/* ════════════════════════════════════════════════════════════
   BITCOIN BLOCK HISTORY
   ════════════════════════════════════════════════════════════ */

-- name: UpsertBlockHistory :exec
-- Record a processed block. Pruned blocks use block_hash = NULL, pruned = TRUE.
-- ON CONFLICT DO NOTHING: the PK is (height, network) — duplicate processing
-- attempts are silently ignored for idempotency.
INSERT INTO bitcoin_block_history (height, network, block_hash, pruned)
VALUES (
    @height::bigint,
    @network,
    sqlc.narg('block_hash'),
    @pruned::boolean
)
ON CONFLICT (height, network) DO NOTHING;


-- name: GetBlockHistoryRange :many
-- Fetch processed blocks in a height range for reconciliation gap detection.
-- Index: idx_bbh_network_height ON bitcoin_block_history(network, height DESC)
SELECT height, network, block_hash, pruned, processed_at
FROM bitcoin_block_history
WHERE network = @network
  AND height  >= @from_height::bigint
  AND height  <= @to_height::bigint
ORDER BY height ASC;


/* ════════════════════════════════════════════════════════════
   OUTAGE LOG
   ════════════════════════════════════════════════════════════ */

-- name: InsertOutageRecord :one
-- Record the start of a node outage.
-- Use pg_try_advisory_lock on the caller side to prevent concurrent duplicate
-- INSERTs from horizontal instances. The unique partial index
-- uq_outage_one_open_per_network (WHERE ended_at IS NULL) is the DB-level
-- backstop if the lock is skipped.
-- ON CONFLICT DO NOTHING: a concurrent instance already inserted — log and continue.
INSERT INTO btc_outage_log (network)
VALUES (@network)
ON CONFLICT DO NOTHING
RETURNING id, network, started_at;


-- name: CloseOutageRecord :execrows
-- Record the end of an outage. Caller MUST assert RowsAffected() == 1.
UPDATE btc_outage_log
SET ended_at = NOW()
WHERE id       = @outage_id::bigint
  AND ended_at IS NULL;


-- name: GetOpenOutage :one
-- Hot-path check: "is there an open outage for this network?"
-- Also used on startup to detect outage records left by a crashed process.
-- Index: uq_outage_one_open_per_network (unique partial WHERE ended_at IS NULL)
SELECT id, network, started_at
FROM btc_outage_log
WHERE network  = @network
  AND ended_at IS NULL;


-- name: CloseStaleOutages :exec
-- Maintenance job (runs every 6 hours): close outage records older than 48 hours.
-- Caps ended_at at started_at + 48h so the formula does not overcount.
UPDATE btc_outage_log
SET ended_at = LEAST(NOW(), started_at + INTERVAL '48 hours')
WHERE ended_at   IS NULL
  AND started_at  < NOW() - INTERVAL '48 hours';


-- name: GetOutageOverlapForInvoice :one
-- Expiry compensation formula: total outage duration that overlapped with
-- an invoice's life. The expiry cleanup job adds this to expires_at before
-- deciding whether to mark the invoice expired.
--
-- Overlap = LEAST(outage_end, invoice_expires_at)
--           - GREATEST(outage_start, invoice_created_at)
-- Summed across all outages that touch the invoice window.
-- Returns INTERVAL '0' when no overlapping outages exist.
-- Index: idx_outage_range ON btc_outage_log(network, started_at, ended_at)
SELECT COALESCE(
    SUM(
        LEAST(COALESCE(o.ended_at, NOW()), @expires_at::timestamptz)
        - GREATEST(o.started_at, @invoice_created_at::timestamptz)
    ),
    '0 seconds'::interval
) AS total_outage_overlap
FROM btc_outage_log o
WHERE o.network    = @network
  AND o.started_at < @expires_at::timestamptz
  AND COALESCE(o.ended_at, NOW()) > @invoice_created_at::timestamptz;


/* ════════════════════════════════════════════════════════════
   EXCHANGE RATE LOG
   ════════════════════════════════════════════════════════════ */

-- name: InsertExchangeRate :one
-- Record a rate fetch. Written on every successful fetch from the provider
-- regardless of whether the rate changed — this gives a complete audit trail.
INSERT INTO btc_exchange_rate_log (
    network,
    fiat_currency,
    rate,
    source,
    anomaly_flag,
    anomaly_reason
)
VALUES (
    @network,
    @fiat_currency,
    @rate::numeric,
    @source,
    @anomaly_flag::boolean,
    sqlc.narg('anomaly_reason')
)
RETURNING id, network, fiat_currency, rate, source, fetched_at, anomaly_flag;


-- name: GetLatestExchangeRate :one
-- Invoice creation hot path: fetch the most recent rate for a (network, currency).
-- Index: idx_ber_network_currency_time ON btc_exchange_rate_log(network, fiat_currency, fetched_at DESC)
SELECT id, rate, source, fetched_at, anomaly_flag
FROM btc_exchange_rate_log
WHERE network       = @network
  AND fiat_currency = @fiat_currency
ORDER BY fetched_at DESC
LIMIT 1;


/* ════════════════════════════════════════════════════════════
   ZMQ DEAD LETTER
   ════════════════════════════════════════════════════════════ */

-- name: InsertZMQDeadLetter :one
-- Record an unmatched ZMQ event rather than silently dropping it.
-- Covers: late payments, double-spend attempts, retired addresses, unknown txids.
-- Not all dead letters are errors — late payments on expired invoices are expected.
INSERT INTO btc_zmq_dead_letter (
    network,
    event_type,
    raw_payload,
    reason
)
VALUES (
    @network,
    @event_type,
    @raw_payload,
    @reason
)
RETURNING id, network, event_type, raw_payload, reason, received_at;


-- name: GetUnresolvedDeadLetters :many
-- Periodic review: return unresolved dead letters for investigation.
-- Index: idx_zdl_unresolved ON btc_zmq_dead_letter(network, received_at DESC)
--        WHERE resolved = FALSE
SELECT id, network, event_type, raw_payload, reason, received_at
FROM btc_zmq_dead_letter
WHERE network  = @network
  AND resolved = FALSE
ORDER BY received_at DESC
LIMIT 100;


-- name: ResolveDeadLetter :execrows
-- Mark a dead letter as investigated and resolved with a note.
UPDATE btc_zmq_dead_letter
SET
    resolved        = TRUE,
    resolved_at     = NOW(),
    resolution_note = @resolution_note
WHERE id       = @dead_letter_id::bigint
  AND resolved = FALSE;


/* ════════════════════════════════════════════════════════════
   FINANCIAL AUDIT EVENTS
   INSERT and SELECT only — UPDATE and DELETE are blocked by
   fn_btc_audit_immutable and fn_btc_audit_no_truncate triggers.

   GDPR note: do NOT store raw IPs here (immutable table = can't erase).
   Use sse_token_issuances for IP tracking.
   For actor_label: store HMAC-SHA256(email, server_secret) not raw email,
   so nothing needs to be erased here on GDPR Article 17 requests.
   ════════════════════════════════════════════════════════════ */

-- name: InsertFinancialAuditEvent :one
-- Append an event to the immutable financial audit trail.
-- fn_fae_validate_actor_label trigger verifies actor_label matches
-- COALESCE(email, username) for the given actor_id. Use actor_type = 'system'
-- for background job events (skips the label validation).
-- For balance-change events, BOTH balance_before_sat and balance_after_sat
-- MUST be set — chk_fae_balance_required_for_credit_debit enforces this.
INSERT INTO financial_audit_events (
    event_type,
    network,
    actor_type,
    actor_id,
    actor_label,
    invoice_id,
    payout_record_id,
    references_event_id,
    amount_sat,
    balance_before_sat,
    balance_after_sat,
    fiat_equivalent,
    fiat_currency_code,
    rate_stale,
    metadata
)
VALUES (
    @event_type,
    sqlc.narg('network'),
    @actor_type,
    sqlc.narg('actor_id')::uuid,
    @actor_label,
    sqlc.narg('invoice_id')::uuid,
    sqlc.narg('payout_record_id')::uuid,
    sqlc.narg('references_event_id')::bigint,
    sqlc.narg('amount_sat')::bigint,
    sqlc.narg('balance_before_sat')::bigint,
    sqlc.narg('balance_after_sat')::bigint,
    sqlc.narg('fiat_equivalent')::bigint,
    sqlc.narg('fiat_currency_code'),
    @rate_stale::boolean,
    @metadata::jsonb
)
RETURNING id, event_type, timestamp, network, actor_type, actor_id, actor_label,
          invoice_id, payout_record_id, amount_sat, balance_before_sat, balance_after_sat;


-- name: GetAuditEventsForInvoice :many
-- Return all financial audit events for an invoice, most recent first.
-- Index: idx_fae_invoice ON financial_audit_events(invoice_id, timestamp DESC)
--        WHERE invoice_id IS NOT NULL
SELECT id, event_type, timestamp, actor_type, actor_label, amount_sat,
       balance_before_sat, balance_after_sat, metadata
FROM financial_audit_events
WHERE invoice_id = @invoice_id::uuid
ORDER BY timestamp DESC;


-- name: GetAuditEventsForPayout :many
-- Return all financial audit events for a payout record, most recent first.
-- Index: idx_fae_payout ON financial_audit_events(payout_record_id, timestamp DESC)
--        WHERE payout_record_id IS NOT NULL
SELECT id, event_type, timestamp, actor_type, actor_label, amount_sat, metadata
FROM financial_audit_events
WHERE payout_record_id = @payout_record_id::uuid
ORDER BY timestamp DESC;


/* ════════════════════════════════════════════════════════════
   WATCH RESOURCES
   Address and transaction watches owned by users.
   ════════════════════════════════════════════════════════════ */

-- name: CreateBitcoinWatch :one
-- Create one active watch resource.
-- On 23505 (uq_bw_active_address / uq_bw_active_transaction): the user already
-- has an active watch for this target. Return ErrWatchExists; do NOT retry.
INSERT INTO btc_watches (
    user_id,
    network,
    watch_type,
    address,
    txid,
    status
)
VALUES (
    @user_id::uuid,
    @network,
    @watch_type,
    sqlc.narg('address'),
    sqlc.narg('txid'),
    'active'
)
RETURNING *;


-- name: GetBitcoinWatchByID :one
-- Return one active watch owned by the given user.
SELECT * FROM btc_watches
WHERE id      = @id::bigint
  AND user_id = @user_id::uuid;


-- name: ListBitcoinWatches :many
-- List active watch resources for one user/network with optional type/address/txid filters.
-- The base scan follows created_at DESC ordering. Exact address/txid filters are
-- backed by the partial unique indexes; watch_type-filtered lists use idx_bw_user_type_time.
-- Keyset pagination uses (created_at, id) DESC. Pass the previous page's final
-- row as (@before_created_at, @before_id) to continue without OFFSET scans.
SELECT * FROM btc_watches
WHERE user_id = @user_id::uuid
  AND network = @network
  AND (@watch_type = '' OR watch_type = @watch_type)
  AND (@address = '' OR address = @address)
  AND (@txid = '' OR txid = @txid)
  AND (
      sqlc.narg('before_created_at')::timestamptz IS NULL
      OR (created_at, id) < (sqlc.narg('before_created_at')::timestamptz, @before_id::bigint)
  )
ORDER BY created_at DESC, id DESC
LIMIT @limit_rows::integer;


-- name: UpdateBitcoinWatchByID :one
-- Update one active watch resource owned by the given user.
-- watch_type is immutable after creation. The WHERE clause enforces that the
-- caller may update the target within the same type, but may not switch an
-- address watch into a transaction watch (or the reverse) mid-lifecycle.
UPDATE btc_watches
SET
    address    = sqlc.narg('address'),
    txid       = sqlc.narg('txid'),
    updated_at = NOW()
WHERE id      = @id::bigint
  AND user_id = @user_id::uuid
  AND watch_type = @watch_type
RETURNING *;


-- name: DeleteBitcoinWatchByID :execrows
-- Hard-delete one watch resource owned by the given user.
DELETE FROM btc_watches
WHERE id      = @id::bigint
  AND user_id = @user_id::uuid;


-- name: CountActiveBitcoinAddressWatchesByUser :one
-- Return the active address-watch count for one user/network.
-- Backed by idx_bw_user_network_address_count so quota checks stay cheap even
-- when transaction watches greatly outnumber address watches.
SELECT COUNT(*) AS count
FROM btc_watches
WHERE user_id    = @user_id::uuid
  AND network    = @network
  AND watch_type = 'address';


-- name: ListActiveBitcoinWatchAddressesByUser :many
-- Return active address-watch targets for one user/network.
SELECT address
FROM btc_watches
WHERE user_id    = @user_id::uuid
  AND network    = @network
  AND watch_type = 'address'
ORDER BY address ASC;


-- name: ListActiveBitcoinTransactionWatchUsersByTxID :many
-- Return users with an active transaction watch on txid.
-- uq_bw_active_transaction guarantees one row per (user_id, network, txid), so
-- DISTINCT would only add an unnecessary hash/sort step on this fanout path.
SELECT user_id
FROM btc_watches
WHERE network    = @network
  AND txid       = @txid
  AND watch_type = 'transaction';


/* ════════════════════════════════════════════════════════════
   TRACKED TRANSACTIONS
   Durable read model owned by the txstatus package and updated by events.
   ════════════════════════════════════════════════════════════ */

-- name: CreateBitcoinTxStatus :one
-- Create one explicit txstatus tracking row. Used by POST /bitcoin/tx.
-- tracking_mode must be 'txid'; watch-discovered related addresses are created by events.
INSERT INTO btc_tracked_transactions (
    user_id,
    network,
    tracking_mode,
    address,
    txid,
    status,
    confirmations,
    amount_sat,
    fee_rate_sat_vbyte,
    first_seen_at,
    last_seen_at,
    confirmed_at,
    block_hash,
    block_height,
    replacement_txid
)
VALUES (
    @user_id::uuid,
    @network,
    'txid',
    sqlc.narg('address'),
    @txid,
    @status,
    @confirmations::integer,
    @amount_sat::bigint,
    @fee_rate_sat_vbyte,
    @first_seen_at::timestamptz,
    @last_seen_at::timestamptz,
    sqlc.narg('confirmed_at')::timestamptz,
    sqlc.narg('block_hash'),
    sqlc.narg('block_height')::bigint,
    sqlc.narg('replacement_txid')
)
RETURNING
    id,
    user_id,
    network,
    tracking_mode,
    address,
    txid,
    status,
    confirmations,
    amount_sat,
    fee_rate_sat_vbyte,
    first_seen_at,
    last_seen_at,
    confirmed_at,
    block_hash,
    block_height,
    replacement_txid,
    created_at,
    updated_at;


-- name: UpsertTrackedBitcoinTxStatus :one
-- Create or refresh one explicit txid-tracking row keyed by (user, network, txid).
INSERT INTO btc_tracked_transactions (
    user_id,
    network,
    tracking_mode,
    address,
    txid,
    status,
    confirmations,
    amount_sat,
    fee_rate_sat_vbyte,
    first_seen_at,
    last_seen_at,
    confirmed_at,
    block_hash,
    block_height,
    replacement_txid
)
VALUES (
    @user_id::uuid,
    @network,
    'txid',
    sqlc.narg('address'),
    @txid,
    @status,
    @confirmations::integer,
    @amount_sat::bigint,
    @fee_rate_sat_vbyte,
    @first_seen_at::timestamptz,
    @last_seen_at::timestamptz,
    sqlc.narg('confirmed_at')::timestamptz,
    sqlc.narg('block_hash'),
    sqlc.narg('block_height')::bigint,
    sqlc.narg('replacement_txid')
)
ON CONFLICT (user_id, network, txid) DO UPDATE
SET
    tracking_mode      = CASE
        WHEN btc_tracked_transactions.tracking_mode = 'watch' THEN 'txid'
        ELSE btc_tracked_transactions.tracking_mode
    END,
    address            = COALESCE(EXCLUDED.address, btc_tracked_transactions.address),
    status             = CASE
        WHEN btc_tracked_transactions.status IN ('replaced', 'conflicting', 'abandoned')
            THEN btc_tracked_transactions.status
        WHEN btc_tracked_transactions.status = 'confirmed'
             AND EXCLUDED.status <> 'confirmed'
            THEN btc_tracked_transactions.status
        ELSE EXCLUDED.status
    END,
    confirmations      = CASE
        WHEN btc_tracked_transactions.status IN ('replaced', 'conflicting', 'abandoned')
            THEN btc_tracked_transactions.confirmations
        WHEN btc_tracked_transactions.status = 'confirmed'
             AND EXCLUDED.status <> 'confirmed'
            THEN btc_tracked_transactions.confirmations
        WHEN EXCLUDED.status = 'confirmed'
            THEN GREATEST(EXCLUDED.confirmations, 1)
        ELSE EXCLUDED.confirmations
    END,
    amount_sat         = CASE
        WHEN EXCLUDED.amount_sat > 0 THEN EXCLUDED.amount_sat
        ELSE btc_tracked_transactions.amount_sat
    END,
    fee_rate_sat_vbyte = CASE
        WHEN EXCLUDED.fee_rate_sat_vbyte > 0 THEN EXCLUDED.fee_rate_sat_vbyte
        ELSE btc_tracked_transactions.fee_rate_sat_vbyte
    END,
    first_seen_at      = LEAST(btc_tracked_transactions.first_seen_at, EXCLUDED.first_seen_at),
    last_seen_at       = CASE
        WHEN btc_tracked_transactions.status IN ('replaced', 'conflicting', 'abandoned')
            THEN btc_tracked_transactions.last_seen_at
        WHEN btc_tracked_transactions.status = 'confirmed'
             AND EXCLUDED.status <> 'confirmed'
            THEN btc_tracked_transactions.last_seen_at
        ELSE EXCLUDED.last_seen_at
    END,
    confirmed_at       = CASE
        WHEN btc_tracked_transactions.status IN ('replaced', 'conflicting', 'abandoned')
            THEN btc_tracked_transactions.confirmed_at
        WHEN btc_tracked_transactions.status = 'confirmed'
             AND EXCLUDED.status <> 'confirmed'
            THEN btc_tracked_transactions.confirmed_at
        ELSE COALESCE(btc_tracked_transactions.confirmed_at, EXCLUDED.confirmed_at)
    END,
    block_hash         = CASE
        WHEN btc_tracked_transactions.status IN ('replaced', 'conflicting', 'abandoned')
            THEN btc_tracked_transactions.block_hash
        WHEN btc_tracked_transactions.status = 'confirmed'
             AND EXCLUDED.status <> 'confirmed'
            THEN btc_tracked_transactions.block_hash
        ELSE COALESCE(btc_tracked_transactions.block_hash, EXCLUDED.block_hash)
    END,
    block_height       = CASE
        WHEN btc_tracked_transactions.status IN ('replaced', 'conflicting', 'abandoned')
            THEN btc_tracked_transactions.block_height
        WHEN btc_tracked_transactions.status = 'confirmed'
             AND EXCLUDED.status <> 'confirmed'
            THEN btc_tracked_transactions.block_height
        ELSE COALESCE(btc_tracked_transactions.block_height, EXCLUDED.block_height)
    END,
    replacement_txid   = CASE
        WHEN btc_tracked_transactions.status = 'replaced'
            THEN btc_tracked_transactions.replacement_txid
        WHEN btc_tracked_transactions.status IN ('conflicting', 'abandoned')
            THEN btc_tracked_transactions.replacement_txid
        WHEN btc_tracked_transactions.status = 'confirmed'
             AND EXCLUDED.status <> 'confirmed'
            THEN btc_tracked_transactions.replacement_txid
        ELSE COALESCE(EXCLUDED.replacement_txid, btc_tracked_transactions.replacement_txid)
    END
RETURNING
    id,
    user_id,
    network,
    tracking_mode,
    address,
    txid,
    status,
    confirmations,
    amount_sat,
    fee_rate_sat_vbyte,
    first_seen_at,
    last_seen_at,
    confirmed_at,
    block_hash,
    block_height,
    replacement_txid,
    created_at,
    updated_at;


-- name: GetBitcoinTxStatusByID :one
-- Return one explicit-txid txstatus row owned by the given user.
SELECT
    id,
    user_id,
    network,
    tracking_mode,
    address,
    txid,
    status,
    confirmations,
    amount_sat,
    fee_rate_sat_vbyte,
    first_seen_at,
    last_seen_at,
    confirmed_at,
    block_hash,
    block_height,
    replacement_txid,
    created_at,
    updated_at
FROM btc_tracked_transactions
WHERE id      = @id::bigint
  AND user_id = @user_id::uuid
  AND tracking_mode = 'txid';


-- name: ListBitcoinTxStatuses :many
-- List txstatus rows for one user/network with optional address, txid, and mode filters.
-- High-load shape:
--   * @address = ''  → one ordered scan over btc_tracked_transactions
--   * @address != '' → UNION ALL of:
--       1. explicit parent-address matches (idx_btt_user_address_time)
--       2. child-address matches from btc_tracked_transaction_addresses (idx_btta_address)
--          excluding rows already returned by the explicit-address branch
-- Keyset pagination uses (COALESCE(confirmed_at, first_seen_at), id) DESC via
-- (@before_sort_time, @before_id) so deep pages do not degrade into OFFSET scans.
-- This avoids a broad OR + correlated EXISTS over the full parent row set.
WITH filtered_tx_statuses AS (
    SELECT
        btt.id,
        btt.user_id,
        btt.network,
        btt.tracking_mode,
        btt.address,
        btt.txid,
        btt.status,
        btt.confirmations,
        btt.amount_sat,
        btt.fee_rate_sat_vbyte,
        btt.first_seen_at,
        btt.last_seen_at,
        btt.confirmed_at,
        btt.block_hash,
        btt.block_height,
        btt.replacement_txid,
        btt.created_at,
        btt.updated_at
    FROM btc_tracked_transactions btt
    WHERE btt.user_id       = @user_id::uuid
      AND btt.network       = @network
      AND @address::text = ''
      AND (@txid::text = '' OR btt.txid = @txid::text)
      AND (@tracking_mode::text = '' OR btt.tracking_mode = @tracking_mode::text)

    UNION ALL

    SELECT
        btt.id,
        btt.user_id,
        btt.network,
        btt.tracking_mode,
        btt.address,
        btt.txid,
        btt.status,
        btt.confirmations,
        btt.amount_sat,
        btt.fee_rate_sat_vbyte,
        btt.first_seen_at,
        btt.last_seen_at,
        btt.confirmed_at,
        btt.block_hash,
        btt.block_height,
        btt.replacement_txid,
        btt.created_at,
        btt.updated_at
    FROM btc_tracked_transactions btt
    WHERE btt.user_id       = @user_id::uuid
      AND btt.network       = @network
      AND @address::text    <> ''
      AND btt.address       = @address::text
      AND (@txid::text = '' OR btt.txid = @txid::text)
      AND (@tracking_mode::text = '' OR btt.tracking_mode = @tracking_mode::text)

    UNION ALL

    SELECT
        btt.id,
        btt.user_id,
        btt.network,
        btt.tracking_mode,
        btt.address,
        btt.txid,
        btt.status,
        btt.confirmations,
        btt.amount_sat,
        btt.fee_rate_sat_vbyte,
        btt.first_seen_at,
        btt.last_seen_at,
        btt.confirmed_at,
        btt.block_hash,
        btt.block_height,
        btt.replacement_txid,
        btt.created_at,
        btt.updated_at
    FROM btc_tracked_transactions btt
    JOIN btc_tracked_transaction_addresses ra
      ON ra.tracked_transaction_id = btt.id
    WHERE btt.user_id       = @user_id::uuid
      AND btt.network       = @network
      AND @address::text    <> ''
      AND ra.address        = @address::text
      AND (@txid::text = '' OR btt.txid = @txid::text)
      AND (@tracking_mode::text = '' OR btt.tracking_mode = @tracking_mode::text)
      AND btt.address IS DISTINCT FROM @address::text
)
SELECT
    id::bigint AS id,
    user_id,
    network,
    tracking_mode,
    address,
    txid,
    status,
    confirmations,
    amount_sat,
    fee_rate_sat_vbyte,
    first_seen_at,
    last_seen_at,
    confirmed_at,
    block_hash,
    block_height,
    replacement_txid,
    created_at,
    updated_at
FROM filtered_tx_statuses
WHERE (
    sqlc.narg('before_sort_time')::timestamptz IS NULL
    OR (COALESCE(confirmed_at, first_seen_at), id) < (sqlc.narg('before_sort_time')::timestamptz, @before_id::bigint)
)
ORDER BY COALESCE(confirmed_at, first_seen_at) DESC, id DESC
LIMIT @limit_rows::integer;


-- name: UpdateBitcoinTxStatusByID :one
-- Update one explicit txid tracking row. Watch-managed rows are immutable here.
-- txid is intentionally excluded from SET because it is immutable after creation.
-- The query also normalizes status-dependent fields so callers cannot persist
-- mempool/not_found/conflicting/abandoned rows with stale confirmation metadata.
UPDATE btc_tracked_transactions
SET
    address             = sqlc.narg('address'),
    status              = @status,
    confirmations       = CASE
        WHEN @status = 'confirmed' THEN GREATEST(@confirmations::integer, 1)
        ELSE 0
    END,
    amount_sat          = @amount_sat::bigint,
    fee_rate_sat_vbyte  = @fee_rate_sat_vbyte,
    first_seen_at       = @first_seen_at::timestamptz,
    last_seen_at        = @last_seen_at::timestamptz,
    confirmed_at        = CASE
        WHEN @status = 'confirmed' THEN sqlc.narg('confirmed_at')::timestamptz
        ELSE NULL
    END,
    block_hash          = CASE
        WHEN @status = 'confirmed' THEN sqlc.narg('block_hash')
        ELSE NULL
    END,
    block_height        = CASE
        WHEN @status = 'confirmed' THEN sqlc.narg('block_height')::bigint
        ELSE NULL
    END,
    replacement_txid    = CASE
        WHEN @status = 'replaced' THEN sqlc.narg('replacement_txid')
        ELSE NULL
    END
WHERE id            = @id::bigint
  AND user_id       = @user_id::uuid
  AND tracking_mode = 'txid'
RETURNING
    id,
    user_id,
    network,
    tracking_mode,
    address,
    txid,
    status,
    confirmations,
    amount_sat,
    fee_rate_sat_vbyte,
    first_seen_at,
    last_seen_at,
    confirmed_at,
    block_hash,
    block_height,
    replacement_txid,
    created_at,
    updated_at;


-- name: DeleteBitcoinTxStatusByID :execrows
-- Hard-delete one explicit txstatus row owned by the given user.
DELETE FROM btc_tracked_transactions
WHERE id      = @id::bigint
  AND user_id = @user_id::uuid
  AND tracking_mode = 'txid';


-- name: ListBitcoinTxStatusRelatedAddressesByStatusIDs :many
-- Return related watched addresses for the given txstatus row IDs.
SELECT
    tracked_transaction_id AS tx_status_id,
    address,
    amount_sat,
    created_at,
    updated_at
FROM btc_tracked_transaction_addresses
WHERE tracked_transaction_id = ANY(@tx_status_ids::bigint[])
ORDER BY tracked_transaction_id, address;


-- name: UpsertWatchBitcoinTxStatus :one
-- Create or refresh one watch-discovered txstatus row.
-- One durable row exists per (user_id, network, txid); matched addresses are
-- linked separately through btc_tx_status_related_addresses.
INSERT INTO btc_tracked_transactions (
    user_id,
    network,
    tracking_mode,
    txid,
    status,
    confirmations,
    amount_sat,
    fee_rate_sat_vbyte,
    first_seen_at,
    last_seen_at
)
VALUES (
    @user_id::uuid,
    @network,
    'watch',
    @txid,
    'mempool',
    0,
    @amount_sat::bigint,
    @fee_rate_sat_vbyte,
    @first_seen_at::timestamptz,
    @last_seen_at::timestamptz
)
ON CONFLICT (user_id, network, txid) DO UPDATE
SET
    amount_sat         = CASE
        WHEN EXCLUDED.amount_sat > 0 THEN EXCLUDED.amount_sat
        ELSE btc_tracked_transactions.amount_sat
    END,
    fee_rate_sat_vbyte = CASE
        WHEN EXCLUDED.fee_rate_sat_vbyte > 0 THEN EXCLUDED.fee_rate_sat_vbyte
        ELSE btc_tracked_transactions.fee_rate_sat_vbyte
    END,
    first_seen_at      = LEAST(btc_tracked_transactions.first_seen_at, EXCLUDED.first_seen_at),
    last_seen_at       = CASE
        WHEN btc_tracked_transactions.status IN ('confirmed', 'replaced', 'conflicting', 'abandoned')
            THEN btc_tracked_transactions.last_seen_at
        ELSE EXCLUDED.last_seen_at
    END,
    status             = CASE
        WHEN btc_tracked_transactions.status IN ('confirmed', 'replaced', 'conflicting', 'abandoned')
            THEN btc_tracked_transactions.status
        ELSE 'mempool'
    END,
    confirmations      = CASE
        WHEN btc_tracked_transactions.status = 'confirmed' THEN btc_tracked_transactions.confirmations
        ELSE 0
    END,
    confirmed_at       = CASE
        WHEN btc_tracked_transactions.status = 'confirmed' THEN btc_tracked_transactions.confirmed_at
        ELSE NULL
    END,
    block_hash         = CASE
        WHEN btc_tracked_transactions.status = 'confirmed' THEN btc_tracked_transactions.block_hash
        ELSE NULL
    END,
    block_height       = CASE
        WHEN btc_tracked_transactions.status = 'confirmed' THEN btc_tracked_transactions.block_height
        ELSE NULL
    END,
    replacement_txid   = CASE
        WHEN btc_tracked_transactions.status = 'replaced' THEN btc_tracked_transactions.replacement_txid
        ELSE NULL
    END
RETURNING
    id,
    user_id,
    network,
    tracking_mode,
    address,
    txid,
    status,
    confirmations,
    amount_sat,
    fee_rate_sat_vbyte,
    first_seen_at,
    last_seen_at,
    confirmed_at,
    block_hash,
    block_height,
    replacement_txid,
    created_at,
    updated_at;


-- name: UpsertBitcoinTxStatusRelatedAddress :exec
-- Create or refresh one watched address linked to a tracked tx row.
INSERT INTO btc_tracked_transaction_addresses (
    tracked_transaction_id,
    address,
    amount_sat
)
VALUES (
    @tx_status_id::bigint,
    @address,
    @amount_sat::bigint
)
ON CONFLICT (tracked_transaction_id, address) DO UPDATE
SET
    amount_sat = EXCLUDED.amount_sat
WHERE btc_tracked_transaction_addresses.amount_sat IS DISTINCT FROM EXCLUDED.amount_sat;


-- name: TouchBitcoinTxStatusMempool :execrows
-- Mark all rows for (user_id, network, txid) as present in mempool.
UPDATE btc_tracked_transactions
SET
    status             = 'mempool',
    confirmations      = 0,
    fee_rate_sat_vbyte = @fee_rate_sat_vbyte,
    last_seen_at       = @last_seen_at::timestamptz,
    confirmed_at       = NULL,
    block_hash         = NULL,
    block_height       = NULL,
    replacement_txid   = NULL
WHERE user_id = @user_id::uuid
  AND network = @network
  AND txid    = @txid
  AND status NOT IN ('confirmed', 'replaced', 'conflicting', 'abandoned');


-- name: ConfirmBitcoinTxStatus :execrows
-- Mark all live rows for (user_id, network, txid) as confirmed.
-- @confirmations MUST be >= 1. GREATEST() coerces 0 to 1 as a safety net,
-- but callers should treat 0 as a bug in the confirmation source.
-- confirmed_at / block_hash / block_height capture first confirmation metadata
-- and are therefore preserved once set.
UPDATE btc_tracked_transactions
SET
    status           = 'confirmed',
    confirmations    = GREATEST(@confirmations::integer, 1),
    confirmed_at     = COALESCE(btc_tracked_transactions.confirmed_at, @confirmed_at::timestamptz),
    block_hash       = COALESCE(btc_tracked_transactions.block_hash, @block_hash),
    block_height     = COALESCE(btc_tracked_transactions.block_height, @block_height::bigint),
    last_seen_at     = @confirmed_at::timestamptz,
    replacement_txid = NULL
WHERE user_id = @user_id::uuid
  AND network = @network
  AND txid    = @txid
  AND status NOT IN ('replaced', 'conflicting', 'abandoned');


-- name: MarkBitcoinTxStatusReplaced :execrows
-- Mark all rows for (user_id, network, replaced_txid) as replaced.
-- An already-replaced row is terminal here so a late duplicate event cannot
-- overwrite the first replacement_txid that established the chain.
UPDATE btc_tracked_transactions
SET
    status           = 'replaced',
    confirmations    = 0,
    replacement_txid = @replacement_txid,
    last_seen_at     = @replaced_at::timestamptz,
    confirmed_at     = NULL,
    block_hash       = NULL,
    block_height     = NULL
WHERE user_id = @user_id::uuid
  AND network = @network
  AND txid    = @replaced_txid
  AND status NOT IN ('confirmed', 'replaced', 'conflicting', 'abandoned');


-- name: ListBitcoinTxStatusUsersByTxID :many
-- Return user_ids that currently track the given txid.
-- uq_btt_user_network_txid guarantees one row per (user_id, network, txid), so
-- DISTINCT would only add planner work with no deduplication benefit.
SELECT user_id
FROM btc_tracked_transactions
WHERE network = @network
  AND txid    = @txid;


/* ════════════════════════════════════════════════════════════
   WEBHOOK DELIVERIES — transactional outbox
   Always write in the same DB transaction as the state change.
   ════════════════════════════════════════════════════════════ */

-- name: InsertWebhookDelivery :one
-- Write a vendor notification to the outbox. MUST be in the same transaction
-- as the triggering state change to guarantee at-least-once delivery.
INSERT INTO webhook_deliveries (
    vendor_id,
    event_type,
    payload,
    invoice_id,
    payout_record_id,
    max_attempts
)
VALUES (
    @vendor_id::uuid,
    @event_type,
    @payload::jsonb,
    sqlc.narg('invoice_id')::uuid,
    sqlc.narg('payout_record_id')::uuid,
    @max_attempts::integer
)
RETURNING id, vendor_id, event_type, status, attempt_count, max_attempts, created_at;


-- name: GetPendingWebhookDeliveries :many
-- Delivery worker poll: return pending deliveries due for attempt.
-- Oldest-due deliveries (next_retry_at ASC NULLS FIRST) are processed first.
-- Index: idx_wd_pending ON webhook_deliveries(next_retry_at)
--        WHERE status = 'pending'
SELECT id, vendor_id, event_type, payload, attempt_count, max_attempts, last_error
FROM webhook_deliveries
WHERE status = 'pending'
  AND (next_retry_at IS NULL OR next_retry_at <= NOW())
ORDER BY next_retry_at ASC NULLS FIRST
LIMIT 100;


-- name: MarkWebhookDelivered :execrows
-- Mark a delivery as successfully acknowledged by the vendor's endpoint.
UPDATE webhook_deliveries
SET
    status        = 'delivered',
    delivered_at  = NOW(),
    attempt_count = attempt_count + 1
WHERE id     = @delivery_id::uuid
  AND status = 'pending';


-- name: MarkWebhookAttempted :execrows
-- Record a failed delivery attempt. The caller computes next_retry_at with
-- exponential backoff. Pass status = 'dead_lettered' when
-- attempt_count + 1 >= max_attempts.
UPDATE webhook_deliveries
SET
    status        = @new_status,
    attempt_count = attempt_count + 1,
    next_retry_at = sqlc.narg('next_retry_at')::timestamptz,
    last_error    = @last_error
WHERE id     = @delivery_id::uuid
  AND status = 'pending';


/* ════════════════════════════════════════════════════════════
   SSE TOKEN ISSUANCES
   Pseudonymised IP hashes — erasable for GDPR Article 17.
   ════════════════════════════════════════════════════════════ */

-- name: InsertSSETokenIssuance :one
-- Record an SSE token issuance with pseudonymised hashes.
-- jti_hash       = HMAC-SHA256(jti, server_secret) — computed by caller.
-- source_ip_hash = SHA256(ip || daily_rotation_key) — computed by caller.
-- uq_sti_jti_hash UNIQUE index catches any accidental duplicate issuances.
INSERT INTO sse_token_issuances (
    vendor_id,
    network,
    jti_hash,
    source_ip_hash,
    expires_at
)
VALUES (
    @vendor_id::uuid,
    @network,
    @jti_hash,
    sqlc.narg('source_ip_hash'),
    @expires_at::timestamptz
)
RETURNING id, vendor_id, network, jti_hash, issued_at, expires_at;


-- name: EraseSSETokenIssuances :execrows
-- GDPR Article 17: nullify source_ip_hash and set erased = TRUE for all
-- non-erased issuances belonging to the given vendor.
-- chk_sti_erased_coherent constraint enforces source_ip_hash IS NULL when erased.
UPDATE sse_token_issuances
SET
    erased         = TRUE,
    source_ip_hash = NULL
WHERE vendor_id = @vendor_id::uuid
  AND erased    = FALSE;
