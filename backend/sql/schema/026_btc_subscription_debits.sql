-- +goose Up
-- +goose StatementBegin

/*
 * 026_btc_subscription_debits.sql — Subscription fee debit tracking.
 *
 * Tables defined here:
 *   btc_subscription_debits — persistent state for the stale-rate deferral safety valve
 *
 * This table resolves:
 *   The stale debit logic in rate/rate-technical.md §4 references debit_defer_count and
 *   debit_first_deferred_at by name. These values MUST be persisted (not in-memory) to
 *   survive application restarts.
 *
 * Safety valve (rate-technical.md §4):
 *   If NOT (debit_defer_count >= 3 AND NOW() - debit_first_deferred_at > 24h):
 *     Defer. Return ErrRateStale to billing system.
 *   Else:
 *     Proceed with current cached rate, flag as ErrRateStale.
 *
 * If debit_defer_count is in-memory, a process restart resets it to 0 and the
 * forced-proceed safety valve can never trigger — subscription fees would never be
 * collected during an extended rate outage, silently accumulating vendor debt.
 *
 * Depends on: 010_btc_core.sql (vendor_wallet_config, vendor_balances FKs)
 *             017_btc_audit.sql (financial_audit_events for completed debits)
 */

/*
 * Tracks subscription fee debit attempts against vendor BTC balances.
 * One row per pending debit request (created by the billing system, consumed by
 * the rate package). debit_defer_count and debit_first_deferred_at persist the
 * deferral state across restarts.
 */
CREATE TABLE btc_subscription_debits (
    id                          UUID        PRIMARY KEY DEFAULT uuidv7(),

    -- Vendor whose balance is being debited.
    vendor_id                   UUID        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,

    -- 'mainnet' or 'testnet4'.
    network                     TEXT        NOT NULL,

    -- Billing system period identifier (opaque external reference).
    -- Combined with vendor_id as a unique key — prevents double-charging.
    billing_period_ref          TEXT        NOT NULL,

    -- Fiat amount to debit, in minor currency units (e.g. USD cents).
    fiat_amount                 BIGINT      NOT NULL,

    -- ISO 4217 currency code.
    fiat_currency_code          TEXT        NOT NULL,

    -- Satoshi equivalent computed at debit execution time (floor-rounded).
    -- NULL until the debit executes (status becomes 'completed').
    satoshis_debited            BIGINT,

    -- Exchange rate used at execution time.
    -- NULL until the debit executes.
    rate_used                   NUMERIC(18,8),

    -- Debit lifecycle status.
    --   pending:   awaiting rate availability
    --   completed: debit executed; balance decremented; financial_audit_events written
    --   failed:    permanent failure (e.g. insufficient balance, billing override)
    --   deferred:  rate too stale; retry scheduled by billing system
    status                      TEXT        NOT NULL DEFAULT 'pending'
                                CHECK (status IN ('pending', 'completed', 'failed', 'deferred')),

    -- ── Stale-rate deferral tracking ─────────

    -- How many times this debit has been deferred due to a stale rate cache.
    -- Incremented on each deferral. Safety valve fires when >= 3 over 24 hours.
    debit_defer_count           INTEGER     NOT NULL DEFAULT 0
                                CHECK (debit_defer_count >= 0),

    -- Timestamp of the first deferral for this debit request.
    -- NULL before the first deferral. Never reset.
    -- Safety valve: debit_defer_count >= 3 AND NOW() - debit_first_deferred_at > 24h → proceed.
    debit_first_deferred_at     TIMESTAMPTZ,

    -- TRUE when this debit executed with a stale rate (forced-proceed path).
    -- Triggers a WARNING alert. Rate diagnostics written to financial_audit_events metadata.
    executed_with_stale_rate    BOOLEAN     NOT NULL DEFAULT FALSE,

    -- When the debit was requested by the billing system.
    requested_at                TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- When the debit was executed (completed or failed).
    executed_at                 TIMESTAMPTZ,

    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_bsd_network
        CHECK (network IN ('mainnet', 'testnet4')),
    CONSTRAINT chk_bsd_fiat_positive
        CHECK (fiat_amount > 0),
    CONSTRAINT chk_bsd_satoshis_positive
        CHECK (satoshis_debited IS NULL OR satoshis_debited > 0),
    CONSTRAINT chk_bsd_rate_positive
        CHECK (rate_used IS NULL OR rate_used > 0),
    -- Coherence: first deferral timestamp required once count > 0.
    CONSTRAINT chk_bsd_defer_coherent
        CHECK (debit_defer_count = 0 OR debit_first_deferred_at IS NOT NULL),
    -- Coherence: execution timestamp required for terminal statuses.
    CONSTRAINT chk_bsd_executed_coherent
        CHECK (status NOT IN ('completed', 'failed') OR executed_at IS NOT NULL),
    -- One pending debit per (vendor, billing_period_ref) prevents duplicate charges.
    CONSTRAINT uq_bsd_vendor_period
        UNIQUE (vendor_id, billing_period_ref)
);

CREATE TRIGGER trg_btc_subscription_debits_updated_at
    BEFORE UPDATE ON btc_subscription_debits
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Pending/deferred debit queue: "which vendor debits need rate-package attention?"
CREATE INDEX idx_bsd_pending
    ON btc_subscription_debits(vendor_id, network)
    WHERE status IN ('pending', 'deferred');

COMMENT ON TABLE btc_subscription_debits IS
    'Subscription fee debit requests. '
    'debit_defer_count + debit_first_deferred_at are the persistent Gap C state: '
    'they survive restarts so the safety valve ''3 deferrals over 24h → forced-proceed'' '
    'works correctly across process restarts.';

COMMENT ON COLUMN btc_subscription_debits.debit_defer_count IS
    'Persisted across restarts.'
    'Safety valve: when >= 3 AND NOW() - debit_first_deferred_at > 24h → proceed with stale rate.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS btc_subscription_debits CASCADE;

-- +goose StatementEnd
