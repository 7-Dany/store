-- +goose Up
-- +goose StatementBegin

/*
 * 014_btc_infrastructure.sql — Bitcoin node operational state tables.
 *
 * Tables defined here:
 *   btc_outage_log         — periods when Bitcoin Core was unreachable
 *                            (used by the expiry cleanup job to adjust invoice expiry
 *                             deadlines; outage overlap is added to effective_expires_at)
 *   bitcoin_block_history  — processed-block log with pruned-block placeholders
 *                            (used for confirmation-depth tracking and reorg detection)
 *
 * No functions or triggers are defined in this file — these tables are append-only
 * with no mutation triggers.
 *
 * Depends on: 009_btc_types.sql
 * Continued in: 015_btc_payouts.sql
 */

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

DROP TABLE IF EXISTS bitcoin_block_history CASCADE;
DROP TABLE IF EXISTS btc_outage_log        CASCADE;

-- +goose StatementEnd
