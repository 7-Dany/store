-- +goose Up
-- +goose StatementBegin

/*
 * 028_btc_watch_transactions.sql — Durable tracked Bitcoin transaction status records.
 *
 * WHY THIS TABLE EXISTS:
 *   The bitcoin/watch and bitcoin/events flow discovers transactions in real time,
 *   but the frontend also needs a durable txstatus read model that survives page
 *   refreshes and reconnects.
 *
 * NORMALISED SHAPE:
 *   btc_watches                       → watch resources (address / transaction)
 *   btc_tracked_transactions          → one lifecycle row per (user_id, network, txid)
 *   btc_tracked_transaction_addresses → watched addresses connected to that tx row
 *
 * This keeps tx lifecycle state in one place while still allowing one tracked
 * transaction to relate to any number of watched addresses and per-address
 * received amounts.
 *
 * DESIGN NOTE — amount_sat ownership:
 *   For watch-discovered rows, btc_tracked_transactions.amount_sat is the
 *   aggregate of all linked btc_tracked_transaction_addresses.amount_sat values.
 *   fn_btta_sync_parent_amount_sat (AFTER INSERT/UPDATE/DELETE trigger on the
 *   address table) keeps that aggregate correct whenever related-address rows
 *   change.
 *
 *   For explicit txid rows created through the txstatus CRUD API, amount_sat may
 *   be written directly because there may be no related-address rows at all.
 *   In practice:
 *     - tracking_mode = 'watch' → child rows are the source of truth
 *     - tracking_mode = 'txid'  → the parent row may carry the direct amount
 *
 * DESIGN NOTE — hard delete on btc_tracked_transactions:
 *   This feature is an auxiliary explorer-style tool, not part of the invoice
 *   settlement ledger. When a user deletes a tracked transaction row, it is
 *   removed physically. Child rows in btc_tracked_transaction_addresses and
 *   btc_tracked_transaction_status_log cascade away with it.
 *
 * STATUS MODEL (btc_tracked_transactions.status):
 *   mempool     — tx is in mempool with 0 confirmations
 *   confirmed   — tx has ≥1 confirmation
 *   not_found   — tx unknown to wallet and public mempool
 *   conflicting — tx was replaced by a confirmed conflict
 *   abandoned   — wallet explicitly abandoned the tx
 *   replaced    — original mempool tx was displaced by RBF
 *
 * STATUS MODEL (btc_watches.status):
 *   active      — watch is live; events are dispatched for this target
 *
 * Depends on: 001_core.sql (users)
 */


/* ═════════════════════════════════════════════════════════════
   TRACKED TRANSACTIONS
   ═════════════════════════════════════════════════════════════ */

/*
 * One durable lifecycle row per (user_id, network, txid).
 *
 * Rows originate from two sources:
 *   'txid'  — created by POST /bitcoin/tx (explicit txid tracking via CRUD API)
 *   'watch' — discovered by the watch/events pipeline when a watched address
 *             appears in a mempool or block transaction
 *
 * Watch-discovered related addresses are stored in btc_tracked_transaction_addresses
 * rather than in this table, so that a single tx that touches multiple watched
 * addresses for the same user produces one row here and N rows in the child table.
 *
 * STATUS GUARD INVARIANT — all UPSERT ON CONFLICT DO UPDATE statements that set
 * status must explicitly guard against overwriting terminal states ('confirmed',
 * 'replaced', 'conflicting', 'abandoned') with earlier states. The application
 * layer must assert RowsAffected() and treat 0 as a no-op (see UpsertWatchBitcoinTxStatus
 * and ConfirmBitcoinTxStatus in sql/queries/btc.sql).
 */
CREATE TABLE btc_tracked_transactions (
    id                 BIGSERIAL    PRIMARY KEY,

    -- The user who owns this tracking row.
    user_id            UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- 'mainnet' or 'testnet4'.
    network            TEXT         NOT NULL,

    -- 'txid'  = created via the explicit txstatus CRUD API.
    -- 'watch' = discovered by the watch/events pipeline.
    -- The mode cannot change from 'watch' → 'txid' once set via explicit tracking;
    -- UpsertTrackedBitcoinTxStatus promotes 'watch' → 'txid' but not the reverse.
    tracking_mode      TEXT         NOT NULL
        CHECK (tracking_mode IN ('txid', 'watch')),

    -- Optional explicit address supplied through the txstatus CRUD API.
    -- Watch-discovered related addresses live in btc_tracked_transaction_addresses.
    -- NULL on 'watch' rows until the user explicitly supplies one via the API.
    address            TEXT,

    -- The Bitcoin txid being tracked. Immutable after row creation.
    -- Must be exactly 64 lowercase hex characters (32-byte hash).
    txid               TEXT         NOT NULL,

    -- Current lifecycle state. Transitions are driven by mempool/block events
    -- and explicit user actions. See STATUS MODEL in the file header.
    status             TEXT         NOT NULL
        CHECK (status IN ('confirmed', 'mempool', 'not_found', 'conflicting', 'abandoned', 'replaced')),

    -- Number of block confirmations. 0 for mempool, ≥1 for confirmed.
    -- chk_btt_confirmed_fields enforces confirmations > 0 when status='confirmed'.
    -- chk_btt_non_confirmed_confirms enforces confirmations = 0 for all other states.
    confirmations      INTEGER      NOT NULL DEFAULT 0 CHECK (confirmations >= 0),

    -- Aggregate received amount in satoshis.
    -- tracking_mode='watch': maintained from btc_tracked_transaction_addresses
    -- by fn_btta_sync_parent_amount_sat.
    -- tracking_mode='txid': may be written directly by the explicit txstatus API.
    -- 0 when unknown.
    amount_sat         BIGINT       NOT NULL DEFAULT 0 CHECK (amount_sat >= 0),

    -- Fee rate in sat/vbyte at the time the tx was last seen in mempool.
    -- 0 when unavailable. NUMERIC(18,8) to avoid IEEE 754 rounding artefacts
    -- that DOUBLE PRECISION would introduce for display and calculation.
    fee_rate_sat_vbyte NUMERIC(18,8) NOT NULL DEFAULT 0 CHECK (fee_rate_sat_vbyte >= 0),

    -- When this tx was first seen (mempool or block). Set on INSERT, then preserved
    -- using LEAST() on subsequent updates so out-of-order events cannot advance it.
    first_seen_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    -- When this tx was most recently observed. Updated on every event.
    last_seen_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    -- When the tx reached its first confirmation. NULL until status = 'confirmed'.
    -- Required by chk_btt_confirmed_fields when status = 'confirmed'.
    confirmed_at       TIMESTAMPTZ,

    -- Block hash in which the tx was first confirmed. NULL until confirmed.
    -- chk_btt_block_hash_hex enforces 64-char lowercase hex when present.
    block_hash         TEXT,

    -- Block height at which the tx was first confirmed. NULL until confirmed.
    block_height       BIGINT,

    -- txid of the RBF replacement transaction. NULL unless status = 'replaced'.
    -- Required by chk_btt_replaced_fields when status = 'replaced'.
    -- Use JOIN on (user_id, network, txid = replacement_txid) to follow the chain.
    replacement_txid   TEXT,

    -- Row created timestamp.
    created_at         TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    -- Row last-modified timestamp. Updated by trg_btt_updated_at trigger.
    updated_at         TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_btt_network
        -- Prevent unknown network strings. New networks require a migration.
        CHECK (network IN ('mainnet', 'testnet4')),
    CONSTRAINT chk_btt_txid_hex
        -- Enforce Bitcoin txid format: exactly 64 lowercase hex chars.
        -- A txid that fails this is a bug in the caller, not a user error.
        CHECK (txid ~ '^[0-9a-f]{64}$'),
    CONSTRAINT chk_btt_address_nonempty
        -- Prevent empty-string addresses that would silently match any '' filter.
        CHECK (address IS NULL OR address <> ''),
    CONSTRAINT chk_btt_replacement_txid_hex
        -- Enforce replacement txid format when present.
        CHECK (
            replacement_txid IS NULL
            OR replacement_txid ~ '^[0-9a-f]{64}$'
        ),
    CONSTRAINT chk_btt_block_hash_hex
        -- Enforce block-hash format when present. Keep NULL valid until first
        -- confirmation so mempool rows do not need placeholder values.
        CHECK (
            block_hash IS NULL
            OR block_hash ~ '^[0-9a-f]{64}$'
        ),
    CONSTRAINT chk_btt_confirmed_fields
        -- A confirmed tx must have confirmations > 0 plus the first-confirmation
        -- metadata needed for display, drill-down, and follow-up block scans.
        CHECK (
            status != 'confirmed'
            OR (
                confirmations > 0
                AND confirmed_at IS NOT NULL
                AND block_hash IS NOT NULL
                AND block_height IS NOT NULL
            )
        ),
    CONSTRAINT chk_btt_non_confirmed_block_fields
        -- Non-confirmed rows must not retain block-confirmation metadata. Keeping
        -- stale block fields on mempool/not_found/conflicting/abandoned/replaced
        -- rows would mislead the UI and break state-machine assumptions.
        CHECK (
            status = 'confirmed'
            OR (
                confirmed_at IS NULL
                AND block_hash IS NULL
                AND block_height IS NULL
            )
        ),
    CONSTRAINT chk_btt_non_confirmed_confirms
        -- Non-confirmed rows must have confirmations = 0 to avoid misleading the UI.
        -- GREATEST(..., 1) in ConfirmBitcoinTxStatus is the safety net against callers
        -- passing 0; this constraint prevents any other path from storing a positive
        -- count on a non-confirmed row.
        CHECK (
            status = 'confirmed'
            OR confirmations = 0
        ),
    CONSTRAINT chk_btt_replaced_fields
        -- A replaced tx must carry the replacement txid so the UI can link to it.
        CHECK (
            status != 'replaced'
            OR replacement_txid IS NOT NULL
        ),
    CONSTRAINT chk_btt_non_replaced_replacement_null
        -- replacement_txid is exclusive to the replaced state. Prevent stale links
        -- from surviving later transitions back into other states.
        CHECK (
            status = 'replaced'
            OR replacement_txid IS NULL
        ),
    CONSTRAINT chk_btt_seen_order
        -- last_seen_at must be ≥ first_seen_at. A violation indicates swapped arguments
        -- in the caller (e.g. (last, first) passed in wrong order).
        CHECK (last_seen_at >= first_seen_at)
);

-- Uniqueness constraint: one tracking row per (user, network, txid).
-- The ON CONFLICT target for UpsertTrackedBitcoinTxStatus and UpsertWatchBitcoinTxStatus.
CREATE UNIQUE INDEX uq_btt_user_network_txid
    ON btc_tracked_transactions(user_id, network, txid);

-- Default list ordering: most recent activity first.
-- Serves ListBitcoinTxStatuses (no tracking_mode filter).
-- Uses COALESCE so confirmed txs sort by confirmed_at, pending txs by first_seen_at.
CREATE INDEX idx_btt_user_time
    ON btc_tracked_transactions(user_id, network, COALESCE(confirmed_at, first_seen_at) DESC, id DESC);

-- Same ordering split by tracking_mode.
-- Serves ListBitcoinTxStatuses when @tracking_mode filter is active.
CREATE INDEX idx_btt_user_tracking_time
    ON btc_tracked_transactions(user_id, network, tracking_mode, COALESCE(confirmed_at, first_seen_at) DESC, id DESC);

-- Exact-address list/lookup for explicit txstatus rows.
-- Serves the explicit-address branch of ListBitcoinTxStatuses without scanning
-- unrelated rows for the same user/network.
CREATE INDEX idx_btt_user_address_time
    ON btc_tracked_transactions(user_id, network, address, COALESCE(confirmed_at, first_seen_at) DESC, id DESC)
    WHERE address IS NOT NULL;

-- Cross-user fanout lookup: "which users track this txid on this network?"
-- Serves ListBitcoinTxStatusUsersByTxID and event dispatch.
CREATE INDEX idx_btt_network_txid
    ON btc_tracked_transactions(network, txid, user_id);

-- Confirmed-tx list for a user, ordered by confirmation time.
-- Serves future status-filtered queries without scanning unconfirmed rows.
CREATE INDEX idx_btt_user_confirmed_time
    ON btc_tracked_transactions(user_id, network, confirmed_at DESC, id DESC)
    WHERE status = 'confirmed';

CREATE TRIGGER trg_btt_updated_at
    BEFORE UPDATE ON btc_tracked_transactions
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

COMMENT ON TABLE btc_tracked_transactions IS
    'Durable Bitcoin transaction status rows for the txstatus feature. One row per user/network/txid. '
    'Two sources: ''txid'' rows from POST /bitcoin/tx; ''watch'' rows from watch/events pipeline. '
    'tracking_mode=''watch'': amount_sat is reconciled from btc_tracked_transaction_addresses. '
    'tracking_mode=''txid'': amount_sat may be set directly by the explicit txstatus API. '
    'Hard-deleted on user removal request; child addresses and status-log rows cascade away.';
COMMENT ON COLUMN btc_tracked_transactions.tracking_mode IS
    '"txid" rows originate from the explicit txstatus CRUD API. '
    '"watch" rows originate from watch/event discovery. '
    'UpsertTrackedBitcoinTxStatus may promote ''watch'' → ''txid'' but never the reverse.';
COMMENT ON COLUMN btc_tracked_transactions.address IS
    'Optional explicit address supplied through the txstatus CRUD API. '
    'Watch-discovered related addresses live in btc_tracked_transaction_addresses. '
    'NULL on watch-discovered rows until the user supplies one via the API.';
COMMENT ON COLUMN btc_tracked_transactions.amount_sat IS
    'Aggregate amount in satoshis. '
    'tracking_mode=''watch'': maintained automatically from btc_tracked_transaction_addresses '
    'by fn_btta_sync_parent_amount_sat. '
    'tracking_mode=''txid'': may be written directly by the explicit txstatus CRUD API. '
    '0 when unknown.';
COMMENT ON COLUMN btc_tracked_transactions.fee_rate_sat_vbyte IS
    'Fee rate in sat/vbyte when last seen in mempool. 0 when unavailable. '
    'NUMERIC(18,8) — not DOUBLE PRECISION — to prevent IEEE 754 rounding artefacts. '
    'Matches the type used by payout_records.fee_rate_sat_vbyte for consistency.';
COMMENT ON COLUMN btc_tracked_transactions.replacement_txid IS
    'Replacement txid when the original mempool transaction was displaced by RBF. '
    'NULL unless status = ''replaced''. '
    'Follow the chain with: JOIN ON (user_id, network, txid = replacement_txid).';
COMMENT ON COLUMN btc_tracked_transactions.first_seen_at IS
    'When this tx was first observed (mempool or block). '
    'Preserved using LEAST() on updates — out-of-order events cannot advance it.';


/* ═════════════════════════════════════════════════════════════
   TRACKED TRANSACTION ADDRESSES
   ═════════════════════════════════════════════════════════════ */

/*
 * Per-address received amounts for watch-discovered transactions.
 *
 * A single tx may touch multiple watched addresses for the same user, or the same
 * address may appear in multiple outputs of the same tx. This table records the
 * per-address satoshi amount for each (tx, address) pair so the UI can break down
 * a combined transaction by address.
 *
 * TRIGGER INVARIANT:
 *   fn_btta_sync_parent_amount_sat fires AFTER INSERT/UPDATE OF amount_sat/DELETE
 *   on this table and recalculates btc_tracked_transactions.amount_sat as SUM(amount_sat)
 *   across all rows with the same tracked_transaction_id. The parent column is never
 *   written directly by the application.
 *
 * LIFECYCLE:
 *   Rows cascade-delete when the parent btc_tracked_transactions row is deleted.
 */
CREATE TABLE btc_tracked_transaction_addresses (
    -- FK to the owning tracked transaction. Cascade-delete on parent removal.
    tracked_transaction_id BIGINT       NOT NULL REFERENCES btc_tracked_transactions(id) ON DELETE CASCADE,

    -- The watched Bitcoin address. Immutable after row creation.
    address                TEXT         NOT NULL,

    -- Satoshis received by this address within the tracked transaction.
    -- Updated by UpsertBitcoinTxStatusRelatedAddress when a new event arrives.
    -- Triggers fn_btta_sync_parent_amount_sat to update the parent aggregate.
    amount_sat             BIGINT       NOT NULL DEFAULT 0 CHECK (amount_sat >= 0),

    -- Row created timestamp.
    created_at             TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    -- Row last-modified timestamp. Updated by trg_btta_updated_at trigger.
    updated_at             TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    PRIMARY KEY (tracked_transaction_id, address),
    CONSTRAINT chk_btta_address_nonempty
        -- Prevent empty-string addresses that would silently match '' filters.
        CHECK (address <> '')
);

-- Address-first lookup: "which tracked transactions reference this address?"
-- Serves UpsertBitcoinTxStatusRelatedAddress and the address-filter branch
-- inside ListBitcoinTxStatuses (correlated subquery / JOIN path).
CREATE INDEX idx_btta_address
    ON btc_tracked_transaction_addresses(address, tracked_transaction_id);

CREATE TRIGGER trg_btta_updated_at
    BEFORE UPDATE ON btc_tracked_transaction_addresses
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

/*
 * Trigger: keep btc_tracked_transactions.amount_sat in sync with child rows.
 *
 * Fires AFTER any write to btc_tracked_transaction_addresses that changes
 * the per-address amount. Recalculates the parent aggregate with a single
 * GROUP BY query keyed on tracked_transaction_id. Without this trigger the
 * parent column drifts silently whenever address-level amounts change.
 */
CREATE OR REPLACE FUNCTION fn_btta_sync_parent_amount_sat()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    UPDATE btc_tracked_transactions
    SET amount_sat = (
        SELECT COALESCE(SUM(a.amount_sat), 0)
        FROM btc_tracked_transaction_addresses a
        WHERE a.tracked_transaction_id = COALESCE(NEW.tracked_transaction_id,
                                                   OLD.tracked_transaction_id)
    )
    WHERE id = COALESCE(NEW.tracked_transaction_id, OLD.tracked_transaction_id);
    RETURN COALESCE(NEW, OLD);
END;
$$;

CREATE TRIGGER trg_btta_sync_parent_amount_sat
    AFTER INSERT OR UPDATE OF amount_sat OR DELETE
    ON btc_tracked_transaction_addresses
    FOR EACH ROW EXECUTE FUNCTION fn_btta_sync_parent_amount_sat();

COMMENT ON TABLE btc_tracked_transaction_addresses IS
    'Addresses related to one tracked Bitcoin transaction row, with per-address received totals. '
    'amount_sat changes here automatically propagate to btc_tracked_transactions.amount_sat '
    'via fn_btta_sync_parent_amount_sat. Never update the parent column directly.';
COMMENT ON COLUMN btc_tracked_transaction_addresses.amount_sat IS
    'Amount received by this specific address within the tracked transaction. '
    'Writing to this column triggers fn_btta_sync_parent_amount_sat to recalculate '
    'the parent btc_tracked_transactions.amount_sat aggregate.';


/* ═════════════════════════════════════════════════════════════
   TRANSACTION STATUS HISTORY
   ═════════════════════════════════════════════════════════════ */

/*
 * Immutable log of every btc_tracked_transactions.status transition.
 *
 * WHY THIS TABLE EXISTS:
 *   Status transitions in btc_tracked_transactions are driven by mempool/block events
 *   and may race in concurrent systems. Without a history table, investigating
 *   production incidents (e.g. FIND-5.1: stale confirm event overwrote 'replaced')
 *   is impossible post-facto — only the current status is visible.
 *
 *   Questions this table answers:
 *     "Did this tx ever reach 'confirmed' before being marked 'replaced'?"
 *     "How long was this tx in mempool before confirming?"
 *     "Which event triggered the unexpected status regression?"
 *
 * Populated by fn_btt_log_status_change AFTER UPDATE trigger (below).
 * Rows are cascade-deleted when the parent tx row is deleted (administrative only).
 */
CREATE TABLE btc_tracked_transaction_status_log (
    id                     BIGSERIAL    PRIMARY KEY,

    -- Parent tracking row. CASCADE so the log cleans up with the parent.
    tracked_transaction_id BIGINT       NOT NULL REFERENCES btc_tracked_transactions(id) ON DELETE CASCADE,

    -- Status before the transition. Nullable by design so the table can support
    -- a future INSERT-time status log without another schema rewrite. Under the
    -- current AFTER UPDATE trigger, this is always non-NULL in practice.
    old_status             TEXT,

    -- Status after the transition. Never NULL. Mirrors the parent table's valid
    -- status set via chk_bttsl_new_status.
    new_status             TEXT         NOT NULL,

    -- When the transition row was committed.
    changed_at             TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_bttsl_old_status
        -- Keep log rows self-consistent with the parent state machine while still
        -- allowing NULL for a future INSERT-time "initial status" event.
        CHECK (
            old_status IS NULL
            OR old_status IN ('confirmed', 'mempool', 'not_found', 'conflicting', 'abandoned', 'replaced')
        ),
    CONSTRAINT chk_bttsl_new_status
        -- Mirror the parent table's valid status set so broken trigger code cannot
        -- append impossible states to the audit trail.
        CHECK (
            new_status IN ('confirmed', 'mempool', 'not_found', 'conflicting', 'abandoned', 'replaced')
        )
);

-- Timeline query: "show all status transitions for tx row X in chronological order."
CREATE INDEX idx_btt_status_log_tx_time
    ON btc_tracked_transaction_status_log(tracked_transaction_id, changed_at ASC);

/*
 * Trigger: append a status log row whenever btc_tracked_transactions.status changes.
 * Fires AFTER UPDATE on btc_tracked_transactions when the status column value changes.
 */
CREATE OR REPLACE FUNCTION fn_btt_log_status_change()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status IS DISTINCT FROM NEW.status THEN
        INSERT INTO btc_tracked_transaction_status_log(tracked_transaction_id, old_status, new_status)
        VALUES (NEW.id, OLD.status, NEW.status);
    END IF;
    RETURN COALESCE(NEW, OLD);
END;
$$;

CREATE TRIGGER trg_btt_status_log
    AFTER UPDATE ON btc_tracked_transactions
    FOR EACH ROW EXECUTE FUNCTION fn_btt_log_status_change();

COMMENT ON TABLE btc_tracked_transaction_status_log IS
    'Immutable status-transition log for btc_tracked_transactions. '
    'Populated by fn_btt_log_status_change AFTER UPDATE trigger. '
    'Required for incident investigation: concurrent event races can produce '
    'unexpected status regressions that are invisible without this history.';


/* ═════════════════════════════════════════════════════════════
   WATCH RESOURCES
   ═════════════════════════════════════════════════════════════ */

/*
 * One row per live watch resource owned by a user.
 *
 * A watch resource tells the event pipeline what to observe:
 *   'address'     — monitor all on-chain activity related to the given address.
 *                   When a transaction arrives that includes the address, a
 *                   btc_tracked_transactions row is created or refreshed, and
 *                   the address is linked in btc_tracked_transaction_addresses.
 *   'transaction' — monitor one txid lifecycle (mempool → confirmed / replaced).
 *                   Creates or refreshes a btc_tracked_transactions row in
 *                   tracking_mode='watch'.
 *
 * DELETE MODEL:
 *   Watches are hard-deleted. This feature is an auxiliary watch/explorer tool,
 *   not part of the invoice ledger, so retaining deleted watch rows is unnecessary.
 *   The status column remains as a simple persisted "active" marker to avoid
 *   changing the external response shape, but deleted rows do not remain in the table.
 *
 * MUTATION GUARD:
 *   watch_type must not change after creation. UpdateBitcoinWatchByID enforces this
 *   by including AND watch_type = @watch_type in its WHERE clause. Changing the
 *   type would race against in-flight events dispatched for the original target.
 */
CREATE TABLE btc_watches (
    id          BIGSERIAL   PRIMARY KEY,

    -- The user who owns this watch.
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- 'mainnet' or 'testnet4'. Must match the network of the watched address or txid.
    network     TEXT        NOT NULL,

    -- 'address' = watch all tx activity for an address.
    -- 'transaction' = watch one txid lifecycle.
    -- Immutable after creation — UpdateBitcoinWatchByID checks this column in WHERE.
    watch_type  TEXT        NOT NULL CHECK (watch_type IN ('address', 'transaction')),

    -- The Bitcoin address being watched. Required and non-empty when watch_type='address'.
    -- NULL when watch_type='transaction'. chk_bw_target_shape enforces mutual exclusivity.
    address     TEXT,

    -- The txid being watched. Required, non-empty, valid hex when watch_type='transaction'.
    -- NULL when watch_type='address'. chk_bw_target_shape enforces mutual exclusivity.
    txid        TEXT,

    -- Persisted lifecycle marker for the live row. Hard delete removes the row entirely.
    -- Kept only to preserve the external response shape without schema churn.
    status      TEXT        NOT NULL DEFAULT 'active',

    -- Row created timestamp.
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Row last-modified timestamp. Updated by trg_bw_updated_at trigger.
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_bw_network
        -- Prevent unknown network strings.
        CHECK (network IN ('mainnet', 'testnet4')),
    CONSTRAINT chk_bw_target_shape
        -- Enforce exclusive ownership: an address watch must have address, no txid;
        -- a transaction watch must have a valid txid hex, no address. Mixed rows
        -- would be silently accepted without this constraint and produce undefined
        -- event dispatch behaviour.
        CHECK (
            (watch_type = 'address' AND address IS NOT NULL AND address <> '' AND txid IS NULL)
            OR (watch_type = 'transaction' AND txid IS NOT NULL AND txid ~ '^[0-9a-f]{64}$' AND address IS NULL)
        ),
    CONSTRAINT chk_bw_status_active
        CHECK (status = 'active')
);

-- Uniqueness constraint for address watches.
-- ON CONFLICT target for CreateBitcoinWatch when watch_type='address'.
CREATE UNIQUE INDEX uq_bw_active_address
    ON btc_watches(user_id, network, address)
    WHERE watch_type = 'address';

-- Uniqueness constraint for transaction watches.
-- ON CONFLICT target for CreateBitcoinWatch when watch_type='transaction'.
CREATE UNIQUE INDEX uq_bw_active_transaction
    ON btc_watches(user_id, network, txid)
    WHERE watch_type = 'transaction';

-- Default list ordering for a user's watches: most recent first.
-- Serves ListBitcoinWatches (no type filter).
CREATE INDEX idx_bw_user_time
    ON btc_watches(user_id, network, created_at DESC, id DESC);

-- Same list ordering split by watch_type.
-- Serves ListBitcoinWatches when @watch_type filter is active.
CREATE INDEX idx_bw_user_type_time
    ON btc_watches(user_id, network, watch_type, created_at DESC, id DESC);

-- Fast address-watch quota/count lookup for one user/network.
-- Lets CountActiveBitcoinAddressWatchesByUser stay on a tiny partial index under load.
CREATE INDEX idx_bw_user_network_address_count
    ON btc_watches(user_id, network)
    WHERE watch_type = 'address';

-- Cross-user fanout lookup by txid: "which users have an active transaction watch on this txid?"
-- Serves ListActiveBitcoinTransactionWatchUsersByTxID and event dispatch.
CREATE INDEX idx_bw_active_transaction_by_txid
    ON btc_watches(network, txid, user_id)
    WHERE watch_type = 'transaction';

CREATE TRIGGER trg_bw_updated_at
    BEFORE UPDATE ON btc_watches
    FOR EACH ROW
    WHEN (OLD IS DISTINCT FROM NEW)
    EXECUTE FUNCTION fn_set_updated_at();

COMMENT ON TABLE btc_watches IS
    'Bitcoin watch resources owned by users. '
    'Address watches track all chain activity related to the address; '
    'transaction watches track one txid lifecycle. '
    'Hard-deleted when removed by the user. '
    'watch_type is immutable after creation to prevent races with in-flight events.';
COMMENT ON COLUMN btc_watches.watch_type IS
    '"address" watches observe all discovered transaction activity related to the address. '
    '"transaction" watches observe one txid lifecycle. '
    'Immutable: UpdateBitcoinWatchByID enforces AND watch_type = @watch_type in WHERE.';
COMMENT ON COLUMN btc_watches.network IS
    'Bitcoin network for the watched resource. Valid values are enforced by chk_bw_network.';
COMMENT ON COLUMN btc_watches.address IS
    'Bitcoin address being watched when watch_type = ''address''. '
    'NULL when watch_type = ''transaction''. '
    'chk_bw_target_shape enforces mutual exclusivity with txid.';
COMMENT ON COLUMN btc_watches.txid IS
    'Bitcoin txid being watched when watch_type = ''transaction''. '
    'NULL when watch_type = ''address''. '
    'chk_bw_target_shape enforces mutual exclusivity with address and validates 64-char lowercase hex.';
COMMENT ON COLUMN btc_watches.status IS
    'Persisted live-row marker. Only "active" rows exist because deleted watches are hard-deleted.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Drop in reverse dependency order: children before parents.
DROP TABLE IF EXISTS btc_tracked_transaction_status_log    CASCADE;
DROP TABLE IF EXISTS btc_tracked_transaction_addresses     CASCADE;
DROP TABLE IF EXISTS btc_tracked_transactions              CASCADE;
DROP TABLE IF EXISTS btc_watches                           CASCADE;

-- +goose StatementEnd
