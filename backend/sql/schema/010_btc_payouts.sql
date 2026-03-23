-- +goose Up
-- +goose StatementBegin

/*
 * 010_btc_payouts.sql — Bitcoin payout pipeline, financial audit trail, and compliance tables.
 *
 * This file covers everything that happens AFTER an invoice settles: the payout lifecycle,
 * the immutable financial audit trail, and all the supporting tables required for compliance,
 * operational visibility, and regulatory obligations.
 *
 * Tables defined here (in dependency order):
 *   payout_records               — vendor payout lifecycle: held → queued → constructing → broadcast → confirmed
 *   financial_audit_events       — immutable append-only financial audit trail
 *   wallet_backup_success        — wallet.dat backup health tracking
 *   btc_tier_config_history      — immutable history of every btc_tier_config change
 *   vendor_wallet_config_history — field-level history of vendor wallet config changes
 *   ops_audit_log                — non-financial admin operation audit trail
 *   reconciliation_run_history   — full history of all reconciliation runs
 *   kyc_submissions              — KYC/AML submission lifecycle records
 *   sse_token_issuances          — SSE token issuances with erasable pseudonymised IP
 *   webhook_deliveries           — outbox for vendor state-change notifications
 *   btc_zmq_dead_letter          — ZMQ events that could not be matched to an active invoice
 *   dispute_records              — buyer/vendor payment disputes
 *   gdpr_erasure_requests        — GDPR Article 17 erasure request tracking
 *   fatf_travel_rule_records     — FATF Travel Rule compliance for high-value payouts
 *
 * Depends on:
 *   001_core.sql        — users table (UUID PK)
 *   009_btc.sql         — invoices, btc_tier_config, vendor_wallet_config, all ENUMs
 *
 * Functions, triggers, grants, and autovacuum settings are in 011_btc_functions.sql.
 */


/* ═════════════════════════════════════════════════════════════
   PAYOUT RECORDS
   ═════════════════════════════════════════════════════════════ */

/*
 * Vendor payout lifecycle from settlement credit to on-chain confirmation.
 * One record per settled invoice. Multiple records may share a batch_txid when
 * multiple vendors are swept in a single batched transaction.
 *
 * TRIGGER GUARDS (all defined in 011_btc_functions.sql):
 *   fn_btc_payout_guard         — BEFORE INSERT: rejects if invoice.status != 'settled'.
 *                                 Uses SELECT FOR SHARE to close the TOCTOU window between
 *                                 concurrent settlement workers.
 *   fn_pr_vendor_consistency    — BEFORE INSERT: rejects if vendor_id != invoice.vendor_id.
 *                                 Prevents funds being swept to the wrong vendor.
 *   fn_pr_destination_consistency — BEFORE INSERT: rejects if destination_address doesn't
 *                                 match invoice.bridge_destination_address (the frozen snapshot).
 *                                 Prevents sweeping to the wrong address due to a code bug.
 *   fn_pr_status_guard          — BEFORE UPDATE OF status: enforces the transition matrix.
 *                                 Terminal states cannot be exited. Confirmed payouts cannot
 *                                 regress to queued (double-sweep risk).
 *
 * DOUBLE-PAYOUT GUARD:
 *   UNIQUE (invoice_id) is the race-safe DB-level guard.
 *   Two concurrent settlement workers can both pass fn_btc_payout_guard (they both see
 *   status='settled' before either INSERT commits). The UNIQUE constraint ensures only
 *   one INSERT succeeds — the second gets a 23505 unique violation and rolls back.
 *
 * BROADCAST ORDERING INVARIANT (settlement-technical.md §1):
 *   The constructing → broadcast UPDATE (which sets batch_txid) MUST commit and assert
 *   RowsAffected > 0 BEFORE sendrawtransaction is called.
 *   If RowsAffected = 0, the watchdog reclaimed the record — abort the broadcast.
 *   Never reverse this ordering: broadcasting before DB commit means a crash between
 *   the two operations leaves the TX on-chain with no DB record.
 *
 * FEE BREAKDOWN:
 *   The fee_breakdown JSONB column records the exact computation that produced
 *   net_satoshis. This allows vendors to verify their payout and auditors to
 *   reconstruct the fee calculation for any historical payout.
 */
CREATE TABLE payout_records (
    id          UUID              PRIMARY KEY DEFAULT uuidv7(),

    -- Parent invoice. RESTRICT: payout cannot exist without a settled invoice.
    -- UNIQUE (invoice_id) enforces one payout per invoice at the DB level.
    invoice_id  UUID              NOT NULL REFERENCES invoices(id) ON DELETE RESTRICT,

    -- Vendor receiving this payout. fn_pr_vendor_consistency (011) verifies this
    -- matches invoice.vendor_id at INSERT time.
    vendor_id   UUID              NOT NULL REFERENCES users(id) ON DELETE RESTRICT,

    -- 'mainnet' or 'testnet4'. Copied from the parent invoice.
    network     TEXT              NOT NULL,

    -- Current lifecycle state. fn_pr_status_guard (011) enforces the transition matrix.
    -- Terminal states: confirmed, refunded, manual_payout.
    status      btc_payout_status NOT NULL DEFAULT 'held',

    -- ── Amounts ────────────────────────────────────────────────────────────────

    -- Net satoshis owed to the vendor: received_amount - platform_fee - miner_fee_share.
    -- Always positive. See fee_breakdown for the exact computation.
    net_satoshis            BIGINT          NOT NULL,

    -- Platform processing fee deducted at settlement.
    -- Tracked separately from miner_fee_satoshis for tier profitability analysis.
    platform_fee_satoshis   BIGINT          NOT NULL DEFAULT 0,

    -- Wallet mode snapshotted from the parent invoice.
    -- Required for destination_address coherence constraints and audit.
    wallet_mode             btc_wallet_mode NOT NULL,

    -- Sweep destination. Copied from invoice.bridge_destination_address at creation.
    -- fn_pr_destination_consistency (011) verifies this matches the invoice snapshot.
    -- NULL for platform wallet mode (no on-chain sweep needed — value stays internal).
    destination_address     TEXT,

    -- ── Sweep batch fields ─────────────────────────────────────────────────────

    -- UUID grouping all payout records swept together in the same Bitcoin TX.
    -- NULL until this record enters constructing status.
    batch_id                UUID,

    -- Bitcoin txid of the sweep transaction.
    -- Set at constructing → broadcast UPDATE — BEFORE sendrawtransaction is called.
    -- NULL until broadcast. If non-NULL but status = constructing, the watchdog fires.
    batch_txid              TEXT,

    -- This vendor's vout index within the sweep TX. Set at constructing.
    -- NULL until constructing. Non-negative when set.
    vout_index_in_batch     INTEGER,

    -- Fee rate used when constructing this batch. In satoshis per virtual byte.
    -- Positive when set. NULL until constructing.
    fee_rate_sat_vbyte      NUMERIC(10,4),

    -- Estimated miner fee attributable to this vendor's output in the sweep TX.
    -- Derived from (total_TX_fee / number_of_outputs) — approximate, not exact.
    -- NULL until constructing. Non-negative when set.
    miner_fee_satoshis      BIGINT,

    -- Original txid before Replace-By-Fee (RBF) was applied.
    -- Populated when a fee-bump replacement TX is constructed.
    -- Both original_txid and batch_txid are preserved so the audit trail shows
    -- the full replacement history.
    original_txid           TEXT,

    -- ── Fee breakdown ──────────────────────────────────────────────────────────
    -- Structured record of exactly how net_satoshis was computed. Populated at settlement.
    -- Allows vendors to verify their payout and auditors to reconstruct fee calculations.
    -- Expected shape:
    --   {
    --     "received_sat":         <BIGINT>,    -- total satoshis received by the invoice address
    --     "tolerance_adj_sat":    <BIGINT>,    -- adjustment for over/underpayment within tolerance
    --     "processing_fee_sat":   <BIGINT>,    -- platform fee deducted
    --     "miner_fee_sat":        <BIGINT>,    -- estimated miner fee share
    --     "net_sat":              <BIGINT>,    -- must equal net_satoshis column
    --     "fee_rate_pct":         <STRING>,    -- e.g. "2.50"
    --     "batch_size_used":      <INT>,       -- actual batch size used in fee floor calc
    --     "fee_estimate_source":  <STRING>     -- e.g. "estimatesmartfee"
    --   }
    fee_breakdown           JSONB,

    -- ── Compliance ────────────────────────────────────────────────────────────

    -- KYC/AML state at payout time. Copied from vendor_wallet_config.kyc_status
    -- at creation. High-value payouts may be held until KYC is approved.
    kyc_status  btc_kyc_status  NOT NULL DEFAULT 'not_required',

    -- ── Admin resolution fields ────────────────────────────────────────────────

    -- Required for manual_payout and admin-forced transitions (held → failed, etc.).
    -- Records why the normal payout lifecycle was bypassed.
    resolution_reason       TEXT,

    -- Admin who performed the resolution. SET NULL if that admin's account is later deleted.
    resolution_admin_id     UUID REFERENCES users(id) ON DELETE SET NULL,

    -- ── Timestamps ────────────────────────────────────────────────────────────

    -- Set when status transitions to broadcast (same UPDATE that sets batch_txid).
    broadcast_at    TIMESTAMPTZ,

    -- Set when status transitions to confirmed (same UPDATE that increments
    -- platform_config.treasury_reserve_satoshis).
    confirmed_at    TIMESTAMPTZ,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

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

    -- DOUBLE-PAYOUT GUARD: the DB-level race-safe enforcement.
    -- fn_btc_payout_guard catches most cases but two concurrent workers can both pass
    -- the trigger check. This unique constraint ensures only one INSERT commits.
    CONSTRAINT uq_pr_invoice_id UNIQUE (invoice_id),

    -- Destination address coherence mirrors the invoice snapshot invariants.
    -- A bridge vendor with NULL destination_address would have the sweep silently skipped.
    CONSTRAINT chk_pr_destination_coherent
        CHECK (wallet_mode = 'platform' OR destination_address IS NOT NULL),
    CONSTRAINT chk_pr_platform_no_destination
        CHECK (wallet_mode != 'platform' OR destination_address IS NULL),

    CONSTRAINT chk_pr_fee_rate_positive
        CHECK (fee_rate_sat_vbyte IS NULL OR fee_rate_sat_vbyte > 0),

    -- Net must exceed the miner fee share. If miner_fee >= net, the vendor receives nothing
    -- or goes negative, which is a settlement configuration error that should fail loudly.
    CONSTRAINT chk_pr_net_exceeds_miner_fee
        CHECK (miner_fee_satoshis IS NULL OR net_satoshis > miner_fee_satoshis),

    -- fee_breakdown must be a JSON object when present.
    CONSTRAINT chk_pr_fee_breakdown_valid
        CHECK (fee_breakdown IS NULL OR jsonb_typeof(fee_breakdown) = 'object')
);

CREATE TRIGGER trg_payout_records_updated_at
    BEFORE UPDATE ON payout_records
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- NOTE: idx_pr_invoice_id has been intentionally removed. UNIQUE (invoice_id) creates
-- an implicit unique index on invoice_id. The planner always prefers the unique index
-- for equality lookups. A separate non-unique index is pure write overhead. (IDX-03)

-- Vendor payout list: admin dashboard, suspension check, deletion guard.
-- Covers "show payouts for vendor X ordered by recency with status filter."
CREATE INDEX idx_pr_vendor_status
    ON payout_records(vendor_id, status, created_at DESC);

-- Sweep job: claim all queued records for a vendor on a specific network.
-- Query: WHERE network = $n AND vendor_id = $v AND status = 'queued'
CREATE INDEX idx_pr_queued_vendor
    ON payout_records(network, vendor_id)
    WHERE status = 'queued';

-- Approval workflow: find queued records at or above the approval threshold.
-- Query: WHERE network = $n AND status = 'queued' AND net_satoshis >= $threshold
-- Descending net_satoshis so highest-value records surface first. (IDX-11)
CREATE INDEX idx_pr_queued_net_satoshis
    ON payout_records(network, net_satoshis DESC)
    WHERE status = 'queued';

-- Batch lookup: find all records in a sweep TX for confirmation handler and RBF.
-- Query: WHERE batch_txid = $txid
-- Partial: batch_txid is NULL until broadcast, so this excludes most rows.
CREATE INDEX idx_pr_batch_txid
    ON payout_records(batch_txid)
    WHERE batch_txid IS NOT NULL;

-- Stale constructing watchdog: records in constructing > BTC_CONSTRUCTING_WATCHDOG_THRESHOLD
-- (default 10 min) are returned to queued. Query: WHERE status='constructing' AND updated_at < $cutoff
CREATE INDEX idx_pr_stale_constructing
    ON payout_records(network, updated_at)
    WHERE status = 'constructing';

-- Held aging monitor: fires 7-day WARNING and 30-day CRITICAL alerts.
-- Query: WHERE status='held' AND created_at < $cutoff
CREATE INDEX idx_pr_held_aging
    ON payout_records(network, created_at)
    WHERE status = 'held';

COMMENT ON TABLE payout_records IS
    'Vendor payout lifecycle per settled invoice. '
    'Trigger guards (011): fn_btc_payout_guard, fn_pr_vendor_consistency, '
    'fn_pr_destination_consistency, fn_pr_status_guard. '
    'DOUBLE-PAYOUT GUARD: UNIQUE (invoice_id) is the race-safe backstop. '
    'BROADCAST ORDERING: constructing→broadcast UPDATE MUST commit BEFORE sendrawtransaction.';
COMMENT ON COLUMN payout_records.batch_txid IS
    'Set atomically with constructing → broadcast. MUST be committed BEFORE sendrawtransaction. '
    'If sendrawtransaction fails after DB commit, the watchdog detects the stuck record '
    'and triggers RBF or escalation.';
COMMENT ON COLUMN payout_records.fee_breakdown IS
    'Exact fee computation record. '
    'Keys: received_sat, tolerance_adj_sat, processing_fee_sat, miner_fee_sat, net_sat, '
    'fee_rate_pct, batch_size_used, fee_estimate_source.';
COMMENT ON COLUMN payout_records.original_txid IS
    'Populated when RBF is applied. Both original_txid and batch_txid are preserved '
    'so the audit trail shows the full TX replacement history.';
COMMENT ON COLUMN payout_records.destination_address IS
    'Copied from invoice.bridge_destination_address at creation. '
    'fn_pr_destination_consistency (011) verifies this matches the invoice snapshot. '
    'NULL for platform wallet mode.';


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
    CONSTRAINT chk_fae_financial_anchor
        CHECK (
            event_type NOT IN (
                'settlement_complete', 'balance_credit', 'balance_debit'
            )
            OR invoice_id IS NOT NULL
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
   KYC SUBMISSIONS
   ═════════════════════════════════════════════════════════════ */

/*
 * Minimum viable KYC/AML submission lifecycle table.
 *
 * The btc_kyc_status ENUM exists on vendor_wallet_config and payout_records as a
 * placeholder. This table provides the backing records needed for a real KYC flow.
 * A vendor's effective KYC status is driven by the latest submission row for that vendor.
 *
 * COMP-01 in todo.md describes the full KYC schema requirements. This table is the
 * minimum viable foundation — document storage references (kyc_documents) and provider
 * webhook payloads (kyc_provider_webhooks) are deferred to future migrations.
 *
 * KYC documents (passport photos, proof of address) must NOT be stored as column values.
 * Store encrypted references to external storage (e.g. S3 object keys) only.
 *
 * KYC may expire and require periodic refresh (jurisdiction-dependent, typically 2 years).
 * expires_at is non-NULL for approved submissions with a known expiry.
 */
CREATE TABLE kyc_submissions (
    id               UUID        PRIMARY KEY DEFAULT uuidv7(),

    -- The vendor submitting for KYC review.
    -- RESTRICT: vendor cannot be deleted while a KYC submission exists.
    vendor_id        UUID        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,

    -- KYC provider identifier. Examples: 'jumio', 'onfido', 'sumsub'.
    provider         TEXT        NOT NULL,

    -- The provider's own reference ID for this submission.
    -- UNIQUE per (provider, provider_ref_id): prevents duplicate submissions from the
    -- same provider being recorded twice (e.g. from webhook replay).
    provider_ref_id  TEXT        NOT NULL,

    -- Current KYC state machine position.
    -- pending → approved or rejected. rejected submissions may be re-submitted.
    status           btc_kyc_status NOT NULL DEFAULT 'pending',

    -- When the submission was initiated on our side.
    submitted_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- When a human reviewer completed the review. NULL until reviewed.
    reviewed_at      TIMESTAMPTZ,

    -- The reviewer (internal admin or system). SET NULL if reviewer's account is deleted.
    reviewer_id      UUID        REFERENCES users(id) ON DELETE SET NULL,

    -- Required when status = 'rejected'. Documents why the submission failed.
    rejection_reason TEXT,

    -- When this KYC approval expires and requires re-submission. NULL = no known expiry.
    -- The application should alert vendors approaching expiry.
    expires_at       TIMESTAMPTZ,

    -- One submission per (provider, provider_ref_id) — prevents duplicate webhook inserts.
    CONSTRAINT uq_kyc_provider_ref UNIQUE (provider, provider_ref_id),

    -- Coherence: rejection_reason required when status = 'rejected'.
    -- A rejected submission without a reason prevents the vendor from understanding
    -- what corrective action to take.
    CONSTRAINT chk_kyc_rejection_coherent
        CHECK (status != 'rejected' OR rejection_reason IS NOT NULL)
);

-- Per-vendor KYC history: "show all submissions for vendor X, most recent first."
CREATE INDEX idx_kyc_vendor ON kyc_submissions(vendor_id, submitted_at DESC);

-- Pending queue: "which vendors are waiting for KYC review?"
CREATE INDEX idx_kyc_status ON kyc_submissions(status) WHERE status = 'pending';

-- Expiry alert: "which approved submissions are approaching or past expiry?"
-- Partial: only approved submissions have a meaningful expiry.
CREATE INDEX idx_kyc_expiring ON kyc_submissions(expires_at)
    WHERE expires_at IS NOT NULL AND status = 'approved';

COMMENT ON TABLE kyc_submissions IS
    'KYC/AML submission lifecycle records. Minimum viable schema (COMP-01). '
    'Vendor effective KYC status = status of latest submission row for that vendor. '
    'UNIQUE (provider, provider_ref_id) prevents duplicate webhook replays. '
    'Document storage refs and provider webhooks are in future kyc_documents / kyc_provider_webhooks tables.';
COMMENT ON COLUMN kyc_submissions.provider_ref_id IS
    'Provider''s own reference ID. UNIQUE per (provider, ref_id) to prevent duplicate submissions.';
COMMENT ON COLUMN kyc_submissions.expires_at IS
    'When this KYC approval expires. NULL = no expiry. '
    'Application should alert vendors approaching expiry (typically 2 years from approval).';


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
    vendor_id       UUID        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,

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
   WEBHOOK DELIVERIES
   ═════════════════════════════════════════════════════════════ */

/*
 * Transactional outbox for vendor event notifications (ARCH-03).
 *
 * When an invoice or payout changes state, the application writes a row here in the
 * same DB transaction as the state change. A background delivery worker then reads
 * pending rows and delivers them to the vendor's registered webhook endpoint.
 *
 * This outbox pattern guarantees at-least-once delivery:
 *   - If the delivery worker crashes before delivering, the row remains pending
 *     and will be retried on next startup.
 *   - If the vendor's endpoint returns an error, next_retry_at is advanced using
 *     exponential backoff and the row is retried.
 *   - After max_attempts failures, status → dead_lettered for manual review.
 *
 * Vendor webhook endpoints are stored separately in a vendor_webhook_endpoints table
 * (future migration) or fetched from the application config layer at delivery time.
 * The payload stored here is the complete event payload to be delivered.
 */
CREATE TABLE webhook_deliveries (
    id               UUID        PRIMARY KEY DEFAULT uuidv7(),

    -- Vendor to receive this notification.
    -- RESTRICT: delivery record cannot exist without a vendor.
    vendor_id        UUID        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,

    -- Application-defined event type for the notification.
    -- Examples: 'invoice.settled', 'payout.confirmed', 'invoice.expired'.
    event_type       TEXT        NOT NULL,

    -- Complete event payload to deliver to the vendor's endpoint.
    -- Must be a JSON object. Includes all data the vendor needs to act on the event.
    payload          JSONB       NOT NULL,

    -- ── Source event references ────────────────────────────────────────────────

    -- The invoice that triggered this notification. NULL for non-invoice events.
    invoice_id       UUID        REFERENCES invoices(id) ON DELETE RESTRICT,

    -- The payout record that triggered this notification. NULL for non-payout events.
    payout_record_id UUID        REFERENCES payout_records(id) ON DELETE RESTRICT,

    -- ── Delivery state ─────────────────────────────────────────────────────────

    -- Current delivery lifecycle position.
    -- pending → delivered (success) or failed (temporary) → dead_lettered (permanent failure).
    status           TEXT        NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'delivered', 'failed', 'dead_lettered')),

    -- How many delivery attempts have been made. Incremented on each attempt.
    attempt_count    INTEGER     NOT NULL DEFAULT 0,

    -- Maximum attempts before status → dead_lettered.
    -- Default 5. Override per event type if needed.
    max_attempts     INTEGER     NOT NULL DEFAULT 5,

    -- When to attempt the next delivery. NULL = deliver immediately.
    -- Set using exponential backoff after each failed attempt.
    -- Delivery worker query: WHERE status = 'pending' AND (next_retry_at IS NULL OR next_retry_at <= NOW())
    next_retry_at    TIMESTAMPTZ,

    -- Last error message from a failed delivery attempt. Overwritten on each attempt.
    last_error       TEXT,

    -- Timestamp when the delivery was successfully acknowledged by the vendor's endpoint.
    -- NULL until delivered.
    delivered_at     TIMESTAMPTZ,

    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_wd_payload_is_object
        CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT chk_wd_attempt_non_negative
        CHECK (attempt_count >= 0),
    -- attempt_count can never exceed max_attempts (would be set to dead_lettered first).
    CONSTRAINT chk_wd_attempt_within_max
        CHECK (attempt_count <= max_attempts)
);

-- Delivery worker hot path: find pending deliveries ready to attempt.
-- Query: WHERE status = 'pending' AND (next_retry_at IS NULL OR next_retry_at <= NOW())
CREATE INDEX idx_wd_pending ON webhook_deliveries(next_retry_at)
    WHERE status = 'pending';

-- Per-vendor delivery history: admin investigation of missed notifications.
CREATE INDEX idx_wd_vendor ON webhook_deliveries(vendor_id, created_at DESC);

-- Dead letter review: find permanently failed deliveries requiring manual intervention.
CREATE INDEX idx_wd_dead_letter ON webhook_deliveries(created_at DESC)
    WHERE status = 'dead_lettered';

COMMENT ON TABLE webhook_deliveries IS
    'Transactional outbox for vendor state-change notifications. '
    'Written in the same TX as the triggering state change. '
    'At-least-once delivery with exponential backoff retry up to max_attempts. '
    'dead_lettered rows require manual review. '
    'Delivery worker polls: WHERE status=''pending'' AND (next_retry_at IS NULL OR next_retry_at <= NOW()).';
COMMENT ON COLUMN webhook_deliveries.max_attempts IS
    'Maximum delivery attempts before status → dead_lettered. Default 5.';
COMMENT ON COLUMN webhook_deliveries.next_retry_at IS
    'NULL = deliver immediately. Set with exponential backoff after each failed attempt.';


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


/* ═════════════════════════════════════════════════════════════
   DISPUTE RECORDS
   ═════════════════════════════════════════════════════════════ */

/*
 * Buyer and vendor payment dispute records.
 *
 * Disputes link a raised concern to the specific financial object in question.
 * At least one of invoice_id or payout_record_id must be set (chk_dr_has_subject).
 *
 * The lifecycle is: open → investigating → resolved or rejected.
 * Resolved and rejected disputes require a resolved_at timestamp (chk_dr_resolved_coherent).
 *
 * Resolution is handled by an admin (resolved_by). The resolution_note records what action
 * was taken (e.g. "Refund issued via payout record PR-xxx", "Verified correct — no action").
 */
CREATE TABLE dispute_records (
    id               UUID        PRIMARY KEY DEFAULT uuidv7(),

    -- The invoice the dispute is about. NULL if the dispute is about a payout only.
    invoice_id       UUID        REFERENCES invoices(id) ON DELETE RESTRICT,

    -- The payout record the dispute is about. NULL if the dispute is about an invoice only.
    -- At least one of invoice_id or payout_record_id must be non-NULL.
    payout_record_id UUID        REFERENCES payout_records(id) ON DELETE RESTRICT,

    -- Who raised the dispute. RESTRICT: cannot delete user while dispute is unresolved.
    raised_by        UUID        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,

    -- Category of dispute. Determines which resolution workflow applies.
    dispute_type     TEXT        NOT NULL
                     CHECK (dispute_type IN (
                         'payment_not_credited',  -- buyer paid but invoice not settled
                         'wrong_amount',          -- incorrect amount received or charged
                         'fee_dispute',           -- vendor disputes the fee calculation
                         'refund_request',        -- buyer is requesting a refund
                         'other'                  -- does not fit standard categories
                     )),

    -- Detailed description of the dispute from the raiser's perspective.
    -- Must have visible content (chk_dr_description_not_empty).
    description      TEXT        NOT NULL,

    -- Current resolution lifecycle position.
    status           TEXT        NOT NULL DEFAULT 'open'
                     CHECK (status IN ('open', 'investigating', 'resolved', 'rejected')),

    -- Admin who resolved the dispute. SET NULL if resolver's account is later deleted.
    resolved_by      UUID        REFERENCES users(id) ON DELETE SET NULL,

    -- What action was taken to resolve the dispute.
    -- Required when status = resolved or rejected (application layer enforced).
    resolution_note  TEXT,

    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- When the dispute was resolved or rejected.
    -- Required when status = resolved or rejected (chk_dr_resolved_coherent).
    resolved_at      TIMESTAMPTZ,

    -- At least one financial object must be referenced.
    -- A dispute with no invoice_id and no payout_record_id cannot be investigated.
    CONSTRAINT chk_dr_has_subject
        CHECK (invoice_id IS NOT NULL OR payout_record_id IS NOT NULL),

    -- Coherence: resolved_at required when dispute is closed.
    CONSTRAINT chk_dr_resolved_coherent
        CHECK (status NOT IN ('resolved', 'rejected') OR resolved_at IS NOT NULL),

    -- Description must have visible content.
    CONSTRAINT chk_dr_description_not_empty
        CHECK (length(trim(description)) > 0)
);

-- Invoice dispute lookup: "are there any open disputes for invoice X?"
CREATE INDEX idx_dr_invoice ON dispute_records(invoice_id) WHERE invoice_id IS NOT NULL;

-- Open dispute queue: "show all open disputes sorted by creation date."
CREATE INDEX idx_dr_open ON dispute_records(created_at DESC) WHERE status = 'open';

-- Per-raiser history: "show all disputes raised by vendor/buyer X."
CREATE INDEX idx_dr_vendor ON dispute_records(raised_by, created_at DESC);

COMMENT ON TABLE dispute_records IS
    'Buyer/vendor payment disputes. '
    'At least one of invoice_id or payout_record_id must be set (chk_dr_has_subject). '
    'Lifecycle: open → investigating → resolved or rejected.';


/* ═════════════════════════════════════════════════════════════
   GDPR ERASURE REQUESTS
   ═════════════════════════════════════════════════════════════ */

/*
 * GDPR Article 17 (right to erasure) request tracking.
 *
 * PII columns subject to erasure across the BTC schema:
 *   invoices.buyer_refund_address           — nullify after retention window
 *   vendor_wallet_config.bridge_destination_address — nullify
 *   payout_records.destination_address      — nullify
 *   sse_token_issuances.source_ip_hash      — nullify + set erased=TRUE
 *   vendor_wallet_config_history.*          — nullify old_value / new_value where PII
 *
 * financial_audit_events.actor_label CANNOT be erased (immutable table, DELETE blocked).
 * Mitigation: store HMAC-SHA256(email, server_secret) instead of raw email at write time.
 * See COMP-02 in todo.md for the full mitigation strategy.
 *
 * tables_processed: array of table names where erasure was applied for this request.
 * Used by the erasure worker to resume incomplete runs after a crash without
 * re-processing tables that were already handled.
 *
 * Status lifecycle: pending → in_progress → completed or rejected.
 */
CREATE TABLE gdpr_erasure_requests (
    id               UUID        PRIMARY KEY DEFAULT uuidv7(),

    -- The user requesting erasure. SET NULL if the account is purged before erasure completes.
    user_id          UUID        REFERENCES users(id) ON DELETE SET NULL,

    -- When the erasure request was received.
    requested_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- When all erasure steps were completed. Required when status = 'completed'.
    completed_at     TIMESTAMPTZ,

    -- Which tables have been processed by the erasure worker so far.
    -- Updated incrementally so a crashed worker can resume without re-processing.
    -- Example: ARRAY['invoices', 'vendor_wallet_config', 'sse_token_issuances']
    tables_processed TEXT[],

    -- Current erasure workflow position.
    status           TEXT        NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'in_progress', 'completed', 'rejected')),

    -- Required when status = 'rejected'. Documents why erasure was denied.
    -- Example: "Legal hold active — erasure blocked pending litigation."
    rejection_reason TEXT,

    -- Coherence: completed_at required when status = 'completed'.
    CONSTRAINT chk_ger_completed_coherent
        CHECK (status != 'completed' OR completed_at IS NOT NULL),
    -- Coherence: rejection_reason required when status = 'rejected'.
    CONSTRAINT chk_ger_rejected_coherent
        CHECK (status != 'rejected' OR rejection_reason IS NOT NULL)
);

-- Active request queue: "show all pending/in-progress erasure requests."
CREATE INDEX idx_ger_pending ON gdpr_erasure_requests(requested_at DESC)
    WHERE status IN ('pending', 'in_progress');

COMMENT ON TABLE gdpr_erasure_requests IS
    'GDPR Article 17 erasure request tracking. '
    'tables_processed enables crash recovery without re-processing completed tables. '
    'NOTE: financial_audit_events.actor_label cannot be erased (immutable table). '
    'Mitigation: store HMAC hash at write time. See COMP-02 in todo.md.';
COMMENT ON COLUMN gdpr_erasure_requests.tables_processed IS
    'Tables where erasure steps have been applied. Updated incrementally for crash recovery. '
    'The erasure worker checks this before processing each table to avoid duplicate work.';


/* ═════════════════════════════════════════════════════════════
   FATF TRAVEL RULE RECORDS
   ═════════════════════════════════════════════════════════════ */

/*
 * FATF (Financial Action Task Force) Travel Rule compliance records for payouts
 * above the jurisdictional threshold.
 *
 * The Travel Rule (FATF Recommendation 16) requires VASPs (Virtual Asset Service
 * Providers) to collect and transmit originator and beneficiary information for
 * transfers above a threshold:
 *   USA (FinCEN): $1,000  |  EU (TFR Regulation): €1,000  |  Others: varies
 *
 * This platform is a VASP when it sweeps vendor payouts. For each qualifying payout:
 *   originator = the vendor receiving the payout (our customer)
 *   beneficiary = the owner of the destination Bitcoin address
 *   beneficiary_vasp = the exchange or wallet service controlling that address
 *
 * TIMING: This record must be created BEFORE or AT the same time as the payout broadcast.
 * The application checks for a qualifying fatf_travel_rule_records row before allowing
 * the constructing → broadcast transition for payouts above the threshold.
 *
 * compliance_status tracks whether the originator/beneficiary data was successfully
 * transmitted to the receiving VASP (via TRISA, OpenVASP, or similar protocol).
 *
 * beneficiary_address must match payout_records.destination_address.
 * The application enforces this at creation time. A trigger could also enforce it but
 * the cross-table check adds latency to every payout record INSERT.
 */
CREATE TABLE fatf_travel_rule_records (
    id               UUID        PRIMARY KEY DEFAULT uuidv7(),

    -- The payout this Travel Rule record covers. One record per qualifying payout.
    -- RESTRICT: payout cannot be deleted while a compliance record exists.
    payout_record_id UUID        NOT NULL REFERENCES payout_records(id) ON DELETE RESTRICT,

    -- The jurisdictional threshold that was exceeded, in satoshis.
    -- Recorded at creation time so the record is self-contained even if thresholds change.
    threshold_sat    BIGINT      NOT NULL,

    -- ── Originator (our vendor — the sender) ──────────────────────────────────

    -- Full legal name of the originator (vendor).
    originator_name  TEXT        NOT NULL,

    -- This platform's VASP identifier in the relevant VASP directory.
    -- NULL if not yet registered or if jurisdiction doesn't require VASP identification.
    originator_vasp  TEXT,

    -- ── Beneficiary (owner of the destination address — the receiver) ──────────

    -- Full legal name or entity name of the beneficiary.
    beneficiary_name TEXT        NOT NULL,

    -- Receiving VASP's identifier (e.g. the exchange where the destination address is held).
    -- NULL if the beneficiary is self-custodying (no known receiving VASP).
    beneficiary_vasp TEXT,

    -- The Bitcoin address receiving the payout. Must match payout_records.destination_address.
    -- Enforced by the application at creation. Recorded here for self-contained audit.
    beneficiary_address TEXT     NOT NULL,

    -- ── Compliance transmission state ─────────────────────────────────────────

    -- Whether the Travel Rule data has been transmitted to the receiving VASP.
    -- pending   = not yet transmitted (payout may proceed; transmission is async)
    -- sent      = transmission initiated
    -- acknowledged = receiving VASP confirmed receipt
    -- failed    = transmission failed after retries; requires manual follow-up
    compliance_status TEXT       NOT NULL DEFAULT 'pending'
                      CHECK (compliance_status IN (
                          'pending', 'sent', 'acknowledged', 'failed'
                      )),

    -- When the transmission was initiated. NULL while compliance_status = 'pending'.
    submitted_at     TIMESTAMPTZ,

    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- One Travel Rule record per payout. If payout is split, each output gets its own record.
    CONSTRAINT uq_ftr_payout UNIQUE (payout_record_id),

    CONSTRAINT chk_ftr_threshold_positive
        CHECK (threshold_sat > 0),

    -- Coherence: submitted_at required once transmission has been initiated.
    CONSTRAINT chk_ftr_submitted_coherent
        CHECK (compliance_status = 'pending' OR submitted_at IS NOT NULL)
);

-- Compliance monitoring: "which Travel Rule records are pending or failed transmission?"
-- Partial index keeps this focused on actionable records.
CREATE INDEX idx_ftr_status ON fatf_travel_rule_records(compliance_status, created_at DESC)
    WHERE compliance_status IN ('pending', 'failed');

COMMENT ON TABLE fatf_travel_rule_records IS
    'FATF Travel Rule compliance records for payouts above the threshold. '
    'Must be created BEFORE the constructing → broadcast transition. '
    'compliance_status tracks transmission to the receiving VASP. '
    'beneficiary_address must match payout_records.destination_address '
    '(application-enforced and DB trigger-enforced by fn_fatf_address_consistency in 011).';
COMMENT ON COLUMN fatf_travel_rule_records.threshold_sat IS
    'The threshold that was exceeded at record creation time, in satoshis. '
    'Thresholds: USA $1000, EU €1000. Recorded for self-contained audit.';
COMMENT ON COLUMN fatf_travel_rule_records.compliance_status IS
    'pending=not yet transmitted. sent=initiated. acknowledged=VASP confirmed. '
    'failed=transmission failed, requires manual follow-up.';


-- All functions, triggers, grants, and autovacuum settings are in 011_btc_functions.sql.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

/*
 * Drop in reverse FK dependency order.
 * Tables in 009_btc.sql are dropped by that file's Down migration.
 * Goose runs Down migrations in reverse numerical order: 011 → 010 → 009.
 */

DROP TABLE IF EXISTS fatf_travel_rule_records     CASCADE;
DROP TABLE IF EXISTS gdpr_erasure_requests        CASCADE;
DROP TABLE IF EXISTS dispute_records              CASCADE;
DROP TABLE IF EXISTS btc_zmq_dead_letter          CASCADE;
DROP TABLE IF EXISTS webhook_deliveries           CASCADE;
DROP INDEX  IF EXISTS uq_sti_jti_hash;
DROP TABLE  IF EXISTS sse_token_issuances          CASCADE;
DROP TABLE IF EXISTS kyc_submissions              CASCADE;
DROP TABLE IF EXISTS reconciliation_run_history   CASCADE;
DROP TABLE IF EXISTS ops_audit_log                CASCADE;
DROP TABLE IF EXISTS vendor_wallet_config_history CASCADE;
DROP TABLE IF EXISTS btc_tier_config_history      CASCADE;
DROP TABLE IF EXISTS wallet_backup_success        CASCADE;
DROP TABLE IF EXISTS financial_audit_events       CASCADE;
DROP TABLE IF EXISTS payout_records               CASCADE;

-- +goose StatementEnd
