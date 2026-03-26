-- +goose Up
-- +goose StatementBegin

/*
 * 025_btc_grants.sql — Autovacuum tuning and minimum-privilege GRANT statements.
 *
 * This file must run LAST in the BTC migration sequence — it references every
 * BTC table by name, so all previous migrations must already be applied.
 *
 * Autovacuum settings:
 *   invoices and payout_records have hot partial indexes that accumulate dead tuples
 *   rapidly under load. Aggressive autovacuum prevents index bloat.
 *
 * Grants:
 *   Minimum-privilege grants for btc_app_role. Conditional on role existence —
 *   emits WARNING (not ERROR) so CI migrations don't fail in environments without
 *   the role. Replace 'btc_app_role' with the actual application DB role name.
 *
 *   Key security decisions:
 *     financial_audit_events: INSERT + SELECT only (trigger enforces no UPDATE/DELETE)
 *     vendor_balances:        SELECT only — all mutations via btc_credit_balance /
 *                             btc_debit_balance stored procedures (SEC-08)
 *
 * New tables added since the original 011_btc_functions.sql grants block:
 *   vendor_webhook_config, btc_subscription_debits, btc_withdrawal_requests
 *
 * See TODO-C in todo.md for the full DB role architecture (app_role, readonly_role, audit_role).
 */

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
  -- autovacuum settings

/* ═════════════════════════════════════════════════════════════
   GRANTS
   ═════════════════════════════════════════════════════════════ */

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
        EXECUTE 'REVOKE ALL ON ops_audit_log                FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON kyc_submissions              FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON fatf_travel_rule_records     FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON gdpr_erasure_requests        FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON vendor_webhook_config        FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON webhook_deliveries           FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON dispute_records              FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON btc_tier_config_history      FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON vendor_wallet_config_history FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON reconciliation_run_history   FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON sse_token_issuances          FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON btc_zmq_dead_letter          FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON btc_subscription_debits      FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON btc_withdrawal_requests      FROM PUBLIC';

        -- App role: CRUD on all operational tables.
        EXECUTE 'GRANT SELECT, INSERT, UPDATE ON
            btc_tier_config, platform_config, reconciliation_job_state,
            bitcoin_sync_state, vendor_wallet_config, vendor_tier_overrides,
            btc_exchange_rate_log, invoices, invoice_addresses,
            invoice_address_monitoring, invoice_payments, btc_outage_log,
            bitcoin_block_history, payout_records, wallet_backup_success,
            btc_tier_config_history, vendor_wallet_config_history,
            ops_audit_log, reconciliation_run_history, kyc_submissions,
            sse_token_issuances, vendor_webhook_config, webhook_deliveries,
            btc_zmq_dead_letter, dispute_records, gdpr_erasure_requests,
            fatf_travel_rule_records, btc_subscription_debits, btc_withdrawal_requests
            TO btc_app_role';

        -- financial_audit_events: INSERT and SELECT only (immutability enforced by trigger).
        EXECUTE 'GRANT INSERT, SELECT ON financial_audit_events TO btc_app_role';

        -- vendor_balances: SELECT only — all mutations via stored procedures (SEC-08).
        EXECUTE 'GRANT SELECT ON vendor_balances TO btc_app_role';

        -- Stored procedure execution for balance mutations.
        EXECUTE 'GRANT EXECUTE ON FUNCTION btc_credit_balance(UUID, TEXT, BIGINT) TO btc_app_role';
        EXECUTE 'GRANT EXECUTE ON FUNCTION btc_debit_balance(UUID, TEXT, BIGINT) TO btc_app_role';
    END IF;
END;
$btc_grants$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Reset autovacuum to defaults.
ALTER TABLE IF EXISTS invoices       RESET (autovacuum_vacuum_scale_factor, autovacuum_analyze_scale_factor);
ALTER TABLE IF EXISTS payout_records RESET (autovacuum_vacuum_scale_factor, autovacuum_analyze_scale_factor);

-- +goose StatementEnd
