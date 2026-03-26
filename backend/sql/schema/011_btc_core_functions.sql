-- +goose Up
-- +goose StatementBegin

/*
 * 011_btc_core_functions.sql — Functions and triggers for btc_tier_config and vendor tables.
 *
 * Functions defined here:
 *   fn_btc_wallet_mode_guard()       — prevents invalid wallet mode transitions on vendor_wallet_config
 *   btc_credit_balance()             — safe balance credit (atomic, with audit event)
 *   btc_debit_balance()             — safe balance debit with under-balance guard (atomic)
 *   fn_sync_vendor_tier_role()      — keeps user_roles in sync when a vendor's tier changes
 *   fn_vendor_effective_kyc_status()— canonical KYC gate; call instead of reading
 *                                      vendor_wallet_config.kyc_status for payout decisions
 *
 * COMPLIANCE NOTE:
 *   fn_vendor_effective_kyc_status() is the authoritative KYC gate. It queries
 *   kyc_submissions directly to detect expired approvals even when the background
 *   propagation job has not yet run. The payout promotion path (held → queued) MUST
 *   call this function rather than reading vendor_wallet_config.kyc_status directly,
 *   because kyc_status is a denormalized cache that can lag behind reality.
 *
 * Depends on: 010_btc_core.sql (tables), 009_btc_types.sql (ENUMs)
 * Continued in: 012_btc_invoices.sql
 */

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
 *
 * ══════════════════════════════════════════════════════════════════════════════
 * ⚠️  F-01 — DOUBLE-CREDIT RISK ON SETTLEMENT CRASH-AND-RETRY
 * ══════════════════════════════════════════════════════════════════════════════
 * btc_credit_balance() is NOT idempotent. If the settlement transaction commits
 * btc_credit_balance() but then crashes before INSERT INTO payout_records commits,
 * the watchdog resets the invoice to 'confirming' and a second worker re-runs
 * Phase 2. The second call to btc_credit_balance() credits the balance again —
 * creating phantom satoshis with no corresponding payout_record.
 *
 * The reconciliation formula diverges by exactly net_satoshis per occurrence:
 *   vendor_balances.balance_satoshis > SUM(held/queued payout_records)
 *
 * APPLICATION LAYER REQUIREMENT:
 *   Settlement Phase 2 MUST execute btc_credit_balance() and INSERT INTO payout_records
 *   in the SAME database transaction. There must be no commit boundary between them.
 *
 *   Before calling btc_credit_balance(), check whether a payout_record already
 *   exists for this invoice_id (the UNIQUE constraint will 23505 on the second
 *   INSERT, but the balance is already double-credited by then):
 *
 *     SELECT id FROM payout_records WHERE invoice_id = $invoice_id FOR SHARE;
 *     -- If found: payout_record exists from a previous (possibly partial) run.
 *     --   Do NOT call btc_credit_balance() again.
 *     --   The prior credit is still in the balance; proceed to audit event only.
 *     -- If not found: safe to call btc_credit_balance() and insert payout_record.
 *
 *   This pre-check MUST run inside the same transaction as the credit, with
 *   FOR SHARE on the payout_records row to prevent a concurrent worker from
 *   inserting between the check and the credit.
 * ══════════════════════════════════════════════════════════════════════════════
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
    'Direct UPDATE on vendor_balances is revoked from btc_app_role. '
    'F-01 WARNING: NOT idempotent. btc_credit_balance() and INSERT INTO payout_records '
    'MUST execute in the same DB transaction with no commit boundary between them. '
    'Before calling, check SELECT id FROM payout_records WHERE invoice_id=$id FOR SHARE '
    'inside the same transaction. If a payout_record exists, skip the credit — '
    'the prior credit is already in the balance. See F-01 in settlement-technical.md §1.';


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

    -- Filter on active (non-expired) role assignments only.
    -- An expired user_roles row must not be updated: changing role_id on an expired row
    -- could silently reactivate it with a different role if expires_at is later extended,
    -- granting unintended permissions.
    SELECT role_id INTO v_old_role_id
    FROM user_roles
    WHERE user_id = NEW.vendor_id
      AND (expires_at IS NULL OR expires_at > NOW());

    IF NOT FOUND THEN
        -- No active role assignment — log and skip.
        INSERT INTO ops_audit_log
            (actor_label, operation, table_name, record_id, new_values, reason)
        VALUES (
            COALESCE(current_setting('app.current_actor_label', TRUE), 'system'),
            'tier_role_sync_skipped',
            'user_roles',
            NEW.vendor_id::TEXT,
            jsonb_build_object('new_tier_id', NEW.tier_id, 'new_role_id', v_new_role_id),
            'Tier role sync skipped: no active (non-expired) user_roles row for vendor ' || NEW.vendor_id::TEXT
        );
        RETURN NEW;
    END IF;

    UPDATE user_roles
    SET role_id = v_new_role_id,
        updated_at = NOW()
    WHERE user_id = NEW.vendor_id
      AND (expires_at IS NULL OR expires_at > NOW());

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
    'an ACTIVE (non-expired) user_roles row exists for the vendor. '
    'Expired role assignments are skipped to prevent unintended reactivation. '
    'Writes to ops_audit_log on both successful sync and skipped-due-to-expiry.';

CREATE TRIGGER trg_vwc_tier_role_sync
    AFTER UPDATE OF tier_id ON vendor_wallet_config
    FOR EACH ROW EXECUTE FUNCTION fn_sync_vendor_tier_role();


/* ════════════════════════════════════════════════════════════
   KYC EFFECTIVE STATUS FUNCTION (CRIT-4)
   ════════════════════════════════════════════════════════════ */

/*
 * fn_vendor_effective_kyc_status
 * ──────────────────────────────
 * Returns the vendor's authoritative KYC status by querying kyc_submissions directly.
 * Use this function from the payout promotion path (held → queued) instead of reading
 * vendor_wallet_config.kyc_status, which is a denormalized cache.
 *
 * PROBLEM:
 *   vendor_wallet_config.kyc_status is updated by a background job when kyc_submissions
 *   expire. If that job stalls or has a bug, kyc_status stays 'approved' past expiry,
 *   allowing high-value payouts to be promoted without valid KYC (compliance failure).
 *
 * LOGIC:
 *   - 'not_required' when kyc_enabled = FALSE OR the vendor's tier threshold is NULL
 *   - 'approved'     when the latest submission is approved AND not expired
 *   - 'pending'      when the latest submission is submitted/under_review/expired
 *   - 'rejected'     when the latest submission is rejected
 *   - 'not_required' when no submission exists (vendor below threshold)
 */
CREATE OR REPLACE FUNCTION fn_vendor_effective_kyc_status(
    p_vendor_id UUID,
    p_network   TEXT
) RETURNS btc_kyc_status
LANGUAGE plpgsql
STABLE
AS $fn$
DECLARE
    v_kyc_enabled    BOOLEAN;
    v_threshold_sat  BIGINT;
    v_sub_status     btc_kyc_submission_status;
    v_sub_expires_at TIMESTAMPTZ;
BEGIN
    -- If the platform KYC feature flag is off, KYC is never required.
    SELECT kyc_enabled INTO v_kyc_enabled
    FROM platform_config WHERE network = p_network;

    IF NOT FOUND OR NOT v_kyc_enabled THEN
        RETURN 'not_required';
    END IF;

    -- If the vendor's tier has no KYC threshold, KYC is not required for this vendor.
    SELECT t.kyc_check_required_at_threshold_satoshis
    INTO v_threshold_sat
    FROM vendor_wallet_config vwc
    JOIN btc_tier_config t ON t.id = vwc.tier_id
    WHERE vwc.vendor_id = p_vendor_id AND vwc.network = p_network;

    IF NOT FOUND OR v_threshold_sat IS NULL THEN
        RETURN 'not_required';
    END IF;

    -- Fetch the most recent KYC submission for this vendor.
    SELECT status, expires_at
    INTO v_sub_status, v_sub_expires_at
    FROM kyc_submissions
    WHERE vendor_id = p_vendor_id
    ORDER BY submitted_at DESC
    LIMIT 1;

    IF NOT FOUND THEN
        -- No submission yet; vendor has not initiated KYC.
        RETURN 'not_required';
    END IF;

    CASE v_sub_status
        WHEN 'approved' THEN
            -- Approval is valid only if the submission has not expired.
            IF v_sub_expires_at IS NULL OR v_sub_expires_at > NOW() THEN
                RETURN 'approved';
            ELSE
                -- Approval has lapsed; vendor must re-submit.
                RETURN 'pending';
            END IF;
        WHEN 'submitted', 'under_review' THEN
            RETURN 'pending';
        WHEN 'rejected' THEN
            RETURN 'rejected';
        WHEN 'expired' THEN
            RETURN 'pending';  -- vendor must re-submit
        ELSE
            RETURN 'pending';  -- unknown state — fail closed
    END CASE;
END;
$fn$;

COMMENT ON FUNCTION fn_vendor_effective_kyc_status(UUID, TEXT) IS
    'Authoritative KYC gate. Queries kyc_submissions directly to account for expiry '
    'regardless of background job propagation lag. '
    'Returns btc_kyc_status: not_required | approved | pending | rejected. '
    'MUST be called from payout promotion (held → queued) instead of reading '
    'vendor_wallet_config.kyc_status, which is a denormalized cache. '
    'STABLE: deterministic within a transaction; safe in query predicates.';


-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS trg_vwc_tier_role_sync             ON vendor_wallet_config;
DROP TRIGGER IF EXISTS trg_vendor_wallet_config_mode_guard ON vendor_wallet_config;

DROP FUNCTION IF EXISTS fn_vendor_effective_kyc_status(UUID, TEXT);
DROP FUNCTION IF EXISTS fn_sync_vendor_tier_role();
DROP FUNCTION IF EXISTS btc_debit_balance(UUID, TEXT, BIGINT);
DROP FUNCTION IF EXISTS btc_credit_balance(UUID, TEXT, BIGINT);
DROP FUNCTION IF EXISTS fn_btc_wallet_mode_guard();

-- +goose StatementEnd
