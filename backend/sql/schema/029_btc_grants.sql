-- +goose Up
-- +goose StatementBegin

/*
 * 029_btc_grants.sql — Autovacuum tuning and minimum-privilege GRANT statements.
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
 *     vendor_balances:        SELECT only — all mutations via audited btc_credit_balance /
 *                             btc_debit_balance stored procedures
 *     platform_config:        no blanket UPDATE grant; treasury_reserve_satoshis is
 *                             writable only via audited treasury procedures
 *
 * New tables added since the original 011_btc_functions.sql grants block:
 *   vendor_webhook_config, btc_subscription_debits, btc_withdrawal_requests,
 *   btc_watches, btc_tracked_transactions, btc_tracked_transaction_addresses
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

/*
 * btc_tracked_transactions receives frequent status UPDATE churn as the watcher
 * daemon polls and reconciles transaction states. Aggressive autovacuum prevents
 * dead-tuple bloat on the partial indexes (idx_btt_user_time, idx_btt_network_txid).
 */
ALTER TABLE btc_tracked_transactions SET (
    autovacuum_vacuum_scale_factor   = 0.02,
    autovacuum_analyze_scale_factor  = 0.01
);

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
        EXECUTE 'REVOKE ALL ON btc_tier_config                      FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON platform_config                      FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON reconciliation_job_state             FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON bitcoin_sync_state                   FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON vendor_wallet_config                 FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON vendor_balances                      FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON vendor_tier_overrides                FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON btc_exchange_rate_log                FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON invoices                             FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON invoice_addresses                    FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON invoice_address_monitoring           FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON invoice_payments                     FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON btc_outage_log                       FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON bitcoin_block_history                FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON payout_records                       FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON financial_audit_events               FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON wallet_backup_success                FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON ops_audit_log                        FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON kyc_submissions                      FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON fatf_travel_rule_records             FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON gdpr_erasure_requests                FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON vendor_webhook_config                FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON webhook_deliveries                   FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON dispute_records                      FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON btc_tier_config_history              FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON vendor_wallet_config_history         FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON reconciliation_run_history           FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON sse_token_issuances                  FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON btc_zmq_dead_letter                  FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON btc_subscription_debits              FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON btc_withdrawal_requests              FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON btc_watches                          FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON btc_tracked_transactions             FROM PUBLIC';
        EXECUTE 'REVOKE ALL ON btc_tracked_transaction_addresses    FROM PUBLIC';

        -- App role: CRUD on all operational tables except vendor_balances and the
        -- treasury_reserve_satoshis column on platform_config, which are both
        -- routed through audited stored procedures.
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
            fatf_travel_rule_records, btc_subscription_debits, btc_withdrawal_requests,
            btc_watches, btc_tracked_transactions, btc_tracked_transaction_addresses
            TO btc_app_role';

        -- btc_tracked_transaction_addresses: also allow DELETE for direct address removal
        -- (FK cascade from btc_tracked_transactions handles bulk removal, but the app
        -- may need to remove individual address associations explicitly).
        EXECUTE 'GRANT DELETE ON btc_tracked_transaction_addresses TO btc_app_role';

        -- BIGSERIAL sequences: app role needs USAGE to generate new IDs on INSERT.
        EXECUTE 'GRANT USAGE ON SEQUENCE btc_watches_id_seq                 TO btc_app_role';
        EXECUTE 'GRANT USAGE ON SEQUENCE btc_tracked_transactions_id_seq    TO btc_app_role';

        -- financial_audit_events: INSERT and SELECT only (immutability enforced by trigger).
        EXECUTE 'GRANT INSERT, SELECT ON financial_audit_events TO btc_app_role';

        -- vendor_balances: SELECT only — all mutations via audited stored procedures.
        EXECUTE 'GRANT SELECT ON vendor_balances TO btc_app_role';

        -- platform_config: remove blanket UPDATE, then re-grant every operational
        -- column except treasury_reserve_satoshis, which must mutate only through
        -- the audited treasury procedures defined in 019_btc_financial_mutations.sql.
        EXECUTE 'REVOKE UPDATE ON platform_config FROM btc_app_role';
        EXECUTE '' ||
            'GRANT UPDATE (' ||
            'sweep_hold_mode, sweep_hold_reason, sweep_hold_activated_at, ' ||
            'platform_wallet_mode_legal_approved, reconciliation_start_height, ' ||
            'kyc_enabled, fatf_enabled, webhooks_enabled, disputes_enabled, ' ||
            'gdpr_erasure_job_enabled, consolidation_enabled, ' ||
            'consolidation_max_fee_sat_vbyte, consolidation_window_start, ' ||
            'consolidation_window_end, updated_at' ||
            ') ON platform_config TO btc_app_role';

        -- Audited financial mutation entry points.
        EXECUTE 'GRANT EXECUTE ON FUNCTION fn_btc_append_financial_audit_event(TEXT, TEXT, TEXT, UUID, TEXT, UUID, UUID, BIGINT, BIGINT, BIGINT, BIGINT, BIGINT, TEXT, BOOLEAN, JSONB) TO btc_app_role';
        EXECUTE 'GRANT EXECUTE ON FUNCTION btc_credit_balance(UUID, TEXT, BIGINT, TEXT, UUID, TEXT, UUID, UUID, BIGINT, BIGINT, TEXT, BOOLEAN, JSONB, TEXT) TO btc_app_role';
        EXECUTE 'GRANT EXECUTE ON FUNCTION btc_debit_balance(UUID, TEXT, BIGINT, TEXT, UUID, TEXT, UUID, UUID, BIGINT, BIGINT, TEXT, BOOLEAN, JSONB, TEXT) TO btc_app_role';
        EXECUTE 'GRANT EXECUTE ON FUNCTION btc_credit_treasury_reserve(TEXT, BIGINT, TEXT, UUID, TEXT, UUID, BIGINT, BIGINT, TEXT, BOOLEAN, JSONB, TEXT) TO btc_app_role';
        EXECUTE 'GRANT EXECUTE ON FUNCTION btc_debit_treasury_reserve(TEXT, BIGINT, TEXT, UUID, TEXT, UUID, BIGINT, BIGINT, TEXT, BOOLEAN, JSONB, TEXT) TO btc_app_role';
    END IF;
END;
$btc_grants$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Reset autovacuum to defaults.
ALTER TABLE IF EXISTS invoices                    RESET (autovacuum_vacuum_scale_factor, autovacuum_analyze_scale_factor);
ALTER TABLE IF EXISTS payout_records              RESET (autovacuum_vacuum_scale_factor, autovacuum_analyze_scale_factor);
ALTER TABLE IF EXISTS btc_tracked_transactions    RESET (autovacuum_vacuum_scale_factor, autovacuum_analyze_scale_factor);

-- +goose StatementEnd
