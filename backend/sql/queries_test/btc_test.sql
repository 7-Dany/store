/* ============================================================
   sql/queries_test/btc_test.sql
   Bitcoin payment system test-only queries for sqlc code generation.

   These queries are for tests only — never use them in production code.

   They exist to:
     1. Read internal state for test assertions (bypassing the application
        layer so tests can verify DB-level invariants directly).
     2. Insert fixture data that would normally require multi-step application
        flows (e.g. a pre-settled invoice for a payout test).
     3. Clean up test data between test cases.

   Naming convention: all queries are prefixed with "Test" so the generated
   Go symbols are immediately recognisable as test-only helpers.

   Depends on: 009_btc.sql, 010_btc_payouts.sql
   ============================================================ */


/* ════════════════════════════════════════════════════════════
   DIRECT READS — for test assertions
   ════════════════════════════════════════════════════════════ */

-- name: TestGetInvoice :one
-- Read a raw invoice row for assertion. Includes every column including
-- internal fields not normally exposed via application queries.
SELECT * FROM invoices WHERE id = @invoice_id::uuid;


-- name: TestGetInvoiceAddress :one
-- Fetch the address record allocated to an invoice.
SELECT * FROM invoice_addresses WHERE invoice_id = @invoice_id::uuid;


-- name: TestGetInvoiceAddressMonitoring :one
-- Fetch the (single active) monitoring record for an invoice.
SELECT * FROM invoice_address_monitoring WHERE invoice_id = @invoice_id::uuid;


-- name: TestGetInvoicePayments :many
-- Return all payment records for an invoice, ordered by detection time.
SELECT * FROM invoice_payments
WHERE invoice_id = @invoice_id::uuid
ORDER BY detected_at ASC;


-- name: TestGetPayoutRecord :one
-- Fetch a raw payout record for assertion.
SELECT * FROM payout_records WHERE id = @payout_id::uuid;


-- name: TestGetPayoutRecordByInvoice :one
-- Fetch the payout record for an invoice directly.
SELECT * FROM payout_records WHERE invoice_id = @invoice_id::uuid;


-- name: TestGetVendorBalance :one
-- Fetch the raw vendor balance for assertion.
SELECT * FROM vendor_balances
WHERE vendor_id = @vendor_id::uuid
  AND network   = @network;


-- name: TestGetPlatformConfig :one
SELECT * FROM platform_config WHERE network = @network;


-- name: TestGetReconciliationJobState :one
SELECT * FROM reconciliation_job_state WHERE network = @network;


-- name: TestGetBitcoinSyncState :one
SELECT * FROM bitcoin_sync_state WHERE network = @network;


-- name: TestGetOpenOutage :one
SELECT * FROM btc_outage_log
WHERE network  = @network
  AND ended_at IS NULL;


-- name: TestGetAllOutages :many
-- Return all outage records for a network for assertion.
SELECT * FROM btc_outage_log WHERE network = @network ORDER BY started_at ASC;


-- name: TestGetLatestExchangeRate :one
SELECT * FROM btc_exchange_rate_log
WHERE network       = @network
  AND fiat_currency = @fiat_currency
ORDER BY fetched_at DESC
LIMIT 1;


-- name: TestCountActiveMonitoringRecords :one
-- Assert the watch list size for a network.
SELECT COUNT(*)::integer AS count
FROM invoice_address_monitoring
WHERE network = @network
  AND status  = 'active';


-- name: TestGetFinancialAuditEvents :many
-- Return all audit events for an invoice ordered by id (insertion order).
SELECT * FROM financial_audit_events
WHERE invoice_id = @invoice_id::uuid
ORDER BY id ASC;


-- name: TestGetWebhookDeliveries :many
-- Return all webhook delivery records for a vendor, ordered by creation.
SELECT * FROM webhook_deliveries
WHERE vendor_id = @vendor_id::uuid
ORDER BY created_at ASC;


-- name: TestGetSSETokenIssuances :many
-- Return all SSE token issuance records for a vendor.
SELECT * FROM sse_token_issuances
WHERE vendor_id = @vendor_id::uuid
ORDER BY issued_at ASC;


-- name: TestGetZMQDeadLetters :many
-- Return all dead letter records for a network.
SELECT * FROM btc_zmq_dead_letter
WHERE network = @network
ORDER BY received_at ASC;


-- name: TestGetBlockHistory :many
-- Return all processed block records for a network in ascending height order.
SELECT * FROM bitcoin_block_history
WHERE network = @network
ORDER BY height ASC;


-- name: TestGetBitcoinTxStatusByID :one
-- Fetch one raw btc_tracked_transactions row for assertion.
SELECT * FROM btc_tracked_transactions WHERE id = @id::bigint;


-- name: TestGetBitcoinWatchByID :one
-- Fetch one raw btc_watches row for assertion.
SELECT * FROM btc_watches WHERE id = @id::bigint;


-- name: TestListBitcoinWatchesByUser :many
-- Return active watch resources for one user.
SELECT * FROM btc_watches
WHERE user_id = @user_id::uuid
  AND status  = 'active'
ORDER BY created_at DESC, id DESC;


-- name: TestListBitcoinTxStatusesByUser :many
-- Return all txstatus rows for one user, newest first.
SELECT * FROM btc_tracked_transactions
WHERE user_id = @user_id::uuid
ORDER BY COALESCE(confirmed_at, first_seen_at) DESC, id DESC;


-- name: TestListBitcoinTxStatusRelatedAddresses :many
-- Return all related addresses for one tracked tx row.
SELECT * FROM btc_tracked_transaction_addresses
WHERE tracked_transaction_id = @tx_status_id::bigint
ORDER BY address ASC;


/* ════════════════════════════════════════════════════════════
   FIXTURE INSERTS — for setting up test state directly
   Use these to create pre-existing state that would normally
   require multi-step flows (e.g. a pre-settled invoice).
   ════════════════════════════════════════════════════════════ */

-- name: TestInsertVendorBalance :exec
-- Directly insert a vendor_balances row for test fixture setup.
-- Used when a test needs a vendor to have an existing balance.
INSERT INTO vendor_balances (vendor_id, network, balance_satoshis)
VALUES (
    @vendor_id::uuid,
    @network,
    @balance_satoshis::bigint
)
ON CONFLICT (vendor_id, network) DO UPDATE SET
    balance_satoshis = EXCLUDED.balance_satoshis,
    updated_at       = NOW();


-- name: TestSetVendorBalance :exec
-- Directly overwrite a vendor balance. Used in tests to set up
-- specific starting conditions without going through stored procedures.
UPDATE vendor_balances
SET balance_satoshis = @balance_satoshis::bigint,
    updated_at       = NOW()
WHERE vendor_id = @vendor_id::uuid
  AND network   = @network;


-- name: TestInsertBlockHistory :exec
-- Insert a block history record directly for reconciliation tests.
INSERT INTO bitcoin_block_history (height, network, block_hash, pruned)
VALUES (@height::bigint, @network, sqlc.narg('block_hash'), @pruned::boolean)
ON CONFLICT (height, network) DO NOTHING;


-- name: TestInsertOutageRecord :one
-- Insert an outage record with explicit started_at for time-sensitive tests.
INSERT INTO btc_outage_log (network, started_at, ended_at)
VALUES (
    @network,
    @started_at::timestamptz,
    sqlc.narg('ended_at')::timestamptz
)
RETURNING id, network, started_at, ended_at;


-- name: TestForceInvoiceStatus :exec
-- Directly set an invoice status, bypassing the optimistic-locking WHERE clause.
-- Used in tests to set up specific preconditions without running the full
-- state machine. Never call this in production code.
UPDATE invoices
SET
    status     = @status::btc_invoice_status,
    updated_at = NOW()
WHERE id = @invoice_id::uuid;


-- name: TestSetInvoiceFirstConfirmedHeight :exec
-- Directly set first_confirmed_block_height for reorg rollback tests.
UPDATE invoices
SET
    first_confirmed_block_height = @height::bigint,
    updated_at                   = NOW()
WHERE id = @invoice_id::uuid;


/* ════════════════════════════════════════════════════════════
   CLEANUP — for test teardown
   ════════════════════════════════════════════════════════════ */

-- name: TestDeleteInvoicesByVendor :exec
-- Remove all invoices for a test vendor. Cascades to invoice_addresses,
-- invoice_address_monitoring, and invoice_payments via FK constraints.
-- NOTE: financial_audit_events rows with invoice_id are RESTRICT FK — delete
-- those first if needed, or use TestDeleteFinancialAuditEventsByInvoice.
DELETE FROM invoices WHERE vendor_id = @vendor_id::uuid;


-- name: TestDeletePayoutsByVendor :exec
-- Remove all payout records for a test vendor.
DELETE FROM payout_records WHERE vendor_id = @vendor_id::uuid;


-- name: TestDeleteFinancialAuditEventsByInvoice :exec
-- Remove financial audit events for a test invoice.
-- financial_audit_events is normally immutable but test teardown requires
-- cleaning up test data. This query ONLY exists in the test query file.
DELETE FROM financial_audit_events WHERE invoice_id = @invoice_id::uuid;


-- name: TestDeleteWebhookDeliveriesByVendor :exec
DELETE FROM webhook_deliveries WHERE vendor_id = @vendor_id::uuid;


-- name: TestDeleteSSETokensByVendor :exec
DELETE FROM sse_token_issuances WHERE vendor_id = @vendor_id::uuid;


-- name: TestDeleteZMQDeadLettersByNetwork :exec
DELETE FROM btc_zmq_dead_letter WHERE network = @network;


-- name: TestDeleteBlockHistory :exec
-- Clear block history for a network between tests.
DELETE FROM bitcoin_block_history WHERE network = @network;


-- name: TestDeleteBitcoinTxStatusesByUser :exec
DELETE FROM btc_tracked_transactions WHERE user_id = @user_id::uuid;


-- name: TestDeleteBitcoinWatchesByUser :exec
DELETE FROM btc_watches WHERE user_id = @user_id::uuid;


-- name: TestDeleteExchangeRateLog :exec
-- Clear rate log entries for a network between tests.
DELETE FROM btc_exchange_rate_log WHERE network = @network;


-- name: TestDeleteOutageLog :exec
-- Clear outage records for a network between tests.
DELETE FROM btc_outage_log WHERE network = @network;


-- name: TestResetReconciliationState :exec
-- Reset the reconciliation job state for a network to its initial state.
UPDATE reconciliation_job_state
SET
    last_successful_run_at = NULL,
    last_run_at            = NULL,
    last_run_result        = NULL,
    last_discrepancy_sat   = NULL,
    updated_at             = NOW()
WHERE network = @network;


-- name: TestResetBitcoinSyncState :exec
-- Reset the block-height cursor to -1 (fresh deployment sentinel).
UPDATE bitcoin_sync_state
SET
    last_processed_height = -1,
    updated_at            = NOW()
WHERE network = @network;
