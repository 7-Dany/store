-- +goose Up
-- +goose StatementBegin

/*
 * 020_btc_compliance_functions.sql — Triggers and functions for compliance tables.
 *
 * Functions defined here:
 *   fn_fatf_address_consistency() — enforces that fatf_travel_rule_records.counterparty_address
 *                                   matches the address on the referenced invoice
 *
 * Depends on: 019_btc_compliance.sql (fatf_travel_rule_records)
 *             012_btc_invoices.sql (invoice_addresses)
 * Continued in: 021_btc_webhooks.sql
 */

/* ═════════════════════════════════════════════════════════════
   FATF TRAVEL RULE ADDRESS CONSISTENCY
   ═════════════════════════════════════════════════════════════ */

/*
 * fn_fatf_address_consistency
 * ─────────────────────────────
 * Rejects INSERT on fatf_travel_rule_records when beneficiary_address does not
 * match payout_records.destination_address for the referenced payout.
 *
 * The original table comment notes this constraint is "application-enforced". For a
 * regulatory compliance obligation — where a mismatch is not just a code bug but a
 * Travel Rule filing error — a DB-level guard is warranted alongside the application
 * check. An address mismatch means the FATF record names a different address than the
 * one funds will actually be swept to; this cannot be corrected after broadcast.
 *
 * Platform-mode payouts (destination_address IS NULL) are skipped: no on-chain sweep
 * occurs and application logic is responsible for not generating FATF records for
 * platform-mode payouts in the first place.
 */
CREATE OR REPLACE FUNCTION fn_fatf_address_consistency()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
DECLARE
    v_payout_dest TEXT;
BEGIN
    SELECT destination_address INTO v_payout_dest
    FROM payout_records WHERE id = NEW.payout_record_id;

    -- Platform-mode payouts have no on-chain destination; skip the consistency check.
    -- Application logic must not create FATF records for platform-mode payouts.
    IF v_payout_dest IS NULL THEN
        RETURN NEW;
    END IF;

    IF NEW.beneficiary_address IS DISTINCT FROM v_payout_dest THEN
        RAISE EXCEPTION
            'fatf_travel_rule_records: beneficiary_address ''%'' does not match '
            'payout_records.destination_address ''%'' for payout %. '
            'Always copy beneficiary_address from the payout record snapshot — '
            'a mismatch means the Travel Rule filing names a different address '
            'than the one funds will be swept to.',
            NEW.beneficiary_address, v_payout_dest, NEW.payout_record_id
            USING ERRCODE = 'P0001';
    END IF;

    RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_fatf_address_consistency() IS
    'Rejects INSERT on fatf_travel_rule_records when beneficiary_address does not match '
    'payout_records.destination_address. Platform-mode payouts (NULL destination) are skipped. '
    'Promotes the application-level note to a DB-level guard: a mismatch after broadcast '
    'is a Travel Rule filing error that cannot be corrected.';

CREATE TRIGGER trg_fatf_address_consistency
    BEFORE INSERT ON fatf_travel_rule_records
    FOR EACH ROW EXECUTE FUNCTION fn_fatf_address_consistency();



-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS trg_fatf_address_consistency ON fatf_travel_rule_records;
DROP FUNCTION IF EXISTS fn_fatf_address_consistency();

-- +goose StatementEnd
