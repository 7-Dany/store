-- +goose Up
-- +goose StatementBegin

/*
 * 022_btc_webhooks.sql — Vendor webhook configuration and delivery tables.
 *
 * Tables defined here:
 *   vendor_webhook_config — per-vendor webhook endpoint configuration
 *   webhook_deliveries    — transactional outbox for vendor webhook event delivery
 *
 * Depends on: 010_btc_core.sql (vendor FK), 009_btc_types.sql, 001_core.sql
 *             012_btc_invoices.sql (invoices FK)
 * Continued in: 023_btc_disputes.sql
 */

/* ═════════════════════════════════════════════════════════════
   VENDOR WEBHOOK CONFIG
   ═════════════════════════════════════════════════════════════ */

/*
 * Per-vendor webhook endpoint configuration.
 * One row per (vendor_id, network). Created when a vendor configures their endpoint.
 *
 * The original 010_btc_payouts.sql had a comment:
 *   "Vendor webhook endpoints are stored separately in a vendor_webhook_endpoints table
 *    (future migration) or fetched from the application config layer at delivery time."
 * This table is that future migration. The delivery worker requires it to function.
 *
 * webhook_secret_enc: AES-256-GCM encrypted raw secret. The delivery worker decrypts
 *   it with TOKEN_ENCRYPTION_KEY at delivery time. NULL = no secret (header omitted).
 *
 * Auto-suspension: when dead_lettered_7d > 100, a background job sets suspended = TRUE.
 *   The delivery worker writes 'suspended_skip' rows instead of attempting delivery.
 *   Vendors re-enable via their dashboard (resets suspended = FALSE, dead_lettered_7d = 0).
 *
 * (webhook-feature.md §Vendor Webhook Configuration, webhook-technical.md §2, §5)
 */
CREATE TABLE vendor_webhook_config (
    vendor_id           UUID        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    network             TEXT        NOT NULL,

    -- HTTPS-only endpoint URL. Validated with a test ping at save time.
    endpoint_url        TEXT        NOT NULL,

    -- AES-256-GCM encrypted webhook signing secret.
    -- Decrypted with TOKEN_ENCRYPTION_KEY at delivery time.
    -- NULL = vendor chose no secret (X-BTC-Signature header omitted from deliveries).
    webhook_secret_enc  BYTEA,

    -- Maximum delivery attempts before dead_lettered. Configurable per-vendor.
    -- Default 10 (webhook-feature.md §Retry Policy).
    max_attempts        INTEGER     NOT NULL DEFAULT 10,

    -- TRUE when the vendor's endpoint is auto-suspended (> 100 dead letters in 7 days).
    -- Delivery worker writes 'suspended_skip' status rows; does not attempt delivery.
    suspended           BOOLEAN     NOT NULL DEFAULT FALSE,

    -- When suspended was last set TRUE. For SLA and re-enable audit trail.
    suspended_at        TIMESTAMPTZ,

    -- Rolling 7-day dead-letter count. Updated on each dead-letter; reset to 0 on re-enable.
    dead_lettered_7d    INTEGER     NOT NULL DEFAULT 0,

    -- Timestamp of the last test ping. NULL until pinged after initial configuration.
    last_ping_at        TIMESTAMPTZ,

    -- Whether the last test ping returned 2xx.
    last_ping_success   BOOLEAN,

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (vendor_id, network),

    CONSTRAINT chk_vwhc_network
        CHECK (network IN ('mainnet', 'testnet4')),
    CONSTRAINT chk_vwhc_endpoint_https
        CHECK (endpoint_url LIKE 'https://%'),
    CONSTRAINT chk_vwhc_max_attempts_positive
        CHECK (max_attempts > 0),
    CONSTRAINT chk_vwhc_dead_lettered_non_negative
        CHECK (dead_lettered_7d >= 0),
    CONSTRAINT chk_vwhc_suspension_coherent
        CHECK (suspended = FALSE OR suspended_at IS NOT NULL)
);

CREATE TRIGGER trg_vendor_webhook_config_updated_at
    BEFORE UPDATE ON vendor_webhook_config
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Delivery worker: "is this vendor's endpoint currently suspended?"
CREATE INDEX idx_vwhc_suspended
    ON vendor_webhook_config(vendor_id, network) WHERE suspended = TRUE;

COMMENT ON TABLE vendor_webhook_config IS
    'Per-vendor webhook endpoint config (one row per vendor per network). '
    'endpoint_url: HTTPS-only, validated with test ping at save time. '
    'webhook_secret_enc: AES-256-GCM; decrypted with TOKEN_ENCRYPTION_KEY at delivery. '
    'suspended: set TRUE by background job after > 100 dead letters in 7 days. '
    'Re-enabled by vendor via dashboard after fixing endpoint.';
COMMENT ON COLUMN vendor_webhook_config.webhook_secret_enc IS
    'AES-256-GCM encrypted. Key: TOKEN_ENCRYPTION_KEY. NULL = no secret header sent. '
    'NEVER log or expose the decrypted value outside the delivery worker.';
COMMENT ON COLUMN vendor_webhook_config.dead_lettered_7d IS
    'Rolling 7-day dead-letter count. Background job sets suspended=TRUE when > 100. '
    'Reset to 0 when vendor re-enables endpoint via dashboard.';

/* ═════════════════════════════════════════════════════════════
   WEBHOOK DELIVERIES
   ═════════════════════════════════════════════════════════════ */

/*
 * Transactional outbox for vendor event notifications (ARCH-03).
 *
 * When an invoice or payout changes state, the application writes a row here in the
 * same DB transaction as the state change. A background delivery worker then reads
 * pending rows and delivers them to the vendor's registered webhook endpoint.
 *
 * This outbox pattern guarantees at-least-once delivery:
 *   - If the delivery worker crashes before delivering, the row remains pending
 *     and will be retried on next startup.
 *   - If the vendor's endpoint returns an error, next_retry_at is advanced using
 *     exponential backoff and the row is retried.
 *   - After max_attempts failures, status → dead_lettered for manual review.
 *
 * Vendor webhook endpoints are stored separately in a vendor_webhook_endpoints table
 * (future migration) or fetched from the application config layer at delivery time.
 * The payload stored here is the complete event payload to be delivered.
 */
CREATE TABLE webhook_deliveries (
    id               UUID        PRIMARY KEY DEFAULT uuidv7(),

    -- Vendor to receive this notification.
    -- RESTRICT: delivery record cannot exist without a vendor.
    vendor_id        UUID        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,

    -- Application-defined event type for the notification.
    -- Examples: 'invoice.settled', 'payout.confirmed', 'invoice.expired'.
    event_type       TEXT        NOT NULL,

    -- Complete event payload to deliver to the vendor's endpoint.
    -- Must be a JSON object. Includes all data the vendor needs to act on the event.
    payload          JSONB       NOT NULL,

    -- ── Source event references ────────────────────────────────────────────────

    -- The invoice that triggered this notification. NULL for non-invoice events.
    invoice_id       UUID        REFERENCES invoices(id) ON DELETE RESTRICT,

    -- The payout record that triggered this notification. NULL for non-payout events.
    payout_record_id UUID        REFERENCES payout_records(id) ON DELETE RESTRICT,

    -- ── Delivery state ─────────────────────────────────────────────────────────

    -- Current delivery lifecycle position.
    -- pending → delivered (success) or failed (temporary) → dead_lettered (permanent failure).
        -- Delivery lifecycle.
    --   pending:        eligible for delivery attempt by the worker
    --   delivered:      2xx received from vendor endpoint — TERMINAL
    --   failed:         transient failure; next_retry_at set for backoff retry
    --   dead_lettered:  max_attempts exhausted; requires admin review
    --   suspended_skip: vendor endpoint suspended; row written but worker never delivers
    --                   (vendor must re-enable endpoint to resume)
    --   abandoned:      admin-closed dead letter; no further delivery attempts — TERMINAL
    status          TEXT        NOT NULL DEFAULT 'pending'
                    CHECK (status IN (
                        'pending',
                        'delivered',
                        'failed',
                        'dead_lettered',
                        'suspended_skip',   -- vendor endpoint suspended; never picked up by worker
                        'abandoned'         -- admin terminal closure — no further attempts
                    )),

    -- How many delivery attempts have been made. Incremented on each attempt.
    attempt_count    INTEGER     NOT NULL DEFAULT 0,

    -- Maximum attempts before status → dead_lettered.
    -- Default 10 (webhook-feature.md §Retry Policy). Override per event type if needed.
    max_attempts     INTEGER     NOT NULL DEFAULT 10,

    -- When to attempt the next delivery. NULL = deliver immediately.
    -- Set using exponential backoff after each failed attempt.
    -- Delivery worker query: WHERE status = 'pending' AND (next_retry_at IS NULL OR next_retry_at <= NOW())
    next_retry_at    TIMESTAMPTZ,

    -- Last error message from a failed delivery attempt. Overwritten on each attempt.
    last_error       TEXT,

    -- Timestamp when the delivery was successfully acknowledged by the vendor's endpoint.
    -- NULL until delivered.
    delivered_at     TIMESTAMPTZ,

    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_wd_payload_is_object
        CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT chk_wd_attempt_non_negative
        CHECK (attempt_count >= 0),
    -- attempt_count can never exceed max_attempts (would be set to dead_lettered first).
    CONSTRAINT chk_wd_attempt_within_max
        CHECK (attempt_count <= max_attempts)
);

-- Delivery worker hot path: find pending deliveries ready to attempt.
-- Query: WHERE status = 'pending' AND (next_retry_at IS NULL OR next_retry_at <= NOW())
CREATE INDEX idx_wd_pending ON webhook_deliveries(next_retry_at)
    WHERE status = 'pending';

-- Per-vendor delivery history: admin investigation of missed notifications.
CREATE INDEX idx_wd_vendor ON webhook_deliveries(vendor_id, created_at DESC);

-- Dead letter review: find permanently failed deliveries requiring manual intervention.
CREATE INDEX idx_wd_dead_letter ON webhook_deliveries(created_at DESC)
    WHERE status = 'dead_lettered';

COMMENT ON TABLE webhook_deliveries IS
    'Transactional outbox for vendor state-change notifications. '
    'Written in the same TX as the triggering state change. '
    'At-least-once delivery with exponential backoff retry up to max_attempts. '
    'dead_lettered rows require manual review. '
    'Delivery worker polls: WHERE status=''pending'' AND (next_retry_at IS NULL OR next_retry_at <= NOW()).';
COMMENT ON COLUMN webhook_deliveries.max_attempts IS
    'Maximum delivery attempts before status → dead_lettered. Default 10 (webhook-feature.md §Retry Policy).';
COMMENT ON COLUMN webhook_deliveries.next_retry_at IS
    'NULL = deliver immediately. Set with exponential backoff after each failed attempt.';

-- Admin review: permanently closed (abandoned) deliveries.
CREATE INDEX idx_wd_abandoned
    ON webhook_deliveries(created_at DESC)
    WHERE status = 'abandoned';

-- Suspension audit: rows skipped due to vendor endpoint suspension.
CREATE INDEX idx_wd_suspended_skip
    ON webhook_deliveries(vendor_id, created_at DESC)
    WHERE status = 'suspended_skip';



-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_wd_suspended_skip;
DROP INDEX IF EXISTS idx_wd_abandoned;
DROP TABLE IF EXISTS webhook_deliveries    CASCADE;
DROP TABLE IF EXISTS vendor_webhook_config CASCADE;

-- +goose StatementEnd
