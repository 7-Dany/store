-- +goose Up
-- +goose StatementBegin

/*
 * 016_btc_payouts_functions.sql — Guards and triggers for payout_records.
 *
 * Functions defined here:
 *   fn_btc_payout_guard()          — prevents payout creation for wrong network/mode
 *   fn_pr_vendor_consistency()     — enforces vendor/network coherence on payout records
 *   fn_pr_destination_consistency()— ensures destination address matches vendor config
 *   fn_pr_status_guard()           — enforces the payout status transition matrix
 *   fn_pr_fatf_broadcast_guard()   — blocks broadcast transition if FATF record is
 *                                    missing when platform_config.fatf_enabled = TRUE
 *
 * Status transitions enforced:
 *   confirmed → refunded  (post-settlement payment refund)
 *   failed    → refunded  (admin refunds buyer on-chain)
 *
 * Depends on: 015_btc_payouts.sql
 * Continued in: 017_btc_audit.sql
 */

/* ═════════════════════════════════════════════════════════════
   PAYOUT GUARDS
   ═════════════════════════════════════════════════════════════ */

/*
 * fn_btc_payout_guard
 * ───────────────────
 * Rejects INSERT on payout_records when the parent invoice is not in 'settled' status.
 * Uses SELECT FOR SHARE to close the TOCTOU window between two concurrent settlement workers.
 */
CREATE OR REPLACE FUNCTION fn_btc_payout_guard()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
DECLARE
    v_status btc_invoice_status;
BEGIN
    SELECT status INTO v_status
    FROM invoices
    WHERE id = NEW.invoice_id
    FOR SHARE;

    IF v_status IS NULL THEN
        RAISE EXCEPTION
            'payout_records: parent invoice % does not exist.', NEW.invoice_id;
    END IF;

    IF v_status != 'settled' THEN
        RAISE EXCEPTION
            'payout_records: cannot create payout for invoice % with status %. '
            'Invoice must be in ''settled'' status.',
            NEW.invoice_id, v_status;
    END IF;

    RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_btc_payout_guard() IS
    'Rejects INSERT on payout_records when parent invoice.status != ''settled''. '
    'Uses SELECT FOR SHARE to eliminate TOCTOU race. '
    'Defence-in-depth alongside application Phase 2 check and UNIQUE (invoice_id).';

CREATE TRIGGER trg_payout_records_guard
    BEFORE INSERT ON payout_records
    FOR EACH ROW EXECUTE FUNCTION fn_btc_payout_guard();


/*
 * fn_pr_vendor_consistency (SEC-06)
 * ──────────────────────────────────
 * Rejects INSERT on payout_records when vendor_id does not match the parent invoice's
 * vendor_id. Prevents funds from being swept to the wrong vendor due to a bug in
 * the payout creation code path.
 */
CREATE OR REPLACE FUNCTION fn_pr_vendor_consistency()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
DECLARE
    v_invoice_vendor UUID;
BEGIN
    SELECT vendor_id INTO v_invoice_vendor
    FROM invoices WHERE id = NEW.invoice_id;

    IF v_invoice_vendor IS DISTINCT FROM NEW.vendor_id THEN
        RAISE EXCEPTION
            'payout_records: vendor_id % does not match invoice.vendor_id % '
            'for invoice %. Possible cross-vendor payout assignment.',
            NEW.vendor_id, v_invoice_vendor, NEW.invoice_id
            USING ERRCODE = 'P0001';
    END IF;

    RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_pr_vendor_consistency() IS
    'Rejects INSERT on payout_records when vendor_id != invoice.vendor_id. '
    'Prevents funds being swept to the wrong vendor due to a code bug.';

CREATE TRIGGER trg_pr_vendor_consistency
    BEFORE INSERT ON payout_records
    FOR EACH ROW EXECUTE FUNCTION fn_pr_vendor_consistency();


/*
 * fn_pr_destination_consistency (ARCH-05)
 * ────────────────────────────────────────
 * Rejects INSERT on payout_records when destination_address does not match the
 * bridge_destination_address frozen in the parent invoice snapshot.
 *
 * A bug that uses the live vendor_wallet_config address instead of the invoice
 * snapshot would sweep funds to the wrong address — irrecoverable on mainnet.
 */
CREATE OR REPLACE FUNCTION fn_pr_destination_consistency()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
DECLARE
    v_invoice_dest  TEXT;
    v_invoice_mode  btc_wallet_mode;
BEGIN
    SELECT bridge_destination_address, wallet_mode
    INTO v_invoice_dest, v_invoice_mode
    FROM invoices WHERE id = NEW.invoice_id;

    IF NEW.wallet_mode != v_invoice_mode THEN
        RAISE EXCEPTION
            'payout_records: wallet_mode % does not match invoice.wallet_mode % for invoice %.',
            NEW.wallet_mode, v_invoice_mode, NEW.invoice_id
            USING ERRCODE = 'P0001';
    END IF;

    IF v_invoice_mode != 'platform' AND
       NEW.destination_address IS DISTINCT FROM v_invoice_dest THEN
        RAISE EXCEPTION
            'payout_records: destination_address ''%'' does not match '
            'invoice.bridge_destination_address ''%'' for invoice %. '
            'Always copy destination_address from the invoice snapshot, not the live vendor config.',
            NEW.destination_address, v_invoice_dest, NEW.invoice_id
            USING ERRCODE = 'P0001';
    END IF;

    RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_pr_destination_consistency() IS
    'Rejects INSERT on payout_records when destination_address does not match '
    'the bridge_destination_address in the parent invoice snapshot. '
    'Prevents sweeping to the wrong address due to a code bug.';

CREATE TRIGGER trg_pr_destination_consistency
    BEFORE INSERT ON payout_records
    FOR EACH ROW EXECUTE FUNCTION fn_pr_destination_consistency();



/* ═════════════════════════════════════════════════════════════
   PAYOUT STATUS GUARD
   ═════════════════════════════════════════════════════════════ */

/*
 * fn_pr_status_guard — Enforces the payout_records status transition matrix.
 *   confirmed  → refunded  (post-settlement payment refund — settlement-technical.md §4)
 *   failed     → refunded  (admin refunds buyer on-chain — settlement-technical.md §4)
 *
 * Without these transitions the platform cannot recover from a mempool drop or reorg
 * (payout records stuck permanently in 'broadcast'), and the post-settlement refund
 * path is completely blocked.
 *
 * Terminal states: refunded and manual_payout — no outgoing transitions.
 */
CREATE OR REPLACE FUNCTION fn_pr_status_guard()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
BEGIN
    IF OLD.status = NEW.status THEN
        RETURN NEW;
    END IF;

    IF NOT (
        (OLD.status = 'held'         AND NEW.status IN ('queued', 'failed', 'manual_payout'))
     OR (OLD.status = 'queued'       AND NEW.status IN ('constructing', 'failed', 'manual_payout'))
     OR (OLD.status = 'constructing' AND NEW.status IN ('broadcast', 'queued', 'failed'))
     -- broadcast → queued: mempool drop OR reorg rollback (resilience-technical.md §4)
     -- broadcast → refunded: pre-confirmation refund (settlement-technical.md §4)
     OR (OLD.status = 'broadcast'    AND NEW.status IN ('confirmed', 'queued', 'failed', 'refunded'))
     -- confirmed → queued: reorg rollback when sweep tx dropped (resilience-technical.md §4)
     -- confirmed → refunded: post-settlement payment refund (settlement-technical.md §4)
     OR (OLD.status = 'confirmed'    AND NEW.status IN ('queued', 'refunded'))
     -- failed → queued: admin re-queues after investigation
     -- failed → refunded: admin refunds buyer on-chain (settlement-technical.md §4)
     OR (OLD.status = 'failed'       AND NEW.status IN ('queued', 'manual_payout', 'refunded'))
    ) THEN
        RAISE EXCEPTION
            'payout_records: invalid status transition % → % for payout %. '
            'Terminal states: refunded and manual_payout cannot transition out. '
            'confirmed can only transition to queued (reorg) or refunded (post-settlement).',
            OLD.status, NEW.status, OLD.id
            USING ERRCODE = 'P0001';
    END IF;

    RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_pr_status_guard() IS
    'Enforces payout_records status transition matrix.';

CREATE TRIGGER trg_pr_status_guard
    BEFORE UPDATE OF status ON payout_records
    FOR EACH ROW EXECUTE FUNCTION fn_pr_status_guard();


/* ════════════════════════════════════════════════════════════
   FATF TRAVEL RULE BROADCAST GUARD
   ════════════════════════════════════════════════════════════ */

/*
 * fn_pr_fatf_broadcast_guard
 * ──────────────────────────
 * Blocks the constructing → broadcast status transition when:
 *   1. platform_config.fatf_enabled = TRUE for this payout's network, AND
 *   2. No fatf_travel_rule_records row exists for this payout_record_id.
 *
 * RATIONALE:
 *   The FATF Travel Rule (Recommendation 16) requires VASPs to file counterparty data
 *   before transmitting qualifying transfers. The application layer is expected to create
 *   the FATF record before broadcasting, but application bugs, retry storms, or missed
 *   code paths can cause the record to be absent at broadcast time.
 *
 *   This trigger is the DB-level backstop. Without it, a single application failure path
 *   can sweep funds above the jurisdictional threshold without a Travel Rule filing — a
 *   VASP regulatory violation carrying potential license revocation.
 *
 * SCOPE:
 *   Only fires on constructing → broadcast transitions. Other transitions are unaffected.
 *   Platform-mode payouts (destination_address IS NULL) are excluded — no on-chain sweep
 *   occurs and no FATF record is required.
 *
 * BYPASS:
 *   Setting platform_config.fatf_enabled = FALSE disables all FATF enforcement.
 *   This is intentional: FATF enforcement is controlled by the owner feature flag to
 *   allow staged rollout after VASP registration (see platform_config comments).
 */
CREATE OR REPLACE FUNCTION fn_pr_fatf_broadcast_guard()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
DECLARE
    v_fatf_enabled BOOLEAN;
    v_fatf_exists  BOOLEAN;
BEGIN
    -- Only enforce on constructing → broadcast transition.
    IF OLD.status != 'constructing' OR NEW.status != 'broadcast' THEN
        RETURN NEW;
    END IF;

    -- Platform-mode payouts have no on-chain destination; FATF does not apply.
    IF NEW.wallet_mode = 'platform' OR NEW.destination_address IS NULL THEN
        RETURN NEW;
    END IF;

    -- Check whether FATF enforcement is enabled for this network.
    SELECT fatf_enabled INTO v_fatf_enabled
    FROM platform_config
    WHERE network = NEW.network;

    IF NOT FOUND OR NOT v_fatf_enabled THEN
        RETURN NEW;
    END IF;

    -- Verify that a fatf_travel_rule_records row exists for this payout.
    SELECT EXISTS (
        SELECT 1 FROM fatf_travel_rule_records
        WHERE payout_record_id = NEW.id
    ) INTO v_fatf_exists;

    IF NOT v_fatf_exists THEN
        RAISE EXCEPTION
            'payout_records: FATF Travel Rule record is required before broadcast for '
            'payout % on % (net_satoshis=%). '
            'Create a fatf_travel_rule_records row before transitioning to broadcast. '
            'FATF enforcement is active: platform_config.fatf_enabled = TRUE for network %.',
            NEW.id, NEW.network, NEW.net_satoshis, NEW.network
            USING ERRCODE = 'P0001';
    END IF;

    RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_pr_fatf_broadcast_guard() IS
    'Blocks constructing → broadcast when fatf_enabled = TRUE and no '
    'fatf_travel_rule_records row exists for the payout. '
    'Platform-mode payouts (no destination address) are exempt. '
    'FATF enforcement is disabled when platform_config.fatf_enabled = FALSE. '
    'DB-level backstop; application layer should also enforce before sweep construction.';

CREATE TRIGGER trg_pr_fatf_broadcast_guard
    BEFORE UPDATE OF status ON payout_records
    FOR EACH ROW EXECUTE FUNCTION fn_pr_fatf_broadcast_guard();


-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS trg_pr_fatf_broadcast_guard    ON payout_records;
DROP TRIGGER IF EXISTS trg_pr_status_guard            ON payout_records;
DROP TRIGGER IF EXISTS trg_pr_destination_consistency ON payout_records;
DROP TRIGGER IF EXISTS trg_pr_vendor_consistency      ON payout_records;
DROP TRIGGER IF EXISTS trg_payout_records_guard       ON payout_records;

DROP FUNCTION IF EXISTS fn_pr_fatf_broadcast_guard();
DROP FUNCTION IF EXISTS fn_pr_status_guard();
DROP FUNCTION IF EXISTS fn_pr_destination_consistency();
DROP FUNCTION IF EXISTS fn_pr_vendor_consistency();
DROP FUNCTION IF EXISTS fn_btc_payout_guard();

-- +goose StatementEnd
