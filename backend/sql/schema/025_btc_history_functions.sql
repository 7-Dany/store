-- +goose Up
-- +goose StatementBegin

/*
 * 025_btc_history_functions.sql — History capture triggers.
 *
 * Functions defined here:
 *   fn_tier_config_history() — captures btc_tier_config changes into btc_tier_config_history
 *   fn_vwc_history()         — captures vendor_wallet_config changes into vendor_wallet_config_history
 *
 * These functions are defined after 024_btc_history.sql because they INSERT into the
 * history tables defined there. The triggers are created ON tables from 010_btc_core.sql —
 * cross-file trigger creation is valid in PostgreSQL.
 *
 * Depends on: 024_btc_history.sql (history tables)
 *             010_btc_core.sql (btc_tier_config, vendor_wallet_config)
 * Continued in: 025_btc_grants.sql
 */

/* ═════════════════════════════════════════════════════════════
   TIER CONFIG HISTORY (ARCH-07)
   ═════════════════════════════════════════════════════════════ */

CREATE OR REPLACE FUNCTION fn_tier_config_history()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
BEGIN
    INSERT INTO btc_tier_config_history
        (tier_id, changed_by, changed_by_label, old_values, new_values)
    VALUES (
        OLD.id,
        NULLIF(current_setting('app.current_actor_id', TRUE), '')::UUID,
        COALESCE(current_setting('app.current_actor_label', TRUE), 'system'),
        row_to_json(OLD)::JSONB,
        row_to_json(NEW)::JSONB
    );
    RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_tier_config_history() IS
    'Writes btc_tier_config_history on every UPDATE to btc_tier_config. '
    'Captures full before/after snapshot. Application sets app.current_actor_id '
    'and app.current_actor_label session variables before making config changes.';

CREATE TRIGGER trg_tier_config_history
    AFTER UPDATE ON btc_tier_config
    FOR EACH ROW EXECUTE FUNCTION fn_tier_config_history();


/* ═════════════════════════════════════════════════════════════
   VENDOR WALLET CONFIG HISTORY (ARCH-08)
   ═════════════════════════════════════════════════════════════ */

/*
 * fn_vwc_history
 * ──────────────
 * Writes individual field-level history rows to vendor_wallet_config_history
 * when key fields change. Tracks changes to bridge_destination_address,
 * wallet_mode, tier_id, suspended, and kyc_status.
 *
 * One row per changed field — enables targeted queries like
 * "show all address changes for vendor X" without parsing JSON.
 */
CREATE OR REPLACE FUNCTION fn_vwc_history()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
DECLARE
    v_changed_by       UUID;
    v_changed_by_label TEXT;
BEGIN
    v_changed_by       := NULLIF(current_setting('app.current_actor_id', TRUE), '')::UUID;
    v_changed_by_label := COALESCE(current_setting('app.current_actor_label', TRUE), 'system');

    IF OLD.bridge_destination_address IS DISTINCT FROM NEW.bridge_destination_address THEN
        INSERT INTO vendor_wallet_config_history
            (vendor_id, network, changed_by, changed_by_label, field_name, old_value, new_value)
        VALUES (
            NEW.vendor_id, NEW.network, v_changed_by, v_changed_by_label,
            'bridge_destination_address',
            OLD.bridge_destination_address,
            NEW.bridge_destination_address
        );
    END IF;

    IF OLD.wallet_mode IS DISTINCT FROM NEW.wallet_mode THEN
        INSERT INTO vendor_wallet_config_history
            (vendor_id, network, changed_by, changed_by_label, field_name, old_value, new_value)
        VALUES (
            NEW.vendor_id, NEW.network, v_changed_by, v_changed_by_label,
            'wallet_mode',
            OLD.wallet_mode::TEXT,
            NEW.wallet_mode::TEXT
        );
    END IF;

    IF OLD.tier_id IS DISTINCT FROM NEW.tier_id THEN
        INSERT INTO vendor_wallet_config_history
            (vendor_id, network, changed_by, changed_by_label, field_name, old_value, new_value)
        VALUES (
            NEW.vendor_id, NEW.network, v_changed_by, v_changed_by_label,
            'tier_id',
            OLD.tier_id::TEXT,
            NEW.tier_id::TEXT
        );
    END IF;

    IF OLD.suspended IS DISTINCT FROM NEW.suspended THEN
        INSERT INTO vendor_wallet_config_history
            (vendor_id, network, changed_by, changed_by_label, field_name, old_value, new_value)
        VALUES (
            NEW.vendor_id, NEW.network, v_changed_by, v_changed_by_label,
            'suspended',
            OLD.suspended::TEXT,
            NEW.suspended::TEXT
        );
    END IF;

    IF OLD.kyc_status IS DISTINCT FROM NEW.kyc_status THEN
        INSERT INTO vendor_wallet_config_history
            (vendor_id, network, changed_by, changed_by_label, field_name, old_value, new_value)
        VALUES (
            NEW.vendor_id, NEW.network, v_changed_by, v_changed_by_label,
            'kyc_status',
            OLD.kyc_status::TEXT,
            NEW.kyc_status::TEXT
        );
    END IF;

    -- Track auto_sweep_threshold_sat changes (MED-1):
    -- Threshold changes directly affect hybrid-mode sweep behaviour. Without this,
    -- a dispute over a missed auto-sweep cannot be resolved using DB evidence alone.
    IF OLD.auto_sweep_threshold_sat IS DISTINCT FROM NEW.auto_sweep_threshold_sat THEN
        INSERT INTO vendor_wallet_config_history
            (vendor_id, network, changed_by, changed_by_label, field_name, old_value, new_value)
        VALUES (
            NEW.vendor_id, NEW.network, v_changed_by, v_changed_by_label,
            'auto_sweep_threshold_sat',
            OLD.auto_sweep_threshold_sat::TEXT,
            NEW.auto_sweep_threshold_sat::TEXT
        );
    END IF;

    RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_vwc_history() IS
    'Writes field-level history rows to vendor_wallet_config_history on key field changes. '
    'Tracked fields: bridge_destination_address, wallet_mode, tier_id, suspended, '
    'kyc_status, auto_sweep_threshold_sat. '
    'One row per changed field for targeted historical queries. '
    'changed_by_label is populated from app.current_actor_label for identity preservation.';

CREATE TRIGGER trg_vwc_history
    AFTER UPDATE ON vendor_wallet_config
    FOR EACH ROW EXECUTE FUNCTION fn_vwc_history();



-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS trg_vwc_history          ON vendor_wallet_config;
DROP TRIGGER IF EXISTS trg_tier_config_history  ON btc_tier_config;

DROP FUNCTION IF EXISTS fn_vwc_history();
DROP FUNCTION IF EXISTS fn_tier_config_history();

-- +goose StatementEnd
