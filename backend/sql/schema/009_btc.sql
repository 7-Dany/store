-- +goose Up
-- +goose StatementBegin

/*
 * 009_btc.sql — Bitcoin payment system schema.
 *
 * Implements the full Bitcoin payment processing pipeline:
 *   btc_tier_config            — owner-managed tier presets (fees, limits, sweep schedule, RBAC role link)
 *   platform_config            — per-network operational singletons (treasury reserve, sweep-hold, legal flags)
 *   reconciliation_job_state   — tracks last successful reconciliation run per network
 *   bitcoin_sync_state         — last processed block-height cursor per network (HandleRecovery backfill)
 *   vendor_wallet_config       — per-vendor wallet mode, destination address, suspension, KYC state
 *   vendor_balances            — running internal BTC balance (platform wallet) / threshold accumulator (hybrid)
 *   invoices                   — core invoice state machine with immutable tier+wallet snapshot at creation
 *   invoice_addresses          — P2WPKH bech32 address allocated per invoice + HD derivation index
 *   invoice_address_monitoring — DB-backed ZMQ watch list; survives restarts and horizontal scaling
 *   invoice_payments           — append-only on-chain payment records (txid + vout, idempotent inserts)
 *   btc_outage_log             — node outage periods for expiry-timer compensation
 *   bitcoin_block_history      — processed-block log; pruned blocks get placeholder rows
 *   payout_records             — vendor payout lifecycle: held → queued → constructing → broadcast → confirmed
 *   financial_audit_events     — immutable append-only financial audit trail (triggers block all mutations)
 *   wallet_backup_success      — wallet.dat backup health tracking (alert if record is stale)
 *
 * Design notes:
 *   — All surrogate PKs use UUID v7 (temporal sort order, zero external deps).
 *   — Append-only tables (invoice_payments, financial_audit_events, bitcoin_block_history,
 *     wallet_backup_success) use BIGSERIAL PKs for guaranteed monotonic ordering.
 *   — All satoshi amounts are BIGINT; fiat amounts are BIGINT minor-currency-units (cents for USD).
 *   — platform_config and bitcoin_sync_state are keyed on network TEXT PK to support
 *     simultaneous mainnet + testnet4 deployments from a single DB.
 *   — vendor_balances.balance_satoshis is VALUE-BEARING only for platform wallet mode.
 *     For hybrid mode it is a threshold accumulator; value is tracked through payout_records.
 *     The reconciliation formula (audit-technical.md §3) therefore joins to vendor_wallet_config
 *     and sums balance_satoshis only WHERE wallet_mode = 'platform'. See COMMENT on that column.
 *
 * Depends on:
 *   001_core.sql          — users table (UUID PK), uuidv7()
 *   002_core_functions.sql — fn_set_updated_at()
 *   003_rbac.sql          — roles table (UUID PK)
 */


/* ═════════════════════════════════════════════════════════════
   ENUM TYPES
   ═════════════════════════════════════════════════════════════ */

-- Vendor wallet mode — how a vendor receives their earnings after settlement.
-- bridge:   earnings forwarded to an external address on each settlement.
-- platform: earnings accumulate as an internal balance; vendor withdraws manually.
-- hybrid:   earnings accumulate; auto-swept to external address once threshold crossed.
CREATE TYPE btc_wallet_mode AS ENUM ('bridge', 'platform', 'hybrid');

COMMENT ON TYPE btc_wallet_mode IS
    'How a vendor receives BTC earnings after invoice settlement. Snapshotted on every invoice at '
    'creation time; the governing mode for a settlement is always from the invoice snapshot.';

-- Sweep schedule — when outstanding queued payouts are batched and broadcast.
CREATE TYPE btc_sweep_schedule AS ENUM ('weekly', 'daily', 'realtime');

COMMENT ON TYPE btc_sweep_schedule IS
    'Frequency at which settled invoices are swept to vendor addresses. '
    'Free=weekly, Growth/Pro=daily (or realtime), Enterprise=realtime.';

-- Invoice status — the 16-state machine governing invoice lifecycle.
-- See settlement-technical.md §3 for the full permitted-transitions table.
CREATE TYPE btc_invoice_status AS ENUM (
    'pending',               -- created, awaiting payment
    'detected',              -- payment seen in mempool; expiry frozen
    'mempool_dropped',       -- payment disappeared from mempool before confirming
    'confirming',            -- first block confirmation received; awaiting required depth
    'settling',              -- settlement worker has claimed this invoice (transient)
    'settled',               -- settlement complete; payout queued or balance credited
    'settlement_failed',     -- max retries exhausted; admin action required
    'reorg_admin_required',  -- settled+swept invoice hit a block reorg; admin action required
    'expired',               -- window elapsed with no payment detected
    'expired_with_payment',  -- late payment arrived within 30-day monitoring window
    'cancelled',             -- cancelled before any payment
    'cancelled_with_payment',-- cancelled but payment arrived within 30-day monitoring window
    'underpaid',             -- received amount below invoiced − tolerance
    'overpaid',              -- received amount exceeded both overpayment thresholds
    'refunded',              -- on-chain refund issued and confirmed
    'manually_closed'        -- admin wrote off after investigation
);

COMMENT ON TYPE btc_invoice_status IS
    'Invoice lifecycle states. Add values with ALTER TYPE … ADD VALUE; '
    'never remove a value that may exist in live rows. '
    'Permitted transitions are enforced at the application layer — see settlement-technical.md §3.';

-- Payout record status — lifecycle from settlement credit to on-chain confirmation.
CREATE TYPE btc_payout_status AS ENUM (
    'held',         -- net amount below miner fee floor; accumulating with future settlements
    'queued',       -- floor cleared; waiting for next sweep window
    'constructing', -- sweep job has assigned this record to an active batch
    'broadcast',    -- sweep TX sent to the network; awaiting 3 confirmations
    'confirmed',    -- sweep output confirmed on-chain at required depth
    'failed',       -- permanent sweep failure after all retries; admin action required
    'refunded',     -- payout reversed; funds returned to buyer on-chain
    'manual_payout' -- admin declared out-of-band payment; terminal
);

COMMENT ON TYPE btc_payout_status IS
    'Payout record lifecycle. ''constructing'' is a transient claim; stale records (>10 min) '
    'are returned to ''queued'' by the stuck-sweep watchdog. '
    'See settlement-technical.md §4 for the full permitted-transitions table.';

-- KYC/AML state — placeholder for future regulatory compliance.
-- Schema column accepts this type today; logic is gated behind non-NULL tier thresholds.
CREATE TYPE btc_kyc_status AS ENUM ('not_required', 'pending', 'approved', 'rejected');

COMMENT ON TYPE btc_kyc_status IS
    'KYC/AML placeholder. Default not_required. Future implementation sets non-NULL '
    'tier thresholds and drives the btc_kyc_status state machine without schema changes.';

-- Address monitoring lifecycle — whether the ZMQ subscriber is actively watching an address.
CREATE TYPE btc_monitoring_status AS ENUM ('active', 'retired');

COMMENT ON TYPE btc_monitoring_status IS
    'ZMQ watch state for invoice_address_monitoring. '
    'Retired records are kept permanently for audit; the partial index '
    'WHERE status = ''active'' keeps all hot-path queries fast regardless of retired row count.';


/* ═════════════════════════════════════════════════════════════
   TIER CONFIGURATION
   ═════════════════════════════════════════════════════════════ */

/*
 * Owner-managed tier presets. Each tier bundles financial rules and feature flags
 * that govern invoices and payouts for all vendors on that tier.
 *
 * Every tier may optionally be linked to an RBAC role (role_id). When a user is
 * assigned to a tier, the platform also assigns them the linked role — this is the
 * "tier assignment changes the vendor's RBAC role" bridge from the design docs.
 *
 * All validation ranges are enforced at both the admin API layer and as DB CHECK
 * constraints (vendor-technical.md §1). DB-level enforcement ensures that direct
 * DB operations or future code paths cannot silently violate the invariants.
 *
 * Tiers are soft-deactivated (status = 'inactive'), never hard-deleted, because
 * invoices hold a FK reference to tier_id and hard deletion would violate RESTRICT.
 */
CREATE TABLE btc_tier_config (
    id          UUID        PRIMARY KEY DEFAULT uuidv7(),

    -- Human-readable machine slug used as a stable external identifier.
    -- Examples: 'free', 'growth', 'pro', 'enterprise'.
    name        TEXT        NOT NULL,

    -- Display label shown in admin UI.
    display_name TEXT        NOT NULL,

    -- Optional RBAC role linked to this tier. When a vendor is assigned to this tier,
    -- the platform also grants them this role. SET NULL if the role is later deleted.
    role_id     UUID        REFERENCES roles(id) ON DELETE SET NULL,

    -- ── Financial rules ───────────────────────────────────────────────────────

    -- Platform processing fee percentage deducted from vendor earnings at settlement.
    -- Stored as percentage (e.g. 2.50 = 2.5%). Range [0, 50].
    -- Values above 50% require explicit owner acknowledgment at the application layer.
    processing_fee_rate             NUMERIC(5,2)  NOT NULL,

    -- Number of block confirmations required before settlement triggers.
    -- 1 = Enterprise (immediate, elevated reorg risk); 6 = Free (~60 min).
    confirmation_depth              INTEGER       NOT NULL,

    -- Maximum miner fee rate the platform will pay on sweep transactions for this tier.
    -- In satoshis per virtual byte. Range [1, 10000].
    miner_fee_cap_sat_vbyte         INTEGER       NOT NULL,

    -- How often queued payout records are swept to vendor addresses.
    sweep_schedule                  btc_sweep_schedule NOT NULL,

    -- Payout amounts at or above this threshold enter the approval workflow.
    -- 0 = every payout requires approval. Range [0, 1_000_000_000].
    withdrawal_approval_threshold_sat BIGINT      NOT NULL,

    -- How long a pending invoice remains active before expiring.
    -- Effective expiry accounts for node outage periods (invoice-feature.md §5).
    invoice_expiry_minutes          INTEGER       NOT NULL,

    -- Tolerance band for over/underpayment. Payments within this band of the
    -- invoiced amount are settled as exact. Range [0, 10] percent.
    payment_tolerance_pct           NUMERIC(4,2)  NOT NULL,

    -- Minimum satoshi amount for invoices on this tier.
    -- Free tier may be higher (e.g. 50000) due to less-efficient weekly batching.
    minimum_invoice_sat             BIGINT        NOT NULL,

    -- Relative overpayment threshold: payments above (invoiced × (1 + this/100))
    -- trigger the overpaid path only when BOTH thresholds are exceeded. Range [1, 100].
    overpayment_relative_threshold_pct NUMERIC(5,2) NOT NULL,

    -- Absolute overpayment threshold in satoshis: payments above (invoiced + this)
    -- trigger the overpaid path only when BOTH thresholds are exceeded. Min 1000.
    overpayment_absolute_threshold_sat BIGINT     NOT NULL,

    -- Expected number of vendors in a batch sweep. Used in the batch-amortized
    -- fee floor calculation at settlement time. Range [1, 100].
    expected_batch_size             INTEGER       NOT NULL,

    -- ── Feature flags ─────────────────────────────────────────────────────────

    -- Whether vendors on this tier may choose platform wallet mode.
    -- Also gated by the platform-wide PLATFORM_WALLET_MODE_LEGAL_APPROVED flag.
    platform_wallet_mode_allowed    BOOLEAN       NOT NULL DEFAULT FALSE,

    -- ── Lifecycle ─────────────────────────────────────────────────────────────

    -- active: new invoices can be created; deactivating: pre-sweep running, no new invoices;
    -- inactive: fully wound down.
    status  TEXT  NOT NULL DEFAULT 'active',

    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_btc_tier_config_name UNIQUE (name),
    -- Slugs and labels must not be blank; an empty name would produce a valid but
    -- invisible tier that is indistinguishable from a missing value at the API layer.
    CONSTRAINT chk_btc_tier_name_not_empty
        CHECK (length(trim(name)) > 0),
    CONSTRAINT chk_btc_tier_display_name_not_empty
        CHECK (length(trim(display_name)) > 0),

    -- Range constraints (vendor-technical.md §1). Also enforced at admin API layer.
    CONSTRAINT chk_btc_tier_fee_rate
        CHECK (processing_fee_rate >= 0 AND processing_fee_rate <= 50),
    CONSTRAINT chk_btc_tier_confirm_depth
        CHECK (confirmation_depth >= 1 AND confirmation_depth <= 144),
    CONSTRAINT chk_btc_tier_fee_cap
        CHECK (miner_fee_cap_sat_vbyte >= 1 AND miner_fee_cap_sat_vbyte <= 10000),
    CONSTRAINT chk_btc_tier_withdrawal_threshold
        CHECK (withdrawal_approval_threshold_sat >= 0
            AND withdrawal_approval_threshold_sat <= 1000000000),
    CONSTRAINT chk_btc_tier_expiry_window
        CHECK (invoice_expiry_minutes >= 5 AND invoice_expiry_minutes <= 1440),
    CONSTRAINT chk_btc_tier_tolerance
        CHECK (payment_tolerance_pct >= 0 AND payment_tolerance_pct <= 10),
    CONSTRAINT chk_btc_tier_min_invoice
        CHECK (minimum_invoice_sat >= 1000),
    CONSTRAINT chk_btc_tier_ovpay_rel
        CHECK (overpayment_relative_threshold_pct >= 1
            AND overpayment_relative_threshold_pct <= 100),
    CONSTRAINT chk_btc_tier_ovpay_abs
        CHECK (overpayment_absolute_threshold_sat >= 1000),
    CONSTRAINT chk_btc_tier_batch_size
        CHECK (expected_batch_size >= 1 AND expected_batch_size <= 100),
    CONSTRAINT chk_btc_tier_status
        CHECK (status IN ('active', 'deactivating', 'inactive'))
);

CREATE TRIGGER trg_btc_tier_config_updated_at
    BEFORE UPDATE ON btc_tier_config
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Active tier lookup (hot path for invoice creation). Covering index avoids a heap
-- fetch when snapshotting all financial rule columns onto a new invoice.
CREATE INDEX idx_btc_tier_active
    ON btc_tier_config(id, name,
        processing_fee_rate, confirmation_depth, miner_fee_cap_sat_vbyte,
        sweep_schedule, withdrawal_approval_threshold_sat, invoice_expiry_minutes,
        payment_tolerance_pct, minimum_invoice_sat, overpayment_relative_threshold_pct,
        overpayment_absolute_threshold_sat, expected_batch_size,
        platform_wallet_mode_allowed)
    WHERE status = 'active';

-- Role linkage lookup: "which tier maps to role X?" (role-assignment hook).
CREATE INDEX idx_btc_tier_role ON btc_tier_config(role_id) WHERE role_id IS NOT NULL;

COMMENT ON TABLE btc_tier_config IS
    'Owner-managed tier presets. All financial rules, fee caps, and sweep schedules are '
    'defined here and snapshotted immutably onto every invoice at creation time. '
    'Tiers are soft-deactivated (status=inactive), never hard-deleted. '
    'role_id links the tier to an RBAC role; assigning a vendor to this tier also grants that role.';
COMMENT ON COLUMN btc_tier_config.processing_fee_rate IS
    'Platform fee as a percentage [0,50]. Calculated on received satoshi amount at settlement.';
COMMENT ON COLUMN btc_tier_config.role_id IS
    'Optional RBAC role assigned to vendors when they are placed on this tier. '
    'SET NULL if the role is deleted. Application layer handles the role-assignment side-effect.';
COMMENT ON COLUMN btc_tier_config.expected_batch_size IS
    'Used in the batch-amortized fee floor formula: floor = (fee_estimate × 1.1 × vbytes) / expected_batch_size. '
    'Default 50 for Free, 20 for Growth/Pro.';
COMMENT ON COLUMN btc_tier_config.status IS
    'active | deactivating | inactive. Deactivating triggers a pre-sweep of queued payouts '
    'before full wind-down. New invoices are blocked during deactivating and inactive states.';


/* ═════════════════════════════════════════════════════════════
   PLATFORM CONFIGURATION
   ═════════════════════════════════════════════════════════════ */

/*
 * Per-network operational configuration. One row per network ('mainnet', 'testnet4').
 *
 * treasury_reserve_satoshis tracks accumulated miner fee earnings retained by the
 * platform from completed sweeps. Required for reconciliation to balance:
 *   on_chain_UTXO_value = vendor_balances(platform) + payout_records(in-flight) + in-flight invoices + treasury_reserve
 * Incremented when sweep TXs confirm; decremented on withdrawal or UTXO consolidation.
 *
 * sweep_hold_mode is activated by the reconciliation job when it detects a discrepancy.
 * All sweep construction and broadcast is blocked until admin clears the hold.
 */
CREATE TABLE platform_config (
    network     TEXT        PRIMARY KEY,

    -- Accumulated miner fee earnings from completed sweeps retained by the platform.
    -- Incremented atomically (in the same TX as payout_records → confirmed).
    -- Required by the reconciliation formula; incorrect value causes reconciliation drift.
    treasury_reserve_satoshis   BIGINT      NOT NULL DEFAULT 0,

    -- When TRUE, all outgoing sweep construction and broadcast is blocked.
    -- Activated by reconciliation job on discrepancy; cleared by admin after investigation.
    sweep_hold_mode             BOOLEAN     NOT NULL DEFAULT FALSE,
    sweep_hold_reason           TEXT,
    sweep_hold_activated_at     TIMESTAMPTZ,

    -- Platform wallet mode is a custodial service requiring legal review before enabling.
    -- Set TRUE only via deliberate ops action with a written legal approval record.
    -- Default FALSE; never an admin UI toggle.
    platform_wallet_mode_legal_approved BOOLEAN NOT NULL DEFAULT FALSE,

    -- Block height from which the reconciliation backfill scan starts.
    -- Must be set before the first mainnet deployment.
    -- Application rejects btc_reconciliation_start_height = 0 on mainnet unless
    -- BTC_RECONCILIATION_ALLOW_GENESIS_SCAN = true.
    reconciliation_start_height BIGINT      NOT NULL DEFAULT 0,

    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_pconfig_network
        CHECK (network IN ('mainnet', 'testnet4')),
    CONSTRAINT chk_pconfig_treasury_non_negative
        CHECK (treasury_reserve_satoshis >= 0),
    CONSTRAINT chk_pconfig_hold_coherent
        CHECK (sweep_hold_mode = FALSE
            OR (sweep_hold_reason IS NOT NULL AND sweep_hold_activated_at IS NOT NULL))
);

CREATE TRIGGER trg_platform_config_updated_at
    BEFORE UPDATE ON platform_config
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

COMMENT ON TABLE platform_config IS
    'Per-network operational singletons. One row per network, inserted at deployment. '
    'treasury_reserve_satoshis is required by the reconciliation formula — keep it accurate. '
    'sweep_hold_mode blocks all sweeps; only admin can clear it.';
COMMENT ON COLUMN platform_config.treasury_reserve_satoshis IS
    'Accumulated miner fees from confirmed sweep TXs. Increment in the same DB TX as '
    'payout_records transitions to confirmed (gross payout - SUM(vendor net payouts)). '
    'Decrement on treasury withdrawal or UTXO consolidation. Without this term the '
    'reconciliation formula under-counts after sweeps have occurred.';
COMMENT ON COLUMN platform_config.platform_wallet_mode_legal_approved IS
    'Must be TRUE before any tier may enable platform_wallet_mode_allowed. '
    'Set via ops action with written legal approval record, never via admin UI.';


/* ═════════════════════════════════════════════════════════════
   RECONCILIATION JOB STATE
   ═════════════════════════════════════════════════════════════ */

/*
 * Tracks the last successful reconciliation run per network.
 * An independent monitoring job reads last_successful_run_at and fires a CRITICAL
 * alert if it has not been updated within 8 hours ("Reconciliation job missed").
 * The alert key is last_successful_run_at, NOT the job's scheduled run timestamp.
 * A job that runs but fails silently (e.g. copy step error) will still trigger the alert.
 */
CREATE TABLE reconciliation_job_state (
    network             TEXT        PRIMARY KEY,
    last_successful_run_at TIMESTAMPTZ,
    last_run_at         TIMESTAMPTZ,
    -- 'ok' | 'discrepancy' | 'error'
    last_run_result     TEXT,
    -- Non-NULL when last_run_result = 'discrepancy'.
    last_discrepancy_sat BIGINT,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_rjstate_network
        CHECK (network IN ('mainnet', 'testnet4')),
    CONSTRAINT chk_rjstate_result
        CHECK (last_run_result IS NULL
            OR last_run_result IN ('ok', 'discrepancy', 'error')),
    -- When a discrepancy is detected, the satoshi amount must be recorded.
    -- Prevents a silent nil-amount discrepancy from passing reconciliation checks.
    CONSTRAINT chk_rjstate_discrepancy_coherent
        CHECK (last_run_result != 'discrepancy' OR last_discrepancy_sat IS NOT NULL)
);

CREATE TRIGGER trg_reconciliation_job_state_updated_at
    BEFORE UPDATE ON reconciliation_job_state
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

COMMENT ON TABLE reconciliation_job_state IS
    'Reconciliation job health per network. last_successful_run_at is the basis for the '
    '"Reconciliation job missed" CRITICAL alert (fires if > 8 hours stale). '
    'Written only on successful completion; a failing run does not update this timestamp.';


/* ═════════════════════════════════════════════════════════════
   BITCOIN SYNC STATE
   ═════════════════════════════════════════════════════════════ */

/*
 * Stores the last processed block height per network. This is the cursor used by
 * HandleRecovery to backfill missed blocks after a node reconnect.
 *
 * last_processed_height = -1 is the sentinel for "never processed" (fresh deployment).
 * The application initialises it to platform_config.reconciliation_start_height on first run.
 *
 * On chain-reset detection (last_processed_height > getblockcount), the application
 * resets last_processed_height to reconciliation_start_height and fires a CRITICAL alert.
 */
CREATE TABLE bitcoin_sync_state (
    network                 TEXT    PRIMARY KEY,
    -- -1 = never processed. Initialised to reconciliation_start_height on first run.
    last_processed_height   BIGINT  NOT NULL DEFAULT -1,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_bss_network
        CHECK (network IN ('mainnet', 'testnet4')),
    CONSTRAINT chk_bss_height_min
        CHECK (last_processed_height >= -1)
);

CREATE TRIGGER trg_bitcoin_sync_state_updated_at
    BEFORE UPDATE ON bitcoin_sync_state
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

COMMENT ON TABLE bitcoin_sync_state IS
    'Block-height cursor per network. Used by HandleRecovery to backfill missed blocks '
    'after a node reconnect. -1 = never processed. Updated inside each reconcileSegment '
    'transaction (per BTC_RECONCILIATION_CHECKPOINT_INTERVAL blocks) for crash safety.';
COMMENT ON COLUMN bitcoin_sync_state.last_processed_height IS
    '-1 = sentinel for fresh deployment. Set to reconciliation_start_height on first run. '
    'If last_processed_height > getblockcount (chain reset / node reindex), the app '
    'resets this to reconciliation_start_height and fires a CRITICAL alert.';


/* ═════════════════════════════════════════════════════════════
   VENDOR WALLET CONFIGURATION
   ═════════════════════════════════════════════════════════════ */

/*
 * Per-vendor payment configuration. Created when a vendor role is granted (role assignment
 * triggers the wallet-setup prompt). Exactly one row per (vendor_id, network).
 *
 * Mode-change rules:
 *   - Vendors may only select modes permitted by their tier.
 *   - If a tier downgrade removes the vendor's current mode, mode_frozen is set TRUE.
 *     Existing in-flight invoices complete; new invoices are blocked until the vendor
 *     reconfigures to a permitted mode (explicit vendor action required, no auto-unfreeze).
 *
 * Address validation:
 *   - bridge_destination_address must pass both a DB ismine check and an RPC getaddressinfo
 *     check before being stored (vendor-technical.md §2).
 *   - buyer_refund addresses are validated per-invoice (contracts.md Contract 10).
 *
 * Deletion is blocked (ON DELETE RESTRICT on users FK) if any of the following are
 * outstanding: pending invoices, queued payouts, non-zero balance, unresolved admin states.
 */
CREATE TABLE vendor_wallet_config (
    vendor_id   UUID            NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    network     TEXT            NOT NULL,

    -- Current tier assignment.
    tier_id     UUID            NOT NULL REFERENCES btc_tier_config(id) ON DELETE RESTRICT,

    -- Active wallet mode. Snapshotted onto every invoice at creation time.
    wallet_mode btc_wallet_mode NOT NULL,

    -- External destination address for bridge and hybrid modes.
    -- Snapshotted as bridge_destination_address on invoices — changes here do not
    -- affect in-flight invoices. NULL for platform wallet mode.
    -- Validated with two-step ismine check before storage (vendor-technical.md §2).
    bridge_destination_address  TEXT,

    -- Auto-sweep threshold in satoshis (hybrid mode only). NULL for bridge/platform modes.
    -- Snapshotted on invoices at creation. Vendor's current threshold is irrelevant after
    -- an invoice is created.
    auto_sweep_threshold_sat    BIGINT,

    -- KYC/AML state. Future implementation drives the btc_kyc_status state machine.
    kyc_status  btc_kyc_status  NOT NULL DEFAULT 'not_required',

    -- Suspension: new invoices blocked; in-flight invoices complete; payouts accumulate
    -- but sweeps are held at broadcast boundary.
    suspended           BOOLEAN     NOT NULL DEFAULT FALSE,
    suspended_at        TIMESTAMPTZ,
    suspension_reason   TEXT,

    -- Set TRUE when a tier downgrade removes permission for the vendor's current wallet_mode.
    -- Clears when vendor explicitly selects a permitted mode. Never auto-clears.
    mode_frozen         BOOLEAN     NOT NULL DEFAULT FALSE,
    mode_frozen_reason  TEXT,

    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (vendor_id, network),

    CONSTRAINT chk_vwc_network
        CHECK (network IN ('mainnet', 'testnet4')),

    -- bridge and hybrid modes require a destination address; platform mode must not have one.
    CONSTRAINT chk_vwc_bridge_address_coherent
        CHECK (wallet_mode = 'platform'
            OR bridge_destination_address IS NOT NULL),
    CONSTRAINT chk_vwc_platform_no_bridge_address
        CHECK (wallet_mode != 'platform'
            OR bridge_destination_address IS NULL),

    -- auto_sweep_threshold_sat is required for hybrid, irrelevant for others.
    CONSTRAINT chk_vwc_hybrid_threshold_coherent
        CHECK (wallet_mode = 'hybrid' OR auto_sweep_threshold_sat IS NULL),
    -- Inverse: hybrid mode MUST provide a threshold; NULL threshold for hybrid is invalid
    -- and would cause the auto-sweep logic to silently never fire.
    CONSTRAINT chk_vwc_hybrid_threshold_required
        CHECK (wallet_mode != 'hybrid' OR auto_sweep_threshold_sat IS NOT NULL),
    CONSTRAINT chk_vwc_hybrid_threshold_positive
        CHECK (auto_sweep_threshold_sat IS NULL OR auto_sweep_threshold_sat > 0),
    -- Threshold must be large enough to cover a realistic sweep miner fee.
    -- 10 000 sat ≈ 0.0001 BTC; below this the sweep fee could exceed the payout net.
    CONSTRAINT chk_vwc_hybrid_threshold_minimum
        CHECK (auto_sweep_threshold_sat IS NULL OR auto_sweep_threshold_sat >= 10000),

    -- mode_frozen coherence: reason must be recorded whenever the flag is set so
    -- the vendor and admin can understand why new invoices are being blocked.
    CONSTRAINT chk_vwc_mode_frozen_coherent
        CHECK (mode_frozen = FALSE OR mode_frozen_reason IS NOT NULL),

    -- Suspension state coherence.
    CONSTRAINT chk_vwc_suspension_coherent
        CHECK (suspended = FALSE
            OR (suspended_at IS NOT NULL AND suspension_reason IS NOT NULL))
);

CREATE TRIGGER trg_vendor_wallet_config_updated_at
    BEFORE UPDATE ON vendor_wallet_config
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Fast lookup by vendor and network (primary hot path for invoice creation).
CREATE INDEX idx_vwc_vendor_network
    ON vendor_wallet_config(vendor_id, network);

-- Lookup by tier (for pre-deactivation sweep scanning).
CREATE INDEX idx_vwc_tier ON vendor_wallet_config(tier_id);

-- Partial index: suspended vendors queried during broadcast boundary check.
CREATE INDEX idx_vwc_suspended
    ON vendor_wallet_config(vendor_id, network) WHERE suspended = TRUE;

COMMENT ON TABLE vendor_wallet_config IS
    'Per-vendor wallet configuration per network. One row per (vendor_id, network). '
    'wallet_mode and bridge_destination_address are snapshotted onto every invoice — '
    'changes here apply only to future invoices. '
    'Deletion blocked (RESTRICT) while any outstanding financial obligations exist.';
COMMENT ON COLUMN vendor_wallet_config.bridge_destination_address IS
    'External address for bridge/hybrid payouts. Validated via two-step ismine check '
    '(DB lookup + RPC getaddressinfo) before storage. Snapshotted on invoices at creation.';
COMMENT ON COLUMN vendor_wallet_config.mode_frozen IS
    'TRUE when tier downgrade removes permission for the active wallet_mode. '
    'Clears only on explicit vendor reconfiguration to a permitted mode; never auto-clears.';


/* ═════════════════════════════════════════════════════════════
   VENDOR BALANCES
   ═════════════════════════════════════════════════════════════ */

/*
 * Internal BTC balance per vendor per network. One row per (vendor_id, network).
 * Always queried with SELECT FOR UPDATE before any debit or credit operation to
 * prevent concurrent race conditions.
 *
 * ┌─────────────────────────────────────────────────────────────────────┐
 * │ RECONCILIATION NOTE                                                 │
 * │ balance_satoshis has DIFFERENT semantics per wallet_mode:           │
 * │   platform: value-bearing balance. Included in reconciliation sum.  │
 * │   hybrid:   threshold accumulator only. NOT included in the         │
 * │             reconciliation formula — value is fully captured in     │
 * │             payout_records (held/queued status). Including this      │
 * │             balance AND the payout records would double-count.      │
 * │ The reconciliation query (audit-technical.md §3) must join to       │
 * │ vendor_wallet_config and filter WHERE wallet_mode = 'platform'      │
 * │ for the vendor_balances sum.                                        │
 * └─────────────────────────────────────────────────────────────────────┘
 *
 * Hybrid balance lifecycle:
 *   - Incremented at settlement Phase 2 by the net_satoshis for each invoice.
 *   - Decremented at threshold-crossing when all held payout records are promoted to queued.
 *     The decrement equals the SUM of all promoted held records (balance resets to ~0).
 *   - NOT decremented at broadcast or confirmation — those transitions are accounted for
 *     by payout_records moving through their state machine.
 *
 * balance_satoshis can never go below 0. Any UPDATE that would violate this is caught
 * by the CHECK constraint and classified as ErrInsufficientBalance (SQLSTATE 23514).
 */
CREATE TABLE vendor_balances (
    vendor_id   UUID    NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    network     TEXT    NOT NULL,

    -- See reconciliation note above: semantics differ by wallet_mode.
    balance_satoshis    BIGINT  NOT NULL DEFAULT 0,

    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (vendor_id, network),

    CONSTRAINT chk_vbal_network
        CHECK (network IN ('mainnet', 'testnet4')),
    CONSTRAINT chk_vbal_non_negative
        CHECK (balance_satoshis >= 0)
);

CREATE TRIGGER trg_vendor_balances_updated_at
    BEFORE UPDATE ON vendor_balances
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

COMMENT ON TABLE vendor_balances IS
    'Internal BTC balance per vendor per network. '
    'ALWAYS use SELECT FOR UPDATE before any read-modify-write (settlement, debit, promotion). '
    'balance_satoshis >= 0 is enforced by DB CHECK; any violation is ErrInsufficientBalance (SQLSTATE 23514). '
    'For hybrid vendors this is a threshold accumulator — see the reconciliation note in the DDL comment.';
COMMENT ON COLUMN vendor_balances.balance_satoshis IS
    'Platform wallet mode: value-bearing balance included in reconciliation. '
    'Hybrid mode: threshold accumulator only — excluded from reconciliation (value is in payout_records). '
    'CHECK (>= 0) — violation = ErrInsufficientBalance; never retry on this error.';


/* ═════════════════════════════════════════════════════════════
   INVOICES
   ═════════════════════════════════════════════════════════════ */

/*
 * Core invoice state machine. One row per buyer-initiated purchase.
 *
 * Every invoice permanently records a snapshot of the tier config and wallet mode
 * active at creation time. This snapshot is immutable — changes to the tier or vendor
 * configuration after creation do not affect in-flight invoices.
 *
 * Settlement is governed exclusively by the invoice snapshot values:
 *   - wallet_mode and bridge_destination_address determine where funds go.
 *   - processing_fee_rate, confirmation_depth, payment_tolerance_pct govern the math.
 *   - auto_sweep_threshold_sat governs the hybrid threshold check.
 *
 * State machine transitions are enforced at the application layer (all 38 permitted
 * transitions listed in settlement-technical.md §3). The CHECK on status ensures only
 * recognised states can be stored; unlisted values are rejected at the DB level.
 *
 * Concurrency invariants:
 *   - Every status UPDATE must use WHERE status = $expected AND id = $id and assert
 *     RowsAffected() == 1. A return of 0 rows means another worker changed the status
 *     first — return ErrStatusPreconditionFailed, which triggers a rollback.
 *   - settling_source must be set in the same UPDATE that claims settling status.
 */
CREATE TABLE invoices (
    id          UUID                PRIMARY KEY DEFAULT uuidv7(),

    -- Parties
    vendor_id   UUID                NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    buyer_id    UUID                NOT NULL REFERENCES users(id) ON DELETE RESTRICT,

    -- Tier at creation time (preserved for audit; snapshot values are in columns below).
    tier_id     UUID                NOT NULL REFERENCES btc_tier_config(id) ON DELETE RESTRICT,

    network     TEXT                NOT NULL,
    status      btc_invoice_status  NOT NULL DEFAULT 'pending',

    -- ── Invoice amounts ────────────────────────────────────────────────────────

    -- Satoshi amount the buyer is expected to pay. Floor-rounded from fiat at creation.
    amount_sat  BIGINT              NOT NULL,

    -- Fiat price of the product at invoice creation, in minor currency units (e.g. USD cents).
    -- Informational; the satoshi amount is authoritative for payment matching.
    fiat_amount BIGINT              NOT NULL,

    -- ISO 4217 currency code. Must match BTC_FIAT_CURRENCY config. Stored per-invoice
    -- so the record is self-contained even if the platform currency changes.
    fiat_currency_code  TEXT        NOT NULL,

    -- BTC/fiat exchange rate at creation time. Stored for audit and fiat reconstruction.
    btc_rate_at_creation NUMERIC(18,8) NOT NULL,

    -- ── Immutable tier + wallet snapshot ──────────────────────────────────────
    -- These columns are written once at creation and never updated.
    -- The settlement engine reads exclusively from the snapshot, not from live tier config.

    wallet_mode                         btc_wallet_mode NOT NULL,

    -- External destination address governing sweep proceeds for bridge/hybrid modes.
    -- Snapshotted from vendor_wallet_config.bridge_destination_address at creation.
    -- NULL for platform wallet mode. Never updated after creation.
    bridge_destination_address          TEXT,

    -- Auto-sweep threshold for hybrid mode. NULL for bridge/platform.
    auto_sweep_threshold_sat            BIGINT,

    processing_fee_rate                 NUMERIC(5,2)    NOT NULL,
    confirmation_depth                  INTEGER         NOT NULL,
    miner_fee_cap_sat_vbyte             INTEGER         NOT NULL,
    payment_tolerance_pct               NUMERIC(4,2)    NOT NULL,
    minimum_invoice_sat                 BIGINT          NOT NULL,
    overpayment_relative_threshold_pct  NUMERIC(5,2)    NOT NULL,
    overpayment_absolute_threshold_sat  BIGINT          NOT NULL,
    expected_batch_size                 INTEGER         NOT NULL,
    invoice_expiry_minutes              INTEGER         NOT NULL,

    -- ── Expiry ────────────────────────────────────────────────────────────────

    -- Original expiry timestamp, BEFORE any outage compensation.
    -- The expiry cleanup job uses the formula in invoice-feature.md §5 to compute
    -- effective_expires_at by adding btc_outage_log overlap with this window.
    expires_at  TIMESTAMPTZ         NOT NULL,

    -- ── Payment detection state ────────────────────────────────────────────────

    -- txid currently being watched; set on pending → detected transition.
    detected_txid   TEXT,
    detected_at     TIMESTAMPTZ,

    -- Fiat equivalent of the detected payment, in minor currency units. Informational.
    fiat_equiv_at_detection BIGINT,

    -- Two-cycle mempool-drop watchdog state. Set on first absent check; cleared to NULL
    -- on mempool_dropped → detected re-transition (same UPDATE, same statement).
    mempool_absent_since    TIMESTAMPTZ,

    -- Block height when the invoice received its first confirmation.
    -- SET ATOMICALLY in the same DB transaction as the detected → confirming transition.
    -- Used by the reorg rollback query: WHERE first_confirmed_block_height = $disconnected_height.
    first_confirmed_block_height BIGINT,

    -- ── Settlement state ───────────────────────────────────────────────────────

    -- 'confirming' when claimed from confirming status; 'underpaid' when from underpaid.
    -- Preserved in settlement_failed so admin retry sets the correct predecessor.
    -- NULL for all statuses except 'settling' and 'settlement_failed'.
    settling_source TEXT,

    -- Set TRUE atomically with the constructing → broadcast transition on the payout record.
    -- Pivot for reorg rollback: invoices where sweep_completed=TRUE → reorg_admin_required
    -- rather than rolling back to detected.
    sweep_completed BOOLEAN         NOT NULL DEFAULT FALSE,

    -- Settlement retry counter. Reset to 0 on admin-triggered retry.
    retry_count     INTEGER         NOT NULL DEFAULT 0,

    -- ── Fiat equivalents at key timestamps ────────────────────────────────────

    -- Authoritative for tax and accounting purposes (settlement-technical.md).
    fiat_equiv_at_settlement    BIGINT,

    -- ── Buyer data ────────────────────────────────────────────────────────────

    -- Optional; strongly encouraged. Subject to ismine check before storage.
    -- PII — subject to platform data retention policy.
    buyer_refund_address TEXT,

    -- ── Terminal timestamps ────────────────────────────────────────────────────

    expired_at      TIMESTAMPTZ,
    cancelled_at    TIMESTAMPTZ,
    settled_at      TIMESTAMPTZ,
    refunded_at     TIMESTAMPTZ,
    closed_at       TIMESTAMPTZ,

    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_inv_network
        CHECK (network IN ('mainnet', 'testnet4')),
    CONSTRAINT chk_inv_amount_positive
        CHECK (amount_sat > 0),
    CONSTRAINT chk_inv_fiat_amount_non_negative
        CHECK (fiat_amount >= 0),
    CONSTRAINT chk_inv_rate_positive
        CHECK (btc_rate_at_creation > 0),

    -- Snapshot constraints (mirror btc_tier_config CHECK constraints).
    CONSTRAINT chk_inv_snap_fee_rate
        CHECK (processing_fee_rate >= 0 AND processing_fee_rate <= 50),
    CONSTRAINT chk_inv_snap_confirm_depth
        CHECK (confirmation_depth >= 1 AND confirmation_depth <= 144),
    CONSTRAINT chk_inv_snap_fee_cap
        CHECK (miner_fee_cap_sat_vbyte >= 1 AND miner_fee_cap_sat_vbyte <= 10000),
    CONSTRAINT chk_inv_snap_tolerance
        CHECK (payment_tolerance_pct >= 0 AND payment_tolerance_pct <= 10),
    CONSTRAINT chk_inv_snap_min_invoice
        CHECK (minimum_invoice_sat >= 1000),

    -- Bridge/hybrid modes require snapshotted destination address.
    CONSTRAINT chk_inv_bridge_addr_coherent
        CHECK (wallet_mode = 'platform' OR bridge_destination_address IS NOT NULL),
    CONSTRAINT chk_inv_platform_no_bridge_addr
        CHECK (wallet_mode != 'platform' OR bridge_destination_address IS NULL),

    -- Hybrid mode requires snapshotted threshold; NULL is invalid and would cause
    -- the settlement engine to read a NULL threshold from the snapshot, silently
    -- skipping the auto-sweep trigger for the life of the invoice.
    CONSTRAINT chk_inv_hybrid_threshold
        CHECK (wallet_mode = 'hybrid' OR auto_sweep_threshold_sat IS NULL),
    CONSTRAINT chk_inv_hybrid_threshold_required
        CHECK (wallet_mode != 'hybrid' OR auto_sweep_threshold_sat IS NOT NULL),

    -- settling_source must be one of the two valid predecessor statuses.
    CONSTRAINT chk_inv_settling_source
        CHECK (settling_source IS NULL
            OR settling_source IN ('confirming', 'underpaid')),

    CONSTRAINT chk_inv_retry_non_negative
        CHECK (retry_count >= 0),

    -- Invoice amount must satisfy the snapshotted tier minimum.
    -- Enforces at the DB level that no invoice was created below the floor that the
    -- settlement engine relies on for fee-floor calculations.
    CONSTRAINT chk_inv_amount_gte_minimum
        CHECK (amount_sat >= minimum_invoice_sat),

    -- Expiry must be strictly after creation; a non-positive expiry window produces
    -- an invoice that is already expired at creation time.
    CONSTRAINT chk_inv_expires_after_created
        CHECK (expires_at > created_at),

    -- detected_txid and detected_at are always written in the same status transition;
    -- one non-NULL while the other is NULL indicates a partial write.
    CONSTRAINT chk_inv_detected_coherent
        CHECK ((detected_txid IS NULL) = (detected_at IS NULL)),

    -- settling_source must be populated whenever the invoice is in 'settling' or
    -- 'settlement_failed'. Without it, an admin retry cannot know the correct
    -- predecessor status and may apply the wrong settlement path.
    CONSTRAINT chk_inv_settling_source_required
        CHECK (status NOT IN ('settling', 'settlement_failed')
            OR settling_source IS NOT NULL),

    -- first_confirmed_block_height must be set for all statuses that are only reachable
    -- after a block confirmation. Without enforcement, the reorg rollback query
    -- (WHERE first_confirmed_block_height = $disconnected_height) silently misses rows.
    CONSTRAINT chk_inv_confirmed_height_set
        CHECK (status NOT IN (
                'confirming', 'settling', 'settled', 'settlement_failed',
                'reorg_admin_required', 'refunded'
            ) OR first_confirmed_block_height IS NOT NULL)
);

CREATE TRIGGER trg_invoices_updated_at
    BEFORE UPDATE ON invoices
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Vendor's active invoice list (dashboard, suspension check, deletion guard).
CREATE INDEX idx_inv_vendor_status
    ON invoices(vendor_id, status, created_at DESC);

-- Buyer's active invoice list (rate-limiting: max 20 pending per buyer).
CREATE INDEX idx_inv_buyer_status
    ON invoices(buyer_id, status);

-- Settlement worker: claims confirming invoices that have reached depth target.
CREATE INDEX idx_inv_confirming
    ON invoices(network, first_confirmed_block_height)
    WHERE status = 'confirming';

-- Mempool-drop watchdog: scans all detected invoices to call getmempoolentry.
CREATE INDEX idx_inv_detected
    ON invoices(network, detected_at)
    WHERE status = 'detected';

-- Expiry cleanup job: evaluates both pending and mempool_dropped against effective expiry.
CREATE INDEX idx_inv_expiry_candidates
    ON invoices(network, expires_at)
    WHERE status IN ('pending', 'mempool_dropped');

-- Stale settling-claim watchdog: returns invoices stuck in settling > 5 minutes.
CREATE INDEX idx_inv_stale_settling
    ON invoices(network, updated_at)
    WHERE status = 'settling';

-- Underpaid re-settlement: new payment arrives on an underpaid invoice.
CREATE INDEX idx_inv_underpaid
    ON invoices(network)
    WHERE status = 'underpaid';

-- Reorg rollback: find all invoices confirmed at the disconnected block height.
CREATE INDEX idx_inv_first_confirmed_height
    ON invoices(network, first_confirmed_block_height)
    WHERE first_confirmed_block_height IS NOT NULL;

-- Vendor deletion guard: "does this vendor have any live obligations on this network?"
-- Scans only non-terminal statuses so the index stays selective even for high-volume vendors.
CREATE INDEX idx_inv_vendor_network_active
    ON invoices(vendor_id, network)
    WHERE status NOT IN (
        'expired', 'cancelled', 'settled', 'refunded', 'manually_closed',
        'expired_with_payment', 'cancelled_with_payment'
    );

COMMENT ON TABLE invoices IS
    'Core invoice state machine. 16 statuses; 38 permitted transitions enforced at application layer. '
    'The snapshot columns (wallet_mode, bridge_destination_address, processing_fee_rate, …) are '
    'written once at creation and never updated — they govern settlement regardless of subsequent '
    'tier or vendor config changes. '
    'Every status UPDATE must assert RowsAffected() == 1; 0 rows = concurrent status change.';
COMMENT ON COLUMN invoices.bridge_destination_address IS
    'Snapshotted from vendor_wallet_config at creation. Governs sweep destination — '
    'vendor address changes after creation do not affect this invoice.';
COMMENT ON COLUMN invoices.first_confirmed_block_height IS
    'Set atomically with the detected → confirming status transition. '
    'Used by rollbackSettlementFromHeight to find all invoices affected by a reorg.';
COMMENT ON COLUMN invoices.settling_source IS
    'Preserved in settlement_failed status so admin retry knows the original predecessor. '
    'NULL for all statuses except settling and settlement_failed.';
COMMENT ON COLUMN invoices.sweep_completed IS
    'Set TRUE atomically with constructing → broadcast on the payout record. '
    'Pivot for reorg rollback: TRUE → reorg_admin_required; FALSE → roll back to detected.';
COMMENT ON COLUMN invoices.buyer_refund_address IS
    'Optional buyer-provided address for refunds. Validated via RPC ismine check before storage. '
    'PII — subject to platform data retention policy.';
COMMENT ON COLUMN invoices.fiat_equiv_at_settlement IS
    'Fiat value of received payment at settlement time, in minor currency units. '
    'Authoritative value for tax and accounting purposes.';


/* ═════════════════════════════════════════════════════════════
   INVOICE ADDRESSES
   ═════════════════════════════════════════════════════════════ */

/*
 * Exactly one P2WPKH bech32 address per invoice, derived from Bitcoin Core's HD keypool.
 * Both the address string and HD derivation index are stored:
 *   - address is used for on-chain matching and ZMQ watch registration.
 *   - hd_derivation_index is required for Scenario B/C wallet recovery
 *     (wallet-backup-technical.md §3: keypool cursor advance uses MAX(hd_derivation_index)).
 *
 * The UNIQUE constraint on (address, network) is a hard invariant: if Bitcoin Core ever
 * issues the same address twice (should never happen with a healthy keypool), the second
 * invoice creation fails at the DB level rather than creating a duplicate.
 * On constraint violation: return 503 and fire KeypoolOrRPCError CRITICAL alert.
 * Do NOT retry automatically — the duplicate address would cause double-settlement risk.
 *
 * The label column MUST always be 'invoice'. This value is used by Scenario D recovery:
 *   listlabeladdresses("invoice") enumerates all platform-managed addresses from Bitcoin Core.
 * Any code path that calls getnewaddress with a different label silently breaks recovery.
 * The CHECK (label = 'invoice') enforces this invariant at the DB level.
 */
CREATE TABLE invoice_addresses (
    id          BIGSERIAL   PRIMARY KEY,

    -- 1:1 with invoices. RESTRICT prevents address deletion while invoice exists.
    invoice_id  UUID        NOT NULL REFERENCES invoices(id) ON DELETE RESTRICT,

    address     TEXT        NOT NULL,
    network     TEXT        NOT NULL,

    -- Leaf index from the BIP-32 HD path (e.g. m/84'/0'/0'/0/5200 → 5200).
    -- Required for: Scenario B keypool cursor advance, Scenario C import range calculation.
    hd_derivation_index BIGINT  NOT NULL,

    -- Always 'invoice'. Enforced here to protect Scenario D recovery.
    -- CHECK constraint prevents any other value from being stored, even in tests.
    label       TEXT        NOT NULL DEFAULT 'invoice',

    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_invoice_address_per_invoice UNIQUE (invoice_id),
    CONSTRAINT uq_invoice_address_network     UNIQUE (address, network),
    CONSTRAINT chk_ia_network
        CHECK (network IN ('mainnet', 'testnet4')),
    CONSTRAINT chk_ia_label_invariant
        CHECK (label = 'invoice'),
    -- BIP-32 unhardened leaf indices reach 2^31-1. INTEGER overflows at that exact boundary.
    -- BIGINT eliminates the overflow and is consistent with Bitcoin Core's RPC (64-bit JSON ints).
    CONSTRAINT chk_ia_derivation_non_negative
        CHECK (hd_derivation_index >= 0)
);

-- Address lookup: ZMQ hashtx handler resolves address → invoice.
-- Covered by uq_invoice_address_network (UNIQUE creates implicit index).

-- Derivation index range query: wallet recovery cursor advance.
CREATE INDEX idx_ia_max_derivation ON invoice_addresses(network, hd_derivation_index DESC);

COMMENT ON TABLE invoice_addresses IS
    'One P2WPKH bech32 address per invoice derived from Bitcoin Core HD keypool. '
    'UNIQUE (address, network): duplicate address is KeypoolOrRPCError CRITICAL — do NOT retry. '
    'label MUST always be ''invoice'' (CHECK constraint): critical for Scenario D recovery '
    'via listlabeladdresses(\"invoice\"). '
    'hd_derivation_index is required for Scenario B keypool cursor advance and Scenario C import range.';
COMMENT ON COLUMN invoice_addresses.hd_derivation_index IS
    'Leaf index from hdkeypath returned by getaddressinfo (e.g. m/84''/0''/0''/0/5200 → 5200). '
    'Used in Scenario B/C recovery: MAX(hd_derivation_index) × 1.2 = import range for rescan.';
COMMENT ON COLUMN invoice_addresses.label IS
    'Always ''invoice''. Protected by CHECK constraint. '
    'getnewaddress MUST be called as getnewaddress "invoice" "bech32" — using any other label '
    'silently breaks Scenario D recovery (listlabeladdresses(\"invoice\")).';


/* ═════════════════════════════════════════════════════════════
   INVOICE ADDRESS MONITORING
   ═════════════════════════════════════════════════════════════ */

/*
 * DB-backed ZMQ watch list. This is the authoritative set of addresses the ZMQ subscriber
 * must watch. It survives process restarts and is consistent across horizontal replicas.
 *
 * Immediate registration: when a new invoice address is created, the ZMQ subscriber
 * RegisterImmediate() is called AFTER the DB write succeeds (not before). The 5-minute
 * reload is a safety net only. See invoice-technical.md §2 for the ordering requirement.
 *
 * Monitoring window rules (set in the same DB transaction as the terminal transition):
 *   expired / cancelled / settled / refunded / manually_closed → monitor_until = terminal_at + 30 days
 *   reorg_admin_required → monitor_until = NULL (open-ended; set only when leaving this status)
 *   Non-terminal statuses → monitor_until = NULL (never retired)
 *
 * The expiry cleanup job retires elapsed windows:
 *   UPDATE ... SET status = 'retired' WHERE monitor_until < NOW() AND status = 'active'
 * The partial index WHERE status = 'active' keeps the hot ZMQ reload query fast regardless
 * of how many retired rows accumulate. Retired rows are never deleted (audit retention).
 */
CREATE TABLE invoice_address_monitoring (
    id          BIGSERIAL           PRIMARY KEY,
    invoice_id  UUID                NOT NULL REFERENCES invoices(id) ON DELETE RESTRICT,
    address     TEXT                NOT NULL,
    network     TEXT                NOT NULL,

    -- NULL = actively monitored (invoice not yet in a terminal state, or reorg_admin_required).
    -- Set in the same DB transaction as the terminal status transition.
    monitor_until   TIMESTAMPTZ,

    status  btc_monitoring_status   NOT NULL DEFAULT 'active',

    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_iam_network
        CHECK (network IN ('mainnet', 'testnet4')),

    -- A retired row must have had monitor_until set before retirement was recorded.
    -- Enforces that the retirement workflow always goes through the monitor_until
    -- assignment step; a row retired with NULL monitor_until is a workflow bug.
    CONSTRAINT chk_iam_retired_has_monitor_until
        CHECK (status != 'retired' OR monitor_until IS NOT NULL)
);

CREATE TRIGGER trg_invoice_address_monitoring_updated_at
    BEFORE UPDATE ON invoice_address_monitoring
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- HOT PATH: ZMQ hashtx handler resolves address + network → invoice_id.
-- Partial index keeps this fast regardless of retired row count.
CREATE INDEX idx_iam_active
    ON invoice_address_monitoring(address, network)
    WHERE status = 'active';

-- ZMQ startup/reconnect reload: fetch all active monitoring records.
CREATE INDEX idx_iam_reload
    ON invoice_address_monitoring(network, invoice_id)
    WHERE status = 'active';

-- Expiry cleanup job: find elapsed windows.
CREATE INDEX idx_iam_expiry_cleanup
    ON invoice_address_monitoring(monitor_until)
    WHERE status = 'active' AND monitor_until IS NOT NULL;

-- Enforce at most one active monitoring record per invoice.
-- Prevents the ZMQ subscriber from dispatching the same payment event twice
-- if a bug creates a duplicate active row for the same invoice.
CREATE UNIQUE INDEX uq_iam_one_active_per_invoice
    ON invoice_address_monitoring(invoice_id)
    WHERE status = 'active';

-- Enforce that an address is not actively monitored for two different invoices
-- simultaneously (guard against keypool address reuse at the DB layer).
CREATE UNIQUE INDEX uq_iam_active_address_network
    ON invoice_address_monitoring(address, network)
    WHERE status = 'active';

COMMENT ON TABLE invoice_address_monitoring IS
    'Authoritative ZMQ watch list. Survives restarts; consistent across horizontal replicas. '
    'New addresses are registered immediately via RegisterImmediate() AFTER DB write (not before). '
    'The 5-minute reload is a safety net only. '
    'Retired rows are never deleted — the partial index WHERE status = ''active'' keeps queries fast.';
COMMENT ON COLUMN invoice_address_monitoring.monitor_until IS
    'NULL = active monitoring (non-terminal status or reorg_admin_required). '
    'Set in the same TX as terminal transition: terminal_at + 30 days for most statuses. '
    'reorg_admin_required keeps monitor_until = NULL until the invoice leaves that status.';


/* ═════════════════════════════════════════════════════════════
   INVOICE PAYMENTS
   ═════════════════════════════════════════════════════════════ */

/*
 * Append-only record of every on-chain payment received for an invoice.
 * A payment is always recorded regardless of invoice status, including for
 * post-settlement, double-payment, and late-payment cases.
 *
 * Idempotency: all INSERTs use ON CONFLICT (txid, vout_index) DO NOTHING.
 * The UNIQUE constraint enforces this at the DB level.
 *
 * Multi-output handling: a single TX may send multiple vouts to the same address.
 * Settlement Phase 1 sums ALL value_sat for a given invoice_id+txid combination
 * before comparing against the tolerance band. The covering index on invoice_id
 * is required for this SUM query at settlement time.
 */
CREATE TABLE invoice_payments (
    id              BIGSERIAL       PRIMARY KEY,
    invoice_id      UUID            NOT NULL REFERENCES invoices(id) ON DELETE RESTRICT,
    txid            TEXT            NOT NULL,
    vout_index      INTEGER         NOT NULL,
    value_sat       BIGINT          NOT NULL,
    detected_at     TIMESTAMPTZ     NOT NULL DEFAULT NOW(),

    -- TRUE when this txid is distinct from the invoice's detected_txid while the
    -- original is still in the mempool — may indicate a double-spend.
    double_payment  BOOLEAN         NOT NULL DEFAULT FALSE,

    -- TRUE when this payment arrived after the invoice was already in 'settled' status.
    post_settlement BOOLEAN         NOT NULL DEFAULT FALSE,

    CONSTRAINT uq_inv_payment_txid_vout UNIQUE (txid, vout_index),
    CONSTRAINT chk_ip_value_positive
        CHECK (value_sat > 0),
    CONSTRAINT chk_ip_vout_non_negative
        CHECK (vout_index >= 0),

    -- Guarantees the invoice had an address allocated before any payment is recorded.
    -- invoice_addresses.invoice_id is UNIQUE, so this FK resolves to exactly one address.
    -- Without this, a payment could be written for an invoice that was never assigned
    -- an address, making on-chain reconciliation against that address impossible.
    CONSTRAINT fk_ip_invoice_address
        FOREIGN KEY (invoice_id)
        REFERENCES invoice_addresses(invoice_id)
        ON DELETE RESTRICT
);

-- Covering index: Phase 1 SUM query (SUM(value_sat) WHERE invoice_id = $id AND status = 'confirming').
CREATE INDEX idx_ip_invoice_id ON invoice_payments(invoice_id);

-- Reconciliation: sum in-flight payments for invoices in confirming/settling/etc.
CREATE INDEX idx_ip_invoice_detected ON invoice_payments(invoice_id, detected_at DESC);

COMMENT ON TABLE invoice_payments IS
    'Append-only on-chain payment records. One row per (txid, vout_index). '
    'Always INSERT with ON CONFLICT (txid, vout_index) DO NOTHING for idempotency. '
    'Multi-output TXs generate one row per vout — settlement Phase 1 SUMs all rows for the invoice. '
    'double_payment and post_settlement flag anomalous payments that require admin review.';


/* ═════════════════════════════════════════════════════════════
   BTC OUTAGE LOG
   ═════════════════════════════════════════════════════════════ */

/*
 * Records periods when the Bitcoin Core node was unreachable.
 * Used by the expiry cleanup job to compute effective_expires_at for invoices:
 *
 *   effective_expires_at = original_expires_at
 *     + COALESCE((
 *         SELECT SUM(
 *           LEAST(COALESCE(ended_at, NOW()), original_expires_at)
 *           - GREATEST(started_at, invoice.created_at)
 *         )
 *         FROM btc_outage_log
 *         WHERE started_at < original_expires_at
 *           AND COALESCE(ended_at, NOW()) > invoice.created_at
 *       ), INTERVAL '0')
 *
 * Write protocol:
 *   On disconnect:  INSERT with ended_at = NULL; use pg_try_advisory_lock to prevent
 *                   duplicate open records across horizontal instances.
 *   On reconnect:   UPDATE ... SET ended_at = NOW() WHERE id = $id AND ended_at IS NULL.
 *   On startup:     Close any open record from a previous (crashed) process.
 *   Stale records:  A periodic job (every 6h) closes records older than 48h with
 *                   ended_at = MIN(NOW(), started_at + INTERVAL '48 hours').
 *                   Advisory lock: pg_try_advisory_lock(hashtext('btc_outage_log:' || network)).
 */
CREATE TABLE btc_outage_log (
    id          BIGSERIAL       PRIMARY KEY,
    network     TEXT            NOT NULL,
    started_at  TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    ended_at    TIMESTAMPTZ,

    CONSTRAINT chk_outage_network
        CHECK (network IN ('mainnet', 'testnet4')),
    CONSTRAINT chk_outage_times
        CHECK (ended_at IS NULL OR ended_at > started_at)
);

-- DB-level enforcement that at most one open outage record exists per network.
-- Complements the application-side pg_try_advisory_lock; this constraint is the
-- authoritative guard — the advisory lock reduces contention but a horizontal
-- instance that skips the lock or uses the wrong key cannot create a duplicate.
CREATE UNIQUE INDEX uq_outage_one_open_per_network
    ON btc_outage_log(network)
    WHERE ended_at IS NULL;

-- Advisory-lock duplicate check: "is there already an open outage record for this network?"
-- Primary hot path for the advisory lock check and stale-record maintenance job.
CREATE INDEX idx_outage_open
    ON btc_outage_log(network)
    WHERE ended_at IS NULL;

-- Expiry formula range join: overlap check between outage windows and invoice creation/expiry.
CREATE INDEX idx_outage_range
    ON btc_outage_log(network, started_at, ended_at);

COMMENT ON TABLE btc_outage_log IS
    'Node outage periods for expiry-timer compensation. '
    'INSERT on disconnect; UPDATE ended_at on reconnect; close stale records on startup. '
    'Advisory lock (hashtext(''btc_outage_log:'' || network)) prevents duplicate open records '
    'across horizontal instances. Stale records (> 48 hours) closed by a 6h maintenance job.';
COMMENT ON COLUMN btc_outage_log.ended_at IS
    'NULL = outage is ongoing. Application startup must close any open record left by a '
    'crashed previous process before accepting connections.';


/* ═════════════════════════════════════════════════════════════
   BITCOIN BLOCK HISTORY
   ═════════════════════════════════════════════════════════════ */

/*
 * Processed-block log. One row per block height per network.
 * Pruned blocks that fall outside the node's prune window get a placeholder row
 * (block_hash = NULL, pruned = TRUE) so the backfill cursor can advance past them
 * without getting stuck. The cursor in bitcoin_sync_state always reflects the
 * highest height for which a row exists in this table.
 */
CREATE TABLE bitcoin_block_history (
    height      BIGINT      NOT NULL,
    network     TEXT        NOT NULL,
    block_hash  TEXT,           -- NULL for pruned blocks
    pruned      BOOLEAN     NOT NULL DEFAULT FALSE,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (height, network),

    CONSTRAINT chk_bbh_network
        CHECK (network IN ('mainnet', 'testnet4')),
    CONSTRAINT chk_bbh_height_non_negative
        CHECK (height >= 0),
    CONSTRAINT chk_bbh_pruned_coherent
        CHECK (pruned = FALSE OR block_hash IS NULL)
);

-- Range scan: "find all blocks between height A and B for this network".
-- PK covers (height, network) but this covers the opposite direction.
CREATE INDEX idx_bbh_network_height
    ON bitcoin_block_history(network, height DESC);

COMMENT ON TABLE bitcoin_block_history IS
    'Processed-block log per network. Pruned blocks get placeholder rows so the '
    'HandleRecovery cursor can advance past them. '
    'pruned = TRUE implies block_hash = NULL (enforced by CHECK).';


/* ═════════════════════════════════════════════════════════════
   PAYOUT RECORDS
   ═════════════════════════════════════════════════════════════ */

/*
 * Vendor payout lifecycle from settlement credit to on-chain confirmation.
 * One record per settled invoice. Multiple records may share a batch_txid
 * (batch sweep: multiple vendors consolidated into one TX).
 *
 * CRITICAL invariant — DB trigger fn_btc_payout_guard (defined below):
 *   A payout record may only be created when the parent invoice is in 'settled' status.
 *   This is enforced by a BEFORE INSERT trigger that raises an exception if the parent
 *   invoice.status != 'settled'. Enforcement at both application and DB layers.
 *
 * Broadcast ordering invariant (settlement-technical.md §1):
 *   constructing → broadcast DB UPDATE (with txid) MUST commit and assert RowsAffected > 0
 *   BEFORE sendrawtransaction is called. Never reverse this order.
 *
 * RBF tracking: both original_txid and batch_txid are recorded when RBF triggers.
 *
 * Stale constructing watchdog: records in 'constructing' for > BTC_CONSTRUCTING_WATCHDOG_THRESHOLD
 * (default 10 min) are returned to 'queued' by the watchdog job.
 */
CREATE TABLE payout_records (
    id          UUID            PRIMARY KEY DEFAULT uuidv7(),

    -- Parent invoice. RESTRICT prevents orphan payout records.
    invoice_id  UUID            NOT NULL REFERENCES invoices(id) ON DELETE RESTRICT,

    -- Vendor receiving the payout.
    vendor_id   UUID            NOT NULL REFERENCES users(id) ON DELETE RESTRICT,

    network     TEXT            NOT NULL,
    status      btc_payout_status NOT NULL DEFAULT 'held',

    -- Net satoshis owed to the vendor (received_amount - platform_fee).
    net_satoshis            BIGINT      NOT NULL,

    -- Platform processing fee deducted at settlement. Tracked separately from
    -- miner_fee_satoshis for tier profitability analysis.
    platform_fee_satoshis   BIGINT      NOT NULL DEFAULT 0,

    -- Wallet mode snapshotted from the parent invoice. Required for audit and for the
    -- destination_address coherence constraints below.
    wallet_mode             btc_wallet_mode NOT NULL,

    -- Copied from the invoice snapshot at payout creation — the vendor's external address
    -- or NULL for platform wallet mode accumulation (no on-chain sweep needed).
    destination_address     TEXT,

    -- ── Sweep batch fields ─────────────────────────────────────────────────────

    -- UUID grouping all payout records in the same sweep TX.
    -- NULL until the record transitions to constructing.
    batch_id    UUID,

    -- txid of the sweep TX. Set at constructing → broadcast (BEFORE sendrawtransaction).
    -- NULL until broadcast.
    batch_txid  TEXT,

    -- This vendor's output index within the sweep TX. Set at constructing.
    vout_index_in_batch     INTEGER,

    -- Fee rate used when constructing this batch. In sat/vbyte.
    fee_rate_sat_vbyte      NUMERIC(10,4),

    -- Actual miner fee attributable to this payout (estimated from PSBT fee / output count).
    miner_fee_satoshis      BIGINT,

    -- Original txid before RBF. Set when a replacement TX is constructed.
    -- Both original_txid and batch_txid are preserved for audit.
    original_txid           TEXT,

    -- ── Compliance ────────────────────────────────────────────────────────────

    kyc_status  btc_kyc_status  NOT NULL DEFAULT 'not_required',

    -- ── Admin resolution fields ────────────────────────────────────────────────

    -- Required for manual_payout and admin-forced transitions (held → failed, etc.).
    resolution_reason       TEXT,
    resolution_admin_id     UUID REFERENCES users(id) ON DELETE SET NULL,

    -- ── Timestamps ────────────────────────────────────────────────────────────

    broadcast_at    TIMESTAMPTZ,
    confirmed_at    TIMESTAMPTZ,

    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_pr_network
        CHECK (network IN ('mainnet', 'testnet4')),
    CONSTRAINT chk_pr_net_satoshis_positive
        CHECK (net_satoshis > 0),
    CONSTRAINT chk_pr_platform_fee_non_negative
        CHECK (platform_fee_satoshis >= 0),
    CONSTRAINT chk_pr_miner_fee_non_negative
        CHECK (miner_fee_satoshis IS NULL OR miner_fee_satoshis >= 0),
    CONSTRAINT chk_pr_vout_non_negative
        CHECK (vout_index_in_batch IS NULL OR vout_index_in_batch >= 0),

    -- One payout record per invoice: the critical race-safe guard against double-payout.
    -- fn_btc_payout_guard checks invoice.status = 'settled' but cannot prevent two
    -- concurrent settlement workers racing past the trigger before either INSERT commits.
    -- This UNIQUE constraint is the only DB-level enforcement that is race-safe.
    CONSTRAINT uq_pr_invoice_id UNIQUE (invoice_id),

    -- destination_address coherence: mirrors the invoice snapshot invariants.
    CONSTRAINT chk_pr_destination_coherent
        CHECK (wallet_mode = 'platform' OR destination_address IS NOT NULL),
    CONSTRAINT chk_pr_platform_no_destination
        CHECK (wallet_mode != 'platform' OR destination_address IS NULL),

    -- Fee rate must be positive when present.
    CONSTRAINT chk_pr_fee_rate_positive
        CHECK (fee_rate_sat_vbyte IS NULL OR fee_rate_sat_vbyte > 0),

    -- Net payout must exceed the miner fee charged to this output.
    -- Prevents a record becoming permanently stuck in constructing when the fee
    -- estimate at broadcast time exceeds the vendor net (settlement-technical.md §4).
    CONSTRAINT chk_pr_net_exceeds_miner_fee
        CHECK (miner_fee_satoshis IS NULL OR net_satoshis > miner_fee_satoshis)
);

CREATE TRIGGER trg_payout_records_updated_at
    BEFORE UPDATE ON payout_records
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Invoice → payout lookup (reconciliation, settlement Phase 2 guard).
CREATE INDEX idx_pr_invoice_id ON payout_records(invoice_id);

-- Vendor payout list (admin dashboard, suspension check, deletion guard).
CREATE INDEX idx_pr_vendor_status
    ON payout_records(vendor_id, status, created_at DESC);

-- Sweep job: claim all queued records for a vendor.
CREATE INDEX idx_pr_queued_vendor
    ON payout_records(network, vendor_id)
    WHERE status = 'queued';

-- Batch lookup: find all records in a batch TX (confirmation handler, RBF).
CREATE INDEX idx_pr_batch_txid
    ON payout_records(batch_txid)
    WHERE batch_txid IS NOT NULL;

-- Stale constructing watchdog.
CREATE INDEX idx_pr_stale_constructing
    ON payout_records(network, updated_at)
    WHERE status = 'constructing';

-- Held aging monitor (7d WARNING, 30d CRITICAL alerts).
CREATE INDEX idx_pr_held_aging
    ON payout_records(network, created_at)
    WHERE status = 'held';

COMMENT ON TABLE payout_records IS
    'Vendor payout lifecycle per settled invoice. '
    'BEFORE INSERT trigger (fn_btc_payout_guard) rejects inserts when parent invoice.status != ''settled''. '
    'Broadcast ordering invariant: constructing→broadcast UPDATE must commit BEFORE sendrawtransaction. '
    'RowsAffected on the constructing→broadcast UPDATE must be > 0; 0 rows = watchdog reclaimed, abort broadcast.';
COMMENT ON COLUMN payout_records.batch_txid IS
    'Set atomically with the constructing → broadcast transition. '
    'CRITICAL: this DB commit must precede sendrawtransaction. If sendrawtransaction subsequently '
    'fails, the stuck-sweep watchdog detects the broadcast record with no network confirmation '
    'and triggers RBF or escalation.';
COMMENT ON COLUMN payout_records.original_txid IS
    'Populated when RBF is triggered. Both this and batch_txid are preserved '
    'so the audit trail shows the full replacement history.';


/* ═════════════════════════════════════════════════════════════
   FINANCIAL AUDIT EVENTS
   ═════════════════════════════════════════════════════════════ */

/*
 * Immutable, append-only financial audit trail. Retained indefinitely.
 * Separate from the auth_audit_log (which covers authentication events).
 *
 * Immutability is enforced at two independent layers:
 *   1. Application DB user has INSERT and SELECT only on this table.
 *      UPDATE and DELETE are not granted (enforce via DB role in ops runbook).
 *   2. DB triggers (fn_btc_audit_immutable) reject UPDATE and DELETE from
 *      any DB user, including privileged migration users.
 *
 * Admin resolution pattern (audit-technical.md §2):
 *   Admin overrides are written as NEW rows referencing the original event via
 *   references_event_id. The original row is NEVER modified.
 *
 * Column strategy (Hybrid D-7):
 *   Fixed columns for all financial amounts and timestamps — queryable, typed, indexed,
 *   and required by the reconciliation and reporting queries without JSONB extraction.
 *   JSONB metadata column for event-type-specific structured data (admin action details,
 *   step-up auth records, rate-stale diagnostic info, etc.).
 */
CREATE TABLE financial_audit_events (
    id          BIGSERIAL       PRIMARY KEY,

    event_type  TEXT            NOT NULL,
    timestamp   TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    network     TEXT,

    -- Who triggered the event.
    actor_type  TEXT            NOT NULL,   -- 'system' | 'vendor' | 'buyer' | 'admin'
    actor_id    UUID            REFERENCES users(id) ON DELETE SET NULL,
    -- Stable snapshot of the actor's identity (email, username, or 'system') captured at
    -- INSERT time by the application layer. Preserved even after the users row is deleted.
    -- Ensures admin-tier audit events remain attributable regardless of user lifecycle;
    -- actor_id going NULL on user deletion does not lose the identity.
    -- Must not be empty for non-system actors (enforced by chk_fae_actor_label_present).
    actor_label TEXT            NOT NULL DEFAULT '',

    -- References to the financial objects involved.
    invoice_id          UUID    REFERENCES invoices(id)      ON DELETE RESTRICT,
    payout_record_id    UUID    REFERENCES payout_records(id) ON DELETE RESTRICT,

    -- For admin override events: points to the original event being resolved.
    -- The original event is NEVER modified — resolution is always a new row.
    references_event_id BIGINT  REFERENCES financial_audit_events(id) ON DELETE RESTRICT,

    -- ── Fixed financial columns (queryable, typed) ─────────────────────────────

    -- Primary satoshi amount for this event (invoice amount, payout net, debit amount, etc.).
    amount_sat          BIGINT,

    -- Before/after balance for balance-change events (settlement credits, debits, withdrawals).
    balance_before_sat  BIGINT,
    balance_after_sat   BIGINT,

    -- Fiat equivalent at event time, in minor currency units (e.g. USD cents).
    fiat_equivalent     BIGINT,
    fiat_currency_code  TEXT,

    -- TRUE when a subscription debit proceeds using a stale rate (cache > BTC_SUBSCRIPTION_DEBIT_MAX_RATE_AGE_SECONDS).
    -- Triggers a WARNING alert with rate_age, rate_used, and last_known_valid_rate in metadata.
    rate_stale          BOOLEAN NOT NULL DEFAULT FALSE,

    -- ── JSONB metadata for event-specific structured data ──────────────────────
    -- Examples by event_type:
    --   settlement_admin_close: {action_type, reason, step_up_authenticated_at, admin_id}
    --   reorg_resolved_platform_loss: {action_type, reason, step_up_authenticated_at}
    --   subscription_debit_stale_rate: {rate_age_seconds, rate_used, last_known_valid_rate}
    --   hybrid_auto_sweep_triggered: {balance_before, balance_after, records_promoted}
    --   bitcoin_sse_token_issued: {jti_hash, exp, source_ip}
    metadata    JSONB           NOT NULL DEFAULT '{}',

    CONSTRAINT chk_fae_actor_type
        CHECK (actor_type IN ('system', 'vendor', 'buyer', 'admin')),
    CONSTRAINT chk_fae_metadata_is_object
        CHECK (jsonb_typeof(metadata) = 'object'),
    CONSTRAINT chk_fae_event_type_length
        CHECK (length(event_type) <= 128),

    -- balance_before and balance_after must both be present or both absent.
    -- A balance-change event with only one side populated is a silent data corruption
    -- that makes before/after reconciliation calculations impossible.
    CONSTRAINT chk_fae_balance_change_coherent
        CHECK ((balance_before_sat IS NULL) = (balance_after_sat IS NULL)),

    -- For known financial event types, amount_sat must be non-NULL.
    -- A settlement or payout event with a NULL amount silently corrupts reconciliation
    -- and compliance reports. Extend the event list as the catalogue grows.
    CONSTRAINT chk_fae_financial_amount_present
        CHECK (
            event_type NOT IN (
                'settlement_complete', 'payout_confirmed', 'payout_broadcast',
                'balance_credit', 'balance_debit', 'subscription_debit',
                'refund_issued', 'treasury_increment', 'treasury_decrement'
            )
            OR amount_sat IS NOT NULL
        ),

    -- Non-system actors must have a non-empty label so admin events remain
    -- attributable after user deletion (when actor_id becomes NULL).
    CONSTRAINT chk_fae_actor_label_present
        CHECK (actor_type = 'system' OR length(actor_label) > 0)
);

-- Time-range queries for compliance reports and incident response.
CREATE INDEX idx_fae_timestamp ON financial_audit_events(timestamp DESC);

-- Per-invoice audit trail.
CREATE INDEX idx_fae_invoice ON financial_audit_events(invoice_id, timestamp DESC)
    WHERE invoice_id IS NOT NULL;

-- Per-payout audit trail.
CREATE INDEX idx_fae_payout ON financial_audit_events(payout_record_id, timestamp DESC)
    WHERE payout_record_id IS NOT NULL;

-- Per-actor audit trail (admin investigation, vendor history).
CREATE INDEX idx_fae_actor ON financial_audit_events(actor_id, timestamp DESC)
    WHERE actor_id IS NOT NULL;

-- Event-type filtering (e.g. "all settlement_failed events in the last week").
CREATE INDEX idx_fae_event_type ON financial_audit_events(event_type, timestamp DESC);

-- Resolution chain navigation: "what was resolved by event X?"
CREATE INDEX idx_fae_references ON financial_audit_events(references_event_id)
    WHERE references_event_id IS NOT NULL;

COMMENT ON TABLE financial_audit_events IS
    'Immutable append-only financial audit trail. Retained indefinitely. '
    'Immutability enforced at two layers: (1) application DB user has INSERT+SELECT only; '
    '(2) fn_btc_audit_immutable trigger rejects UPDATE/DELETE from any DB user. '
    'Admin resolutions are new rows via references_event_id — the original row is never modified. '
    'Fixed financial columns for queryability; JSONB metadata for event-specific structured data.';
COMMENT ON COLUMN financial_audit_events.references_event_id IS
    'For admin override events: FK to the original event being resolved. '
    'The original row is NEVER modified; resolution = new row.';
COMMENT ON COLUMN financial_audit_events.rate_stale IS
    'TRUE when a subscription debit uses a stale exchange rate. '
    'Triggers WARNING alert; rate diagnostics in metadata.';
COMMENT ON COLUMN financial_audit_events.metadata IS
    'JSONB object for event-type-specific data. Always a valid JSON object (CHECK). '
    'Examples: step_up_authenticated_at for admin overrides, '
    'records_promoted for hybrid auto-sweep events.';


/* ═════════════════════════════════════════════════════════════
   WALLET BACKUP SUCCESS
   ═════════════════════════════════════════════════════════════ */

/*
 * Tracks successful wallet.dat backups. A record is written ONLY after both:
 *   1. The backupwallet RPC returns success.
 *   2. The backup file has been COPIED to backup storage and the copy verified.
 *
 * The "Wallet backup overdue" CRITICAL alert fires when the latest record for a
 * network has not been updated within the expected backup window (4h for mainnet,
 * 24h for testnet4). The alert is based on this table's timestamp, NOT on the
 * job's scheduled run time — a job that runs but whose copy step silently fails
 * will still trigger the alert.
 */
CREATE TABLE wallet_backup_success (
    id          BIGSERIAL       PRIMARY KEY,
    network     TEXT            NOT NULL,

    -- Written only after copy-to-storage completes and checksum is verified.
    timestamp   TIMESTAMPTZ     NOT NULL,

    filename            TEXT    NOT NULL,
    sha256_checksum     TEXT    NOT NULL,
    storage_location    TEXT    NOT NULL,

    created_at  TIMESTAMPTZ     NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_wbs_network
        CHECK (network IN ('mainnet', 'testnet4'))
);

-- Hot path: "what is the latest backup record for this network?"
CREATE INDEX idx_wbs_latest
    ON wallet_backup_success(network, timestamp DESC);

COMMENT ON TABLE wallet_backup_success IS
    'Wallet backup health tracking. Written only after copy-to-storage completes and is verified. '
    '"Wallet backup overdue" CRITICAL alert fires when latest record timestamp is stale '
    '(> 4h on mainnet, > 24h on testnet4). '
    'A job that runs but fails the copy step does NOT write here, ensuring the alert fires.';
COMMENT ON COLUMN wallet_backup_success.timestamp IS
    'Timestamp of the COMPLETED backup (both backupwallet RPC + copy verified). '
    'This is the basis for the overdue alert — not the job run timestamp.';


-- Functions and triggers are defined in 010_btc_functions.sql.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

/*
 * No explicit trigger drops needed here.
 *   - Guard/immutability triggers and functions are owned by 010_btc_functions.sql
 *     and dropped there (Goose rolls back 010 before 009).
 *   - updated_at triggers are dropped implicitly by DROP TABLE ... CASCADE below.
 */

/* ── Tables (reverse FK dependency order) ──────────────────── */
DROP TABLE IF EXISTS wallet_backup_success        CASCADE;
DROP TABLE IF EXISTS financial_audit_events       CASCADE;
DROP TABLE IF EXISTS payout_records               CASCADE;
DROP TABLE IF EXISTS bitcoin_block_history        CASCADE;
DROP TABLE IF EXISTS btc_outage_log               CASCADE;
DROP TABLE IF EXISTS invoice_payments             CASCADE;
DROP TABLE IF EXISTS invoice_address_monitoring   CASCADE;
DROP TABLE IF EXISTS invoice_addresses            CASCADE;
DROP TABLE IF EXISTS invoices                     CASCADE;
DROP TABLE IF EXISTS vendor_balances              CASCADE;
DROP TABLE IF EXISTS vendor_wallet_config         CASCADE;
DROP TABLE IF EXISTS bitcoin_sync_state           CASCADE;
DROP TABLE IF EXISTS reconciliation_job_state     CASCADE;
DROP TABLE IF EXISTS platform_config              CASCADE;
DROP TABLE IF EXISTS btc_tier_config              CASCADE;

/* ── ENUMs ─────────────────────────────────────────────────── */
DROP TYPE IF EXISTS btc_monitoring_status CASCADE;
DROP TYPE IF EXISTS btc_kyc_status        CASCADE;
DROP TYPE IF EXISTS btc_payout_status     CASCADE;
DROP TYPE IF EXISTS btc_invoice_status    CASCADE;
DROP TYPE IF EXISTS btc_sweep_schedule    CASCADE;
DROP TYPE IF EXISTS btc_wallet_mode       CASCADE;

-- +goose StatementEnd
