-- +goose Up
-- +goose StatementBegin

/*
 * 017_btc_audit.sql — Financial audit trail and operational log tables.
 *
 * Tables defined here:
 *   financial_audit_events — immutable append-only financial audit trail
 *   wallet_backup_success  — wallet.dat backup completion tracking
 *   ops_audit_log          — non-financial administrative operation audit trail
 *     Original: balance_debit required invoice_id IS NOT NULL
 *     Fixed:    balance_debit requires (invoice_id IS NOT NULL OR payout_record_id IS NOT NULL)
 *
 * Depends on: 015_btc_payouts.sql (payout_records FK), 012_btc_invoices.sql (invoices FK)
 * Continued in: 018_btc_audit_functions.sql
 */

/* ═════════════════════════════════════════════════════════════
   FINANCIAL AUDIT EVENTS
   ═════════════════════════════════════════════════════════════ */

/*
 * Immutable, append-only financial audit trail. Retained indefinitely.
 * Separate from auth_audit_log (which covers authentication events only).
 *
 * IMMUTABILITY — enforced at two independent layers:
 *   Layer 1 — DB privilege:
 *     btc_app_role has INSERT and SELECT only on this table.
 *     UPDATE and DELETE are not granted. See grants in 011_btc_functions.sql.
 *   Layer 2 — DB triggers (011_btc_functions.sql):
 *     fn_btc_audit_immutable: fires BEFORE UPDATE and BEFORE DELETE — rejects unconditionally
 *                             from any DB user including superuser (during migrations).
 *     fn_btc_audit_no_truncate: fires BEFORE TRUNCATE — closes the gap left by row-level triggers.
 *
 * ADMIN RESOLUTION PATTERN (audit-technical.md §2):
 *   Admin corrections are written as NEW rows that reference the original event via
 *   references_event_id. The original row is NEVER modified.
 *   Example: an admin overrides a failed settlement → INSERT a new 'settlement_admin_close'
 *   event with references_event_id = original settlement_failed event id.
 *
 * EVENT TYPES:
 *   event_type uses typed constants from internal/audit/audit.go (Go compiled constants).
 *   A DB ENUM is intentionally NOT used — typed Go constants enforce validity at compile
 *   time without requiring ALTER TYPE migrations for every new event type.
 *   See SEC-04 decision in todo.md.
 *
 * GDPR COMPLIANCE:
 *   IMPORTANT: Do NOT store raw IP addresses in this table.
 *   financial_audit_events is immutable (DELETE blocked by trigger) — any PII stored here
 *   cannot be erased for GDPR Article 17 requests. Use sse_token_issuances for SSE token
 *   IP tracking (which supports erasure). See SEC-07 decision in todo.md.
 *
 *   actor_label stores the actor's email/username. For GDPR compliance, consider storing
 *   HMAC-SHA256(email, server_secret) instead of raw email. See COMP-02 in todo.md.
 *
 * RECONCILIATION:
 *   The balance_before_sat / balance_after_sat columns allow a financial auditor to
 *   reconstruct the exact vendor balance at any point in time by replaying events in
 *   timestamp order. Both columns must be populated together for balance-change events
 *   (chk_fae_balance_required_for_credit_debit).
 *
 * INDEXES:
 *   BRIN replaces B-tree for timestamp and id. On this append-only table, physical insertion
 *   order correlates with timestamp, making BRIN orders of magnitude smaller with equivalent
 *   range-query selectivity.
 */
CREATE TABLE financial_audit_events (
    -- BIGSERIAL PK for guaranteed monotonic ordering on the append-only table.
    -- Monotonic order enables BRIN indexes and efficient range-based compliance scans.
    id          BIGSERIAL       PRIMARY KEY,

    -- Application-defined event type. Use constants from internal/audit/audit.go.
    -- TEXT not ENUM: Go compile-time constants enforce validity without DB migrations.
    -- Max 128 chars.
    event_type  TEXT            NOT NULL,

    -- When the event occurred. Default = transaction commit time.
    timestamp   TIMESTAMPTZ     NOT NULL DEFAULT NOW(),

    -- 'mainnet', 'testnet4', or NULL for system events not specific to a network.
    -- chk_fae_network: when set, must be a recognised value.
    network     TEXT,

    -- ── Actor information ──────────────────────────────────────────────────────

    -- Broad category of who triggered the event. TEXT+CHECK intentionally kept
    -- flexible (not ENUM) given the dynamic RBAC system. See SEC-11 decision in todo.md.
    -- Valid values: 'system' (background job), 'vendor', 'buyer', 'admin'.
    actor_type  TEXT            NOT NULL,

    -- UUID of the actor. SET NULL when the user is deleted so the audit row is preserved
    -- even after account deletion. The label below provides the permanent identity record.
    actor_id    UUID            REFERENCES users(id) ON DELETE SET NULL,

    -- Stable identity snapshot captured at INSERT time by the application.
    -- fn_fae_validate_actor_label (011) verifies this matches users.email (or username for
    -- OAuth-only accounts) for non-NULL actor_id, preventing actor spoofing (SEC-05).
    -- This field remains readable after actor_id goes NULL on user deletion.
    -- GDPR note: for raw email, consider HMAC hash instead. See COMP-02 in todo.md.
    -- Required (non-empty) for non-system actors — enforced by chk_fae_actor_label_present.
    actor_label TEXT            NOT NULL DEFAULT '',

    -- ── Financial object references ────────────────────────────────────────────

    -- The invoice this event relates to. NULL for events not tied to a specific invoice.
    -- chk_fae_financial_anchor requires this to be non-NULL for settlement/balance events.
    invoice_id          UUID    REFERENCES invoices(id)       ON DELETE RESTRICT,

    -- The payout record this event relates to. NULL when not payout-specific.
    -- chk_fae_payout_anchor requires this to be non-NULL for payout lifecycle events.
    payout_record_id    UUID    REFERENCES payout_records(id) ON DELETE RESTRICT,

    -- For admin override / correction events: the original event being resolved.
    -- The original row is NEVER modified — the resolution is a new row.
    references_event_id BIGINT  REFERENCES financial_audit_events(id) ON DELETE RESTRICT,

    -- ── Fixed financial columns ────────────────────────────────────────────────
    -- These columns are typed and indexed, enabling reconciliation queries without
    -- JSONB extraction. Use these for all financial amounts and balance snapshots.

    -- Primary satoshi amount for this event (invoice amount, payout net, credit/debit, etc.).
    -- Required for financial event types (chk_fae_financial_amount_present).
    amount_sat          BIGINT,

    -- Vendor balance immediately BEFORE this event was applied.
    -- Required for balance-change events (chk_fae_balance_required_for_credit_debit).
    -- Both before and after must be present or both absent (chk_fae_balance_change_coherent).
    balance_before_sat  BIGINT,

    -- Vendor balance immediately AFTER this event was applied.
    -- Together with balance_before_sat, allows auditors to reconstruct balance history.
    balance_after_sat   BIGINT,

    -- Fiat equivalent of the amount at event time, in minor currency units (e.g. USD cents).
    fiat_equivalent     BIGINT,

    -- ISO 4217 currency code for fiat_equivalent. NULL when fiat_equivalent is NULL.
    fiat_currency_code  TEXT,

    -- TRUE when a subscription debit proceeds using a stale exchange rate.
    -- (Rate cache age > BTC_SUBSCRIPTION_DEBIT_MAX_RATE_AGE_SECONDS.)
    -- Triggers a WARNING alert. Rate diagnostics are in the metadata JSONB.
    rate_stale          BOOLEAN NOT NULL DEFAULT FALSE,

    -- ── JSONB metadata ─────────────────────────────────────────────────────────
    -- Event-type-specific structured data that does not fit the fixed columns.
    -- Must always be a JSON object (never array or scalar).
    --
    -- Example shapes by event_type:
    --   settlement_admin_close:          {action_type, reason, step_up_authenticated_at}
    --   reorg_resolved_platform_loss:    {action_type, reason, step_up_authenticated_at}
    --   subscription_debit_stale_rate:   {rate_age_seconds, rate_used, last_known_valid_rate}
    --   hybrid_auto_sweep_triggered:     {balance_before, balance_after, records_promoted}
    --
    -- IMPORTANT: Do NOT store source_ip here. Use sse_token_issuances table instead.
    -- This table is immutable — PII stored here cannot be erased for GDPR requests.
    metadata    JSONB           NOT NULL DEFAULT '{}',

    -- ── Constraints ───────────────────────────────────────────────────────────

    CONSTRAINT chk_fae_actor_type
        CHECK (actor_type IN ('system', 'vendor', 'buyer', 'admin')),
    CONSTRAINT chk_fae_metadata_is_object
        CHECK (jsonb_typeof(metadata) = 'object'),
    CONSTRAINT chk_fae_event_type_length
        CHECK (length(event_type) <= 128),

    -- SEC-03: network CHECK was missing in the original schema. All other tables validate
    -- network; the audit table must be consistent or network-scoped queries miss events.
    CONSTRAINT chk_fae_network
        CHECK (network IS NULL OR network IN ('mainnet', 'testnet4')),

    -- balance_before and balance_after must both be present or both absent.
    -- One side without the other makes before/after balance reconstruction impossible.
    CONSTRAINT chk_fae_balance_change_coherent
        CHECK ((balance_before_sat IS NULL) = (balance_after_sat IS NULL)),

    -- COMP-04: balance-change events must have both before and after populated.
    -- An audit event with only amount_sat but no before/after breaks balance reconstruction.
    CONSTRAINT chk_fae_balance_required_for_credit_debit
        CHECK (
            event_type NOT IN (
                'balance_credit', 'balance_debit', 'settlement_complete',
                'payout_confirmed', 'treasury_increment', 'treasury_decrement',
                'subscription_debit'
            )
            OR (balance_before_sat IS NOT NULL AND balance_after_sat IS NOT NULL)
        ),

    -- amount_sat must be present for known financial event types.
    -- A settlement_complete event with NULL amount silently corrupts reconciliation totals.
    CONSTRAINT chk_fae_financial_amount_present
        CHECK (
            event_type NOT IN (
                'settlement_complete', 'payout_confirmed', 'payout_broadcast',
                'balance_credit', 'balance_debit', 'subscription_debit',
                'refund_issued', 'treasury_increment', 'treasury_decrement'
            )
            OR amount_sat IS NOT NULL
        ),

    -- SEC-04/ARCH-09: financial events must be anchored to an invoice.
    -- An unanchored settlement_complete event is counted in reconciliation totals
    -- but cannot be traced to any real invoice — silent corruption.
    -- settlement_complete and balance_credit events require invoice_id.
    -- balance_debit events may be anchored by invoice_id (subscription path)
    --   OR payout_record_id (platform-mode withdrawal path — has no invoice).
    CONSTRAINT chk_fae_financial_anchor
        CHECK (
            event_type NOT IN ('settlement_complete', 'balance_credit')
            OR invoice_id IS NOT NULL
        ),
    CONSTRAINT chk_fae_balance_debit_anchored
        CHECK (
            event_type != 'balance_debit'
            OR (invoice_id IS NOT NULL OR payout_record_id IS NOT NULL)
        ),

    -- Payout lifecycle events must reference a payout_record.
    -- An unanchored payout_confirmed event cannot be reconciled to an on-chain TX.
    CONSTRAINT chk_fae_payout_anchor
        CHECK (
            event_type NOT IN (
                'payout_confirmed', 'payout_broadcast', 'payout_failed',
                'payout_created', 'treasury_increment'
            )
            OR payout_record_id IS NOT NULL
        ),

    -- Non-system actors must have a non-empty label.
    -- After actor_id goes NULL on user deletion, the label is the only identity record.
    CONSTRAINT chk_fae_actor_label_present
        CHECK (actor_type = 'system' OR length(actor_label) > 0)
);

-- BRIN replaces B-tree for timestamp on this append-only table. (IDX-07)
-- Rows are inserted in roughly monotonic timestamp order, so BRIN provides equivalent
-- range-query selectivity at a fraction of the storage cost of a B-tree index.
CREATE INDEX idx_fae_timestamp_brin ON financial_audit_events
    USING BRIN (timestamp) WITH (pages_per_range = 128);

-- BRIN on id for sequential access patterns (e.g. "all events after event id X").
CREATE INDEX idx_fae_id_brin ON financial_audit_events
    USING BRIN (id) WITH (pages_per_range = 128);

-- Network + timestamp: reconciliation queries and compliance reports filtered by network. (IDX-09)
-- Without this, every network-scoped query is a full table scan.
CREATE INDEX idx_fae_network_timestamp
    ON financial_audit_events(network, timestamp DESC)
    WHERE network IS NOT NULL;

-- Per-invoice audit trail: "show all events for invoice X."
CREATE INDEX idx_fae_invoice ON financial_audit_events(invoice_id, timestamp DESC)
    WHERE invoice_id IS NOT NULL;

-- Per-payout audit trail: "show all events for payout record X."
CREATE INDEX idx_fae_payout ON financial_audit_events(payout_record_id, timestamp DESC)
    WHERE payout_record_id IS NOT NULL;

-- Per-actor audit trail: "show all events triggered by user X." (admin investigation)
CREATE INDEX idx_fae_actor ON financial_audit_events(actor_id, timestamp DESC)
    WHERE actor_id IS NOT NULL;

-- Event-type filter: "show all settlement_failed events in the last week."
CREATE INDEX idx_fae_event_type ON financial_audit_events(event_type, timestamp DESC);

-- Resolution chain: "which event resolved event X?" (admin override chain navigation)
CREATE INDEX idx_fae_references ON financial_audit_events(references_event_id)
    WHERE references_event_id IS NOT NULL;

COMMENT ON TABLE financial_audit_events IS
    'Immutable append-only financial audit trail. Retained indefinitely. '
    'Immutability layer 1: btc_app_role has INSERT+SELECT only. '
    'Immutability layer 2: fn_btc_audit_immutable + fn_btc_audit_no_truncate (011) block all mutations. '
    'Admin corrections: new rows with references_event_id — original rows never modified. '
    'GDPR: do NOT store source_ip here — use sse_token_issuances table. '
    'Reconciliation: balance_before/after columns enable full balance history reconstruction.';
COMMENT ON COLUMN financial_audit_events.actor_label IS
    'Identity snapshot at INSERT time. '
    'fn_fae_validate_actor_label (011) verifies this matches users.email or username for actor_id. '
    'Preserved permanently after user deletion (actor_id becomes NULL). '
    'GDPR: consider HMAC-SHA256(email, server_secret) instead of raw email. See COMP-02 in todo.md.';
COMMENT ON COLUMN financial_audit_events.network IS
    'NULL for system events not network-specific. '
    'When set, must be mainnet or testnet4 (chk_fae_network).';
COMMENT ON COLUMN financial_audit_events.metadata IS
    'Event-type-specific JSONB. Always a JSON object (chk_fae_metadata_is_object). '
    'IMPORTANT: do NOT store source_ip — use sse_token_issuances (SEC-07 decision).';
COMMENT ON COLUMN financial_audit_events.rate_stale IS
    'TRUE when a subscription debit uses a stale exchange rate. '
    'Triggers WARNING alert. Rate diagnostics in metadata.';


/* ═════════════════════════════════════════════════════════════
   WALLET BACKUP SUCCESS
   ═════════════════════════════════════════════════════════════ */

/*
 * Tracks successful wallet.dat backup completions. A record is written ONLY after:
 *   1. The backupwallet RPC returns success.
 *   2. The backup file is COPIED to backup storage.
 *   3. The copy is VERIFIED (checksum matches sha256_checksum).
 *
 * The "Wallet backup overdue" CRITICAL alert monitors timestamp, not the job's
 * scheduled run time. A job that runs but whose copy or verification step silently
 * fails will NOT write a row here, so the alert will fire correctly.
 *
 * Alert thresholds: > 4 hours stale on mainnet, > 24 hours stale on testnet4.
 */
CREATE TABLE wallet_backup_success (
    id               BIGSERIAL   PRIMARY KEY,

    -- 'mainnet' or 'testnet4'. Alert thresholds differ per network.
    network          TEXT        NOT NULL,

    -- Timestamp of the completed backup (all three steps done: RPC + copy + verify).
    -- This is the basis for the overdue alert — NOT the job run timestamp.
    timestamp        TIMESTAMPTZ NOT NULL,

    -- Filename of the backup file in storage (e.g. 'wallet-mainnet-2024-03-05T14:23:00Z.dat').
    filename         TEXT        NOT NULL,

    -- SHA-256 checksum of the backup file verified after copy.
    -- Used for integrity verification on restore.
    sha256_checksum  TEXT        NOT NULL,

    -- Where the backup was copied (e.g. 's3://bucket/backups/mainnet/wallet-2024-03-05.dat').
    storage_location TEXT        NOT NULL,

    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_wbs_network
        CHECK (network IN ('mainnet', 'testnet4'))
);

-- Hot path: "what is the latest backup timestamp for network X?" — alert check.
CREATE INDEX idx_wbs_latest
    ON wallet_backup_success(network, timestamp DESC);

COMMENT ON TABLE wallet_backup_success IS
    'Wallet backup completion records. Written only after RPC + copy + verify all succeed. '
    '"Wallet backup overdue" CRITICAL alert fires when latest timestamp is stale '
    '(> 4h mainnet, > 24h testnet4). A failed copy step means no row is written here.';
COMMENT ON COLUMN wallet_backup_success.timestamp IS
    'Time the completed backup (all three steps). This is the alert basis — NOT job start time. '
    'A job that runs but fails copy/verify does not write here, ensuring the alert fires.';


/* ═════════════════════════════════════════════════════════════
   OPS AUDIT LOG
   ═════════════════════════════════════════════════════════════ */

/*
 * Non-financial administrative operation audit trail.
 *
 * This table captures operational changes that are NOT financial events but are
 * still material for SOC 2 compliance, incident response, and insider threat detection:
 *   — btc_tier_config fee rate changes (who changed the fee and when)
 *   — platform_config sweep_hold_mode toggles (who activated/cleared the brake)
 *   — platform_config legal flag changes (platform_wallet_mode_legal_approved)
 *   — vendor_wallet_config suspension changes
 *   — Tier-role sync events (automatic role assignment on tier change)
 *
 * Complements financial_audit_events (which covers financial flows only).
 * Written by triggers defined in 011_btc_functions.sql.
 *
 * For privileged operations (sweep_hold_mode clear, legal flag toggle), the calling
 * admin must provide a step_up_authenticated_at timestamp from their step-up auth
 * record. The application sets this before calling the DB operation.
 *
 * Retention: ops_audit_log rows may be pruned after the compliance retention window
 * (typically 7 years). Unlike financial_audit_events, these rows CAN be deleted.
 */
CREATE TABLE ops_audit_log (
    id          BIGSERIAL       PRIMARY KEY,

    -- When the operation was committed.
    timestamp   TIMESTAMPTZ     NOT NULL DEFAULT NOW(),

    -- Actor who performed the operation. SET NULL if account is later deleted.
    actor_id    UUID            REFERENCES users(id) ON DELETE SET NULL,

    -- Stable identity snapshot. Populated from app.current_actor_label session variable.
    -- 'system' for trigger-generated entries (e.g. tier-role sync).
    actor_label TEXT            NOT NULL DEFAULT '',

    -- Machine-readable operation identifier.
    -- Examples: 'tier_update', 'vendor_suspend', 'sweep_hold_set', 'sweep_hold_cleared',
    --           'legal_flag_toggled', 'tier_role_sync', 'platform_config_update'.
    operation   TEXT            NOT NULL,

    -- Which table was modified.
    table_name  TEXT            NOT NULL,

    -- PK value of the modified row, stored as TEXT.
    -- For UUID PKs: the UUID string. For composite PKs: JSON representation.
    record_id   TEXT            NOT NULL,

    -- Full JSON snapshot of the row BEFORE the change. NULL for INSERT operations.
    old_values  JSONB,

    -- Full JSON snapshot of the row AFTER the change. NULL for DELETE operations.
    new_values  JSONB,

    -- Human-readable justification for the operation. Optional for automated changes,
    -- required for privileged manual operations (enforced at application layer).
    reason      TEXT,

    -- Timestamp of the admin's step-up re-authentication for privileged operations.
    -- NULL for routine operations. Non-NULL for: sweep_hold clear, legal flag toggle.
    step_up_authenticated_at TIMESTAMPTZ,

    -- Both old_values and new_values must be JSON objects when present.
    CONSTRAINT chk_oal_metadata_shapes
        CHECK (
            (old_values IS NULL OR jsonb_typeof(old_values) = 'object')
            AND (new_values IS NULL OR jsonb_typeof(new_values) = 'object')
        )
);

-- Time-range query: "what operations happened in the last 24 hours?"
CREATE INDEX idx_oal_timestamp ON ops_audit_log(timestamp DESC);

-- Per-actor query: "what did admin X do?" — investigation and access review.
-- Partial excludes SET NULL rows (deleted users) from actor-scoped queries.
CREATE INDEX idx_oal_actor ON ops_audit_log(actor_id, timestamp DESC)
    WHERE actor_id IS NOT NULL;

-- Operation filter: "show all sweep_hold_set events this month."
CREATE INDEX idx_oal_operation ON ops_audit_log(operation, timestamp DESC);

-- Record history: "show all changes to btc_tier_config row X."
CREATE INDEX idx_oal_table_record ON ops_audit_log(table_name, record_id, timestamp DESC);

COMMENT ON TABLE ops_audit_log IS
    'Non-financial admin operation audit trail. Covers: tier fee changes, vendor suspensions, '
    'sweep_hold_mode toggles, legal flag changes, tier-role syncs. '
    'Complements financial_audit_events (which covers financial flows only). '
    'Rows CAN be deleted after the compliance retention window (unlike financial_audit_events). '
    'Populated by triggers in 011_btc_functions.sql.';
COMMENT ON COLUMN ops_audit_log.step_up_authenticated_at IS
    'Non-NULL for privileged operations requiring step-up re-authentication. '
    'Mandatory for: sweep_hold clear, legal flag toggle. Enforced at application layer.';



-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS ops_audit_log          CASCADE;
DROP TABLE IF EXISTS wallet_backup_success  CASCADE;
DROP TABLE IF EXISTS financial_audit_events CASCADE;

-- +goose StatementEnd
