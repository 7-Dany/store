-- +goose Up
-- +goose StatementBegin

/*
 * 009_btc.sql — Bitcoin payment system core schema.
 *
 * This file establishes all foundational types and tables required before any
 * payment can be processed. The dependency chain runs strictly top-to-bottom:
 * ENUMs must exist before tables, tiers before vendors, vendors before invoices.
 *
 * Tables defined here (in dependency order):
 *   btc_tier_config            — owner-managed fee and feature presets; links to RBAC roles
 *   platform_config            — per-network operational singletons (treasury, sweep-hold)
 *   reconciliation_job_state   — last-run cursor for the reconciliation health monitor
 *   bitcoin_sync_state         — last processed block-height cursor per network
 *   vendor_wallet_config       — per-vendor wallet mode, destination address, tier, KYC state
 *   vendor_balances            — running internal satoshi balance per vendor per network
 *   vendor_tier_overrides      — per-vendor rule overrides that shadow tier defaults
 *   btc_exchange_rate_log      — time-series BTC/fiat rate feed for audit and anomaly detection
 *   invoices                   — core 16-state invoice state machine
 *   invoice_addresses          — P2WPKH bech32 address derived from HD keypool per invoice
 *   invoice_address_monitoring — DB-backed ZMQ watch list; survives process restarts
 *   invoice_payments           — append-only on-chain payment records (txid + vout)
 *   btc_outage_log             — node outage windows for expiry-timer compensation
 *   bitcoin_block_history      — processed-block log with pruned-block placeholders
 *
 * Design invariants that apply across this entire file:
 *   — All surrogate PKs are UUID v7 (temporal sort order). Append-only tables use BIGSERIAL.
 *   — All satoshi amounts are BIGINT. Fiat minor-unit amounts are BIGINT (e.g. USD cents).
 *   — network is always TEXT constrained to ('mainnet', 'testnet4') via CHECK.
 *   — Every mutable table has an updated_at column maintained by fn_set_updated_at().
 *   — Invoice snapshot columns are written once at creation and never updated.
 *   — All balance mutations MUST go through the btc_credit_balance / btc_debit_balance
 *     stored procedures in 011_btc_functions.sql — never direct UPDATE on vendor_balances.
 *
 * Continued in:
 *   010_btc_payouts.sql   — payout_records, financial_audit_events, compliance tables
 *   011_btc_functions.sql — all triggers, stored procedures, grants, autovacuum settings
 *
 * Depends on:
 *   001_core.sql           — users table (UUID PK), uuidv7()
 *   002_core_functions.sql — fn_set_updated_at()
 *   003_rbac.sql           — roles table (UUID PK)
 */


/* ═════════════════════════════════════════════════════════════
   ENUM TYPES
   ═════════════════════════════════════════════════════════════ */

-- Vendor wallet mode — governs how a vendor's settled earnings leave the platform.
--   bridge:   every settlement triggers an immediate on-chain sweep to the vendor's address.
--   platform: earnings accumulate as an internal balance; vendor withdraws manually.
--   hybrid:   earnings accumulate until a threshold is crossed, then auto-sweep fires.
-- This value is snapshotted onto every invoice at creation time. Changing the vendor's
-- live wallet_mode after invoice creation does not affect in-flight settlements.
CREATE TYPE btc_wallet_mode AS ENUM ('bridge', 'platform', 'hybrid');

COMMENT ON TYPE btc_wallet_mode IS
    'How a vendor receives BTC earnings after invoice settlement. '
    'Snapshotted immutably onto invoices at creation; live config changes do not affect in-flight invoices.';

-- Sweep schedule — how often queued payouts are batched and broadcast on-chain.
--   weekly:   Free tier — one batch per week; highest batch amortisation.
--   daily:    Growth/Pro — one batch per day.
--   realtime: Enterprise — sweep fires as soon as a payout record is queued.
CREATE TYPE btc_sweep_schedule AS ENUM ('weekly', 'daily', 'realtime');

COMMENT ON TYPE btc_sweep_schedule IS
    'Frequency at which queued payout records are swept to vendor addresses. '
    'Snapshotted onto invoices at creation.';

-- Tier lifecycle — replaces TEXT + CHECK on btc_tier_config.status (audit decision SEC-09).
-- Using an ENUM eliminates the silent-NULL problem: a NOT NULL ENUM column is always a
-- valid state, whereas a NOT NULL TEXT column with a CHECK can still accept NULLs if
-- the NOT NULL constraint is ever dropped in a future migration.
CREATE TYPE btc_tier_status AS ENUM ('active', 'deactivating', 'inactive');

COMMENT ON TYPE btc_tier_status IS
    'Tier lifecycle. active=new invoices allowed. deactivating=pre-sweep running, no new invoices. '
    'inactive=fully wound down. ENUM replaces TEXT+CHECK to eliminate silent-NULL risk.';

-- Reconciliation result — replaces TEXT + CHECK on reconciliation_job_state (SEC-09).
CREATE TYPE btc_reconciliation_result AS ENUM ('ok', 'discrepancy', 'error');

COMMENT ON TYPE btc_reconciliation_result IS
    'Outcome of a reconciliation run. ENUM replaces TEXT+CHECK to eliminate silent-NULL risk.';

-- Settling source — which invoice status the settling transition came from.
-- The valid values are a strict subset of btc_invoice_status. Keeping a separate ENUM
-- instead of TEXT + CHECK means adding a new invoice status never accidentally silences
-- a constraint that should still apply here. The value is preserved in settlement_failed
-- so an admin retry can take the correct code path (the settlement logic differs depending
-- on whether the invoice arrived from confirming vs underpaid).
CREATE TYPE btc_settling_source AS ENUM ('confirming', 'underpaid');

COMMENT ON TYPE btc_settling_source IS
    'Predecessor status for a settling invoice. Preserved in settlement_failed so '
    'admin retry takes the correct settlement path. ENUM prevents CHECK drift.';

-- Invoice status — the 16-state machine that governs the full invoice lifecycle.
-- State transitions are enforced at the application layer (settlement-technical.md §3).
-- The ENUM ensures the DB rejects any unrecognised state at the type level rather than
-- relying solely on the application to produce valid strings.
-- NEVER remove a value: existing live rows would fail to decode.
-- ADD values with: ALTER TYPE btc_invoice_status ADD VALUE 'new_state';
CREATE TYPE btc_invoice_status AS ENUM (
    'pending',                -- created, waiting for a payment to appear
    'detected',               -- payment seen in the mempool; expiry timer frozen
    'mempool_dropped',        -- payment disappeared from mempool before a block confirmation
    'confirming',             -- first block confirmation received; waiting for required depth
    'settling',               -- settlement worker has claimed this invoice (transient lock)
    'settled',                -- settlement complete; payout queued or balance credited
    'settlement_failed',      -- all retries exhausted; requires admin action to retry
    'reorg_admin_required',   -- invoice was swept then hit a block reorg; admin must verify
    'expired',                -- expiry window elapsed with no confirmed payment
    'expired_with_payment',   -- late payment arrived after expiry, within 30-day window
    'cancelled',              -- vendor cancelled before any payment was detected
    'cancelled_with_payment', -- vendor cancelled, but a payment arrived within 30-day window
    'underpaid',              -- received amount was below the invoiced amount minus tolerance
    'overpaid',               -- received amount exceeded both overpayment thresholds
    'refunded',               -- on-chain refund issued and confirmed on-chain
    'manually_closed'         -- admin wrote off the invoice after investigation
);

COMMENT ON TYPE btc_invoice_status IS
    'Invoice lifecycle: 16 states, 38 permitted transitions (settlement-technical.md §3). '
    'Never remove a value — live rows would fail to decode. '
    'Add new states with ALTER TYPE … ADD VALUE.';

-- Payout status — lifecycle from settlement credit through to on-chain confirmation.
-- Transitions are enforced by the fn_pr_status_guard trigger (011_btc_functions.sql)
-- in addition to the application layer. Terminal states (confirmed, refunded, manual_payout)
-- cannot be transitioned out of at the DB level regardless of application logic.
CREATE TYPE btc_payout_status AS ENUM (
    'held',         -- net amount is below the miner fee floor; accumulating
    'queued',       -- floor cleared; waiting for the next sweep window
    'constructing', -- sweep job has claimed this record for an active batch (transient)
    'broadcast',    -- sweep TX sent to the Bitcoin network; awaiting confirmations
    'confirmed',    -- sweep output confirmed on-chain at required depth — TERMINAL
    'failed',       -- permanent sweep failure after all retries — requires admin action
    'refunded',     -- payout reversed; funds returned to buyer on-chain — TERMINAL
    'manual_payout' -- admin declared an out-of-band payment — TERMINAL
);

COMMENT ON TYPE btc_payout_status IS
    'Payout lifecycle. constructing is transient: stale records (> 10 min) are reclaimed '
    'to queued by the stuck-sweep watchdog. Terminal states: confirmed, refunded, manual_payout. '
    'fn_pr_status_guard trigger enforces the transition matrix at the DB level.';

-- KYC/AML state — used on both vendor_wallet_config and payout_records.
-- The schema supports the enum today; actual KYC logic is gated behind tier thresholds
-- that are not yet configured. kyc_submissions (010_btc_payouts.sql) holds the backing data.
CREATE TYPE btc_kyc_status AS ENUM (
    'not_required', -- default; vendor below KYC threshold for their tier
    'pending',      -- submission in progress at the KYC provider
    'approved',     -- identity verified; no restrictions
    'rejected'      -- identity check failed; payouts may be blocked
);

COMMENT ON TYPE btc_kyc_status IS
    'KYC/AML verification state. Backed by kyc_submissions (010_btc_payouts.sql). '
    'Logic is gated behind non-NULL tier thresholds — currently a placeholder.';

-- Address monitoring lifecycle — whether the ZMQ subscriber is watching a given address.
-- Retired rows are kept permanently for the audit trail.
CREATE TYPE btc_monitoring_status AS ENUM (
    'active',  -- the ZMQ subscriber must watch this address
    'retired'  -- the monitoring window has elapsed; row kept for audit, no longer watched
);

COMMENT ON TYPE btc_monitoring_status IS
    'ZMQ watch state. Retired rows are kept permanently. '
    'The partial index WHERE status = ''active'' keeps hot-path queries fast.';


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

    -- ── Feature flags ─────────────────────────────────────────────────────────

    -- Whether vendors on this tier may select platform wallet mode.
    -- Also gated by platform_config.platform_wallet_mode_legal_approved — both must
    -- be TRUE before any vendor on this tier can use platform wallet mode.
    -- Default FALSE; only the owner can enable this.
    platform_wallet_mode_allowed      BOOLEAN            NOT NULL DEFAULT FALSE,

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
    CONSTRAINT chk_pconfig_hold_coherent
        CHECK (sweep_hold_mode = FALSE
            OR (sweep_hold_reason IS NOT NULL AND sweep_hold_activated_at IS NOT NULL))
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

    -- mode_frozen coherence: reason required so vendor and admin can understand the block.
    CONSTRAINT chk_vwc_mode_frozen_coherent
        CHECK (mode_frozen = FALSE OR mode_frozen_reason IS NOT NULL),

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
    'New invoice creation is blocked. Clears only on explicit vendor reconfiguration — never auto-clears.';


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


/* ═════════════════════════════════════════════════════════════
   INVOICES
   ═════════════════════════════════════════════════════════════ */

/*
 * Core invoice state machine. One row per buyer-initiated purchase request.
 *
 * SNAPSHOT INVARIANT — the most important design rule in this table:
 *   At creation time, every financial rule is resolved as
 *     COALESCE(vendor_tier_overrides.field, btc_tier_config.field)
 *   and snapshotted verbatim onto the invoice row. The settlement engine reads
 *   ONLY from the snapshot columns — never from live tier or vendor config.
 *   This means tier changes, vendor address changes, and override changes that
 *   happen AFTER invoice creation have zero effect on that invoice's settlement.
 *
 * CONCURRENCY INVARIANT — every status UPDATE must follow this pattern:
 *   UPDATE invoices SET status = $new, ... WHERE id = $id AND status = $expected
 *   Assert RowsAffected() == 1. Zero rows = concurrent worker changed status first.
 *   Return ErrStatusPreconditionFailed and trigger a rollback. Never silently continue.
 *
 * STATE MACHINE — 16 states, 38 permitted transitions (settlement-technical.md §3).
 *   Transitions are enforced at the application layer. The btc_invoice_status ENUM
 *   ensures only recognised states can be written.
 *
 * EXPIRY — expires_at is the unadjusted deadline. The expiry cleanup job computes
 *   effective_expires_at = expires_at + total_outage_overlap(btc_outage_log, invoice)
 *   before marking an invoice expired, compensating for node downtime.
 *
 * REORG SAFETY — first_confirmed_block_height is set atomically with the
 *   detected → confirming transition. The reorg rollback query uses this to find
 *   all invoices confirmed at the disconnected block height.
 *   sweep_completed is set atomically with the constructing → broadcast transition
 *   on the payout record. If a reorg hits a sweep_completed=TRUE invoice, the
 *   status becomes reorg_admin_required rather than rolling back to detected.
 */
CREATE TABLE invoices (
    id          UUID                PRIMARY KEY DEFAULT uuidv7(),

    -- ── Parties ────────────────────────────────────────────────────────────────

    -- Vendor who created this invoice. RESTRICT prevents vendor deletion while invoices exist.
    vendor_id   UUID                NOT NULL REFERENCES users(id) ON DELETE RESTRICT,

    -- Buyer who initiated the purchase. RESTRICT prevents buyer deletion while invoice active.
    -- See buyer_refund_address below for the PII note on this field.
    buyer_id    UUID                NOT NULL REFERENCES users(id) ON DELETE RESTRICT,

    -- Tier at creation time. Preserved for audit; snapshot values are in columns below.
    -- RESTRICT prevents tier deletion while any invoice references it.
    tier_id     UUID                NOT NULL REFERENCES btc_tier_config(id) ON DELETE RESTRICT,

    -- 'mainnet' or 'testnet4'.
    network     TEXT                NOT NULL,

    -- Current lifecycle state. btc_invoice_status ENUM enforces valid values at DB level.
    -- All transitions enforced at application layer — see settlement-technical.md §3.
    status      btc_invoice_status  NOT NULL DEFAULT 'pending',

    -- ── Invoice amounts ────────────────────────────────────────────────────────

    -- Satoshi amount the buyer is expected to pay. Floor-rounded from fiat at creation.
    -- This is the authoritative amount for payment matching — fiat_amount is informational.
    amount_sat          BIGINT          NOT NULL,

    -- Fiat price of the product at invoice creation, in minor currency units (e.g. USD cents).
    -- Informational only. amount_sat is authoritative for payment matching.
    fiat_amount         BIGINT          NOT NULL,

    -- ISO 4217 currency code. Stored per-invoice so the record is self-contained
    -- even if the platform currency changes in the future.
    fiat_currency_code  TEXT            NOT NULL,

    -- BTC/fiat exchange rate at creation time, from btc_exchange_rate_log.
    -- Stored per-invoice for audit and fiat-equivalent reconstruction.
    btc_rate_at_creation NUMERIC(18,8)  NOT NULL,

    -- ── Immutable tier + wallet snapshot ──────────────────────────────────────
    -- Written ONCE at creation from COALESCE(vendor_tier_overrides.field, btc_tier_config.field).
    -- NEVER updated after creation. The settlement engine reads ONLY from these columns.
    -- See the SNAPSHOT INVARIANT note in the block comment above.

    -- wallet_mode governs where settlement proceeds go.
    wallet_mode                         btc_wallet_mode NOT NULL,

    -- External destination for bridge/hybrid sweeps. NULL for platform mode.
    -- Copied from vendor_wallet_config.bridge_destination_address at creation.
    -- Changes to the live vendor config do NOT affect this invoice.
    bridge_destination_address          TEXT,

    -- Auto-sweep threshold for hybrid mode. NULL for bridge/platform.
    -- Copied from vendor_wallet_config.auto_sweep_threshold_sat at creation.
    auto_sweep_threshold_sat            BIGINT,

    -- Fee, depth, and cap rules — copied from tier (or override). All immutable post-creation.
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

    -- Unadjusted expiry deadline = created_at + invoice_expiry_minutes.
    -- The expiry cleanup job adds outage overlap from btc_outage_log to compute
    -- the effective deadline. Invoices are only marked expired after the adjusted time.
    expires_at          TIMESTAMPTZ     NOT NULL,

    -- ── Payment detection state ────────────────────────────────────────────────

    -- txid of the payment currently being tracked. Set in the pending → detected transition.
    -- NULL until a payment is detected in the mempool.
    detected_txid           TEXT,

    -- Timestamp when the txid was first seen in the mempool. Set with detected_txid.
    -- NULL until detected. chk_inv_detected_coherent: both NULL or both non-NULL.
    detected_at             TIMESTAMPTZ,

    -- Fiat equivalent of the detected payment amount at detection time. Informational.
    fiat_equiv_at_detection BIGINT,

    -- Two-cycle mempool-drop watchdog state. Set on first absent check; cleared to NULL
    -- in the same UPDATE that transitions mempool_dropped → detected on re-appearance.
    mempool_absent_since    TIMESTAMPTZ,

    -- Block height when the invoice received its first on-chain confirmation.
    -- Set ATOMICALLY in the same DB transaction as detected → confirming.
    -- Required by the reorg rollback query:
    --   WHERE network = $n AND first_confirmed_block_height = $disconnected_height
    -- NULL for all statuses that have not yet received a block confirmation.
    first_confirmed_block_height BIGINT,

    -- ── Settlement state ───────────────────────────────────────────────────────

    -- Which status the invoice was in when it entered settling.
    -- btc_settling_source ENUM (confirming | underpaid). Preserved in settlement_failed
    -- so an admin retry can apply the correct settlement code path.
    -- NULL for all statuses except settling and settlement_failed.
    settling_source     btc_settling_source,

    -- Set TRUE atomically with the constructing → broadcast transition on the payout record.
    -- Reorg rollback pivot:
    --   TRUE → transition to reorg_admin_required (sweep may have broadcast, can't auto-rollback)
    --   FALSE → roll back to detected (payment confirmed but sweep not yet broadcast)
    sweep_completed     BOOLEAN         NOT NULL DEFAULT FALSE,

    -- Settlement retry counter. Incremented on settlement_failed transitions.
    -- Reset to 0 on admin-triggered retry. Never negative.
    retry_count         INTEGER         NOT NULL DEFAULT 0,

    -- ── Fiat equivalents at key timestamps ────────────────────────────────────

    -- Fiat value of received payment at settlement time, in minor currency units.
    -- Authoritative for tax and accounting purposes.
    fiat_equiv_at_settlement BIGINT,

    -- ── Buyer data ────────────────────────────────────────────────────────────

    -- Optional buyer-provided refund address. Validated via RPC ismine check.
    -- PII — subject to platform data retention policy (COMP-05 in todo.md).
    -- Nullified after the retention window by the scheduled purge job.
    buyer_refund_address TEXT,

    -- ── Terminal timestamps ────────────────────────────────────────────────────
    -- Set in the same UPDATE that transitions to the corresponding terminal status.
    -- NULL until the terminal state is reached.

    expired_at      TIMESTAMPTZ,  -- set when status → expired or expired_with_payment
    cancelled_at    TIMESTAMPTZ,  -- set when status → cancelled or cancelled_with_payment
    settled_at      TIMESTAMPTZ,  -- set when status → settled
    refunded_at     TIMESTAMPTZ,  -- set when status → refunded
    closed_at       TIMESTAMPTZ,  -- set when status → manually_closed

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

    -- Snapshot constraints mirror btc_tier_config CHECKs.
    -- Ensures no invoice was created with an out-of-range snapshot value, which would
    -- cause the settlement engine to silently apply invalid rules.
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

    -- Bridge/hybrid modes require a destination address. Platform mode must not have one.
    -- A bridge vendor with NULL destination_address would have the sweep silently skipped.
    CONSTRAINT chk_inv_bridge_addr_coherent
        CHECK (wallet_mode = 'platform' OR bridge_destination_address IS NOT NULL),
    CONSTRAINT chk_inv_platform_no_bridge_addr
        CHECK (wallet_mode != 'platform' OR bridge_destination_address IS NULL),

    -- Hybrid mode requires a threshold; NULL would cause auto-sweep logic to never fire.
    CONSTRAINT chk_inv_hybrid_threshold
        CHECK (wallet_mode = 'hybrid' OR auto_sweep_threshold_sat IS NULL),
    CONSTRAINT chk_inv_hybrid_threshold_required
        CHECK (wallet_mode != 'hybrid' OR auto_sweep_threshold_sat IS NOT NULL),

    CONSTRAINT chk_inv_retry_non_negative
        CHECK (retry_count >= 0),

    -- amount_sat must be at least minimum_invoice_sat from the snapshot.
    -- Enforces at DB level that no invoice was created below the tier floor.
    CONSTRAINT chk_inv_amount_gte_minimum
        CHECK (amount_sat >= minimum_invoice_sat),

    -- Expiry must be strictly after creation. A non-positive expiry window produces
    -- an invoice that is already expired at creation time.
    CONSTRAINT chk_inv_expires_after_created
        CHECK (expires_at > created_at),

    -- detected_txid and detected_at are always set in the same status transition.
    -- One non-NULL while the other is NULL indicates a partial write.
    CONSTRAINT chk_inv_detected_coherent
        CHECK ((detected_txid IS NULL) = (detected_at IS NULL)),

    -- settling_source must be populated in settling and settlement_failed.
    -- Without it, admin retry cannot determine the correct settlement path.
    CONSTRAINT chk_inv_settling_source_required
        CHECK (status NOT IN ('settling', 'settlement_failed')
            OR settling_source IS NOT NULL),

    -- first_confirmed_block_height must be set for all post-confirmation statuses.
    -- Without this, the reorg rollback query silently misses affected invoices.
    CONSTRAINT chk_inv_confirmed_height_set
        CHECK (status NOT IN (
                'confirming', 'settling', 'settled', 'settlement_failed',
                'reorg_admin_required', 'refunded'
            ) OR first_confirmed_block_height IS NOT NULL)
);

CREATE TRIGGER trg_invoices_updated_at
    BEFORE UPDATE ON invoices
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Vendor's invoice list: dashboard display, suspension check, deletion guard.
-- Covers the common "show active invoices for vendor X" sorted by recency.
CREATE INDEX idx_inv_vendor_status
    ON invoices(vendor_id, status, created_at DESC);

-- Buyer's invoice list: rate-limiting check (max 20 pending per buyer).
-- Used on invoice creation to count pending invoices for the requesting buyer.
CREATE INDEX idx_inv_buyer_status
    ON invoices(buyer_id, status);

-- Settlement worker: claim confirming invoices that have reached the required depth.
-- Query pattern: WHERE network = $n AND status = 'confirming'
--   AND first_confirmed_block_height <= (current_height - confirmation_depth)
CREATE INDEX idx_inv_confirming
    ON invoices(network, first_confirmed_block_height)
    WHERE status = 'confirming';

-- Mempool-drop watchdog: scan all detected invoices to call getmempoolentry.
-- Partial index keeps this fast even when most invoices are in terminal states.
CREATE INDEX idx_inv_detected
    ON invoices(network, detected_at)
    WHERE status = 'detected';

-- Expiry cleanup job: candidates for expiry evaluation.
-- NOTE: expires_at here is the UNADJUSTED deadline. The job applies the outage
-- compensation formula to compute effective_expires_at before marking expired.
-- This index returns a superset; the job filters further after adjustment.
CREATE INDEX idx_inv_expiry_candidates
    ON invoices(network, expires_at)
    WHERE status IN ('pending', 'mempool_dropped');

-- Stale settling-claim watchdog: returns invoices stuck in settling > 5 minutes.
-- Query pattern: WHERE network = $n AND status = 'settling' AND updated_at < NOW() - '5 min'
CREATE INDEX idx_inv_stale_settling
    ON invoices(network, updated_at)
    WHERE status = 'settling';

-- Underpaid re-settlement: a new payment arrives on an underpaid invoice.
-- Partial index — underpaid invoices are uncommon so this stays small.
CREATE INDEX idx_inv_underpaid
    ON invoices(network)
    WHERE status = 'underpaid';

-- Reorg rollback: find all invoices first-confirmed at the disconnected block height.
-- Query pattern: WHERE network = $n AND first_confirmed_block_height = $disconnected_height
CREATE INDEX idx_inv_first_confirmed_height
    ON invoices(network, first_confirmed_block_height)
    WHERE first_confirmed_block_height IS NOT NULL;

-- Vendor deletion guard: "does this vendor have any live financial obligations?"
-- Partial index scans only non-terminal statuses, keeping it selective for
-- high-volume vendors who have many completed (terminal) invoices.
CREATE INDEX idx_inv_vendor_network_active
    ON invoices(vendor_id, network)
    WHERE status NOT IN (
        'expired', 'cancelled', 'settled', 'refunded', 'manually_closed',
        'expired_with_payment', 'cancelled_with_payment'
    );

-- FK support: tier_id has no implicit index. Required for:
--   (a) ON DELETE RESTRICT check on btc_tier_config — without this, every tier deletion
--       attempt scans the full invoices table.
--   (b) Admin query: "how many active invoices are on the free tier?"
-- (IDX-06 audit decision)
CREATE INDEX idx_inv_tier_id ON invoices(tier_id);

-- Reconciliation inflight sum: network + status covering index for the query:
--   SELECT SUM(amount_sat) FROM invoices WHERE network = $n AND status IN (...)
-- INCLUDE eliminates heap fetch for amount_sat and created_at. (IDX-10)
CREATE INDEX idx_inv_network_status_inflight
    ON invoices(network, status)
    INCLUDE (amount_sat, created_at)
    WHERE status IN ('pending', 'detected', 'confirming', 'settling',
                     'underpaid', 'mempool_dropped');

COMMENT ON TABLE invoices IS
    'Core 16-state invoice state machine. '
    'SNAPSHOT INVARIANT: all financial columns are snapshotted once at creation and never updated. '
    'COALESCE(vendor_tier_overrides.field, btc_tier_config.field) is the resolution rule. '
    'CONCURRENCY INVARIANT: every status UPDATE must assert RowsAffected() == 1. '
    '0 rows = concurrent worker changed status first → ErrStatusPreconditionFailed + rollback.';
COMMENT ON COLUMN invoices.settling_source IS
    'btc_settling_source ENUM: confirming | underpaid. '
    'Preserved through settlement_failed so admin retry uses the correct code path. '
    'NULL for all statuses except settling and settlement_failed.';
COMMENT ON COLUMN invoices.sweep_completed IS
    'TRUE = payout record reached broadcast before any reorg. '
    'Pivot for reorg rollback: TRUE → reorg_admin_required, FALSE → roll back to detected.';
COMMENT ON COLUMN invoices.first_confirmed_block_height IS
    'Set atomically with detected → confirming. Required for reorg rollback query. '
    'NULL for statuses that have not yet received a block confirmation.';
COMMENT ON COLUMN invoices.buyer_refund_address IS
    'PII — subject to data retention policy. Nullified after retention window by purge job.';
COMMENT ON COLUMN invoices.expires_at IS
    'Unadjusted expiry deadline. The expiry cleanup job adds outage overlap '
    'from btc_outage_log before marking expired. Never update this column after creation.';


/* ═════════════════════════════════════════════════════════════
   INVOICE ADDRESSES
   ═════════════════════════════════════════════════════════════ */

/*
 * Exactly one P2WPKH bech32 address per invoice, derived from Bitcoin Core's HD keypool.
 * Both the address string and the HD derivation index are stored:
 *
 *   address              — used for on-chain matching and ZMQ watch registration.
 *   hd_derivation_index  — required for wallet recovery scenarios:
 *     Scenario B: keypool cursor advance uses MAX(hd_derivation_index) + buffer.
 *     Scenario C: import range calculation for rescan.
 *     Scenario D: listlabeladdresses("invoice") enumerates all platform-managed addresses.
 *
 * The UNIQUE constraint on (address, network) is a critical safety invariant.
 * If Bitcoin Core ever issues the same address twice (should never happen with a healthy
 * keypool), the second invoice creation fails at the DB level rather than creating a
 * duplicate that would cause double-settlement. On constraint violation: return 503 and
 * fire a KeypoolOrRPCError CRITICAL alert. Do NOT retry automatically.
 *
 * label MUST always be 'invoice'. This value is used by Scenario D wallet recovery:
 *   listlabeladdresses("invoice") enumerates all platform-managed addresses.
 * Any getnewaddress call that uses a different label silently breaks recovery.
 * The CHECK (label = 'invoice') enforces this at the DB level.
 *
 * The fn_iam_address_consistency trigger (011_btc_functions.sql) verifies that every
 * insert into invoice_address_monitoring uses the same address and network as this table.
 */
CREATE TABLE invoice_addresses (
    id          BIGSERIAL   PRIMARY KEY,

    -- 1:1 with invoices. RESTRICT prevents address deletion while the invoice exists.
    -- UNIQUE (invoice_id) enforces the 1:1 relationship at the DB level.
    invoice_id  UUID        NOT NULL REFERENCES invoices(id) ON DELETE RESTRICT,

    -- P2WPKH bech32 address (e.g. bc1q...). UNIQUE per (address, network) — duplicate
    -- address is KeypoolOrRPCError CRITICAL; do NOT retry.
    address     TEXT        NOT NULL,

    -- 'mainnet' or 'testnet4'. Combined with address for global uniqueness.
    network     TEXT        NOT NULL,

    -- Leaf index from the BIP-32 HD path (e.g. m/84'/0'/0'/0/5200 → 5200).
    -- BIGINT: BIP-32 unhardened leaf indices reach 2^31-1; INTEGER would overflow at boundary.
    -- Used by: Scenario B (MAX + buffer for cursor advance), Scenario C (import range).
    hd_derivation_index BIGINT  NOT NULL,

    -- Always 'invoice'. CHECK (label = 'invoice') enforced here to protect Scenario D
    -- recovery. getnewaddress MUST be called as: getnewaddress "invoice" "bech32".
    -- Using any other label silently breaks listlabeladdresses("invoice") recovery.
    label       TEXT        NOT NULL DEFAULT 'invoice',

    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Exactly one address per invoice.
    CONSTRAINT uq_invoice_address_per_invoice UNIQUE (invoice_id),

    -- Duplicate address = KeypoolOrRPCError CRITICAL. Do NOT retry.
    CONSTRAINT uq_invoice_address_network     UNIQUE (address, network),

    CONSTRAINT chk_ia_network
        CHECK (network IN ('mainnet', 'testnet4')),
    -- Enforces the Scenario D recovery invariant at the DB level.
    -- Any value other than 'invoice' silently breaks listlabeladdresses recovery.
    CONSTRAINT chk_ia_label_invariant
        CHECK (label = 'invoice'),
    CONSTRAINT chk_ia_derivation_non_negative
        CHECK (hd_derivation_index >= 0)
);

-- Wallet recovery cursor: MAX(hd_derivation_index) WHERE network = $n.
-- Descending direction is optimal for a LIMIT 1 MAX query.
CREATE INDEX idx_ia_max_derivation ON invoice_addresses(network, hd_derivation_index DESC);

-- NOTE: The (address, network) lookup index is covered by UNIQUE uq_invoice_address_network.
-- No separate index needed for ZMQ address resolution — the unique index handles it.

COMMENT ON TABLE invoice_addresses IS
    'One P2WPKH bech32 address per invoice, derived from Bitcoin Core HD keypool. '
    'UNIQUE (address, network): duplicate = KeypoolOrRPCError CRITICAL — do NOT retry. '
    'label = ''invoice'' enforced by CHECK — critical for Scenario D recovery. '
    'hd_derivation_index required for Scenario B/C wallet recovery.';
COMMENT ON COLUMN invoice_addresses.hd_derivation_index IS
    'Leaf index from hdkeypath (e.g. m/84''/0''/0''/0/5200 → 5200). '
    'BIGINT to avoid overflow at 2^31-1. '
    'Scenario B: MAX(index) × 1.2 = keypool cursor advance. Scenario C: import range.';
COMMENT ON COLUMN invoice_addresses.label IS
    'Always ''invoice'' — enforced by CHECK. '
    'getnewaddress MUST use "invoice" label. Any other label breaks Scenario D recovery.';


/* ═════════════════════════════════════════════════════════════
   INVOICE ADDRESS MONITORING
   ═════════════════════════════════════════════════════════════ */

/*
 * DB-backed ZMQ watch list. This is the authoritative source of truth for which
 * addresses the ZMQ subscriber must actively watch. Because it lives in the DB, it:
 *   — Survives process restarts (the subscriber reloads on startup).
 *   — Is consistent across horizontal replicas (each subscriber reads the same table).
 *   — Provides an audit trail of what was watched and when.
 *
 * Registration ordering invariant (invoice-technical.md §2):
 *   1. INSERT into invoice_addresses (allocate the address from Bitcoin Core).
 *   2. INSERT into invoice_address_monitoring (register the watch in the DB).
 *   3. Call RegisterImmediate() on the ZMQ subscriber AFTER the DB write commits.
 *   The 5-minute periodic reload is a safety net only — never rely on it for
 *   newly created invoices.
 *
 * Address consistency: fn_iam_address_consistency (011) fires BEFORE INSERT and
 * rejects any row where address/network doesn't match invoice_addresses for the
 * same invoice_id. This prevents the ZMQ subscriber from watching a different
 * address than the one actually allocated to the invoice.
 *
 * Monitoring window rules (set in the same DB transaction as the terminal transition):
 *   expired / cancelled / settled / refunded / manually_closed → monitor_until = terminal_at + 30 days
 *   reorg_admin_required → monitor_until = NULL (open-ended; set when leaving this status)
 *   Non-terminal statuses → monitor_until = NULL (monitored indefinitely)
 *
 * Retirement: the expiry cleanup job sets status = 'retired' where monitor_until < NOW().
 * Retired rows are NEVER deleted — they form the permanent monitoring audit trail.
 * The partial index WHERE status = 'active' keeps all hot-path queries fast regardless
 * of how many retired rows accumulate over time.
 */
CREATE TABLE invoice_address_monitoring (
    id          BIGSERIAL           PRIMARY KEY,

    -- Parent invoice. RESTRICT prevents deletion while monitoring row exists.
    invoice_id  UUID                NOT NULL REFERENCES invoices(id) ON DELETE RESTRICT,

    -- The address being monitored. Must match invoice_addresses.address for the same
    -- invoice_id — enforced by fn_iam_address_consistency trigger (011).
    address     TEXT                NOT NULL,

    -- 'mainnet' or 'testnet4'. Must match invoice_addresses.network for the same invoice.
    network     TEXT                NOT NULL,

    -- NULL = actively monitored (invoice not in a terminal state, or reorg_admin_required).
    -- Non-NULL = monitoring window expires at this timestamp.
    -- Set in the same DB transaction as the terminal status transition.
    -- chk_iam_retired_has_monitor_until requires this to be non-NULL when status = 'retired'.
    monitor_until   TIMESTAMPTZ,

    -- Active or retired. Retired rows are kept permanently. The partial index on 'active'
    -- ensures the ZMQ reload query stays fast regardless of retired row accumulation.
    status  btc_monitoring_status   NOT NULL DEFAULT 'active',

    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_iam_network
        CHECK (network IN ('mainnet', 'testnet4')),
    -- A retired row must have had monitor_until set before retirement.
    -- Retiring with NULL monitor_until indicates a workflow bug (fn_iam_address_consistency
    -- should have set it in the same transaction as the terminal status transition).
    CONSTRAINT chk_iam_retired_has_monitor_until
        CHECK (status != 'retired' OR monitor_until IS NOT NULL)
);

CREATE TRIGGER trg_invoice_address_monitoring_updated_at
    BEFORE UPDATE ON invoice_address_monitoring
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- HOT PATH: ZMQ hashtx handler → resolve address + network → invoice_id.
-- This is on the critical path of every received Bitcoin transaction.
-- Partial index on 'active' ensures it stays fast as retired rows accumulate.
CREATE INDEX idx_iam_active
    ON invoice_address_monitoring(address, network)
    WHERE status = 'active';

-- ZMQ startup/reconnect reload: fetch all active monitoring records for a network.
-- Used on startup and after every reconnect to rebuild the in-memory watch set.
CREATE INDEX idx_iam_reload
    ON invoice_address_monitoring(network, invoice_id)
    WHERE status = 'active';

-- Expiry cleanup job: find elapsed monitoring windows to retire.
-- Partial index excludes active rows with NULL monitor_until (permanent watchers).
CREATE INDEX idx_iam_expiry_cleanup
    ON invoice_address_monitoring(monitor_until)
    WHERE status = 'active' AND monitor_until IS NOT NULL;

-- Safety: at most one active monitoring record per invoice.
-- Prevents the ZMQ subscriber from dispatching the same payment event twice if a
-- bug creates a duplicate active row for the same invoice.
CREATE UNIQUE INDEX uq_iam_one_active_per_invoice
    ON invoice_address_monitoring(invoice_id)
    WHERE status = 'active';

-- Safety: an address cannot be actively monitored for two different invoices.
-- Guards against keypool address reuse reaching the DB layer.
CREATE UNIQUE INDEX uq_iam_active_address_network
    ON invoice_address_monitoring(address, network)
    WHERE status = 'active';

COMMENT ON TABLE invoice_address_monitoring IS
    'Authoritative ZMQ watch list. Survives restarts; consistent across horizontal replicas. '
    'fn_iam_address_consistency (011) verifies address matches invoice_addresses at INSERT. '
    'Registration order: INSERT invoice_addresses → INSERT monitoring → RegisterImmediate(). '
    'Retired rows never deleted — partial index WHERE status=active keeps hot path fast.';
COMMENT ON COLUMN invoice_address_monitoring.monitor_until IS
    'NULL = active monitoring (non-terminal or reorg_admin_required). '
    'Set in same TX as terminal transition: terminal_at + 30 days. '
    'Required before retirement (chk_iam_retired_has_monitor_until).';


/* ═════════════════════════════════════════════════════════════
   INVOICE PAYMENTS
   ═════════════════════════════════════════════════════════════ */

/*
 * Append-only record of every on-chain payment received for an invoice.
 * A payment is always recorded regardless of invoice status, including for
 * post-settlement, double-payment, and late-payment cases.
 *
 * Idempotency: all INSERTs use ON CONFLICT (txid, vout_index) DO NOTHING.
 * The UNIQUE constraint enforces this at the DB level. ZMQ events may deliver
 * the same txid multiple times (reconnect, deduplication window); idempotent
 * INSERTs ensure the table remains consistent.
 *
 * Multi-output handling: a single TX may send multiple vouts to the same address.
 * Settlement Phase 1 sums ALL value_sat rows for a given invoice_id before
 * comparing against the invoiced amount and tolerance band.
 *   SELECT SUM(value_sat) FROM invoice_payments WHERE invoice_id = $id AND txid = $txid
 *
 * The FK constraint to invoice_addresses guarantees that an address was properly
 * allocated to the invoice before any payment can be recorded. Without this guard,
 * a payment could be written for an invoice that was never assigned an address,
 * making on-chain reconciliation against that address impossible.
 *
 * double_payment and post_settlement are flag columns for anomaly detection.
 * The settlement engine does not special-case them — they are informational flags
 * that surface in admin dashboards and trigger review workflows.
 */
CREATE TABLE invoice_payments (
    id              BIGSERIAL       PRIMARY KEY,

    -- Parent invoice. RESTRICT: payment cannot exist without a parent invoice.
    invoice_id      UUID            NOT NULL REFERENCES invoices(id) ON DELETE RESTRICT,

    -- Bitcoin transaction ID (64-character hex string).
    txid            TEXT            NOT NULL,

    -- Output index within the transaction. Combined with txid for global uniqueness.
    -- A single TX may fund multiple invoices via different vout indices.
    vout_index      INTEGER         NOT NULL,

    -- Satoshi value of this specific output (not the total TX value).
    -- Always positive — zero-value outputs are rejected.
    value_sat       BIGINT          NOT NULL,

    -- Timestamp when this payment was first observed (mempool or confirmed).
    detected_at     TIMESTAMPTZ     NOT NULL DEFAULT NOW(),

    -- TRUE when this txid is different from the invoice's detected_txid while the original
    -- is still in the mempool — may indicate a double-spend attempt.
    -- Triggers admin review. Does NOT block settlement of the first-seen payment.
    double_payment  BOOLEAN         NOT NULL DEFAULT FALSE,

    -- TRUE when this payment arrived after the invoice was already in 'settled' status.
    -- Triggers admin review — the funds arrived but the settlement is already complete.
    post_settlement BOOLEAN         NOT NULL DEFAULT FALSE,

    -- Global uniqueness: one row per (txid, vout_index) across the entire table.
    -- INSERT with ON CONFLICT (txid, vout_index) DO NOTHING for idempotency.
    CONSTRAINT uq_inv_payment_txid_vout UNIQUE (txid, vout_index),

    CONSTRAINT chk_ip_value_positive
        CHECK (value_sat > 0),
    CONSTRAINT chk_ip_vout_non_negative
        CHECK (vout_index >= 0),

    -- Ensures the invoice had an address allocated before any payment can be recorded.
    -- invoice_addresses.invoice_id is UNIQUE so this resolves to exactly one address row.
    -- Without this, a payment could be orphaned from its allocation address.
    CONSTRAINT fk_ip_invoice_address
        FOREIGN KEY (invoice_id)
        REFERENCES invoice_addresses(invoice_id)
        ON DELETE RESTRICT
);

-- Phase 1 SUM query: SUM(value_sat) WHERE invoice_id = $id.
-- INCLUDE makes this index-only — value_sat, txid, vout_index, detected_at are
-- all returned from the index without touching the heap. (IDX-05 audit decision)
CREATE INDEX idx_ip_invoice_id ON invoice_payments(invoice_id)
    INCLUDE (value_sat, txid, vout_index, detected_at);

-- Per-invoice time-ordered payment history: used in reconciliation and admin views.
CREATE INDEX idx_ip_invoice_detected ON invoice_payments(invoice_id, detected_at DESC);

-- BRIN index for time-range reconciliation scans across the append-only table.
-- Rows are appended in roughly monotonic detected_at order, so BRIN is orders of
-- magnitude smaller than B-tree with equivalent selectivity for range queries. (IDX-08)
CREATE INDEX idx_ip_detected_brin ON invoice_payments
    USING BRIN (detected_at) WITH (pages_per_range = 128);

COMMENT ON TABLE invoice_payments IS
    'Append-only on-chain payment records. One row per (txid, vout_index). '
    'Always INSERT with ON CONFLICT (txid, vout_index) DO NOTHING for idempotency. '
    'Multi-output TXs generate one row per vout — settlement Phase 1 SUMs all rows for invoice. '
    'idx_ip_invoice_id uses INCLUDE for index-only Phase 1 SUM scan.';
COMMENT ON COLUMN invoice_payments.double_payment IS
    'TRUE = different txid arrived while original still in mempool — possible double-spend. '
    'Does not block settlement. Triggers admin review.';
COMMENT ON COLUMN invoice_payments.post_settlement IS
    'TRUE = payment arrived after invoice was already settled. '
    'Funds received but settlement is complete. Triggers admin review.';


/* ═════════════════════════════════════════════════════════════
   BTC OUTAGE LOG
   ═════════════════════════════════════════════════════════════ */

/*
 * Records periods when the Bitcoin Core node was unreachable. Used by the expiry
 * cleanup job to compute effective_expires_at for invoices, compensating for time
 * the invoice could not have been paid even if the buyer tried.
 *
 * Effective expiry formula (invoice-feature.md §5):
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
 *   On disconnect:  INSERT with ended_at = NULL.
 *                   Use pg_try_advisory_lock(hashtext('btc_outage_log:' || network))
 *                   to prevent duplicate open records across horizontal instances.
 *   On reconnect:   UPDATE SET ended_at = NOW() WHERE id = $id AND ended_at IS NULL.
 *   On startup:     Close any open record from a previous (crashed) process.
 *   Stale records:  A 6-hour maintenance job closes records older than 48 hours with
 *                   ended_at = MIN(NOW(), started_at + INTERVAL '48 hours').
 *
 * The uq_outage_one_open_per_network UNIQUE partial index is the authoritative guard
 * against duplicate open records — it applies even if the advisory lock is skipped.
 */
CREATE TABLE btc_outage_log (
    id          BIGSERIAL       PRIMARY KEY,

    -- 'mainnet' or 'testnet4'. Node connectivity is tracked per-network independently.
    network     TEXT            NOT NULL,

    -- Timestamp when the node became unreachable. Default = now() at INSERT time.
    started_at  TIMESTAMPTZ     NOT NULL DEFAULT NOW(),

    -- Timestamp when connectivity was restored. NULL = outage is ongoing.
    -- Application startup must close any open record from a crashed previous process.
    ended_at    TIMESTAMPTZ,

    CONSTRAINT chk_outage_network
        CHECK (network IN ('mainnet', 'testnet4')),
    -- ended_at must be strictly after started_at.
    -- An outage of zero duration (ended_at = started_at) is not a valid record.
    CONSTRAINT chk_outage_times
        CHECK (ended_at IS NULL OR ended_at > started_at)
);

-- DB-level enforcement: at most one open outage record per network.
-- This index also serves as the hot-path lookup for "is there an open outage?"
-- so the separate idx_outage_open (which was an exact duplicate) has been removed. (IDX-02)
-- The advisory lock reduces contention but a replica that skips the lock still
-- cannot create a duplicate open record due to this unique index.
CREATE UNIQUE INDEX uq_outage_one_open_per_network
    ON btc_outage_log(network)
    WHERE ended_at IS NULL;

-- Expiry formula range join: overlap check between outage windows and invoice windows.
-- Covers: WHERE started_at < original_expires_at AND COALESCE(ended_at, NOW()) > created_at
CREATE INDEX idx_outage_range
    ON btc_outage_log(network, started_at, ended_at);

COMMENT ON TABLE btc_outage_log IS
    'Node outage periods for invoice expiry-timer compensation. '
    'INSERT on disconnect; UPDATE ended_at on reconnect; close stale records on startup. '
    'Advisory lock (hashtext(''btc_outage_log:'' || network)) prevents concurrent duplicate INSERTs. '
    'uq_outage_one_open_per_network is both the uniqueness guard AND the hot-path lookup index. '
    '6-hour maintenance job closes records older than 48 hours.';
COMMENT ON COLUMN btc_outage_log.ended_at IS
    'NULL = outage ongoing. Application startup MUST close any open record left by a crashed process '
    'before accepting new connections.';


/* ═════════════════════════════════════════════════════════════
   BITCOIN BLOCK HISTORY
   ═════════════════════════════════════════════════════════════ */

/*
 * Processed-block log. One row per (height, network). Written by the block-processing
 * pipeline as each block is confirmed and all transactions in it are reconciled.
 *
 * Pruned blocks: Bitcoin Core's pruning mode deletes old block data below the prune
 * height. When HandleRecovery encounters a pruned block, it inserts a placeholder row
 * (block_hash = NULL, pruned = TRUE) so the bitcoin_sync_state cursor can advance past
 * the pruned range without getting stuck waiting for data that no longer exists.
 *
 * The PK is (height, network) rather than a surrogate key so that a duplicate block
 * processing attempt is caught at the DB level (insert conflicts rather than creating
 * duplicate rows that inflate reconciliation counts).
 */
CREATE TABLE bitcoin_block_history (
    -- Block height in the chain. Must be non-negative.
    height       BIGINT      NOT NULL,

    -- 'mainnet' or 'testnet4'. Each network has an independent block history.
    network      TEXT        NOT NULL,

    -- 64-character block hash hex string. NULL when pruned = TRUE.
    block_hash   TEXT,

    -- TRUE when Bitcoin Core pruned this block before it was processed.
    -- Placeholder rows allow the cursor to advance past the pruned range.
    -- chk_bbh_pruned_coherent: pruned = TRUE implies block_hash must be NULL.
    pruned       BOOLEAN     NOT NULL DEFAULT FALSE,

    -- Timestamp when this block was processed by the reconciliation pipeline.
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (height, network),

    CONSTRAINT chk_bbh_network
        CHECK (network IN ('mainnet', 'testnet4')),
    CONSTRAINT chk_bbh_height_non_negative
        CHECK (height >= 0),
    -- A pruned block cannot have a known hash — we never had the data.
    -- A non-pruned block always has a hash — if missing it indicates a processing bug.
    CONSTRAINT chk_bbh_pruned_coherent
        CHECK (pruned = FALSE OR block_hash IS NULL)
);

-- Range scan: "fetch all blocks between height A and B for network N."
-- PK covers (height, network) ascending; this covers (network, height DESC)
-- for "most recent blocks first" queries used by the block-processing watchdog.
CREATE INDEX idx_bbh_network_height
    ON bitcoin_block_history(network, height DESC);

COMMENT ON TABLE bitcoin_block_history IS
    'Processed-block log. One row per (height, network). '
    'Pruned blocks get placeholder rows (block_hash=NULL, pruned=TRUE) '
    'so HandleRecovery cursor can advance past the pruned range. '
    'Duplicate processing attempts are caught by the composite PK.';
COMMENT ON COLUMN bitcoin_block_history.pruned IS
    'TRUE when Bitcoin Core pruned this block before processing. '
    'block_hash must be NULL when pruned=TRUE (chk_bbh_pruned_coherent).';


-- All triggers, functions, grants, and autovacuum settings are in 011_btc_functions.sql.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

/*
 * Drop in reverse FK dependency order.
 * 010_btc_payouts.sql tables (payout_records, financial_audit_events, etc.)
 * are dropped by that file's Down migration and must be dropped BEFORE this one.
 * Goose processes Down migrations in reverse numerical order, so 010 Down runs
 * before 009 Down automatically.
 */

DROP TABLE IF EXISTS bitcoin_block_history        CASCADE;
DROP TABLE IF EXISTS btc_outage_log               CASCADE;
DROP TABLE IF EXISTS invoice_payments             CASCADE;
DROP TABLE IF EXISTS invoice_address_monitoring   CASCADE;
DROP TABLE IF EXISTS invoice_addresses            CASCADE;
DROP TABLE IF EXISTS invoices                     CASCADE;
DROP TABLE IF EXISTS btc_exchange_rate_log        CASCADE;
DROP TABLE IF EXISTS vendor_tier_overrides        CASCADE;
DROP TABLE IF EXISTS vendor_balances              CASCADE;
DROP TABLE IF EXISTS vendor_wallet_config         CASCADE;
DROP TABLE IF EXISTS bitcoin_sync_state           CASCADE;
DROP TABLE IF EXISTS reconciliation_job_state     CASCADE;
DROP TABLE IF EXISTS platform_config              CASCADE;
DROP TABLE IF EXISTS btc_tier_config              CASCADE;

-- Drop ENUMs after all tables referencing them are gone.
DROP TYPE IF EXISTS btc_monitoring_status         CASCADE;
DROP TYPE IF EXISTS btc_kyc_status                CASCADE;
DROP TYPE IF EXISTS btc_payout_status             CASCADE;
DROP TYPE IF EXISTS btc_invoice_status            CASCADE;
DROP TYPE IF EXISTS btc_settling_source           CASCADE;
DROP TYPE IF EXISTS btc_reconciliation_result     CASCADE;
DROP TYPE IF EXISTS btc_tier_status               CASCADE;
DROP TYPE IF EXISTS btc_sweep_schedule            CASCADE;
DROP TYPE IF EXISTS btc_wallet_mode               CASCADE;

-- +goose StatementEnd
