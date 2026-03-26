-- +goose Up
-- +goose StatementBegin

/*
 * 013_btc_invoices_functions.sql — Triggers and functions for invoice tables.
 *
 * Functions defined here:
 *   fn_iam_address_consistency() — enforces that invoice_address_monitoring rows
 *                                  reference only the address belonging to their invoice
 *
 * Depends on: 012_btc_invoices.sql
 * Continued in: 014_btc_infrastructure.sql
 */

/* ═════════════════════════════════════════════════════════════
   INVOICE ADDRESS MONITORING CONSISTENCY (ARCH-04)
   ═════════════════════════════════════════════════════════════ */

/*
 * fn_iam_address_consistency
 * ──────────────────────────
 * Rejects INSERT on invoice_address_monitoring when the address or network
 * does not match the invoice_addresses row for the same invoice_id.
 *
 * Without this, a bug in the two-step INSERT sequence could register a different
 * address in the ZMQ watch list than the one actually allocated to the invoice.
 * Payments would arrive at the correct address but the ZMQ event would not match.
 */
CREATE OR REPLACE FUNCTION fn_iam_address_consistency()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
DECLARE
    v_expected_address TEXT;
    v_expected_network TEXT;
BEGIN
    SELECT address, network
    INTO v_expected_address, v_expected_network
    FROM invoice_addresses
    WHERE invoice_id = NEW.invoice_id;

    IF v_expected_address IS NULL THEN
        RAISE EXCEPTION
            'invoice_address_monitoring: no invoice_addresses row found for invoice %. '
            'Address must be allocated before monitoring is registered.',
            NEW.invoice_id;
    END IF;

    IF NEW.address != v_expected_address OR NEW.network != v_expected_network THEN
        RAISE EXCEPTION
            'invoice_address_monitoring: address/network (''%'', ''%'') does not match '
            'invoice_addresses (''%'', ''%'') for invoice %. '
            'ZMQ watch address must match the invoice-allocated address.',
            NEW.address, NEW.network,
            v_expected_address, v_expected_network,
            NEW.invoice_id
            USING ERRCODE = 'P0001';
    END IF;

    RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_iam_address_consistency() IS
    'Rejects INSERT on invoice_address_monitoring when address/network does not match '
    'invoice_addresses for the same invoice. Prevents ZMQ subscriber from watching '
    'a different address than the one allocated to the invoice.';

CREATE TRIGGER trg_iam_address_consistency
    BEFORE INSERT ON invoice_address_monitoring
    FOR EACH ROW EXECUTE FUNCTION fn_iam_address_consistency();



-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS trg_iam_address_consistency ON invoice_address_monitoring;
DROP FUNCTION IF EXISTS fn_iam_address_consistency();

-- +goose StatementEnd
