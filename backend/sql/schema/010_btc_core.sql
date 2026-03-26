-- +goose Up
-- +goose StatementBegin

/*
 * 010_btc_core.sql — Bitcoin payment system: configuration and vendor core tables.
 *
 * Tables defined here (in dependency order):
 *   btc_tier_config          — owner-managed fee/feature presets; links to RBAC roles
 *   platform_config          — per-network operational singletons (treasury, sweep-hold)
 *   reconciliation_job_state — last-run cursor for the reconciliation health monitor
 *   bitcoin_sync_state       — last processed block-height cursor per network
 *   vendor_wallet_config     — per-vendor wallet mode, destination address, tier, KYC state
 *   vendor_balances          — running internal satoshi balance per vendor per network
 *   vendor_tier_overrides    — per-vendor rule overrides that shadow tier defaults
 *   btc_exchange_rate_log    — time-series BTC/fiat rate feed for audit and anomaly detection
 *
 * Depends on: 009_btc_types.sql, 001_core.sql (users), 002_core_functions.sql, 003_rbac.sql
 * Continued in: 011_btc_core_functions.sql
 */

/* ═════════════════════════════════════════════════════════════
   TIER CONFIGURATION
   ═════════════════════════════════════════════════════════════ */

/*
 * Owner-managed tier presets. A tier bundles all the financial rules, fee caps,
 * sweep schedules, and feature flags that govern invoices and payouts for every
 * vendor assigned to it.
 *
 * RBAC bridge: each tier optionally references an RBAC role via role_id. When the
 * application assigns a vendor to a tier (via vendor_wallet_config.tier_id), it also
 * grants the vendor that role. The fn_sync_vendor_tier_role trigger (011_btc_functions.sql)
 * keeps user_roles in sync when tier_id changes directly on vendor_wallet_config.
 *
 * Per-vendor exceptions: if a specific vendor needs different rules from their tier
 * (e.g. a discounted fee rate), use vendor_tier_overrides. Invoice creation resolves
 * the effective rules as COALESCE(override.field, tier.field).
 *
 * Immutable snapshot: at invoice creation every financial column is copied verbatim
 * onto the invoice row. Subsequent tier changes do not affect in-flight invoices.
 * This is the "snapshot at creation" invariant that makes the entire settlement
 * pipeline deterministic and auditable.
 *
 * Deletion: tiers are NEVER hard-deleted because invoices hold a RESTRICT FK to tier_id.
 * Soft-deactivate via status = 'deactivating' (triggers pre-sweep), then 'inactive'.
 *
 * History: every UPDATE to this table is captured in btc_tier_config_history
 * (010_btc_payouts.sql) by fn_tier_config_history (011_btc_functions.sql).
 */
CREATE TABLE btc_tier_config (
    id           UUID            PRIMARY KEY DEFAULT uuidv7(),

    -- Stable machine slug used as the external identifier. Examples: 'free', 'growth', 'pro'.
    -- Immutable once invoices exist on this tier (renaming would break external references).
    name         TEXT            NOT NULL,

    -- Human-readable label shown in the admin UI. May be changed freely.
    display_name TEXT            NOT NULL,

    -- Optional RBAC role linked to this tier. When a vendor is moved to this tier the
    -- application also grants them this role via user_roles. SET NULL if the role is deleted
    -- so the tier remains usable even after the associated role is removed.
    role_id      UUID            REFERENCES roles(id) ON DELETE SET NULL,

    -- ── Financial rules ───────────────────────────────────────────────────────
    -- All values below are range-checked at both the admin API and DB layers.
    -- The DB CHECK constraints are the final backstop; violating them raises SQLSTATE 23514.

    -- Platform processing fee deducted from vendor earnings at settlement.
    -- Stored as a percentage: 2.50 = 2.5%. Applied to received_sat, not invoiced_sat.
    -- Range [0, 50]. Values above 50% require explicit owner acknowledgment in the API.
    processing_fee_rate               NUMERIC(5,2)       NOT NULL,

    -- Block confirmations required before settlement is triggered.
    -- 1 = immediate (Enterprise, elevated reorg risk). 6 = ~60 min (Free).
    confirmation_depth                INTEGER            NOT NULL,

    -- Maximum miner fee rate the platform will pay for sweep TXs on this tier.
    -- If the current mempool rate exceeds this cap the sweep is delayed until the
    -- fee drops or an admin overrides. Units: satoshis per virtual byte. Range [1, 10000].
    miner_fee_cap_sat_vbyte           INTEGER            NOT NULL,

    -- How often queued payout records are batched and broadcast.
    sweep_schedule                    btc_sweep_schedule NOT NULL,

    -- Payouts at or above this threshold require manual admin approval before broadcast.
    -- 0 means every payout requires approval regardless of amount. Range [0, 1_000_000_000].
    withdrawal_approval_threshold_sat BIGINT             NOT NULL,

    -- How long a pending invoice remains open before expiring.
    -- The expiry cleanup job adds outage overlap from btc_outage_log before marking expired.
    -- Range [5, 1440] minutes.
    invoice_expiry_minutes            INTEGER            NOT NULL,

    -- Tolerance band for over/underpayment. Payments within ±tolerance of the invoiced
    -- amount are settled as exact (no adjustment). Range [0, 10] percent.
    payment_tolerance_pct             NUMERIC(4,2)       NOT NULL,

    -- Minimum invoice amount in satoshis. Below this the API rejects invoice creation.
    -- Free tier is typically higher (e.g. 50 000 sat) to ensure the miner fee floor
    -- never exceeds the net payout. Must be >= 1000.
    minimum_invoice_sat               BIGINT             NOT NULL,

    -- Relative overpayment threshold (percent above invoiced). The overpaid path is only
    -- triggered when BOTH the relative AND absolute thresholds are exceeded simultaneously.
    -- Range [1, 100] percent.
    overpayment_relative_threshold_pct NUMERIC(5,2)     NOT NULL,

    -- Absolute overpayment threshold in satoshis. Combined with the relative threshold.
    -- Must be >= 1000. Setting both thresholds high avoids false-positive overpaid status
    -- from dust rounding.
    overpayment_absolute_threshold_sat BIGINT           NOT NULL,

    -- Expected number of vendors in a single sweep batch. Used in the batch-amortised
    -- fee floor formula: floor = (fee_estimate × 1.1 × vbytes) / expected_batch_size.
    -- A higher value lowers the per-payout fee floor but increases the risk of under-funding
    -- if the actual batch is smaller. Range [1, 100].
    expected_batch_size               INTEGER            NOT NULL,

    -- Webhook delivery level. Controls which event types are written to webhook_deliveries
    -- for vendors on this tier. Applied at InsertWebhookDelivery call time — filtered
    -- events are never written.
    --   none:     no webhook deliveries written (default — Free tier)
    --   basic:    only invoice.settled and payout.confirmed
    --   standard: all events except reorg and compliance events
    --   full:     all documented event types
    webhook_level                     TEXT               NOT NULL DEFAULT 'none'
        CHECK (webhook_level IN ('none', 'basic', 'standard', 'full')),

    -- ── KYC configuration ────────────────────────────────────────────
    -- These values are snapshotted onto every invoice at creation time so the KYC
    -- thresholds that applied when an invoice was created can always be reconstructed.

    -- Fiat-equivalent satoshi threshold above which KYC is required before payout
    -- can be promoted from held → queued. NULL = KYC never required on this tier.
    -- Non-NULL: payout net_satoshis × btc_rate >= threshold triggers KYC check.
    -- Range: >= 1000 sat when non-NULL. (vendor-feature.md §6)
    kyc_check_required_at_threshold_satoshis BIGINT
        CHECK (kyc_check_required_at_threshold_satoshis IS NULL
            OR kyc_check_required_at_threshold_satoshis >= 1000),

    -- How long an approved KYC submission remains valid before re-verification is
    -- required. Default 365 days. Range [30, 1825]. (kyc-feature.md §KYC Expiry)
    kyc_approval_validity_days        INTEGER            NOT NULL DEFAULT 365
        CHECK (kyc_approval_validity_days >= 30 AND kyc_approval_validity_days <= 1825),

    -- Minimum hours between re-submissions after rejection. Prevents rapid abuse.
    -- Default 24 hours. Range [1, 168]. (kyc-feature.md §Re-submission)
    kyc_resubmission_cooldown_hours   INTEGER            NOT NULL DEFAULT 24
        CHECK (kyc_resubmission_cooldown_hours >= 1 AND kyc_resubmission_cooldown_hours <= 168),

    -- ── Lifecycle ─────────────────────────────────────────────────────────────

    -- btc_tier_status ENUM: active | deactivating | inactive.
    -- Deactivating: triggers a pre-sweep of all queued payouts before full wind-down.
    --   New invoice creation is blocked during deactivating and inactive states.
    -- Inactive: fully wound down. No new invoices. Existing invoices still settle normally.
    status       btc_tier_status    NOT NULL DEFAULT 'active',

    created_at   TIMESTAMPTZ        NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ        NOT NULL DEFAULT NOW(),  -- maintained by fn_set_updated_at

    CONSTRAINT uq_btc_tier_config_name UNIQUE (name),

    -- Slugs and labels must have visible content. An empty slug would produce a valid
    -- but invisible tier that is indistinguishable from a missing value at the API layer.
    CONSTRAINT chk_btc_tier_name_not_empty
        CHECK (length(trim(name)) > 0),
    CONSTRAINT chk_btc_tier_display_name_not_empty
        CHECK (length(trim(display_name)) > 0),

    -- Financial rule range constraints — mirrored in the admin API layer.
    -- DB enforcement ensures direct DB operations or future code paths cannot violate them.
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
        CHECK (expected_batch_size >= 1 AND expected_batch_size <= 100)
);

CREATE TRIGGER trg_btc_tier_config_updated_at
    BEFORE UPDATE ON btc_tier_config
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Role linkage: "which tier is linked to RBAC role X?" — used by the role-assignment hook
-- when setting up a new vendor. Partial because most tiers will have a role_id set, but
-- the NULL case is excluded to keep the index lean.
CREATE INDEX idx_btc_tier_role ON btc_tier_config(role_id) WHERE role_id IS NOT NULL;


COMMENT ON TABLE btc_tier_config IS
    'Owner-managed tier presets. All financial rules are defined here and snapshotted '
    'immutably onto every invoice at creation (after COALESCE with vendor_tier_overrides). '
    'Soft-deactivated (status=inactive), never hard-deleted — invoices hold a RESTRICT FK. '
    'role_id bridges tiers to RBAC: fn_sync_vendor_tier_role (011) keeps user_roles in sync. '
    'Changes are captured in btc_tier_config_history (010) by fn_tier_config_history (011).';
COMMENT ON COLUMN btc_tier_config.name IS
    'Stable machine slug (e.g. ''free'', ''growth'', ''pro''). '
    'Treat as immutable once invoices reference this tier.';
COMMENT ON COLUMN btc_tier_config.role_id IS
    'RBAC role granted to vendors on this tier. SET NULL on role deletion. '
    'fn_sync_vendor_tier_role (011) propagates tier changes to user_roles automatically.';
COMMENT ON COLUMN btc_tier_config.processing_fee_rate IS
    'Platform fee as a percentage [0, 50] applied to received_sat at settlement.';
COMMENT ON COLUMN btc_tier_config.expected_batch_size IS
    'Batch-amortised fee floor: floor = (fee_estimate × 1.1 × vbytes) / expected_batch_size. '
    'Default 50 for Free, 20 for Growth/Pro.';
COMMENT ON COLUMN btc_tier_config.status IS
    'btc_tier_status ENUM. active → deactivating (pre-sweep) → inactive. '
    'New invoice creation blocked in deactivating and inactive states.';


/* ═════════════════════════════════════════════════════════════
   PLATFORM CONFIGURATION
   ═════════════════════════════════════════════════════════════ */

/*
 * Per-network operational singletons. Exactly one row per network, inserted at
 * deployment. These rows are never deleted; only updated.
 *
 * treasury_reserve_satoshis is a critical reconciliation term:
 *   on_chain_UTXO_value = SUM(vendor_balances WHERE mode=platform)
 *                       + SUM(payout_records WHERE status IN (held, queued, constructing, broadcast))
 *                       + SUM(in-flight invoice payments)
 *                       + treasury_reserve_satoshis
 * If this value is wrong, every reconciliation run will report a false discrepancy.
 * It must be incremented in the SAME DB transaction as payout_records → confirmed.
 *
 * sweep_hold_mode is the emergency brake. When the reconciliation job detects a
 * discrepancy it sets sweep_hold_mode = TRUE and sweep_hold_reason. All sweep
 * construction and broadcast is blocked until an admin investigates and clears it.
 * The clearing action itself must write to ops_audit_log (fn_ops_audit_platform_config
 * in 011_btc_functions.sql handles this automatically).
 *
 * platform_wallet_mode_legal_approved must be TRUE before any tier may enable
 * platform_wallet_mode_allowed. Set via a deliberate ops action with a written
 * legal approval record, never via the admin UI.
 */
CREATE TABLE platform_config (
    -- Natural PK: 'mainnet' or 'testnet4'. Only these two values are permitted.
    network      TEXT        PRIMARY KEY,

    -- Accumulated miner fee earnings retained by the platform from completed sweeps.
    -- Incremented atomically (in the same TX as payout_records → confirmed) by the amount:
    --   gross_payout_sat - SUM(vendor net_satoshis for that batch).
    -- Decremented on treasury withdrawal or UTXO consolidation (admin operation).
    -- Never goes below 0; any negative delta is a reconciliation defect.
    treasury_reserve_satoshis   BIGINT      NOT NULL DEFAULT 0,

    -- Emergency sweep brake. TRUE = all sweep construction and broadcast is blocked.
    -- Set by the reconciliation job on discrepancy; cleared by admin after investigation.
    -- chk_pconfig_hold_coherent requires reason and timestamp whenever this is TRUE.
    sweep_hold_mode             BOOLEAN     NOT NULL DEFAULT FALSE,

    -- Human-readable explanation of why sweep_hold_mode was activated.
    -- Required when sweep_hold_mode = TRUE; must be NULL when sweep_hold_mode = FALSE.
    sweep_hold_reason           TEXT,

    -- Timestamp when sweep_hold_mode was last set to TRUE.
    -- Required when sweep_hold_mode = TRUE; must be NULL when sweep_hold_mode = FALSE.
    sweep_hold_activated_at     TIMESTAMPTZ,

    -- Custodial platform wallet mode requires legal review before enabling.
    -- Must be TRUE before any tier may set platform_wallet_mode_allowed = TRUE.
    -- Default FALSE; set via deliberate ops action with written legal approval — never admin UI.
    platform_wallet_mode_legal_approved BOOLEAN NOT NULL DEFAULT FALSE,

    -- Block height from which reconciliation backfill scans start.
    -- Must be set before the first mainnet deployment.
    -- Application rejects height = 0 on mainnet unless BTC_RECONCILIATION_ALLOW_GENESIS_SCAN = true.
    reconciliation_start_height BIGINT      NOT NULL DEFAULT 0,

    -- ── Beta feature flags ────────────────────────────────────────────────────
    -- All flags default FALSE (off). Toggled via owner-only API.
    -- Every change is automatically captured in ops_audit_log by
    -- fn_ops_audit_platform_config (011) — no extra audit code needed.

    -- KYC gate: when FALSE, every payout record is created with kyc_status='not_required'
    -- and the KYC submission flow never triggers, regardless of tier thresholds.
    -- Flip to TRUE when the KYC provider integration is ready for production.
    kyc_enabled                 BOOLEAN NOT NULL DEFAULT FALSE,

    -- FATF Travel Rule: when FALSE, the platform does not require a
    -- fatf_travel_rule_records row before broadcasting a sweep.
    -- The fn_fatf_address_consistency trigger enforcement is also bypassed.
    -- Flip to TRUE only after VASP registration and TRISA/OpenVASP integration.
    fatf_enabled                BOOLEAN NOT NULL DEFAULT FALSE,

    -- Webhooks: when FALSE, no webhook_deliveries rows are written on any state
    -- change. The delivery worker runs but finds nothing to do. Vendors will not
    -- receive any HTTP notifications until this is TRUE.
    webhooks_enabled            BOOLEAN NOT NULL DEFAULT FALSE,

    -- Disputes: when FALSE, the dispute creation endpoint returns a
    -- 'feature not yet available' response. No dispute_records rows are written,
    -- no payout records can be frozen by a dispute, and no SLA timers fire.
    disputes_enabled            BOOLEAN NOT NULL DEFAULT FALSE,

    -- GDPR erasure job: when FALSE, the platform still accepts erasure requests
    -- (gdpr_erasure_requests rows are written — you must accept them legally),
    -- but the background job that actually nullifies PII does not run.
    -- Flip to TRUE only after the erasure job has been validated end-to-end.
    gdpr_erasure_job_enabled    BOOLEAN NOT NULL DEFAULT FALSE,

    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_pconfig_network
        CHECK (network IN ('mainnet', 'testnet4')),
    CONSTRAINT chk_pconfig_treasury_non_negative
        CHECK (treasury_reserve_satoshis >= 0),
    -- Coherence: reason and timestamp must both be present when the hold is active.
    -- A hold without a reason is invisible to ops. A reason without a timestamp
    -- prevents calculating how long the hold has been active.
    -- ── UTXO consolidation configuration ────────────────────────────
    -- When TRUE and fee conditions are met, the UTXO consolidation job may run.
    -- Default FALSE (disabled). Set via owner-only API with written justification.
    -- (sweep-feature.md §11, sweep-technical.md §8)
    consolidation_enabled             BOOLEAN            NOT NULL DEFAULT FALSE,

    -- Maximum fee rate at which consolidation will run. NULL = feature not yet
    -- configured (job will not run regardless of consolidation_enabled).
    -- Range [1, 1000] sat/vbyte when non-NULL.
    consolidation_max_fee_sat_vbyte   INTEGER
        CHECK (consolidation_max_fee_sat_vbyte IS NULL
            OR (consolidation_max_fee_sat_vbyte >= 1
                AND consolidation_max_fee_sat_vbyte <= 1000)),

    -- Optional daily low-fee window (UTC). Both must be set together or both NULL.
    -- Consolidation job checks: current UTC time is between window_start and window_end.
    consolidation_window_start        TIME,
    consolidation_window_end          TIME,

    CONSTRAINT chk_pconfig_hold_coherent
        CHECK (sweep_hold_mode = FALSE
            OR (sweep_hold_reason IS NOT NULL AND sweep_hold_activated_at IS NOT NULL)),

    CONSTRAINT chk_pconfig_consolidation_window_coherent
        CHECK ((consolidation_window_start IS NULL) = (consolidation_window_end IS NULL)),

    -- Enforce that the window start is strictly before end.
    -- An inverted window (start >= end) is always FALSE in a BETWEEN check, causing the
    -- consolidation job to silently never run. An overnight window (e.g. 23:00–04:00)
    -- cannot be represented with a simple TIME BETWEEN and must be handled in application
    -- logic using modular arithmetic. If you need an overnight window, set start and end
    -- in application logic (not this constraint), and document the wrap-around handling.
    CONSTRAINT chk_pconfig_consolidation_window_order
        CHECK (consolidation_window_start IS NULL OR
               consolidation_window_end IS NULL OR
               consolidation_window_start < consolidation_window_end)
);

CREATE TRIGGER trg_platform_config_updated_at
    BEFORE UPDATE ON platform_config
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Every UPDATE is captured in ops_audit_log by fn_ops_audit_platform_config (011).

COMMENT ON TABLE platform_config IS
    'Per-network operational singletons. One row per network inserted at deployment. '
    'treasury_reserve_satoshis is a required reconciliation term — keep it accurate. '
    'sweep_hold_mode is the emergency brake; only admin can clear it. '
    'All UPDATEs are captured in ops_audit_log by fn_ops_audit_platform_config (011).';
COMMENT ON COLUMN platform_config.treasury_reserve_satoshis IS
    'Accumulated platform miner fees. Must be incremented in the same TX as '
    'payout_records → confirmed. Required for the reconciliation formula to balance.';
COMMENT ON COLUMN platform_config.sweep_hold_mode IS
    'TRUE = all sweeps blocked. Set by reconciliation job on discrepancy; '
    'cleared by admin after investigation. Requires reason + timestamp (chk_pconfig_hold_coherent).';
COMMENT ON COLUMN platform_config.platform_wallet_mode_legal_approved IS
    'Must be TRUE before any tier can enable platform_wallet_mode_allowed. '
    'Set via ops action with written legal approval — never via admin UI.';
COMMENT ON COLUMN platform_config.kyc_enabled IS
    'Owner kill-switch for the KYC payout gate. FALSE = KYC logic fully bypassed '
    'regardless of tier thresholds. Toggle via feature_flags:write permission.';
COMMENT ON COLUMN platform_config.fatf_enabled IS
    'Owner kill-switch for FATF Travel Rule enforcement. FALSE = no FATF records '
    'required before sweep broadcast. Requires VASP registration before enabling.';
COMMENT ON COLUMN platform_config.webhooks_enabled IS
    'Owner kill-switch for vendor webhook notifications. FALSE = no webhook_deliveries '
    'rows written. Delivery worker is idle. Safe to flip TRUE at any time.';
COMMENT ON COLUMN platform_config.disputes_enabled IS
    'Owner kill-switch for the dispute system. FALSE = dispute creation endpoint '
    'returns 503. No payout freezing, no SLA timers. Requires admin playbook before enabling.';
COMMENT ON COLUMN platform_config.gdpr_erasure_job_enabled IS
    'Owner kill-switch for the GDPR erasure background job. FALSE = requests are '
    'accepted and recorded but PII is not yet erased. Enable after job validation.';


/* ═════════════════════════════════════════════════════════════
   RECONCILIATION JOB STATE
   ═════════════════════════════════════════════════════════════ */

/*
 * Tracks the latest reconciliation run result per network. This is the table
 * that the "Reconciliation job missed" alert monitors — a CRITICAL alert fires if
 * last_successful_run_at has not advanced within 8 hours.
 *
 * Key distinction: last_successful_run_at is ONLY updated when the job completes
 * successfully without discrepancy. A job that runs but encounters an error or
 * discrepancy updates last_run_at and last_run_result but NOT last_successful_run_at.
 * This ensures the alert fires for both missed runs and silently-failing runs.
 *
 * Full run history is in reconciliation_run_history (010_btc_payouts.sql).
 */
CREATE TABLE reconciliation_job_state (
    network                 TEXT                      PRIMARY KEY,

    -- Timestamp of the last run that completed with result = 'ok'.
    -- This is the value the health monitor checks. NULL = never run successfully.
    last_successful_run_at  TIMESTAMPTZ,

    -- Timestamp of the most recent run attempt, regardless of outcome.
    -- Useful for diagnosing whether the job is running at all vs running but failing.
    last_run_at             TIMESTAMPTZ,

    -- Outcome of the most recent run. btc_reconciliation_result ENUM.
    -- NULL = never run. Set on every run completion.
    last_run_result         btc_reconciliation_result,

    -- Satoshi discrepancy recorded when last_run_result = 'discrepancy'.
    -- Sign convention: positive = on-chain total exceeds internal total (unexpected funds);
    -- negative = on-chain total is less than internal total (missing funds, critical).
    last_discrepancy_sat    BIGINT,

    updated_at              TIMESTAMPTZ               NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_rjstate_network
        CHECK (network IN ('mainnet', 'testnet4')),
    -- When a discrepancy is detected, the satoshi amount must be recorded.
    -- A nil-amount discrepancy would pass reconciliation checks silently.
    CONSTRAINT chk_rjstate_discrepancy_coherent
        CHECK (last_run_result != 'discrepancy' OR last_discrepancy_sat IS NOT NULL)
);

CREATE TRIGGER trg_reconciliation_job_state_updated_at
    BEFORE UPDATE ON reconciliation_job_state
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

COMMENT ON TABLE reconciliation_job_state IS
    'Latest reconciliation run result per network. '
    'last_successful_run_at drives the "Reconciliation job missed" CRITICAL alert (> 8h stale). '
    'Only updated on result = ok. Failing runs update last_run_at but not last_successful_run_at. '
    'Full run history is in reconciliation_run_history (010_btc_payouts.sql).';
COMMENT ON COLUMN reconciliation_job_state.last_successful_run_at IS
    'Health monitor basis. NULL = never run successfully. '
    'CRITICAL alert fires when this timestamp is > 8 hours stale.';
COMMENT ON COLUMN reconciliation_job_state.last_discrepancy_sat IS
    'Positive = unexpected on-chain funds. Negative = missing funds (critical). '
    'Required when last_run_result = ''discrepancy'' (chk_rjstate_discrepancy_coherent).';


/* ═════════════════════════════════════════════════════════════
   BITCOIN SYNC STATE
   ═════════════════════════════════════════════════════════════ */

/*
 * Stores the last processed block height per network. This is the cursor used by
 * HandleRecovery to backfill missed blocks after a node reconnect or process restart.
 *
 * -1 is the sentinel for "never processed" (fresh deployment). The application
 * initialises it to platform_config.reconciliation_start_height on first run.
 *
 * The cursor is updated inside each reconcileSegment transaction
 * (every BTC_RECONCILIATION_CHECKPOINT_INTERVAL blocks) so that a crash mid-backfill
 * resumes from the last checkpoint rather than reprocessing from the beginning.
 *
 * Chain-reset detection: if last_processed_height > getblockcount() (e.g. the node
 * was reindexed or switched to a fork), the application resets to
 * reconciliation_start_height and fires a CRITICAL alert.
 */
CREATE TABLE bitcoin_sync_state (
    network                 TEXT    PRIMARY KEY,

    -- Last block height for which all transactions have been processed.
    -- -1 = sentinel for fresh deployment (never processed any block).
    -- Initialised to platform_config.reconciliation_start_height on first run.
    last_processed_height   BIGINT  NOT NULL DEFAULT -1,

    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_bss_network
        CHECK (network IN ('mainnet', 'testnet4')),
    -- -1 is the only valid negative value (fresh deployment sentinel).
    CONSTRAINT chk_bss_height_min
        CHECK (last_processed_height >= -1)
);

CREATE TRIGGER trg_bitcoin_sync_state_updated_at
    BEFORE UPDATE ON bitcoin_sync_state
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

COMMENT ON TABLE bitcoin_sync_state IS
    'Block-height cursor per network. -1 = never processed (fresh deployment sentinel). '
    'Used by HandleRecovery to resume backfill after reconnect. '
    'Updated per checkpoint interval inside reconcileSegment for crash safety.';
COMMENT ON COLUMN bitcoin_sync_state.last_processed_height IS
    '-1 = fresh deployment. Initialised to reconciliation_start_height on first run. '
    'If this exceeds getblockcount() (chain reset / reindex), app resets and fires CRITICAL alert.';


/* ═════════════════════════════════════════════════════════════
   VENDOR WALLET CONFIGURATION
   ═════════════════════════════════════════════════════════════ */

/*
 * Per-vendor payment configuration. One row per (vendor_id, network), created when
 * the vendor completes the wallet-setup flow after their role is granted.
 *
 * wallet_mode and bridge_destination_address are snapshotted onto every invoice at
 * creation time. Changes to the live config do not affect in-flight invoices — the
 * snapshot values are always authoritative for settlement.
 *
 * Mode-change rules:
 *   - Vendors may only select modes permitted by their tier.
 *   - fn_btc_wallet_mode_guard (011) blocks mode changes while balance_satoshis > 0.
 *     The vendor must drain the balance via a withdrawal payout first.
 *   - If a tier downgrade removes permission for the current mode, mode_frozen = TRUE.
 *     New invoices are blocked until the vendor explicitly selects a permitted mode.
 *     mode_frozen NEVER auto-clears.
 *
 * Suspension: new invoice creation is blocked; in-flight invoices complete normally;
 * payout accumulation continues but sweeps are held at the broadcast boundary.
 *
 * Address validation: bridge_destination_address must pass a two-step ismine check
 * (DB lookup + RPC getaddressinfo) before being stored (vendor-technical.md §2).
 *
 * Deletion: blocked (ON DELETE RESTRICT on users FK) while any pending invoices,
 * queued payouts, non-zero balance, or unresolved admin states exist.
 *
 * History: key field changes are captured in vendor_wallet_config_history (010)
 * by fn_vwc_history (011). Tier changes also sync user_roles via fn_sync_vendor_tier_role.
 */
CREATE TABLE vendor_wallet_config (
    -- vendor_id + network form the composite PK. One row per vendor per network.
    vendor_id   UUID            NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    network     TEXT            NOT NULL,

    -- Current tier assignment. RESTRICT prevents tier deletion while vendors use it.
    -- Changing tier_id fires fn_sync_vendor_tier_role (011) to update user_roles.
    tier_id     UUID            NOT NULL REFERENCES btc_tier_config(id) ON DELETE RESTRICT,

    -- Active wallet mode. Snapshotted onto every invoice at creation.
    -- fn_btc_wallet_mode_guard (011) blocks changes while balance_satoshis > 0.
    wallet_mode btc_wallet_mode NOT NULL,

    -- External destination address for bridge and hybrid modes. NULL for platform mode.
    -- Validated via two-step ismine check (DB + RPC) before storage.
    -- Snapshotted onto invoices — changes here do not affect in-flight invoices.
    -- Changes captured in vendor_wallet_config_history by fn_vwc_history (011).
    bridge_destination_address  TEXT,

    -- Auto-sweep threshold in satoshis for hybrid mode only. NULL for bridge/platform.
    -- Snapshotted onto invoices. When the vendor's accumulated balance crosses this value
    -- all held payout records are promoted to queued and a sweep fires.
    -- Must be >= 10 000 sat to ensure the sweep fee does not exceed the payout net.
    auto_sweep_threshold_sat    BIGINT,

    -- KYC/AML verification state. Driven by the latest kyc_submissions row (010).
    kyc_status  btc_kyc_status  NOT NULL DEFAULT 'not_required',

    -- ── Suspension state ──────────────────────────────────────────────────────

    -- TRUE = vendor is suspended. New invoice creation blocked. In-flight invoices complete.
    -- Sweeps held at broadcast boundary until suspension is lifted.
    suspended           BOOLEAN     NOT NULL DEFAULT FALSE,

    -- Timestamp when suspended was set to TRUE. Required when suspended = TRUE.
    suspended_at        TIMESTAMPTZ,

    -- Human-readable reason for suspension. Required when suspended = TRUE.
    suspension_reason   TEXT,

    -- ── Mode freeze state ─────────────────────────────────────────────────────

    -- TRUE when a tier downgrade removed permission for the active wallet_mode.
    -- New invoice creation is blocked. Clears only on explicit vendor reconfiguration.
    -- NEVER auto-clears — requires deliberate vendor action.
    mode_frozen         BOOLEAN     NOT NULL DEFAULT FALSE,

    -- Human-readable reason why mode_frozen was set. Required when mode_frozen = TRUE.
    mode_frozen_reason  TEXT,

    -- Timestamp when mode_frozen was last set TRUE. Required when mode_frozen = TRUE.
    -- LOW-3: mirrors the suspended_at pattern on the suspension block so incident
    -- timeline reconstruction for a mode freeze is possible without relying on ops logs.
    mode_frozen_at      TIMESTAMPTZ,

    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (vendor_id, network),

    CONSTRAINT chk_vwc_network
        CHECK (network IN ('mainnet', 'testnet4')),

    -- bridge and hybrid require a destination address; platform must not have one.
    -- This prevents silent misconfiguration where a bridge vendor has no sweep target.
    CONSTRAINT chk_vwc_bridge_address_coherent
        CHECK (wallet_mode = 'platform' OR bridge_destination_address IS NOT NULL),
    CONSTRAINT chk_vwc_platform_no_bridge_address
        CHECK (wallet_mode != 'platform' OR bridge_destination_address IS NULL),

    -- hybrid requires a threshold; non-hybrid must not have one.
    -- Without this, the auto-sweep logic silently never fires for a hybrid vendor
    -- whose threshold was accidentally left NULL.
    CONSTRAINT chk_vwc_hybrid_threshold_coherent
        CHECK (wallet_mode = 'hybrid' OR auto_sweep_threshold_sat IS NULL),
    CONSTRAINT chk_vwc_hybrid_threshold_required
        CHECK (wallet_mode != 'hybrid' OR auto_sweep_threshold_sat IS NOT NULL),
    CONSTRAINT chk_vwc_hybrid_threshold_positive
        CHECK (auto_sweep_threshold_sat IS NULL OR auto_sweep_threshold_sat > 0),
    -- 10 000 sat minimum ensures sweep miner fee cannot exceed payout net.
    CONSTRAINT chk_vwc_hybrid_threshold_minimum
        CHECK (auto_sweep_threshold_sat IS NULL OR auto_sweep_threshold_sat >= 10000),

    -- mode_frozen coherence: reason and timestamp required for incident forensics.
    -- LOW-3: timestamp mirrors the suspended_at pattern so freeze duration can be computed.
    CONSTRAINT chk_vwc_mode_frozen_coherent
        CHECK (mode_frozen = FALSE
            OR (mode_frozen_reason IS NOT NULL AND mode_frozen_at IS NOT NULL)),

    -- Suspension coherence: timestamp and reason required for incident forensics.
    CONSTRAINT chk_vwc_suspension_coherent
        CHECK (suspended = FALSE
            OR (suspended_at IS NOT NULL AND suspension_reason IS NOT NULL))
);

CREATE TRIGGER trg_vendor_wallet_config_updated_at
    BEFORE UPDATE ON vendor_wallet_config
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- NOTE: idx_vwc_vendor_network has been intentionally dropped — it was an exact duplicate
-- of the PRIMARY KEY (vendor_id, network). PostgreSQL creates a unique index for every
-- PRIMARY KEY automatically. The duplicate added pure write overhead. (IDX-01)

-- Tier lookup: "which vendors are on tier X?" — used by pre-deactivation sweep scanning
-- and by the tier-assignment validation logic.
CREATE INDEX idx_vwc_tier ON vendor_wallet_config(tier_id);

-- Suspended vendor fast path: broadcast boundary check queries suspended=TRUE vendors.
-- Partial index keeps this lean — most vendors are not suspended.
CREATE INDEX idx_vwc_suspended
    ON vendor_wallet_config(vendor_id, network) WHERE suspended = TRUE;

-- KYC compliance filter: "which vendors need KYC review or are pending?"
-- Partial index excludes the common not_required case so it stays selective.
CREATE INDEX idx_vwc_kyc_status ON vendor_wallet_config(kyc_status)
    WHERE kyc_status != 'not_required';

COMMENT ON TABLE vendor_wallet_config IS
    'Per-vendor wallet configuration per network. One row per (vendor_id, network). '
    'wallet_mode and bridge_destination_address are snapshotted onto every invoice at creation. '
    'fn_btc_wallet_mode_guard (011) blocks mode changes while balance > 0. '
    'fn_vwc_history (011) captures key field changes in vendor_wallet_config_history (010). '
    'fn_sync_vendor_tier_role (011) syncs user_roles on tier_id change. '
    'Deletion blocked (RESTRICT) while any outstanding financial obligations exist.';
COMMENT ON COLUMN vendor_wallet_config.bridge_destination_address IS
    'External sweep destination. Validated via two-step ismine check before storage. '
    'Snapshotted onto invoices — changes do not affect in-flight settlements. '
    'Changes captured in vendor_wallet_config_history (010).';
COMMENT ON COLUMN vendor_wallet_config.auto_sweep_threshold_sat IS
    'Hybrid-only auto-sweep trigger. Must be >= 10 000 sat. Snapshotted onto invoices. '
    'NULL for bridge and platform modes (enforced by constraints).';
COMMENT ON COLUMN vendor_wallet_config.mode_frozen IS
    'TRUE when tier downgrade removed permission for the current wallet_mode. '
    'New invoice creation is blocked. Clears only on explicit vendor reconfiguration — never auto-clears. '
    'mode_frozen_at and mode_frozen_reason are required when TRUE (chk_vwc_mode_frozen_coherent).';


/* ═════════════════════════════════════════════════════════════
   VENDOR BALANCES
   ═════════════════════════════════════════════════════════════ */

/*
 * Internal BTC balance per vendor per network. One row per (vendor_id, network),
 * created alongside the vendor_wallet_config row.
 *
 * CRITICAL — mutation path:
 *   ALL balance mutations MUST go through the btc_credit_balance / btc_debit_balance
 *   stored procedures defined in 011_btc_functions.sql. Those procedures acquire the
 *   necessary row-level lock internally. Direct UPDATE on this table is REVOKED from
 *   btc_app_role in the grants section of 011_btc_functions.sql.
 *
 *   Why: if a caller forgets SELECT FOR UPDATE before a read-modify-write, two concurrent
 *   settlement workers can both read balance = X, both compute X + amount, and both write
 *   the same final value — crediting the vendor only once when they should be credited twice.
 *   Routing all mutations through the stored procedure makes this race structurally impossible.
 *
 * RECONCILIATION NOTE — balance_satoshis has DIFFERENT semantics per wallet_mode:
 *   platform: value-bearing balance. Included in the reconciliation formula sum.
 *   hybrid:   threshold accumulator only. NOT value-bearing — NOT included in the formula.
 *             The vendor's actual value is fully captured in payout_records (held/queued).
 *             Including both would double-count hybrid funds.
 *
 * Hybrid balance lifecycle:
 *   - Incremented at settlement Phase 2 by the net_satoshis for each settled invoice.
 *   - Decremented (reset to ~0) when the threshold is crossed and held payout records
 *     are promoted to queued. The decrement equals SUM(all promoted held records).
 *   - NOT decremented at broadcast or confirmation — those state transitions are tracked
 *     entirely through the payout_records state machine.
 *
 * The CHECK (balance_satoshis >= 0) catches underflow. A violation raises SQLSTATE 23514
 * which the application maps to ErrInsufficientBalance. Never retry on this error.
 */
CREATE TABLE vendor_balances (
    vendor_id        UUID    NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    network          TEXT    NOT NULL,

    -- For platform mode: value-bearing satoshi balance. For hybrid mode: accumulator only.
    -- See reconciliation note in the block comment above.
    -- MUST be mutated only via btc_credit_balance / btc_debit_balance (011).
    balance_satoshis BIGINT  NOT NULL DEFAULT 0,

    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (vendor_id, network),

    CONSTRAINT chk_vbal_network
        CHECK (network IN ('mainnet', 'testnet4')),
    -- Underflow guard. Violation = ErrInsufficientBalance (SQLSTATE 23514). Never retry.
    CONSTRAINT chk_vbal_non_negative
        CHECK (balance_satoshis >= 0)
);

CREATE TRIGGER trg_vendor_balances_updated_at
    BEFORE UPDATE ON vendor_balances
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

COMMENT ON TABLE vendor_balances IS
    'Internal BTC balance per vendor per network. '
    'CRITICAL: all mutations via btc_credit_balance / btc_debit_balance (011) only. '
    'Direct UPDATE is revoked from btc_app_role to enforce the stored procedure path. '
    'platform mode: value-bearing, included in reconciliation. '
    'hybrid mode: threshold accumulator only, excluded from reconciliation (value is in payout_records). '
    'CHECK (>= 0) = ErrInsufficientBalance (SQLSTATE 23514). Never retry on this error.';
COMMENT ON COLUMN vendor_balances.balance_satoshis IS
    'Platform mode: value-bearing balance. Hybrid mode: accumulator — NOT in reconciliation formula. '
    'Mutate only via btc_credit_balance / btc_debit_balance stored procedures (011).';


/* ═════════════════════════════════════════════════════════════
   VENDOR TIER OVERRIDES
   ═════════════════════════════════════════════════════════════ */

/*
 * Per-vendor financial rule overrides. NULL in any column means "use the tier default."
 *
 * Invoice creation resolves the effective rule set as:
 *   effective_value = COALESCE(vendor_tier_overrides.field, btc_tier_config.field)
 * Every resolved value is then snapshotted onto the invoice row. Subsequent changes
 * to either the tier config or the override do not affect in-flight invoices.
 *
 * Use cases:
 *   - Discounted processing_fee_rate for a high-volume vendor (e.g. 0.5% instead of 2%).
 *   - Extended invoice_expiry_minutes for a vendor with slow-paying customers.
 *   - Lower withdrawal_approval_threshold_sat for a vendor that requested it.
 *   - Tighter confirmation_depth for an enterprise vendor who accepted extra reorg risk.
 *
 * This avoids creating one-off tier rows cluttering btc_tier_config for edge-case vendors.
 *
 * Accountability: every override requires granted_by (a human admin UUID) and
 * granted_reason. RESTRICT on granted_by prevents the admin from being deleted while
 * the override is active — they remain accountable.
 *
 * Expiry: NULL = permanent. Expired overrides are ignored at invoice creation (treated
 * as if no row exists). The application checks:
 *   WHERE vendor_id = $v AND network = $n AND (expires_at IS NULL OR expires_at > NOW())
 * A background cleanup job may delete expired rows but correctness does not depend on it.
 *
 * Constraint: at least one field must be non-NULL — an all-NULL row is meaningless
 * and indicates a bug in the override creation code path.
 */
CREATE TABLE vendor_tier_overrides (
    vendor_id   UUID        NOT NULL,
    network     TEXT        NOT NULL,

    -- NULL = use tier default for this field. Only set the fields you want to override.
    processing_fee_rate               NUMERIC(5,2),      -- [0, 50] percent
    sweep_schedule                    btc_sweep_schedule,
    confirmation_depth                INTEGER,           -- [1, 144] blocks
    invoice_expiry_minutes            INTEGER,           -- [5, 1440] minutes
    payment_tolerance_pct             NUMERIC(4,2),      -- [0, 10] percent
    withdrawal_approval_threshold_sat BIGINT,            -- [0, 1_000_000_000] sat
    minimum_invoice_sat               BIGINT,            -- >= 1000 sat
    miner_fee_cap_sat_vbyte           INTEGER,           -- [1, 10000] sat/vbyte

    -- ── Accountability ─────────────────────────────────────────────────────────

    -- UUID of the admin who created this override.
    -- RESTRICT: this admin cannot be deleted while the override exists.
    granted_by     UUID        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,

    -- Business justification for the override. Required; max 500 chars.
    granted_reason TEXT        NOT NULL,

    -- Optional expiry. NULL = permanent.
    -- Expired overrides are ignored at invoice creation. Application filters with:
    --   AND (expires_at IS NULL OR expires_at > NOW())
    expires_at  TIMESTAMPTZ,

    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- One override row per (vendor, network). To change an override, UPDATE this row.
    -- To remove it, DELETE the row (or set expires_at to a past timestamp).
    PRIMARY KEY (vendor_id, network),

    -- Composite FK: vendor must have a wallet_config row before an override can exist.
    -- Prevents overrides for vendors that were never set up (data integrity guard).
    CONSTRAINT fk_vto_vendor_wallet
        FOREIGN KEY (vendor_id, network)
        REFERENCES vendor_wallet_config(vendor_id, network)
        ON DELETE RESTRICT,

    CONSTRAINT chk_vto_network
        CHECK (network IN ('mainnet', 'testnet4')),

    -- Range constraints mirror btc_tier_config. Override values must be in bounds.
    CONSTRAINT chk_vto_fee_rate
        CHECK (processing_fee_rate IS NULL
            OR (processing_fee_rate >= 0 AND processing_fee_rate <= 50)),
    CONSTRAINT chk_vto_confirm_depth
        CHECK (confirmation_depth IS NULL
            OR (confirmation_depth >= 1 AND confirmation_depth <= 144)),
    CONSTRAINT chk_vto_fee_cap
        CHECK (miner_fee_cap_sat_vbyte IS NULL
            OR (miner_fee_cap_sat_vbyte >= 1 AND miner_fee_cap_sat_vbyte <= 10000)),
    CONSTRAINT chk_vto_expiry_window
        CHECK (invoice_expiry_minutes IS NULL
            OR (invoice_expiry_minutes >= 5 AND invoice_expiry_minutes <= 1440)),
    CONSTRAINT chk_vto_tolerance
        CHECK (payment_tolerance_pct IS NULL
            OR (payment_tolerance_pct >= 0 AND payment_tolerance_pct <= 10)),
    CONSTRAINT chk_vto_min_invoice
        CHECK (minimum_invoice_sat IS NULL OR minimum_invoice_sat >= 1000),
    CONSTRAINT chk_vto_withdrawal_threshold
        CHECK (withdrawal_approval_threshold_sat IS NULL
            OR (withdrawal_approval_threshold_sat >= 0
                AND withdrawal_approval_threshold_sat <= 1000000000)),
    CONSTRAINT chk_vto_granted_reason_length
        CHECK (length(granted_reason) <= 500),
    -- At least one field must be overridden. An all-NULL row is a bug in the creation path.
    CONSTRAINT chk_vto_at_least_one_override
        CHECK (
            processing_fee_rate IS NOT NULL
            OR sweep_schedule IS NOT NULL
            OR confirmation_depth IS NOT NULL
            OR invoice_expiry_minutes IS NOT NULL
            OR payment_tolerance_pct IS NOT NULL
            OR withdrawal_approval_threshold_sat IS NOT NULL
            OR minimum_invoice_sat IS NOT NULL
            OR miner_fee_cap_sat_vbyte IS NOT NULL
        )
);

CREATE TRIGGER trg_vendor_tier_overrides_updated_at
    BEFORE UPDATE ON vendor_tier_overrides
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Hot path at invoice creation: "does this vendor have any active override?"
-- Plain index on (vendor_id, network) — application adds the expiry filter in the query.
-- NOTE: A partial index with WHERE expires_at > NOW() would be WRONG because NOW() is
-- evaluated once at index creation time, making the index silently stale over time.
CREATE INDEX idx_vto_vendor_network ON vendor_tier_overrides(vendor_id, network);

-- Expiry cleanup job: find overrides whose expiry has passed.
-- Partial index keeps it lean — most overrides are permanent (NULL expires_at).
CREATE INDEX idx_vto_expiry ON vendor_tier_overrides(expires_at)
    WHERE expires_at IS NOT NULL;

COMMENT ON TABLE vendor_tier_overrides IS
    'Per-vendor financial rule overrides. NULL column = use tier default. '
    'Resolved at invoice creation as COALESCE(override.field, tier.field). '
    'Resolved values are snapshotted onto the invoice — later changes have no effect. '
    'expires_at IS NULL = permanent. Expired rows ignored at invoice creation. '
    'IMPORTANT: never use NOW() in a partial index predicate — use plain index + query filter.';
COMMENT ON COLUMN vendor_tier_overrides.granted_by IS
    'Required accountability. RESTRICT prevents this admin from being deleted '
    'while the override exists.';
COMMENT ON COLUMN vendor_tier_overrides.expires_at IS
    'NULL = permanent override. When non-NULL, application filters: '
    'AND (expires_at IS NULL OR expires_at > NOW()). '
    'Correctness does not depend on cleanup job removing expired rows.';


/* ═════════════════════════════════════════════════════════════
   BTC EXCHANGE RATE LOG
   ═════════════════════════════════════════════════════════════ */

/*
 * Historical BTC/fiat rate feed. One row per rate fetch from the provider.
 *
 * invoices.btc_rate_at_creation is the authoritative rate for a specific invoice.
 * This table provides the time-series context around that rate — enabling questions like:
 *   "What was the BTC/USD rate at 14:23:01 on March 5?"
 *   "Were any invoices created during the anomaly window from 09:00 to 09:15?"
 *
 * The application writes a row here on every successful rate fetch regardless of whether
 * the rate changed. This gives a complete audit trail without relying on application logs.
 *
 * anomaly_flag is set when the fetched rate deviates from the rolling 1-hour average
 * by more than a configured threshold (e.g. 20%). Anomalies should also be recorded
 * in financial_audit_events so they appear in the financial audit trail alongside any
 * invoices that may have been created at the anomalous rate.
 */
CREATE TABLE btc_exchange_rate_log (
    id            BIGSERIAL       PRIMARY KEY,

    -- 'mainnet' or 'testnet4'. Each network may fetch rates from different providers.
    network       TEXT            NOT NULL,

    -- ISO 4217 currency code (e.g. 'USD', 'EUR'). Must match BTC_FIAT_CURRENCY config.
    fiat_currency TEXT            NOT NULL,

    -- Exchange rate: 1 BTC = rate fiat_currency. Always positive.
    -- NUMERIC(18,8) provides 8 decimal places of precision — sufficient for all fiat pairs.
    rate          NUMERIC(18,8)   NOT NULL,

    -- Rate provider identifier. Examples: 'coinbase', 'binance', 'coingecko'.
    -- Recorded per-row so anomaly investigations can identify provider-specific failures.
    source        TEXT            NOT NULL,

    -- Timestamp when this rate was fetched from the provider. Default = now().
    fetched_at    TIMESTAMPTZ     NOT NULL DEFAULT NOW(),

    -- TRUE when rate deviates from the rolling average beyond the configured threshold.
    -- Triggers a WARNING alert. anomaly_reason must be populated when TRUE.
    anomaly_flag  BOOLEAN         NOT NULL DEFAULT FALSE,

    -- Human-readable explanation of the anomaly. Examples:
    --   "Rate 18% below 1h rolling average. Provider: coinbase."
    --   "Rate feed gap of 47 seconds. Previous fetch: 2024-03-05T09:12:44Z."
    -- Required when anomaly_flag = TRUE.
    anomaly_reason TEXT,

    CONSTRAINT chk_ber_network
        CHECK (network IN ('mainnet', 'testnet4')),
    -- LOW-2: enforce ISO 4217 format (exactly 3 uppercase letters).
    -- Prevents storing 'usd' or 'USD ' which would cause currency-scoped rate lookups
    -- to silently miss entries, making invoice creation fail or use a stale rate.
    CONSTRAINT chk_ber_fiat_currency_format
        CHECK (length(fiat_currency) = 3 AND fiat_currency = upper(fiat_currency)),
    CONSTRAINT chk_ber_rate_positive
        CHECK (rate > 0),
    -- Coherence: anomaly_reason required when anomaly_flag is set.
    -- A flagged anomaly without a reason is invisible to ops.
    CONSTRAINT chk_ber_anomaly_coherent
        CHECK (anomaly_flag = FALSE OR anomaly_reason IS NOT NULL)
);

-- Hot path: "what is the latest rate for mainnet/USD?" — invoice creation.
-- Descending on fetched_at for LIMIT 1 queries.
CREATE INDEX idx_ber_network_currency_time
    ON btc_exchange_rate_log(network, fiat_currency, fetched_at DESC);

-- Anomaly investigation: find all anomalous rate events in a time window.
-- Partial index excludes normal rows so anomaly queries scan only relevant rows.
CREATE INDEX idx_ber_anomaly
    ON btc_exchange_rate_log(fetched_at DESC)
    WHERE anomaly_flag = TRUE;

-- BRIN index for time-range scans across the full append-only rate history.
-- Physical insertion order correlates with fetched_at, so BRIN is far more
-- space-efficient than B-tree with equivalent range-query selectivity.
CREATE INDEX idx_ber_fetched_brin ON btc_exchange_rate_log
    USING BRIN (fetched_at) WITH (pages_per_range = 128);

COMMENT ON TABLE btc_exchange_rate_log IS
    'Time-series BTC/fiat rate feed. Written on every rate fetch from the provider. '
    'Enables audit of: "what was the rate at time T?" and "which invoices used an anomalous rate?" '
    'invoices.btc_rate_at_creation is authoritative per invoice; this table provides the context.';
COMMENT ON COLUMN btc_exchange_rate_log.anomaly_flag IS
    'TRUE when rate deviates from rolling average beyond configured threshold. '
    'Triggers WARNING alert. anomaly_reason required when TRUE (chk_ber_anomaly_coherent).';



-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS btc_exchange_rate_log       CASCADE;
DROP TABLE IF EXISTS vendor_tier_overrides        CASCADE;
DROP TABLE IF EXISTS vendor_balances              CASCADE;
DROP TABLE IF EXISTS vendor_wallet_config         CASCADE;
DROP TABLE IF EXISTS bitcoin_sync_state           CASCADE;
DROP TABLE IF EXISTS reconciliation_job_state     CASCADE;
DROP TABLE IF EXISTS platform_config              CASCADE;
DROP TABLE IF EXISTS btc_tier_config              CASCADE;

-- +goose StatementEnd
