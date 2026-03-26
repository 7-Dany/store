-- +goose Up
-- +goose StatementBegin

/*
 * 023_btc_history.sql — Audit support and infrastructure history tables.
 *
 * Tables defined here:
 *   btc_tier_config_history        — immutable log of every btc_tier_config row change
 *   vendor_wallet_config_history   — field-level history of key vendor_wallet_config changes
 *   reconciliation_run_history     — full history of every reconciliation run result
 *   sse_token_issuances            — SSE token issuance audit log
 *   btc_zmq_dead_letter            — ZMQ events that could not be matched to an active invoice
 *     deletable. SET NULL allows deletion while preserving the jti_hash audit trail.
 *     Consistent with financial_audit_events.actor_id (also SET NULL).
 *
 * Depends on: 010_btc_core.sql (btc_tier_config FK, vendor_wallet_config FK)
 *             015_btc_payouts.sql (payout_records FK in reconciliation_run_history)
 * Continued in: 024_btc_history_functions.sql
 */

/* ═════════════════════════════════════════════════════════════
   TIER CONFIG HISTORY
   ═════════════════════════════════════════════════════════════ */

/*
 * Immutable history of every btc_tier_config row change.
 * Populated by the fn_tier_config_history AFTER UPDATE trigger (011_btc_functions.sql).
 *
 * Without this table, a fee rate change in btc_tier_config permanently overwrites the
 * previous value. The invoice snapshot captures what rate applied to each individual
 * invoice, but "what was the fee schedule for the pro tier during Q1 2024?" is
 * unanswerable without this history table.
 *
 * Rows in this table are never deleted or modified — they form an audit trail that
 * grows only by appending. old_values and new_values are full JSONB snapshots of the
 * entire btc_tier_config row before and after each change.
 */
CREATE TABLE btc_tier_config_history (
    id               BIGSERIAL   PRIMARY KEY,

    -- The tier that was changed. RESTRICT: tier cannot be deleted while history exists.
    tier_id          UUID        NOT NULL REFERENCES btc_tier_config(id) ON DELETE RESTRICT,

    -- When the change was committed.
    changed_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Admin who made the change. SET NULL if their account is later deleted.
    -- changed_by_label preserves the identity even after SET NULL.
    changed_by       UUID        REFERENCES users(id) ON DELETE SET NULL,

    -- Stable snapshot of the changer's identity. Populated from app.current_actor_label
    -- session variable by fn_tier_config_history (011). 'system' for automated changes.
    changed_by_label TEXT        NOT NULL DEFAULT '',

    -- Full JSON snapshot of the btc_tier_config row before the change.
    -- NULL would only occur if the trigger fired on INSERT (it doesn't — AFTER UPDATE only).
    old_values       JSONB       NOT NULL,

    -- Full JSON snapshot of the btc_tier_config row after the change.
    new_values       JSONB       NOT NULL
);

-- Tier change timeline: "show all changes to tier X in chronological order."
CREATE INDEX idx_tch_tier_time ON btc_tier_config_history(tier_id, changed_at DESC);

COMMENT ON TABLE btc_tier_config_history IS
    'Immutable history of btc_tier_config changes. '
    'Populated by fn_tier_config_history AFTER UPDATE trigger (011). '
    'Required for SOC 2 audit: "what were the fee rates for tier X during period Y?"';
COMMENT ON COLUMN btc_tier_config_history.changed_by_label IS
    'Stable identity snapshot from app.current_actor_label session variable. '
    'Preserved after changed_by goes NULL on user deletion.';


/* ═════════════════════════════════════════════════════════════
   VENDOR WALLET CONFIG HISTORY
   ═════════════════════════════════════════════════════════════ */

/*
 * Field-level history of key vendor_wallet_config changes.
 * Populated by the fn_vwc_history AFTER UPDATE trigger (011_btc_functions.sql).
 *
 * Tracks individual field changes rather than full row snapshots. This enables
 * targeted queries like "show all address changes for vendor X" without parsing JSON.
 *
 * Fields tracked: bridge_destination_address, wallet_mode, tier_id, suspended, kyc_status.
 *
 * Without this table, the previous destination address is permanently lost on update.
 * A compliance review asking "what address was vendor X using during March 2024?" would
 * be unanswerable. Historical payout routing cannot be reconstructed from payout_records
 * alone because those records only show the address at payout creation time.
 *
 * One row per changed field per UPDATE. An UPDATE that changes three fields generates
 * three rows in this table.
 */
CREATE TABLE vendor_wallet_config_history (
    id          BIGSERIAL   PRIMARY KEY,

    -- The vendor whose config changed. RESTRICT: vendor cannot be deleted while history exists.
    vendor_id   UUID        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,

    -- 'mainnet' or 'testnet4'.
    network     TEXT        NOT NULL,

    -- When the change was committed.
    changed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Admin or system that made the change. SET NULL if account is later deleted.
    changed_by  UUID        REFERENCES users(id) ON DELETE SET NULL,

    -- Which field changed. Examples: 'bridge_destination_address', 'wallet_mode',
    -- 'tier_id', 'suspended', 'kyc_status'.
    field_name  TEXT        NOT NULL,

    -- Value before the change. NULL if the field was previously NULL.
    -- All values stored as TEXT regardless of original type (ENUM, BOOLEAN, UUID, etc.).
    old_value   TEXT,

    -- Value after the change. NULL if the field was set to NULL.
    new_value   TEXT,

    CONSTRAINT chk_vwch_network
        CHECK (network IN ('mainnet', 'testnet4'))
);

-- Per-vendor field history: "show all config changes for vendor X on mainnet."
CREATE INDEX idx_vwch_vendor ON vendor_wallet_config_history(vendor_id, network, changed_at DESC);

COMMENT ON TABLE vendor_wallet_config_history IS
    'Field-level history of vendor wallet config changes. '
    'One row per changed field per UPDATE. '
    'Populated by fn_vwc_history AFTER UPDATE trigger (011). '
    'Required for compliance: "what address/mode/tier did vendor X have during period Y?"';
COMMENT ON COLUMN vendor_wallet_config_history.field_name IS
    'Which field changed. Tracked fields: bridge_destination_address, wallet_mode, '
    'tier_id, suspended, kyc_status.';


/* ═════════════════════════════════════════════════════════════
   RECONCILIATION RUN HISTORY
   ═════════════════════════════════════════════════════════════ */

/*
 * Full history of every reconciliation run.
 *
 * reconciliation_job_state stores only the latest run per network, making it
 * impossible to answer trend questions. This table fills that gap:
 *   "Has there been a discrepancy in the last 30 days?"
 *   "How long did it take to resolve the discrepancy on March 5?"
 *   "What is the average reconciliation run time over the last quarter?"
 *
 * The application writes a row here at the end of every run (success or failure),
 * then separately updates reconciliation_job_state.last_successful_run_at only on success.
 */
CREATE TABLE reconciliation_run_history (
    id          BIGSERIAL                   PRIMARY KEY,

    -- 'mainnet' or 'testnet4'.
    network     TEXT                        NOT NULL,

    -- When the reconciliation run started.
    started_at  TIMESTAMPTZ                 NOT NULL,

    -- When the run completed. NULL if the run is still in progress or crashed mid-run.
    finished_at TIMESTAMPTZ,

    -- Outcome: ok, discrepancy, or error. NULL if run is still in progress.
    result      btc_reconciliation_result,

    -- Satoshi discrepancy when result = 'discrepancy'. Sign convention:
    --   positive = more on-chain than expected (unexpected funds)
    --   negative = less on-chain than expected (missing funds — critical)
    -- Required when result = 'discrepancy' (chk_rrh_discrepancy_coherent).
    discrepancy_sat BIGINT,

    -- Free-text notes about the run outcome. Used for anomaly documentation.
    note        TEXT,

    CONSTRAINT chk_rrh_network
        CHECK (network IN ('mainnet', 'testnet4')),
    CONSTRAINT chk_rrh_discrepancy_coherent
        CHECK (result != 'discrepancy' OR discrepancy_sat IS NOT NULL)
);

-- Timeline query: "show all reconciliation runs for mainnet in the last 30 days."
CREATE INDEX idx_rrh_network_time ON reconciliation_run_history(network, started_at DESC);

COMMENT ON TABLE reconciliation_run_history IS
    'Full history of reconciliation runs. '
    'reconciliation_job_state holds only the latest per network; this table enables '
    'trend analysis: "discrepancy in last 30 days?", "average run time?", etc.';


/* ═════════════════════════════════════════════════════════════
   SSE TOKEN ISSUANCES
   ═════════════════════════════════════════════════════════════ */

/*
 * Records SSE (Server-Sent Events) token issuances with a pseudonymised IP hash.
 *
 * WHY THIS TABLE EXISTS (SEC-07):
 *   The original design stored source_ip in financial_audit_events.metadata. However,
 *   financial_audit_events is immutable (DELETE blocked by trigger) — any PII stored there
 *   cannot be erased for GDPR Article 17 requests. This table is the correct home for
 *   SSE token issuance data because it supports erasure.
 *
 * PSEUDONYMISATION:
 *   source_ip_hash = SHA256(ip || daily_rotation_key)
 *   The rotation key changes every 24 hours and is deleted after rotation.
 *   After key rotation the hash is non-reversible — the original IP cannot be recovered.
 *   This satisfies GDPR pseudonymisation requirements while still allowing
 *   correlation of events within the same 24-hour window.
 *
 *   jti_hash = HMAC-SHA256(jti, server_secret)
 *   Non-reversible without the server secret. Used for cross-event correlation
 *   (e.g. "which events used this token?") without exposing the raw token.
 *
 * ERASURE:
 *   On GDPR erasure request: UPDATE SET erased = TRUE, source_ip_hash = NULL.
 *   chk_sti_erased_coherent ensures source_ip_hash is NULL when erased = TRUE.
 *
 * RETENTION:
 *   Rows may be pruned once expires_at has passed AND erased = TRUE (or erased = FALSE
 *   and the retention window has elapsed). The exact retention policy is in
 *   data_retention_policies (future table — see COMP-05 in todo.md).
 */
CREATE TABLE sse_token_issuances (
    id              BIGSERIAL   PRIMARY KEY,

    -- The vendor who requested the SSE token.
    -- RESTRICT: vendor cannot be deleted while issuance records exist.
    vendor_id       UUID        NOT NULL REFERENCES users(id) ON DELETE SET NULL,

    -- 'mainnet' or 'testnet4'. The SSE stream is network-specific.
    network         TEXT        NOT NULL,

    -- HMAC-SHA256(jti, server_secret). Non-reversible. For cross-event correlation.
    jti_hash        TEXT        NOT NULL,

    -- SHA256(source_ip || daily_rotation_key). Non-reversible after key rotation.
    -- NULL after GDPR erasure (chk_sti_erased_coherent).
    source_ip_hash  TEXT,

    -- When the SSE token expires. Tokens are single-use; this is the validity window.
    expires_at      TIMESTAMPTZ NOT NULL,

    -- When this row was created (token was issued).
    issued_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- TRUE after GDPR erasure request is processed. source_ip_hash must be NULL.
    erased          BOOLEAN     NOT NULL DEFAULT FALSE,

    CONSTRAINT chk_sti_network
        CHECK (network IN ('mainnet', 'testnet4')),
    -- Coherence: source_ip_hash must be nullified when erased = TRUE.
    -- Prevents a partially-erased row where erased = TRUE but IP hash remains readable.
    CONSTRAINT chk_sti_erased_coherent
        CHECK (erased = FALSE OR source_ip_hash IS NULL)
);

-- Per-vendor issuance history: "show all SSE tokens issued to vendor X."
CREATE INDEX idx_sti_vendor ON sse_token_issuances(vendor_id, issued_at DESC);

-- Expiry cleanup: find rows whose token has expired and which can be pruned.
-- Partial excludes already-erased rows (they may still be within retention window).
CREATE INDEX idx_sti_expiry ON sse_token_issuances(expires_at)
    WHERE erased = FALSE;

-- jti_hash is semantically unique: one row per issued token. The UNIQUE index here
-- catches any accidental duplicate issuances (e.g. a retry path issuing the same JTI
-- twice) at the DB level rather than silently creating duplicate records.
CREATE UNIQUE INDEX uq_sti_jti_hash ON sse_token_issuances(jti_hash);

COMMENT ON TABLE sse_token_issuances IS
    'SSE token issuance records with pseudonymised IP hash. '
    'Replaces source_ip in financial_audit_events.metadata (SEC-07 decision). '
    'jti_hash = HMAC-SHA256(jti, server_secret) — non-reversible. '
    'source_ip_hash = SHA256(ip || daily_rotation_key) — non-reversible after key rotation. '
    'GDPR erasure: SET erased=TRUE, source_ip_hash=NULL.';


/* ═════════════════════════════════════════════════════════════
   BTC ZMQ DEAD LETTER
   ═════════════════════════════════════════════════════════════ */

/*
 * Records ZMQ events that could not be matched to an active monitoring record.
 *
 * Without this table, unmatched ZMQ events are silently dropped:
 *   - Late payments on expired invoices
 *   - Payments for cancelled invoices within the 30-day monitoring window
 *   - Double-spend attempt transactions
 *   - Events for retired addresses (monitoring window elapsed)
 *   - Events for unknown txids (outside any invoice's window)
 *
 * These events are not necessarily errors — a late payment on an expired invoice is
 * expected and legitimate. But they require admin review to determine the correct action.
 *
 * A periodic job reviews unresolved rows (resolved = FALSE) and either:
 *   - Creates the appropriate refund or payment record (late payment case)
 *   - Flags for admin review (double-spend case)
 *   - Marks as noise and resolves (unrelated transaction)
 */
CREATE TABLE btc_zmq_dead_letter (
    id              BIGSERIAL   PRIMARY KEY,

    -- 'mainnet' or 'testnet4'.
    network         TEXT        NOT NULL,

    -- ZMQ event type that was received. 'hashtx' (mempool/confirmed TX) or 'hashblock'.
    event_type      TEXT        NOT NULL,

    -- The raw event payload: the txid (for hashtx) or block hash (for hashblock).
    raw_payload     TEXT        NOT NULL,

    -- Why this event could not be processed. Examples:
    --   'no_monitoring_record'      — address not in invoice_address_monitoring
    --   'retired_invoice'           — monitoring window has elapsed
    --   'unknown_txid'              — txid not associated with any known address
    --   'invoice_terminal_no_window' — invoice is terminal with no monitoring window set
    reason          TEXT        NOT NULL,

    -- When this event was received by the ZMQ subscriber.
    received_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- FALSE = awaiting review. TRUE = investigated and resolved.
    resolved        BOOLEAN     NOT NULL DEFAULT FALSE,

    -- When the event was resolved. Required when resolved = TRUE.
    resolved_at     TIMESTAMPTZ,

    -- How the event was resolved. Examples:
    --   'late_payment_refund_created', 'noise_no_action', 'admin_reviewed'
    resolution_note TEXT,

    CONSTRAINT chk_zdl_network
        CHECK (network IN ('mainnet', 'testnet4')),
    -- Coherence: resolved_at required when resolved = TRUE.
    -- Without a timestamp, the resolution timeline cannot be reconstructed.
    CONSTRAINT chk_zdl_resolved_coherent
        CHECK (resolved = FALSE OR resolved_at IS NOT NULL)
);

-- Unresolved dead letter review: "show all unresolved events for mainnet by recency."
-- Partial index keeps this lean — most rows will eventually be resolved.
CREATE INDEX idx_zdl_unresolved ON btc_zmq_dead_letter(network, received_at DESC)
    WHERE resolved = FALSE;

COMMENT ON TABLE btc_zmq_dead_letter IS
    'ZMQ events that could not be matched to an active monitoring record. '
    'Captures: late payments, double-spend attempts, retired addresses, unknown txids. '
    'Periodic review of resolved=FALSE rows detects missed payments and anomalies. '
    'Not all dead letters are errors — late payments on expired invoices are expected.';



-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS btc_zmq_dead_letter            CASCADE;
DROP TABLE IF EXISTS sse_token_issuances            CASCADE;
DROP TABLE IF EXISTS reconciliation_run_history     CASCADE;
DROP TABLE IF EXISTS vendor_wallet_config_history   CASCADE;
DROP TABLE IF EXISTS btc_tier_config_history        CASCADE;

-- +goose StatementEnd
