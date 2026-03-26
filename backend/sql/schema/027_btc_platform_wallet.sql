-- +goose Up
-- +goose StatementBegin

/*
 * 027_btc_platform_wallet.sql — Platform-mode vendor withdrawal requests.
 *
 * Tables defined here:
 *   btc_withdrawal_requests — withdrawal request lifecycle for platform-mode vendors
 *
 * platform-mode vendors accumulate balance in vendor_balances
 *   - The approval workflow has no record to act on
 *   - balance_debit financial_audit_events have no payout_record_id anchor
 *     (the chk_fae_financial_anchor fix in 017_btc_audit.sql handles this)
 *   - In-flight withdrawals cannot be tracked for reconciliation
 *
 * Processing rules (settlement-feature.md §9):
 *   below approval threshold → auto_approved + payout_record created immediately
 *   above threshold          → pending_approval → admin review → approved/rejected
 *   Minimum withdrawal: minimum_invoice_sat + estimated single-output miner fee
 *   Address validation: network-aware + RPC getaddressinfo ismine check (rejects
 *     platform-managed addresses)
 *
 * Depends on: 015_btc_payouts.sql (payout_records FK)
 *             010_btc_core.sql (vendor_wallet_config FK, vendor_balances)
 *             001_core.sql (users FK)
 *             009_btc_types.sql (btc_withdrawal_status ENUM)
 */

CREATE TABLE btc_withdrawal_requests (
    id                  UUID                    PRIMARY KEY DEFAULT uuidv7(),

    -- Vendor requesting the withdrawal. RESTRICT: active withdrawal requests must be
    -- resolved before the vendor account can be deleted.
    vendor_id           UUID                    NOT NULL REFERENCES users(id) ON DELETE RESTRICT,

    -- 'mainnet' or 'testnet4'.
    network             TEXT                    NOT NULL,

    -- Satoshi amount requested.
    -- Must be >= (tier.minimum_invoice_sat + estimated single-output miner fee).
    amount_sat          BIGINT                  NOT NULL,

    -- Validated destination address (network-aware + RPC ismine check passed at save time).
    -- Platform-managed addresses are rejected at the API layer before this row is inserted.
    destination_address TEXT                    NOT NULL,

    -- Request lifecycle. See btc_withdrawal_status in 009_btc_types.sql.
    status              btc_withdrawal_status   NOT NULL DEFAULT 'pending_approval',

    -- The payout record created when this withdrawal was approved and queued for sweep.
    -- NULL until approval + payout record creation.
    -- RESTRICT: payout_record must reach a terminal state before the request can be deleted.
    payout_record_id    UUID                    REFERENCES payout_records(id) ON DELETE RESTRICT,

    -- Admin who approved or rejected the request. SET NULL on account deletion.
    reviewed_by         UUID                    REFERENCES users(id) ON DELETE SET NULL,

    -- Mandatory rejection reason. Required when status = 'rejected'.
    rejection_reason    TEXT,

    -- When the request was reviewed (approved or rejected).
    reviewed_at         TIMESTAMPTZ,

    created_at          TIMESTAMPTZ             NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ             NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_bwr_network
        CHECK (network IN ('mainnet', 'testnet4')),
    CONSTRAINT chk_bwr_amount_positive
        CHECK (amount_sat > 0),
    CONSTRAINT chk_bwr_rejection_coherent
        CHECK (status != 'rejected' OR rejection_reason IS NOT NULL),
    CONSTRAINT chk_bwr_reviewed_coherent
        CHECK (status NOT IN ('approved', 'rejected') OR reviewed_at IS NOT NULL)
);

CREATE TRIGGER trg_btc_withdrawal_requests_updated_at
    BEFORE UPDATE ON btc_withdrawal_requests
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Approval queue: "show all pending-approval withdrawals ordered by submission time."
CREATE INDEX idx_bwr_pending_approval
    ON btc_withdrawal_requests(network, created_at DESC)
    WHERE status = 'pending_approval';

-- Per-vendor withdrawal history.
CREATE INDEX idx_bwr_vendor
    ON btc_withdrawal_requests(vendor_id, created_at DESC);

COMMENT ON TABLE btc_withdrawal_requests IS
    'Platform-mode vendor withdrawal requests. '
    'below approval threshold → auto_approved + payout_record immediately. '
    'above threshold → pending_approval → admin review → approved/rejected. '
    'destination_address: validated (network-aware + RPC ismine) before row inserted. '
    'payout_record_id: linked when request is approved; tracks the full sweep lifecycle.';

COMMENT ON COLUMN btc_withdrawal_requests.destination_address IS
    'Network-aware bech32 address. Validated with RPC getaddressinfo ismine at save time '
    'to reject platform-managed addresses. Immutable after creation.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS btc_withdrawal_requests CASCADE;

-- +goose StatementEnd
