-- +goose Up
-- +goose StatementBegin

/*
 * 010_btc_functions.sql — Bitcoin payment system functions and triggers.
 *
 * All plpgsql functions and their bound triggers for the BTC payment pipeline.
 * Separated from 009_btc.sql so that function logic can be iterated on without
 * touching the schema DDL, and so that the function file can be reviewed and
 * deployed independently during development.
 *
 * Functions defined here:
 *   fn_btc_audit_immutable   — rejects UPDATE and DELETE on financial_audit_events
 *   fn_btc_audit_no_truncate — rejects TRUNCATE on financial_audit_events
 *   fn_btc_payout_guard      — rejects INSERT on payout_records when parent invoice
 *                              is not in 'settled' status (+ FOR SHARE lock)
 *   fn_btc_wallet_mode_guard — blocks wallet_mode changes while balance > 0,
 *                              preventing silent reconciliation drift
 *
 * Each function is immediately followed by its COMMENT and bound trigger(s).
 *
 * Depends on:
 *   009_btc.sql — all BTC enum types and table definitions
 */


/* ═════════════════════════════════════════════════════════════
   AUDIT IMMUTABILITY
   ═════════════════════════════════════════════════════════════ */

/*
 * fn_btc_audit_immutable
 * ──────────────────────
 * Rejects all UPDATE and DELETE attempts on financial_audit_events.
 * Fires from two BEFORE triggers (one for UPDATE, one for DELETE).
 * Enforces immutability even for privileged DB users (e.g. superuser during migration).
 *
 * Note: does NOT cover TRUNCATE — that is handled by fn_btc_audit_no_truncate below.
 */
CREATE OR REPLACE FUNCTION fn_btc_audit_immutable()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
BEGIN
    RAISE EXCEPTION
        'financial_audit_events is immutable: % is not permitted. '
        'Admin resolutions must be written as new rows via references_event_id.',
        TG_OP;
END;
$fn$;

COMMENT ON FUNCTION fn_btc_audit_immutable() IS
    'Rejects UPDATE and DELETE on financial_audit_events unconditionally, '
    'regardless of the DB user. Enforces the append-only immutability invariant. '
    'TRUNCATE is handled separately by fn_btc_audit_no_truncate.';

CREATE TRIGGER trg_fae_no_update
    BEFORE UPDATE ON financial_audit_events
    FOR EACH ROW EXECUTE FUNCTION fn_btc_audit_immutable();

CREATE TRIGGER trg_fae_no_delete
    BEFORE DELETE ON financial_audit_events
    FOR EACH ROW EXECUTE FUNCTION fn_btc_audit_immutable();


/*
 * fn_btc_audit_no_truncate
 * ────────────────────────
 * Blocks TRUNCATE on financial_audit_events. The row-level UPDATE and DELETE
 * triggers (trg_fae_no_update, trg_fae_no_delete) fire FOR EACH ROW and do
 * not fire for TRUNCATE. This statement-level trigger closes that gap so the
 * immutability guarantee is complete against all three mutation operations.
 *
 * A superuser or migration script issuing TRUNCATE without this trigger would
 * silently erase the entire audit trail, destroying evidence for any in-flight
 * or historical dispute.
 */
CREATE OR REPLACE FUNCTION fn_btc_audit_no_truncate()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
BEGIN
    RAISE EXCEPTION
        'financial_audit_events is immutable: TRUNCATE is not permitted. '
        'This table is an append-only financial audit trail and must never be '
        'truncated. Contact the compliance officer if you believe a truncation '
        'is warranted.'
        USING ERRCODE = 'P0001';
END;
$fn$;

COMMENT ON FUNCTION fn_btc_audit_no_truncate() IS
    'Rejects TRUNCATE on financial_audit_events. Closes the immutability gap '
    'left by the row-level UPDATE/DELETE triggers, which do not fire for TRUNCATE.';

CREATE TRIGGER trg_fae_no_truncate
    BEFORE TRUNCATE ON financial_audit_events
    FOR EACH STATEMENT EXECUTE FUNCTION fn_btc_audit_no_truncate();


/* ═════════════════════════════════════════════════════════════
   PAYOUT GUARD
   ═════════════════════════════════════════════════════════════ */

/*
 * fn_btc_payout_guard
 * ───────────────────
 * Rejects INSERT on payout_records when the parent invoice is not in 'settled' status.
 * Enforces the invariant from settlement-technical.md §1:
 *   "No payout record may be created unless the parent invoice is in 'settled' status."
 *
 * This is defence-in-depth alongside the application-layer check in Phase 2.
 * It prevents any future code path from accidentally creating payouts for unsettled invoices.
 *
 * Locking: uses SELECT ... FOR SHARE on the invoice row to close the TOCTOU window
 * where two concurrent settlement workers could both read status = 'settled' before
 * either INSERT commits. FOR SHARE prevents the invoice row from being updated (e.g.
 * status changed away from 'settled') until this transaction commits.
 *
 * The UNIQUE (invoice_id) constraint on payout_records is the complementary race guard
 * at the DB level — the second concurrent INSERT will fail on that constraint even if
 * both workers pass this trigger check.
 */
CREATE OR REPLACE FUNCTION fn_btc_payout_guard()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
DECLARE
    v_status btc_invoice_status;
BEGIN
    -- FOR SHARE acquires a shared row lock on the invoice row, preventing a concurrent
    -- UPDATE on that row from committing until this INSERT transaction completes.
    -- Without this lock there is a TOCTOU window: two workers could both read status =
    -- 'settled' before either INSERT commits, bypassing the guard.
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
    'Rejects INSERT on payout_records when the parent invoice.status != ''settled''. '
    'Uses SELECT FOR SHARE to eliminate the TOCTOU race between status check and insert. '
    'Defence-in-depth alongside the application-layer Phase 2 check and the '
    'UNIQUE (invoice_id) constraint on payout_records.';

CREATE TRIGGER trg_payout_records_guard
    BEFORE INSERT ON payout_records
    FOR EACH ROW EXECUTE FUNCTION fn_btc_payout_guard();


/* ═════════════════════════════════════════════════════════════
   WALLET MODE GUARD
   ═════════════════════════════════════════════════════════════ */

/*
 * fn_btc_wallet_mode_guard
 * ────────────────────────
 * Blocks wallet_mode changes on vendor_wallet_config while the vendor's
 * vendor_balances.balance_satoshis > 0 for that (vendor_id, network).
 *
 * WHY: balance_satoshis is VALUE-BEARING only when wallet_mode = 'platform'.
 * The reconciliation formula (audit-technical.md §3) sums vendor_balances only
 * WHERE wallet_mode = 'platform'. A mode change while a positive platform balance
 * exists would silently exclude that balance from the formula, causing permanent
 * reconciliation drift that is invisible until the next audit.
 *
 * Resolution path: the vendor must first drain the balance via a withdrawal payout
 * (which transitions the balance to a payout_record) before any mode change is
 * permitted. The application enforces this via the vendor settings flow;
 * this trigger is the DB-level backstop.
 *
 * Locking: uses SELECT ... FOR SHARE on vendor_balances to prevent a concurrent
 * settlement credit from incrementing the balance between the check and RETURN NEW.
 */
CREATE OR REPLACE FUNCTION fn_btc_wallet_mode_guard()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
DECLARE
    v_balance BIGINT;
BEGIN
    -- Fast path: no mode change, nothing to check.
    IF NEW.wallet_mode = OLD.wallet_mode THEN
        RETURN NEW;
    END IF;

    -- FOR SHARE prevents the balance from being concurrently incremented between
    -- the read and the RETURN NEW.
    SELECT balance_satoshis INTO v_balance
    FROM vendor_balances
    WHERE vendor_id = NEW.vendor_id AND network = NEW.network
    FOR SHARE;

    IF v_balance IS NOT NULL AND v_balance > 0 THEN
        RAISE EXCEPTION
            'wallet_mode change blocked for vendor % on %: '
            'balance_satoshis = % must be zero before switching modes. '
            'Issue a withdrawal payout to drain the balance first.',
            NEW.vendor_id, NEW.network, v_balance
            USING ERRCODE = 'P0001';
    END IF;

    RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_btc_wallet_mode_guard() IS
    'Blocks wallet_mode changes while vendor_balances.balance_satoshis > 0. '
    'Prevents reconciliation drift: balance_satoshis is value-bearing only for '
    'platform mode; switching mode with a non-zero balance silently removes that '
    'amount from the reconciliation formula. Uses FOR SHARE to close the concurrent-'
    'increment race window.';

CREATE TRIGGER trg_vendor_wallet_config_mode_guard
    BEFORE UPDATE OF wallet_mode ON vendor_wallet_config
    FOR EACH ROW EXECUTE FUNCTION fn_btc_wallet_mode_guard();


/* ═════════════════════════════════════════════════════════════
   GRANTS
   ═════════════════════════════════════════════════════════════ */

/*
 * GRANT statements
 * ────────────────
 * Inline the minimum privilege grants so immutability and access control are
 * self-contained within this migration. Without these, immutability at the
 * DB-user layer relies solely on a separate ops-runbook step that may be
 * missed or applied to the wrong role.
 *
 * Replace btc_app_role with the actual application DB role name.
 * The DO block emits a WARNING (not an ERROR) so that CI migrations do not
 * fail in environments where the role has not yet been created, but the
 * warning will be visible in deployment logs.
 *
 * In production: verify with  \dp financial_audit_events  that the granted
 * privileges match what is documented here.
 */
DO $btc_grants$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'btc_app_role') THEN
        RAISE WARNING
            'btc_app_role does not exist. Replace ''btc_app_role'' in this '
            'migration with the actual application DB role name before deploying '
            'to production. Skipping GRANT statements.';
    ELSE
        -- financial_audit_events: INSERT and SELECT only. The trigger-level
        -- immutability is defence-in-depth; DB-level privileges are the first
        -- layer and must be independently correct.
        EXECUTE 'REVOKE ALL ON financial_audit_events FROM PUBLIC';
        EXECUTE 'GRANT INSERT, SELECT ON financial_audit_events TO btc_app_role';
    END IF;
END;
$btc_grants$;


-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

/*
 * Drop in reverse dependency order:
 *   1. Triggers that call the functions (must go first)
 *   2. Functions themselves
 * Tables and enums are owned by 009_btc.sql and dropped there.
 */

/* ── Triggers ──────────────────────────────────────────────── */
DROP TRIGGER IF EXISTS trg_fae_no_truncate                 ON financial_audit_events;
DROP TRIGGER IF EXISTS trg_fae_no_delete                   ON financial_audit_events;
DROP TRIGGER IF EXISTS trg_fae_no_update                   ON financial_audit_events;
DROP TRIGGER IF EXISTS trg_payout_records_guard            ON payout_records;
DROP TRIGGER IF EXISTS trg_vendor_wallet_config_mode_guard ON vendor_wallet_config;

/* ── Functions ─────────────────────────────────────────────── */
DROP FUNCTION IF EXISTS fn_btc_audit_no_truncate();
DROP FUNCTION IF EXISTS fn_btc_audit_immutable();
DROP FUNCTION IF EXISTS fn_btc_payout_guard();
DROP FUNCTION IF EXISTS fn_btc_wallet_mode_guard();

-- +goose StatementEnd
