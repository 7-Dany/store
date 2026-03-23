-- +goose Up
-- +goose StatementBegin

/*
 * 011_btc_functions.sql — Bitcoin payment system functions, triggers, grants, and storage settings.
 *
 * Depends on:
 *   009_btc.sql        — all core BTC tables and ENUMs
 *   010_btc_payouts.sql — payout_records, financial_audit_events, compliance tables
 *
 * Functions defined here:
 *
 *   ── Audit immutability (financial_audit_events) ──────────────────────────────
 *   fn_btc_audit_immutable     — rejects UPDATE and DELETE on financial_audit_events
 *   fn_btc_audit_no_truncate   — rejects TRUNCATE on financial_audit_events
 *
 *   ── Payout guards ────────────────────────────────────────────────────────────
 *   fn_btc_payout_guard        — rejects INSERT when parent invoice.status != 'settled'
 *   fn_pr_vendor_consistency   — rejects INSERT when vendor_id != invoice.vendor_id (SEC-06)
 *   fn_pr_destination_consistency — rejects INSERT when destination_address doesn't match
 *                                   the invoice snapshot (ARCH-05)
 *   fn_pr_status_guard         — enforces payout status transition matrix (ARCH-06)
 *
 *   ── Wallet and balance guards ────────────────────────────────────────────────
 *   fn_btc_wallet_mode_guard   — blocks wallet_mode changes while balance > 0
 *   btc_credit_balance         — stored procedure for balance credits (enforces FOR UPDATE)
 *   btc_debit_balance          — stored procedure for balance debits (enforces FOR UPDATE)
 *
 *   ── Invoice address integrity ────────────────────────────────────────────────
 *   fn_iam_address_consistency — rejects INSERT when monitoring address != invoice_addresses (ARCH-04)
 *
 *   ── Tier-role synchronisation ────────────────────────────────────────────────
 *   fn_sync_vendor_tier_role   — syncs user_roles when vendor tier_id changes
 *
 *   ── History and ops audit ────────────────────────────────────────────────────
 *   fn_tier_config_history     — writes btc_tier_config_history on UPDATE (ARCH-07)
 *   fn_vwc_history             — writes vendor_wallet_config_history on key field changes (ARCH-08)
 *   fn_ops_audit_platform_config — writes ops_audit_log on platform_config UPDATE (ARCH-02)
 *
 *   ── Actor label validation ───────────────────────────────────────────────────
 *   fn_fae_validate_actor_label — verifies actor_label matches users.email for actor_id (SEC-05)
 *
 *   ── Storage and maintenance ──────────────────────────────────────────────────
 *   Autovacuum settings for invoices and payout_records
 *   GRANT statements (conditional on btc_app_role existence)
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


/*
 * fn_pr_status_guard (ARCH-06)
 * ────────────────────────────
 * Enforces the payout_records status transition matrix at the DB level.
 * Prevents confirmed payouts from being re-queued (double-sweep risk) or
 * any other invalid regression through the state machine.
 *
 * Permitted transitions:
 *   held         → queued, failed, manual_payout
 *   queued       → constructing, failed, manual_payout
 *   constructing → broadcast, queued (watchdog reclaim), failed
 *   broadcast    → confirmed, failed, refunded
 *   failed       → queued (admin retry), manual_payout
 *   confirmed    → (terminal — no transitions permitted)
 *   refunded     → (terminal)
 *   manual_payout → (terminal)
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
     OR (OLD.status = 'broadcast'    AND NEW.status IN ('confirmed', 'failed', 'refunded'))
     OR (OLD.status = 'failed'       AND NEW.status IN ('queued', 'manual_payout'))
    ) THEN
        RAISE EXCEPTION
            'payout_records: invalid status transition % → % for payout %. '
            'Confirmed, refunded, and manual_payout are terminal states.',
            OLD.status, NEW.status, OLD.id
            USING ERRCODE = 'P0001';
    END IF;

    RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_pr_status_guard() IS
    'Enforces payout_records status transition matrix. '
    'Prevents confirmed payouts from regressing to queued (double-sweep risk). '
    'Terminal states: confirmed, refunded, manual_payout.';

CREATE TRIGGER trg_pr_status_guard
    BEFORE UPDATE OF status ON payout_records
    FOR EACH ROW EXECUTE FUNCTION fn_pr_status_guard();


/* ═════════════════════════════════════════════════════════════
   WALLET MODE GUARD
   ═════════════════════════════════════════════════════════════ */

/*
 * fn_btc_wallet_mode_guard
 * ────────────────────────
 * Blocks wallet_mode changes while vendor_balances.balance_satoshis > 0.
 * Prevents reconciliation drift: balance_satoshis is value-bearing only for platform mode.
 */
CREATE OR REPLACE FUNCTION fn_btc_wallet_mode_guard()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
DECLARE
    v_balance BIGINT;
BEGIN
    IF NEW.wallet_mode = OLD.wallet_mode THEN
        RETURN NEW;
    END IF;

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
    'Prevents reconciliation drift: balance_satoshis is value-bearing only for platform mode. '
    'Uses FOR SHARE to close the concurrent-increment race window.';

CREATE TRIGGER trg_vendor_wallet_config_mode_guard
    BEFORE UPDATE OF wallet_mode ON vendor_wallet_config
    FOR EACH ROW EXECUTE FUNCTION fn_btc_wallet_mode_guard();


/* ═════════════════════════════════════════════════════════════
   BALANCE STORED PROCEDURES (SEC-08)
   ═════════════════════════════════════════════════════════════ */

/*
 * btc_credit_balance / btc_debit_balance
 * ────────────────────────────────────────
 * All vendor balance mutations MUST go through these procedures.
 * They perform SELECT FOR UPDATE internally, making it impossible for the caller
 * to accidentally skip the lock (the TOCTOU race that was previously only documented
 * in comments).
 *
 * Direct UPDATE on vendor_balances is revoked from btc_app_role (see GRANTS below).
 * These procedures are the only permitted write path.
 *
 * The caller is still responsible for writing the corresponding financial_audit_events
 * row in the same transaction, as the application layer has the full event context.
 * btc_credit_balance returns the new balance for the audit event's balance_after_sat.
 *
 * Raises ErrInsufficientBalance (SQLSTATE 23514 via CHECK) on debit below zero.
 * Raises an exception if no vendor_balances row exists for (p_vendor_id, p_network).
 */
CREATE OR REPLACE FUNCTION btc_credit_balance(
    p_vendor_id UUID,
    p_network   TEXT,
    p_amount    BIGINT
) RETURNS BIGINT LANGUAGE plpgsql AS $fn$
DECLARE
    v_new_balance BIGINT;
BEGIN
    IF p_amount <= 0 THEN
        RAISE EXCEPTION
            'btc_credit_balance: amount must be positive, got %', p_amount;
    END IF;

    UPDATE vendor_balances
    SET balance_satoshis = balance_satoshis + p_amount
    WHERE vendor_id = p_vendor_id AND network = p_network
    RETURNING balance_satoshis INTO v_new_balance;

    IF NOT FOUND THEN
        RAISE EXCEPTION
            'btc_credit_balance: no vendor_balances row for vendor % on %.',
            p_vendor_id, p_network;
    END IF;

    RETURN v_new_balance;
END;
$fn$;

COMMENT ON FUNCTION btc_credit_balance(UUID, TEXT, BIGINT) IS
    'Credits vendor_balances.balance_satoshis atomically with row-level lock. '
    'Returns new balance for use in financial_audit_events.balance_after_sat. '
    'Caller must write the audit event in the same transaction. '
    'Direct UPDATE on vendor_balances is revoked from btc_app_role.';


CREATE OR REPLACE FUNCTION btc_debit_balance(
    p_vendor_id UUID,
    p_network   TEXT,
    p_amount    BIGINT
) RETURNS BIGINT LANGUAGE plpgsql AS $fn$
DECLARE
    v_current   BIGINT;
    v_new_balance BIGINT;
BEGIN
    IF p_amount <= 0 THEN
        RAISE EXCEPTION
            'btc_debit_balance: amount must be positive, got %', p_amount;
    END IF;

    SELECT balance_satoshis INTO v_current
    FROM vendor_balances
    WHERE vendor_id = p_vendor_id AND network = p_network
    FOR UPDATE;

    IF NOT FOUND THEN
        RAISE EXCEPTION
            'btc_debit_balance: no vendor_balances row for vendor % on %.',
            p_vendor_id, p_network;
    END IF;

    IF v_current < p_amount THEN
        RAISE EXCEPTION
            'btc_debit_balance: insufficient balance for vendor % on %. '
            'Available: %, requested: %.',
            p_vendor_id, p_network, v_current, p_amount
            USING ERRCODE = '23514';  -- same as CHECK violation: ErrInsufficientBalance
    END IF;

    UPDATE vendor_balances
    SET balance_satoshis = balance_satoshis - p_amount
    WHERE vendor_id = p_vendor_id AND network = p_network
    RETURNING balance_satoshis INTO v_new_balance;

    RETURN v_new_balance;
END;
$fn$;

COMMENT ON FUNCTION btc_debit_balance(UUID, TEXT, BIGINT) IS
    'Debits vendor_balances.balance_satoshis atomically with row-level lock. '
    'Returns new balance for use in financial_audit_events.balance_after_sat. '
    'Raises SQLSTATE 23514 (ErrInsufficientBalance) when balance < amount. '
    'Caller must write the audit event in the same transaction.';


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


/* ═════════════════════════════════════════════════════════════
   TIER-ROLE SYNCHRONISATION
   ═════════════════════════════════════════════════════════════ */

/*
 * fn_sync_vendor_tier_role
 * ─────────────────────────
 * Synchronises user_roles when a vendor's tier_id changes on vendor_wallet_config.
 *
 * The design intent (from btc_tier_config.role_id) is: "assigning a vendor to a tier
 * also assigns them the tier's linked RBAC role." Without this trigger, a direct DB
 * UPDATE on vendor_wallet_config.tier_id would silently leave the user on the old role.
 *
 * Behaviour:
 *   - If the new tier has role_id: UPDATE user_roles SET role_id = new_role_id
 *     WHERE user_id = vendor_id (if a user_roles row exists).
 *   - If the new tier has no role_id: no role change (role stays as-is).
 *   - If no user_roles row exists: no action (role assignment is handled elsewhere).
 *   - Always writes to ops_audit_log for the role change.
 *
 * Note: this trigger fires AFTER UPDATE to avoid interfering with the wallet config
 * row itself. The role sync is a side effect, not a guard.
 */
CREATE OR REPLACE FUNCTION fn_sync_vendor_tier_role()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
DECLARE
    v_new_role_id   UUID;
    v_old_role_id   UUID;
BEGIN
    IF NEW.tier_id = OLD.tier_id THEN
        RETURN NEW;
    END IF;

    SELECT role_id INTO v_new_role_id
    FROM btc_tier_config WHERE id = NEW.tier_id;

    IF v_new_role_id IS NULL THEN
        RETURN NEW;
    END IF;

    SELECT role_id INTO v_old_role_id
    FROM user_roles WHERE user_id = NEW.vendor_id;

    IF NOT FOUND THEN
        RETURN NEW;
    END IF;

    UPDATE user_roles
    SET role_id = v_new_role_id,
        updated_at = NOW()
    WHERE user_id = NEW.vendor_id;

    -- Write ops_audit_log for the role change.
    INSERT INTO ops_audit_log
        (actor_label, operation, table_name, record_id, old_values, new_values, reason)
    VALUES (
        COALESCE(current_setting('app.current_actor_label', TRUE), 'system'),
        'tier_role_sync',
        'user_roles',
        NEW.vendor_id::TEXT,
        jsonb_build_object('role_id', v_old_role_id),
        jsonb_build_object('role_id', v_new_role_id),
        'Automatic role sync triggered by vendor tier change to ' || NEW.tier_id::TEXT
    );

    RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_sync_vendor_tier_role() IS
    'Syncs user_roles.role_id when vendor_wallet_config.tier_id changes. '
    'Fires AFTER UPDATE OF tier_id. Only acts when: new tier has a role_id AND '
    'a user_roles row exists for the vendor. Writes to ops_audit_log.';

CREATE TRIGGER trg_vwc_tier_role_sync
    AFTER UPDATE OF tier_id ON vendor_wallet_config
    FOR EACH ROW EXECUTE FUNCTION fn_sync_vendor_tier_role();


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
    v_changed_by UUID;
    v_changed_by_label TEXT;
BEGIN
    v_changed_by := NULLIF(current_setting('app.current_actor_id', TRUE), '')::UUID;
    v_changed_by_label := COALESCE(current_setting('app.current_actor_label', TRUE), 'system');

    IF OLD.bridge_destination_address IS DISTINCT FROM NEW.bridge_destination_address THEN
        INSERT INTO vendor_wallet_config_history
            (vendor_id, network, changed_by, field_name, old_value, new_value)
        VALUES (
            NEW.vendor_id, NEW.network, v_changed_by,
            'bridge_destination_address',
            OLD.bridge_destination_address,
            NEW.bridge_destination_address
        );
    END IF;

    IF OLD.wallet_mode IS DISTINCT FROM NEW.wallet_mode THEN
        INSERT INTO vendor_wallet_config_history
            (vendor_id, network, changed_by, field_name, old_value, new_value)
        VALUES (
            NEW.vendor_id, NEW.network, v_changed_by,
            'wallet_mode',
            OLD.wallet_mode::TEXT,
            NEW.wallet_mode::TEXT
        );
    END IF;

    IF OLD.tier_id IS DISTINCT FROM NEW.tier_id THEN
        INSERT INTO vendor_wallet_config_history
            (vendor_id, network, changed_by, field_name, old_value, new_value)
        VALUES (
            NEW.vendor_id, NEW.network, v_changed_by,
            'tier_id',
            OLD.tier_id::TEXT,
            NEW.tier_id::TEXT
        );
    END IF;

    IF OLD.suspended IS DISTINCT FROM NEW.suspended THEN
        INSERT INTO vendor_wallet_config_history
            (vendor_id, network, changed_by, field_name, old_value, new_value)
        VALUES (
            NEW.vendor_id, NEW.network, v_changed_by,
            'suspended',
            OLD.suspended::TEXT,
            NEW.suspended::TEXT
        );
    END IF;

    IF OLD.kyc_status IS DISTINCT FROM NEW.kyc_status THEN
        INSERT INTO vendor_wallet_config_history
            (vendor_id, network, changed_by, field_name, old_value, new_value)
        VALUES (
            NEW.vendor_id, NEW.network, v_changed_by,
            'kyc_status',
            OLD.kyc_status::TEXT,
            NEW.kyc_status::TEXT
        );
    END IF;

    RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_vwc_history() IS
    'Writes field-level history rows to vendor_wallet_config_history on key field changes. '
    'Tracked fields: bridge_destination_address, wallet_mode, tier_id, suspended, kyc_status. '
    'One row per changed field for targeted historical queries.';

CREATE TRIGGER trg_vwc_history
    AFTER UPDATE ON vendor_wallet_config
    FOR EACH ROW EXECUTE FUNCTION fn_vwc_history();


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


/* ═════════════════════════════════════════════════════════════
   AUTOVACUUM TUNING (IDX-13)
   ═════════════════════════════════════════════════════════════ */

/*
 * invoices and payout_records both have partial indexes on transient statuses
 * (detected, constructing) that accumulate dead tuples rapidly under load.
 * Aggressive autovacuum ensures index bloat is cleaned before it affects scan times.
 */
ALTER TABLE invoices SET (
    autovacuum_vacuum_scale_factor   = 0.01,
    autovacuum_analyze_scale_factor  = 0.005
);

ALTER TABLE payout_records SET (
    autovacuum_vacuum_scale_factor   = 0.01,
    autovacuum_analyze_scale_factor  = 0.005
);


/* ═════════════════════════════════════════════════════════════
   GRANTS
   ═════════════════════════════════════════════════════════════ */

/*
 * Minimum privilege grants for btc_app_role.
 * Conditional on btc_app_role existence — emits WARNING (not ERROR) so CI
 * migrations don't fail in environments where the role hasn't been created yet.
 *
 * Replace 'btc_app_role' with the actual application DB role name before deploying.
 *
 * Key security decisions encoded here:
 *   financial_audit_events: INSERT + SELECT only (no UPDATE/DELETE — trigger enforces this too)
 *   vendor_balances: SELECT only — all mutations must go through btc_credit_balance /
 *                    btc_debit_balance stored procedures (SEC-08)
 *
 * In production: verify with  \dp financial_audit_events  and  \dp vendor_balances
 * that privileges match what is documented here.
 *
 * See TODO-C in todo.md for the full DB role architecture (app_role, readonly_role, audit_role).
 */
DO $btc_grants$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'btc_app_role') THEN
        RAISE WARNING
            'btc_app_role does not exist. Replace ''btc_app_role'' in this migration '
            'with the actual application DB role name before deploying to production. '
            'Skipping GRANT statements. See TODO-C in todo.md for the full DB role architecture.';
    ELSE
        -- Revoke PUBLIC access from all BTC tables (SEC-01).
        EXECUTE 'REVOKE ALL ON btc_tier_config              FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON platform_config              FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON reconciliation_job_state     FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON bitcoin_sync_state           FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON vendor_wallet_config         FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON vendor_balances              FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON vendor_tier_overrides        FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON btc_exchange_rate_log        FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON invoices                     FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON invoice_addresses            FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON invoice_address_monitoring   FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON invoice_payments             FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON btc_outage_log               FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON bitcoin_block_history        FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON payout_records               FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON financial_audit_events       FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON wallet_backup_success        FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON btc_tier_config_history      FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON vendor_wallet_config_history FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON ops_audit_log                FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON reconciliation_run_history   FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON kyc_submissions              FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON sse_token_issuances          FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON webhook_deliveries           FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON btc_zmq_dead_letter          FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON dispute_records              FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON gdpr_erasure_requests        FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON fatf_travel_rule_records     FROM PUBLIC';

        -- App role: CRUD on all operational tables.
        EXECUTE 'GRANT SELECT, INSERT, UPDATE ON
            btc_tier_config, platform_config, reconciliation_job_state,
            bitcoin_sync_state, vendor_wallet_config, vendor_tier_overrides,
            btc_exchange_rate_log, invoices, invoice_addresses,
            invoice_address_monitoring, invoice_payments, btc_outage_log,
            bitcoin_block_history, payout_records, wallet_backup_success,
            btc_tier_config_history, vendor_wallet_config_history,
            ops_audit_log, reconciliation_run_history, kyc_submissions,
            sse_token_issuances, webhook_deliveries, btc_zmq_dead_letter,
            dispute_records, gdpr_erasure_requests, fatf_travel_rule_records
            TO btc_app_role';

        -- financial_audit_events: INSERT and SELECT only.
        EXECUTE 'GRANT INSERT, SELECT ON financial_audit_events TO btc_app_role';

        -- vendor_balances: SELECT only for app role (mutations via stored procedures only).
        EXECUTE 'GRANT SELECT ON vendor_balances TO btc_app_role';

        -- Grant stored procedure execution for balance mutations.
        EXECUTE 'GRANT EXECUTE ON FUNCTION btc_credit_balance(UUID, TEXT, BIGINT) TO btc_app_role';
        EXECUTE 'GRANT EXECUTE ON FUNCTION btc_debit_balance(UUID, TEXT, BIGINT) TO btc_app_role';
    END IF;
END;
$btc_grants$;


-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Drop triggers first, then functions.

-- Audit immutability
DROP TRIGGER IF EXISTS trg_fae_no_truncate                   ON financial_audit_events;
DROP TRIGGER IF EXISTS trg_fae_no_delete                     ON financial_audit_events;
DROP TRIGGER IF EXISTS trg_fae_no_update                     ON financial_audit_events;

-- Actor label validation
DROP TRIGGER IF EXISTS trg_fae_validate_actor                ON financial_audit_events;

-- Payout guards
DROP TRIGGER IF EXISTS trg_payout_records_guard              ON payout_records;
DROP TRIGGER IF EXISTS trg_pr_vendor_consistency             ON payout_records;
DROP TRIGGER IF EXISTS trg_pr_destination_consistency        ON payout_records;
DROP TRIGGER IF EXISTS trg_pr_status_guard                   ON payout_records;

-- Wallet mode guard
DROP TRIGGER IF EXISTS trg_vendor_wallet_config_mode_guard   ON vendor_wallet_config;

-- Invoice address monitoring consistency
DROP TRIGGER IF EXISTS trg_iam_address_consistency           ON invoice_address_monitoring;

-- Tier-role sync
DROP TRIGGER IF EXISTS trg_vwc_tier_role_sync                ON vendor_wallet_config;

-- History triggers
DROP TRIGGER IF EXISTS trg_tier_config_history               ON btc_tier_config;
DROP TRIGGER IF EXISTS trg_vwc_history                       ON vendor_wallet_config;

-- Ops audit triggers
DROP TRIGGER IF EXISTS trg_platform_config_ops_audit         ON platform_config;
DROP TRIGGER IF EXISTS trg_vendor_tier_overrides_ops_audit   ON vendor_tier_overrides;
DROP TRIGGER IF EXISTS trg_fatf_address_consistency          ON fatf_travel_rule_records;

-- Functions
DROP FUNCTION IF EXISTS fn_btc_audit_no_truncate();
DROP FUNCTION IF EXISTS fn_btc_audit_immutable();
DROP FUNCTION IF EXISTS fn_fae_validate_actor_label();
DROP FUNCTION IF EXISTS fn_btc_payout_guard();
DROP FUNCTION IF EXISTS fn_pr_vendor_consistency();
DROP FUNCTION IF EXISTS fn_pr_destination_consistency();
DROP FUNCTION IF EXISTS fn_pr_status_guard();
DROP FUNCTION IF EXISTS fn_btc_wallet_mode_guard();
DROP FUNCTION IF EXISTS btc_credit_balance(UUID, TEXT, BIGINT);
DROP FUNCTION IF EXISTS btc_debit_balance(UUID, TEXT, BIGINT);
DROP FUNCTION IF EXISTS fn_iam_address_consistency();
DROP FUNCTION IF EXISTS fn_sync_vendor_tier_role();
DROP FUNCTION IF EXISTS fn_tier_config_history();
DROP FUNCTION IF EXISTS fn_vwc_history();
DROP FUNCTION IF EXISTS fn_ops_audit_platform_config();
DROP FUNCTION IF EXISTS fn_ops_audit_vendor_tier_overrides();
DROP FUNCTION IF EXISTS fn_fatf_address_consistency();

-- Reset autovacuum to defaults.
ALTER TABLE IF EXISTS invoices      RESET (autovacuum_vacuum_scale_factor, autovacuum_analyze_scale_factor);
ALTER TABLE IF EXISTS payout_records RESET (autovacuum_vacuum_scale_factor, autovacuum_analyze_scale_factor);

-- +goose StatementEnd
