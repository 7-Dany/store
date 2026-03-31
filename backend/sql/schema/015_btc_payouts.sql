-- +goose Up
-- +goose StatementBegin

/*
 * 015_btc_payouts.sql — Vendor payout lifecycle table.
 *
 * Tables defined here:
 *   payout_records — payout lifecycle from settlement credit to on-chain confirmation
 *           records to 'queued'. dispute_id is the link to the freezing dispute record.
 *           Stored as UUID (no FK) because dispute_records is defined in 023_btc_disputes.sql;
 *           the FK relationship goes dispute_records.payout_record_id → payout_records.id.
 *
 * Depends on: 012_btc_invoices.sql (invoices FK), 010_btc_core.sql (vendor_wallet_config FK)
 * Continued in: 016_btc_payouts_functions.sql
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
 * F-07 — BATCH_ID PRECONDITION REQUIRED IN BROADCAST WHERE CLAUSE:
 *   The constructing → broadcast UPDATE MUST include batch_id in its WHERE clause:
 *     UPDATE payout_records SET status='broadcast', batch_txid=$txid, broadcast_at=NOW()
 *     WHERE status='constructing' AND batch_id=$my_batch_id
 *   WITHOUT batch_id, a worker A that was reclaimed by the watchdog (which reset
 *   the record to 'queued'), and then reclaimed again by worker B (now 'constructing'
 *   again), will have its 'constructing' status matched by worker A's UPDATE when
 *   worker A resumes. Worker A overwrites worker B's batch_id with its own txid,
 *   recording a txid that may not contain this vendor's output. The vendor's funds
 *   appear 'broadcast' under the wrong txid. Funds locked until manual admin review.
 *   The fix is simple: always add AND batch_id=$my_batch_id to the WHERE clause.
 *   Zero RowsAffected then means "watchdog reclaimed me" — abort, do not broadcast.
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

    -- TRUE when sendrawtransaction returned an ambiguous result (timeout or connection
    -- loss) after the constructing→broadcast DB commit succeeded. Indicates the TX
    -- may or may not have reached the network — the confirmation handler cannot
    -- distinguish "broadcast succeeded, awaiting confirmation" from "broadcast failed,
    -- tx never on-chain" without calling getrawtransaction or getrawtransaction RPC.
    -- F-02: The sweep watchdog must treat rpc_ambiguous=TRUE differently from clean
    -- broadcast failures:
    --   rpc_ambiguous=FALSE + no mempool entry → safe to RBF immediately
    --   rpc_ambiguous=TRUE  + no mempool entry → call getrawtransaction first;
    --                          if "not found" then safe to RBF; if found then wait.
    -- Application sets rpc_ambiguous=TRUE in the same UPDATE that sets status='broadcast'
    -- when sendrawtransaction times out or returns a connection error (not a rejection).
    -- Clear to FALSE (via RBF UPDATE) once getrawtransaction confirms the original
    -- TX is not on-chain.
    rpc_ambiguous   BOOLEAN         NOT NULL DEFAULT FALSE,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- ── Dispute freeze columns ──────────────────────────────────────
    -- Reason this record is in 'held' status when it would otherwise be 'queued'.
    --   NULL           = normal fee-floor hold (below sweep miner-fee floor)
    --   'dispute_hold' = frozen by an active dispute
    --   'fee_spike'    = temporary hold due to fee spike beyond miner_fee_cap
    -- The fee-floor re-evaluation job skips records with hold_reason IS NOT NULL.
    hold_reason      TEXT,

    -- The dispute that froze this payout. Stored as UUID text (no FK) because
    -- dispute_records is defined in a later migration (023_btc_disputes.sql).
    -- The real FK relationship goes dispute_records.payout_record_id → payout_records.id.
    -- NULL unless hold_reason = 'dispute_hold'.
    -- The sweep job checks dispute_id IS NOT NULL at the broadcast boundary to abort.
    dispute_id       UUID,

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
        CHECK (fee_breakdown IS NULL OR jsonb_typeof(fee_breakdown) = 'object'),
    -- Coherence: dispute_id requires hold_reason = 'dispute_hold'.
    CONSTRAINT chk_pr_dispute_id_coherent
        CHECK (dispute_id IS NULL OR hold_reason = 'dispute_hold'),

    -- Restrict hold_reason to documented values so the fee-floor re-evaluation job
    -- (which skips all non-NULL hold_reason rows) cannot be silently fooled by a
    -- typo or debug value permanently trapping a payout record.
    -- NULL means normal fee-floor hold (below miner fee floor).
    -- 'dispute_hold' = frozen by an active dispute_records row.
    -- 'fee_spike'    = temporarily held because fee rate exceeds miner_fee_cap.
    CONSTRAINT chk_pr_hold_reason_valid
        CHECK (hold_reason IS NULL OR hold_reason IN ('dispute_hold', 'fee_spike'))

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
    'and triggers RBF or escalation. '
    'F-07: the broadcast UPDATE WHERE clause MUST include AND batch_id=$my_batch_id. '
    'Without it, a reclaimed-and-reassigned record can have its batch_txid overwritten '
    'by the original worker, locking vendor funds under the wrong txid.';

COMMENT ON COLUMN payout_records.rpc_ambiguous IS
    'F-02: TRUE when sendrawtransaction timed out or lost the connection after '
    'the constructing→broadcast DB commit succeeded. '
    'TX may or may not be on the network. '
    'Watchdog: if TRUE and no mempool entry, call getrawtransaction before RBF. '
    'Clear to FALSE once getrawtransaction confirms TX is not on-chain.';
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

-- Dispute freeze lookup: find all payout records frozen by a specific dispute.
CREATE INDEX idx_pr_dispute_frozen
    ON payout_records(dispute_id)
    WHERE dispute_id IS NOT NULL;


-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_pr_dispute_frozen;
DROP TABLE IF EXISTS payout_records CASCADE;

-- +goose StatementEnd
