-- +goose Up
-- +goose StatementBegin

/*
 * 018_btc_audit_functions.sql — Immutability guards and audit triggers.
 *
 * Functions defined here:
 *   fn_btc_audit_immutable()             — blocks UPDATE/DELETE on financial_audit_events
 *   fn_btc_audit_no_truncate()           — blocks TRUNCATE on financial_audit_events
 *   fn_fae_validate_actor_label()        — enforces actor_label format rules
 *   fn_ops_audit_platform_config()       — writes ops_audit_log on platform_config changes
 *   fn_ops_audit_vendor_tier_overrides() — writes ops_audit_log on override changes
 *
 * Note: fn_ops_audit_* triggers are created ON tables defined in 010_btc_core.sql.
 * Cross-file trigger creation is valid in PostgreSQL: the function + trigger
 * are defined here (in the audit file) because they write to ops_audit_log,
 * which must exist before these triggers can fire.
 *
 * Depends on: 017_btc_audit.sql (financial_audit_events, ops_audit_log)
 *             010_btc_core.sql (platform_config, vendor_tier_overrides)
 * Continued in: 019_btc_compliance.sql
 */

/* ═════════════════════════════════════════════════════════════
   AUDIT IMMUTABILITY
   ═════════════════════════════════════════════════════════════ */

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
    'regardless of the DB user. TRUNCATE handled by fn_btc_audit_no_truncate.';

CREATE TRIGGER trg_fae_no_update
    BEFORE UPDATE ON financial_audit_events
    FOR EACH ROW EXECUTE FUNCTION fn_btc_audit_immutable();

CREATE TRIGGER trg_fae_no_delete
    BEFORE DELETE ON financial_audit_events
    FOR EACH ROW EXECUTE FUNCTION fn_btc_audit_immutable();


CREATE OR REPLACE FUNCTION fn_btc_audit_no_truncate()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
BEGIN
    RAISE EXCEPTION
        'financial_audit_events is immutable: TRUNCATE is not permitted. '
        'This table is an append-only financial audit trail and must never be truncated. '
        'Contact the compliance officer if a truncation is believed to be warranted.'
        USING ERRCODE = 'P0001';
END;
$fn$;

COMMENT ON FUNCTION fn_btc_audit_no_truncate() IS
    'Rejects TRUNCATE on financial_audit_events. '
    'Closes the immutability gap left by row-level UPDATE/DELETE triggers.';

CREATE TRIGGER trg_fae_no_truncate
    BEFORE TRUNCATE ON financial_audit_events
    FOR EACH STATEMENT EXECUTE FUNCTION fn_btc_audit_no_truncate();


/* ═════════════════════════════════════════════════════════════
   ACTOR LABEL VALIDATION (SEC-05)
   ═════════════════════════════════════════════════════════════ */

/*
 * fn_fae_validate_actor_label
 * ───────────────────────────
 * Verifies that actor_label matches users.email for the given actor_id at INSERT time.
 * Prevents falsely attributing actions to the wrong identity in the permanent audit trail.
 *
 * Rationale: nothing in the schema stops a bug (or malicious code) from inserting
 * actor_id = alice_uuid with actor_label = 'bob@company.com'. After alice's account is
 * deleted, actor_id becomes NULL and only the spoofed label remains permanently.
 *
 * This trigger fires BEFORE INSERT and rejects the row if the label doesn't match.
 * System actors (actor_type = 'system') are excluded — they have no users row.
 * Actors with NULL actor_id are excluded — the label alone is the identifier after deletion.
 */
CREATE OR REPLACE FUNCTION fn_fae_validate_actor_label()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
DECLARE
    v_expected_label TEXT;
BEGIN
    IF NEW.actor_id IS NULL OR NEW.actor_type = 'system' THEN
        RETURN NEW;
    END IF;

    -- Use COALESCE(email, username) so OAuth-only accounts that have no email
    -- (users.email IS NULL) are matched against their username instead. Without this,
    -- v_expected_label would be NULL for any OAuth-only user and the trigger would
    -- always raise a false-positive spoofing exception for those actors.
    SELECT COALESCE(email, username) INTO v_expected_label
    FROM users WHERE id = NEW.actor_id;

    IF v_expected_label IS NULL THEN
        RAISE EXCEPTION
            'financial_audit_events: actor_id % does not reference a known user, '
            'or the user has neither email nor username set. '
            'Possible actor spoofing or stale reference.',
            NEW.actor_id;
    END IF;

    IF v_expected_label IS DISTINCT FROM NEW.actor_label THEN
        RAISE EXCEPTION
            'financial_audit_events: actor_label ''%'' does not match '
            'COALESCE(email, username) ''%'' for actor_id %. Possible actor spoofing.',
            NEW.actor_label, v_expected_label, NEW.actor_id;
    END IF;

    RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_fae_validate_actor_label() IS
    'Rejects INSERT on financial_audit_events when actor_label does not match '
    'COALESCE(users.email, users.username) for the given actor_id. '
    'COALESCE handles OAuth-only accounts that have no email address set. '
    'Prevents identity spoofing in the audit trail. '
    'Skips validation for system actors and NULL actor_id rows.';

CREATE TRIGGER trg_fae_validate_actor
    BEFORE INSERT ON financial_audit_events
    FOR EACH ROW EXECUTE FUNCTION fn_fae_validate_actor_label();


/* ═════════════════════════════════════════════════════════════
   OPS AUDIT — PLATFORM CONFIG (ARCH-02)
   ═════════════════════════════════════════════════════════════ */

/*
 * fn_ops_audit_platform_config
 * ────────────────────────────
 * Writes to ops_audit_log on every platform_config UPDATE.
 * Ensures sweep_hold_mode toggles, treasury adjustments, and legal flag changes
 * leave a DB-level trace of who did it, when, and what changed.
 */
CREATE OR REPLACE FUNCTION fn_ops_audit_platform_config()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
BEGIN
    INSERT INTO ops_audit_log
        (actor_id, actor_label, operation, table_name, record_id, old_values, new_values)
    VALUES (
        NULLIF(current_setting('app.current_actor_id', TRUE), '')::UUID,
        COALESCE(current_setting('app.current_actor_label', TRUE), 'system'),
        'platform_config_update',
        'platform_config',
        NEW.network,
        row_to_json(OLD)::JSONB,
        row_to_json(NEW)::JSONB
    );
    RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_ops_audit_platform_config() IS
    'Writes ops_audit_log on every platform_config UPDATE. '
    'Ensures sweep_hold_mode toggles and legal flag changes leave a DB audit trace.';

CREATE TRIGGER trg_platform_config_ops_audit
    AFTER UPDATE ON platform_config
    FOR EACH ROW EXECUTE FUNCTION fn_ops_audit_platform_config();


/* ═════════════════════════════════════════════════════════════
   VENDOR TIER OVERRIDES OPS AUDIT
   ═════════════════════════════════════════════════════════════ */

/*
 * fn_ops_audit_vendor_tier_overrides
 * ────────────────────────────────────
 * Writes to ops_audit_log on every INSERT, UPDATE, and DELETE on vendor_tier_overrides.
 *
 * Both btc_tier_config and vendor_wallet_config have dedicated history tables, but
 * vendor_tier_overrides previously had no audit trail. Without this, a fee-rate override
 * changed during a dispute or audit window makes the question "what effective rules
 * applied to vendor X in March?" unanswerable.
 *
 * A separate vendor_tier_overrides_history table was considered but ops_audit_log is
 * sufficient: the row is narrow, changes are infrequent, and the full before/after
 * JSONB snapshot provides equivalent auditability without an additional table.
 *
 * record_id stores "vendor_id:network" as the composite PK identifier because this
 * table has no surrogate UUID primary key.
 */
CREATE OR REPLACE FUNCTION fn_ops_audit_vendor_tier_overrides()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
BEGIN
    INSERT INTO ops_audit_log
        (actor_id, actor_label, operation, table_name, record_id, old_values, new_values)
    VALUES (
        NULLIF(current_setting('app.current_actor_id', TRUE), '')::UUID,
        COALESCE(current_setting('app.current_actor_label', TRUE), 'system'),
        CASE TG_OP
            WHEN 'INSERT' THEN 'override_created'
            WHEN 'UPDATE' THEN 'override_updated'
            WHEN 'DELETE' THEN 'override_deleted'
        END,
        'vendor_tier_overrides',
        -- Composite PK (vendor_id, network) encoded as "vendor_id:network" for record_id.
        COALESCE(NEW.vendor_id, OLD.vendor_id)::TEXT || ':' || COALESCE(NEW.network, OLD.network),
        CASE WHEN TG_OP = 'INSERT' THEN NULL ELSE row_to_json(OLD)::JSONB END,
        CASE WHEN TG_OP = 'DELETE' THEN NULL ELSE row_to_json(NEW)::JSONB END
    );
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_ops_audit_vendor_tier_overrides() IS
    'Writes ops_audit_log on every INSERT/UPDATE/DELETE to vendor_tier_overrides. '
    'Closes the audit gap: override changes (discounted fee rates, extended expiry windows, etc.) '
    'are now traceable for dispute resolution and compliance period reviews.';

CREATE TRIGGER trg_vendor_tier_overrides_ops_audit
    AFTER INSERT OR UPDATE OR DELETE ON vendor_tier_overrides
    FOR EACH ROW EXECUTE FUNCTION fn_ops_audit_vendor_tier_overrides();



-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS trg_vendor_tier_overrides_ops_audit ON vendor_tier_overrides;
DROP TRIGGER IF EXISTS trg_platform_config_ops_audit       ON platform_config;
DROP TRIGGER IF EXISTS trg_fae_validate_actor              ON financial_audit_events;
DROP TRIGGER IF EXISTS trg_fae_no_truncate                 ON financial_audit_events;
DROP TRIGGER IF EXISTS trg_fae_no_delete                   ON financial_audit_events;
DROP TRIGGER IF EXISTS trg_fae_no_update                   ON financial_audit_events;

DROP FUNCTION IF EXISTS fn_ops_audit_vendor_tier_overrides();
DROP FUNCTION IF EXISTS fn_ops_audit_platform_config();
DROP FUNCTION IF EXISTS fn_fae_validate_actor_label();
DROP FUNCTION IF EXISTS fn_btc_audit_no_truncate();
DROP FUNCTION IF EXISTS fn_btc_audit_immutable();

-- +goose StatementEnd
