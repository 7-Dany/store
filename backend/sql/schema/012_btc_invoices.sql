-- +goose Up
-- +goose StatementBegin

/*
 * 012_btc_invoices.sql — Invoice state machine and address lifecycle tables.
 *
 * Tables defined here (in dependency order):
 *   invoices                   — core 16-state invoice state machine
 *   invoice_addresses          — P2WPKH bech32 address per invoice (HD keypool)
 *   invoice_address_monitoring — DB-backed ZMQ watch list; survives process restarts
 *   invoice_payments           — append-only on-chain payment records
 *
 * Depends on: 010_btc_core.sql, 009_btc_types.sql, 001_core.sql (users)
 * Continued in: 013_btc_invoices_functions.sql
 */

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
    WHERE status IN (
        'pending',
        'detected',
        'confirming',
        'settling',
        'underpaid',
        'mempool_dropped',
        'reorg_admin_required'
    );

COMMENT ON INDEX idx_inv_network_status_inflight IS
    'Covering index for the SumInflightInvoiceAmounts reconciliation query. '
    'reorg_admin_required is included because those invoices hold on-chain value '
    'not yet accounted for in payout_records. Excluding them causes a permanent '
    'formula imbalance.';

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
   RECONCILIATION FORMULA INDEXES
   ═════════════════════════════════════════════════════════════ */

/*
 *   Term 1 (covered by idx_inv_network_status_inflight above):
 *     SUM(invoices.amount_sat) WHERE status IN (pending, detected, confirming, settling,
 *     underpaid, mempool_dropped, reorg_admin_required)
 *     — pre-settlement states; no on-chain payment received or not yet finalized.
 *
 *   Term 2 (covered by the indexes below — SumUnresolvedPaymentAmounts):
 *     SUM(invoice_payments.value_sat) WHERE invoice.status IN (settlement_failed, overpaid,
 *     expired_with_payment, cancelled_with_payment)
 *     — payment received but no payout_record or balance credit created yet;
 *       these are real on-chain platform obligations not captured elsewhere.
 *
 * Without Term 2 the reconciliation formula silently undercounts by the sum of all
 * invoice_payments.value_sat for the four statuses above.
 */

-- Invoice-side filter for Term 2 of the reconciliation formula.
-- Joins against invoice_payments to sum actual received amounts.
CREATE INDEX idx_inv_unresolved_payment_statuses
    ON invoices(network, id)
    WHERE status IN (
        'settlement_failed',        -- payment confirmed but settlement could not complete
        'overpaid',                 -- received > invoiced; amount_sat undercounts real value
        'expired_with_payment',     -- payment arrived after expiry; platform holds on-chain funds
        'cancelled_with_payment'    -- vendor cancelled after payment; platform holds on-chain funds
    );

COMMENT ON INDEX idx_inv_unresolved_payment_statuses IS
    'Reconciliation formula Term 2: invoice_payments.value_sat for these statuses '
    'is not captured in payout_records or vendor balances but represents real on-chain '
    'platform obligations. Separate from idx_inv_network_status_inflight (Term 1).';

-- Payment-side covering index for Term 2. Used in the join:
--   SELECT SUM(ip.value_sat) FROM invoice_payments ip
--   JOIN invoices i ON i.id = ip.invoice_id
--   WHERE i.network = $1 AND i.status IN (...)
CREATE INDEX idx_ip_reconciliation_unresolved
    ON invoice_payments(invoice_id)
    INCLUDE (value_sat);

COMMENT ON INDEX idx_ip_reconciliation_unresolved IS
    'Covering index for the SumUnresolvedPaymentAmounts reconciliation query. '
    'Pairs with idx_inv_unresolved_payment_statuses on the invoices side.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_ip_reconciliation_unresolved;
DROP INDEX IF EXISTS idx_inv_unresolved_payment_statuses;
DROP TABLE IF EXISTS invoice_payments             CASCADE;
DROP TABLE IF EXISTS invoice_address_monitoring   CASCADE;
DROP TABLE IF EXISTS invoice_addresses            CASCADE;
DROP TABLE IF EXISTS invoices                     CASCADE;

-- +goose StatementEnd
