-- +goose Up
-- +goose StatementBegin

/*
 * 019_btc_financial_mutations.sql — Audited mutation procedures for vendor and platform balances.
 *
 * This migration closes the remaining unaudited financial write paths:
 *   - vendor balance credit/debit now write immutable financial_audit_events rows
 *     inside the stored procedure itself
 *   - platform treasury reserve gets the same stored-procedure + immutable-audit path
 *   - btc_app_role can no longer UPDATE platform_config.treasury_reserve_satoshis directly
 *
 * Depends on: 017_btc_audit.sql, 018_btc_audit_functions.sql, 010_btc_core.sql
 * Continued in: 020_btc_compliance.sql
 */

/* ═════════════════════════════════════════════════════════════
   INTERNAL AUDIT APPEND HELPER
   ═════════════════════════════════════════════════════════════ */

/*
 * fn_btc_append_financial_audit_event
 * ───────────────────────────────────
 * Internal append-only helper for financial_audit_events.
 *
 * Design intent:
 *   - every financial state mutation writes its audit row in the same transaction
 *   - callers do not get a separate "remember to audit" step
 *   - any audit validation failure aborts the balance mutation itself
 *
 * This function is intentionally narrow and side-effect free beyond the single INSERT.
 * The mutation procedures below own all business validation and pass the fully-formed
 * before/after values into this helper after the locked state transition is computed.
 */
CREATE OR REPLACE FUNCTION fn_btc_append_financial_audit_event(
    p_event_type         TEXT,
    p_network            TEXT,
    p_actor_type         TEXT,
    p_actor_id           UUID,
    p_actor_label        TEXT,
    p_invoice_id         UUID,
    p_payout_record_id   UUID,
    p_references_event_id BIGINT,
    p_amount_sat         BIGINT,
    p_balance_before_sat BIGINT,
    p_balance_after_sat  BIGINT,
    p_fiat_equivalent    BIGINT,
    p_fiat_currency_code TEXT,
    p_rate_stale         BOOLEAN,
    p_metadata           JSONB
) RETURNS VOID
LANGUAGE plpgsql AS $fn$
BEGIN
    INSERT INTO financial_audit_events (
        event_type,
        network,
        actor_type,
        actor_id,
        actor_label,
        invoice_id,
        payout_record_id,
        references_event_id,
        amount_sat,
        balance_before_sat,
        balance_after_sat,
        fiat_equivalent,
        fiat_currency_code,
        rate_stale,
        metadata
    ) VALUES (
        p_event_type,
        p_network,
        p_actor_type,
        p_actor_id,
        p_actor_label,
        p_invoice_id,
        p_payout_record_id,
        p_references_event_id,
        p_amount_sat,
        p_balance_before_sat,
        p_balance_after_sat,
        p_fiat_equivalent,
        p_fiat_currency_code,
        COALESCE(p_rate_stale, FALSE),
        COALESCE(p_metadata, '{}'::jsonb)
    );
END;
$fn$;

COMMENT ON FUNCTION fn_btc_append_financial_audit_event(
    TEXT, TEXT, TEXT, UUID, TEXT, UUID, UUID, BIGINT, BIGINT, BIGINT, BIGINT, BIGINT, TEXT, BOOLEAN, JSONB
) IS
    'Internal helper for audited financial mutations. Inserts immutable '
    'financial_audit_events rows inside the same transaction as the state change. '
    'Any validation or immutability failure aborts the enclosing mutation.';


/* ═════════════════════════════════════════════════════════════
   VENDOR BALANCE MUTATIONS
   ═════════════════════════════════════════════════════════════ */

/*
 * btc_credit_balance
 * ──────────────────
 * Credits vendor_balances.balance_satoshis under row lock and writes the immutable
 * financial_audit_events row in the same transaction.
 *
 * Safety properties:
 *   - SELECT ... FOR UPDATE closes the concurrent-credit/debit race window
 *   - positive amount enforced at the DB boundary
 *   - invoice_id is required so every credit has a concrete business anchor
 *   - audit append happens after the new balance is computed, before commit
 *
 * This is now the only sanctioned DB write path for vendor balance credits.
 */
CREATE OR REPLACE FUNCTION btc_credit_balance(
    p_vendor_id           UUID,
    p_network             TEXT,
    p_amount              BIGINT,
    p_actor_type          TEXT,
    p_actor_id            UUID,
    p_actor_label         TEXT,
    p_invoice_id          UUID,
    p_payout_record_id    UUID DEFAULT NULL,
    p_references_event_id BIGINT DEFAULT NULL,
    p_fiat_equivalent     BIGINT DEFAULT NULL,
    p_fiat_currency_code  TEXT DEFAULT NULL,
    p_rate_stale          BOOLEAN DEFAULT FALSE,
    p_metadata            JSONB DEFAULT '{}'::jsonb,
    p_event_type          TEXT DEFAULT 'balance_credit'
) RETURNS BIGINT
LANGUAGE plpgsql AS $fn$
DECLARE
    v_before BIGINT;
    v_after  BIGINT;
BEGIN
    IF p_amount <= 0 THEN
        RAISE EXCEPTION
            'btc_credit_balance: amount must be positive, got %', p_amount;
    END IF;

    IF p_event_type <> 'balance_credit' THEN
        RAISE EXCEPTION
            'btc_credit_balance: unsupported event_type %; expected balance_credit.',
            p_event_type
            USING ERRCODE = 'P0001';
    END IF;

    IF p_invoice_id IS NULL THEN
        RAISE EXCEPTION
            'btc_credit_balance: invoice_id is required for balance_credit audit events.'
            USING ERRCODE = '23502';
    END IF;

    SELECT balance_satoshis
    INTO v_before
    FROM vendor_balances
    WHERE vendor_id = p_vendor_id
      AND network   = p_network
    FOR UPDATE;

    IF NOT FOUND THEN
        RAISE EXCEPTION
            'btc_credit_balance: no vendor_balances row for vendor % on %.',
            p_vendor_id, p_network;
    END IF;

    v_after := v_before + p_amount;

    UPDATE vendor_balances
    SET balance_satoshis = v_after
    WHERE vendor_id = p_vendor_id
      AND network   = p_network;

    PERFORM fn_btc_append_financial_audit_event(
        p_event_type,
        p_network,
        p_actor_type,
        p_actor_id,
        p_actor_label,
        p_invoice_id,
        p_payout_record_id,
        p_references_event_id,
        p_amount,
        v_before,
        v_after,
        p_fiat_equivalent,
        p_fiat_currency_code,
        p_rate_stale,
        p_metadata
    );

    RETURN v_after;
END;
$fn$;

COMMENT ON FUNCTION btc_credit_balance(
    UUID, TEXT, BIGINT, TEXT, UUID, TEXT, UUID, UUID, BIGINT, BIGINT, TEXT, BOOLEAN, JSONB, TEXT
) IS
    'Credits vendor_balances.balance_satoshis under row lock and writes the '
    'immutable financial_audit_events row in the same transaction. '
    'There is no caller-managed post-step audit insertion to forget.';


/*
 * btc_debit_balance
 * ─────────────────
 * Debits vendor_balances.balance_satoshis under row lock and writes the immutable
 * financial_audit_events row in the same transaction.
 *
 * Safety properties:
 *   - SELECT ... FOR UPDATE serializes concurrent debits against the same row
 *   - positive amount enforced at the DB boundary
 *   - insufficient balance fails with SQLSTATE 23514
 *   - every debit must be anchored to invoice_id or payout_record_id
 *
 * This closes the old gap where callers could mutate balance first and forget the
 * corresponding immutable financial audit row.
 */
CREATE OR REPLACE FUNCTION btc_debit_balance(
    p_vendor_id           UUID,
    p_network             TEXT,
    p_amount              BIGINT,
    p_actor_type          TEXT,
    p_actor_id            UUID,
    p_actor_label         TEXT,
    p_invoice_id          UUID DEFAULT NULL,
    p_payout_record_id    UUID DEFAULT NULL,
    p_references_event_id BIGINT DEFAULT NULL,
    p_fiat_equivalent     BIGINT DEFAULT NULL,
    p_fiat_currency_code  TEXT DEFAULT NULL,
    p_rate_stale          BOOLEAN DEFAULT FALSE,
    p_metadata            JSONB DEFAULT '{}'::jsonb,
    p_event_type          TEXT DEFAULT 'balance_debit'
) RETURNS BIGINT
LANGUAGE plpgsql AS $fn$
DECLARE
    v_before BIGINT;
    v_after  BIGINT;
BEGIN
    IF p_amount <= 0 THEN
        RAISE EXCEPTION
            'btc_debit_balance: amount must be positive, got %', p_amount;
    END IF;

    IF p_event_type NOT IN ('balance_debit', 'subscription_debit') THEN
        RAISE EXCEPTION
            'btc_debit_balance: unsupported event_type %; expected balance_debit or subscription_debit.',
            p_event_type
            USING ERRCODE = 'P0001';
    END IF;

    IF p_invoice_id IS NULL AND p_payout_record_id IS NULL THEN
        RAISE EXCEPTION
            'btc_debit_balance: invoice_id or payout_record_id is required for debit audit anchoring.'
            USING ERRCODE = '23502';
    END IF;

    SELECT balance_satoshis
    INTO v_before
    FROM vendor_balances
    WHERE vendor_id = p_vendor_id
      AND network   = p_network
    FOR UPDATE;

    IF NOT FOUND THEN
        RAISE EXCEPTION
            'btc_debit_balance: no vendor_balances row for vendor % on %.',
            p_vendor_id, p_network;
    END IF;

    IF v_before < p_amount THEN
        RAISE EXCEPTION
            'btc_debit_balance: insufficient balance for vendor % on %. '
            'Available: %, requested: %.',
            p_vendor_id, p_network, v_before, p_amount
            USING ERRCODE = '23514';
    END IF;

    v_after := v_before - p_amount;

    UPDATE vendor_balances
    SET balance_satoshis = v_after
    WHERE vendor_id = p_vendor_id
      AND network   = p_network;

    PERFORM fn_btc_append_financial_audit_event(
        p_event_type,
        p_network,
        p_actor_type,
        p_actor_id,
        p_actor_label,
        p_invoice_id,
        p_payout_record_id,
        p_references_event_id,
        p_amount,
        v_before,
        v_after,
        p_fiat_equivalent,
        p_fiat_currency_code,
        p_rate_stale,
        p_metadata
    );

    RETURN v_after;
END;
$fn$;

COMMENT ON FUNCTION btc_debit_balance(
    UUID, TEXT, BIGINT, TEXT, UUID, TEXT, UUID, UUID, BIGINT, BIGINT, TEXT, BOOLEAN, JSONB, TEXT
) IS
    'Debits vendor_balances.balance_satoshis under row lock and writes the '
    'immutable financial_audit_events row in the same transaction. '
    'Raises SQLSTATE 23514 on insufficient balance.';


/* ═════════════════════════════════════════════════════════════
   PLATFORM TREASURY RESERVE MUTATIONS
   ═════════════════════════════════════════════════════════════ */

/*
 * btc_credit_treasury_reserve
 * ───────────────────────────
 * Credits platform_config.treasury_reserve_satoshis under row lock and writes the
 * immutable treasury financial audit event in the same transaction.
 *
 * This gives treasury reserve the same DB-enforced mutation boundary as vendor
 * balances: no caller-managed direct UPDATE and no caller-managed post-step audit.
 * payout_record_id is required because treasury increments are fee-retention events
 * tied to a concrete payout lifecycle transition.
 */
CREATE OR REPLACE FUNCTION btc_credit_treasury_reserve(
    p_network             TEXT,
    p_amount              BIGINT,
    p_actor_type          TEXT,
    p_actor_id            UUID,
    p_actor_label         TEXT,
    p_payout_record_id    UUID DEFAULT NULL,
    p_references_event_id BIGINT DEFAULT NULL,
    p_fiat_equivalent     BIGINT DEFAULT NULL,
    p_fiat_currency_code  TEXT DEFAULT NULL,
    p_rate_stale          BOOLEAN DEFAULT FALSE,
    p_metadata            JSONB DEFAULT '{}'::jsonb,
    p_event_type          TEXT DEFAULT 'treasury_increment'
) RETURNS BIGINT
LANGUAGE plpgsql AS $fn$
DECLARE
    v_before BIGINT;
    v_after  BIGINT;
BEGIN
    IF p_amount <= 0 THEN
        RAISE EXCEPTION
            'btc_credit_treasury_reserve: amount must be positive, got %', p_amount;
    END IF;

    IF p_event_type <> 'treasury_increment' THEN
        RAISE EXCEPTION
            'btc_credit_treasury_reserve: unsupported event_type %; expected treasury_increment.',
            p_event_type
            USING ERRCODE = 'P0001';
    END IF;

    IF p_payout_record_id IS NULL THEN
        RAISE EXCEPTION
            'btc_credit_treasury_reserve: payout_record_id is required for treasury_increment audit events.'
            USING ERRCODE = '23502';
    END IF;

    SELECT treasury_reserve_satoshis
    INTO v_before
    FROM platform_config
    WHERE network = p_network
    FOR UPDATE;

    IF NOT FOUND THEN
        RAISE EXCEPTION
            'btc_credit_treasury_reserve: no platform_config row for network %.',
            p_network;
    END IF;

    v_after := v_before + p_amount;

    UPDATE platform_config
    SET treasury_reserve_satoshis = v_after,
        updated_at                = NOW()
    WHERE network = p_network;

    PERFORM fn_btc_append_financial_audit_event(
        p_event_type,
        p_network,
        p_actor_type,
        p_actor_id,
        p_actor_label,
        NULL,
        p_payout_record_id,
        p_references_event_id,
        p_amount,
        v_before,
        v_after,
        p_fiat_equivalent,
        p_fiat_currency_code,
        p_rate_stale,
        p_metadata
    );

    RETURN v_after;
END;
$fn$;

COMMENT ON FUNCTION btc_credit_treasury_reserve(
    TEXT, BIGINT, TEXT, UUID, TEXT, UUID, BIGINT, BIGINT, TEXT, BOOLEAN, JSONB, TEXT
) IS
    'Credits platform_config.treasury_reserve_satoshis under row lock and writes '
    'the immutable treasury_increment financial audit event in the same transaction.';


/*
 * btc_debit_treasury_reserve
 * ──────────────────────────
 * Debits platform_config.treasury_reserve_satoshis under row lock and writes the
 * immutable treasury financial audit event in the same transaction.
 *
 * Safety properties:
 *   - SELECT ... FOR UPDATE serializes concurrent reserve adjustments
 *   - positive amount enforced at the DB boundary
 *   - insufficient reserve fails with SQLSTATE 23514
 *
 * This is the symmetric treasury-decrement path for refunds, reversals, or
 * corrective reserve adjustments that must remain visible in the permanent audit log.
 */
CREATE OR REPLACE FUNCTION btc_debit_treasury_reserve(
    p_network             TEXT,
    p_amount              BIGINT,
    p_actor_type          TEXT,
    p_actor_id            UUID,
    p_actor_label         TEXT,
    p_payout_record_id    UUID DEFAULT NULL,
    p_references_event_id BIGINT DEFAULT NULL,
    p_fiat_equivalent     BIGINT DEFAULT NULL,
    p_fiat_currency_code  TEXT DEFAULT NULL,
    p_rate_stale          BOOLEAN DEFAULT FALSE,
    p_metadata            JSONB DEFAULT '{}'::jsonb,
    p_event_type          TEXT DEFAULT 'treasury_decrement'
) RETURNS BIGINT
LANGUAGE plpgsql AS $fn$
DECLARE
    v_before BIGINT;
    v_after  BIGINT;
BEGIN
    IF p_amount <= 0 THEN
        RAISE EXCEPTION
            'btc_debit_treasury_reserve: amount must be positive, got %', p_amount;
    END IF;

    IF p_event_type <> 'treasury_decrement' THEN
        RAISE EXCEPTION
            'btc_debit_treasury_reserve: unsupported event_type %; expected treasury_decrement.',
            p_event_type
            USING ERRCODE = 'P0001';
    END IF;

    SELECT treasury_reserve_satoshis
    INTO v_before
    FROM platform_config
    WHERE network = p_network
    FOR UPDATE;

    IF NOT FOUND THEN
        RAISE EXCEPTION
            'btc_debit_treasury_reserve: no platform_config row for network %.',
            p_network;
    END IF;

    IF v_before < p_amount THEN
        RAISE EXCEPTION
            'btc_debit_treasury_reserve: insufficient treasury reserve on %. '
            'Available: %, requested: %.',
            p_network, v_before, p_amount
            USING ERRCODE = '23514';
    END IF;

    v_after := v_before - p_amount;

    UPDATE platform_config
    SET treasury_reserve_satoshis = v_after,
        updated_at                = NOW()
    WHERE network = p_network;

    PERFORM fn_btc_append_financial_audit_event(
        p_event_type,
        p_network,
        p_actor_type,
        p_actor_id,
        p_actor_label,
        NULL,
        p_payout_record_id,
        p_references_event_id,
        p_amount,
        v_before,
        v_after,
        p_fiat_equivalent,
        p_fiat_currency_code,
        p_rate_stale,
        p_metadata
    );

    RETURN v_after;
END;
$fn$;

COMMENT ON FUNCTION btc_debit_treasury_reserve(
    TEXT, BIGINT, TEXT, UUID, TEXT, UUID, BIGINT, BIGINT, TEXT, BOOLEAN, JSONB, TEXT
) IS
    'Debits platform_config.treasury_reserve_satoshis under row lock and writes '
    'the immutable treasury_decrement financial audit event in the same transaction. '
    'Raises SQLSTATE 23514 on insufficient reserve.';


-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP FUNCTION IF EXISTS btc_credit_treasury_reserve(TEXT, BIGINT, TEXT, UUID, TEXT, UUID, BIGINT, BIGINT, TEXT, BOOLEAN, JSONB, TEXT);
DROP FUNCTION IF EXISTS btc_debit_treasury_reserve(TEXT, BIGINT, TEXT, UUID, TEXT, UUID, BIGINT, BIGINT, TEXT, BOOLEAN, JSONB, TEXT);
DROP FUNCTION IF EXISTS btc_credit_balance(UUID, TEXT, BIGINT, TEXT, UUID, TEXT, UUID, UUID, BIGINT, BIGINT, TEXT, BOOLEAN, JSONB, TEXT);
DROP FUNCTION IF EXISTS btc_debit_balance(UUID, TEXT, BIGINT, TEXT, UUID, TEXT, UUID, UUID, BIGINT, BIGINT, TEXT, BOOLEAN, JSONB, TEXT);
DROP FUNCTION IF EXISTS fn_btc_append_financial_audit_event(TEXT, TEXT, TEXT, UUID, TEXT, UUID, UUID, BIGINT, BIGINT, BIGINT, BIGINT, BIGINT, TEXT, BOOLEAN, JSONB);

-- +goose StatementEnd
