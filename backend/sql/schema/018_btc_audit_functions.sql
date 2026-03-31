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
 * Continued in: 019_btc_financial_mutations.sql
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
 * Validates actor_label on INSERT to financial_audit_events.
 * Prevents falsely attributing actions to the wrong identity in the permanent audit trail.
 *
 * HIGH-1 / GDPR compliance:
 *   financial_audit_events is immutable (DELETE blocked by trigger). Storing a raw email
 *   address in actor_label means that data can NEVER be erased for GDPR Article 17
 *   requests. The application MUST store HMAC-SHA256(email, server_secret) instead.
 *
 * This trigger supports two validation modes:
 *
 *   MODE A — HMAC mode (REQUIRED for GDPR compliance):
 *     actor_label is exactly 64 lowercase hex characters — the output of
 *     HMAC-SHA256(COALESCE(email, username), server_secret).
 *     The DB cannot verify the HMAC (no access to server_secret), so we verify only
 *     that actor_id references a live user row. The application layer is responsible
 *     for computing the HMAC correctly. This is the mandated production mode.
 *
 *   MODE B — raw-label mode (legacy / dev only, NOT GDPR-safe):
 *     actor_label is compared directly against COALESCE(users.email, users.username).
 *     This mode is only permitted during initial development before HMAC is implemented.
 *     It MUST NOT be used in production systems subject to GDPR.
 *
 * Detection: if actor_label is exactly 64 hex chars, Mode A is assumed; otherwise Mode B.
 *
 * FOR SHARE: acquires a shared lock on the user row to prevent concurrent
 * deletion or email change from racing with this validation (HIGH-2 fix).
 *
 * System actors (actor_type = 'system') and NULL actor_id rows are skipped.
 */
CREATE OR REPLACE FUNCTION fn_fae_validate_actor_label()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
DECLARE
    v_expected_label TEXT;
    v_is_hmac_format  BOOLEAN;
BEGIN
    IF NEW.actor_id IS NULL OR NEW.actor_type = 'system' THEN
        RETURN NEW;
    END IF;

    -- Detect HMAC mode: exactly 64 lowercase hex characters (SHA-256 output length).
    -- HIGH-1: production deployments MUST use HMAC to satisfy GDPR Article 17.
    v_is_hmac_format := (length(NEW.actor_label) = 64
                         AND NEW.actor_label ~ '^[0-9a-f]{64}');

    -- FOR SHARE: prevents concurrent user deletion or email change from causing
    -- a false-positive rejection of a legitimate audit event (HIGH-2 fix).
    SELECT COALESCE(email, username) INTO v_expected_label
    FROM users WHERE id = NEW.actor_id
    FOR SHARE;

    IF v_expected_label IS NULL THEN
        RAISE EXCEPTION
            'financial_audit_events: actor_id % does not reference a known user, '
            'or the user has neither email nor username set. '
            'Possible actor spoofing or stale reference.',
            NEW.actor_id;
    END IF;

    IF v_is_hmac_format THEN
        -- Mode A (HMAC): DB cannot verify HMAC without server_secret.
        -- We have confirmed actor_id is a live user; label format is correct.
        -- Application layer is responsible for computing HMAC correctly.
        RETURN NEW;
    END IF;

    -- Mode B (raw label): compare directly. Used only during development.
    -- IMPORTANT: this path is NOT GDPR-safe. Migrate to HMAC before production.
    IF v_expected_label IS DISTINCT FROM NEW.actor_label THEN
        RAISE EXCEPTION
            'financial_audit_events: actor_label ''%'' does not match '
            'COALESCE(email, username) ''%'' for actor_id %. '
            'PRODUCTION: actor_label must be HMAC-SHA256(email, server_secret) as a 64-char '
            'lowercase hex string. Raw email storage violates GDPR Article 17 on this '
            'immutable table. See HIGH-1 / COMP-02 in the audit report.',
            NEW.actor_label, v_expected_label, NEW.actor_id;
    END IF;

    RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_fae_validate_actor_label() IS
    'HIGH-1 / SEC-05: validates actor_label on INSERT to financial_audit_events. '
    'Supports two modes: '
    '(A) HMAC mode [REQUIRED for GDPR]: actor_label = HMAC-SHA256(email, server_secret) '
    '    as a 64-char lowercase hex string. DB verifies actor_id exists; HMAC correctness '
    '    is the application''s responsibility. This is the only GDPR-compliant mode since '
    '    financial_audit_events is immutable and raw emails cannot be erased. '
    '(B) Raw-label mode [DEV ONLY, NOT GDPR-safe]: actor_label matched directly against '
    '    COALESCE(users.email, users.username). Must not be used in production. '
    'Mode detection: exactly 64 lowercase hex chars triggers Mode A, anything else triggers Mode B. '
    'FOR SHARE on users row prevents concurrent deletion/email-change race (HIGH-2 fix). '
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
