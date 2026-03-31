-- +goose Up
-- +goose StatementBegin

/*
 * 020_btc_compliance.sql — KYC, FATF Travel Rule, and GDPR compliance tables.
 *
 * Tables defined here:
 *   kyc_submissions          — per-submission KYC/AML workflow lifecycle
 *   fatf_travel_rule_records — FATF Travel Rule compliance data per payout
 *   gdpr_erasure_requests    — GDPR Article 17 erasure request tracking
 *     The expired state is required for the background expiry job (approved → expired
 *     transition). Without it, vendorHasApprovedKYC() returns true indefinitely after expiry.
 *
 * Depends on: 009_btc_types.sql, 012_btc_invoices.sql, 010_btc_core.sql, 001_core.sql
 * Continued in: 021_btc_compliance_functions.sql
 */

/* ═════════════════════════════════════════════════════════════
   KYC SUBMISSIONS
   ═════════════════════════════════════════════════════════════ */

/*
 * Minimum viable KYC/AML submission lifecycle table.
 *
 * The btc_kyc_status ENUM exists on vendor_wallet_config and payout_records as a
 * placeholder. This table provides the backing records needed for a real KYC flow.
 * A vendor's effective KYC status is driven by the latest submission row for that vendor.
 *
 * COMP-01 in todo.md describes the full KYC schema requirements. This table is the
 * minimum viable foundation — document storage references (kyc_documents) and provider
 * webhook payloads (kyc_provider_webhooks) are deferred to future migrations.
 *
 * KYC documents (passport photos, proof of address) must NOT be stored as column values.
 * Store encrypted references to external storage (e.g. S3 object keys) only.
 *
 * KYC may expire and require periodic refresh (jurisdiction-dependent, typically 2 years).
 * expires_at is non-NULL for approved submissions with a known expiry.
 */
CREATE TABLE kyc_submissions (
    id               UUID        PRIMARY KEY DEFAULT uuidv7(),

    -- The vendor submitting for KYC review.
    -- RESTRICT: vendor cannot be deleted while a KYC submission exists.
    vendor_id        UUID        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,

    -- KYC provider identifier. Examples: 'jumio', 'onfido', 'sumsub'.
    provider         TEXT        NOT NULL,

    -- The provider's own reference ID for this submission.
    -- UNIQUE per (provider, provider_ref_id): prevents duplicate submissions from the
    -- same provider being recorded twice (e.g. from webhook replay).
    provider_ref_id  TEXT        NOT NULL,

    -- Current KYC state machine position.
    -- pending → approved or rejected. rejected submissions may be re-submitted.
        -- KYC submission workflow state. Uses btc_kyc_submission_status (009_btc_types.sql).
    -- DO NOT confuse with btc_kyc_status on vendor_wallet_config/payout_records
    -- (the aggregate gate). This column tracks the per-submission workflow.
    -- Transitions: submitted → under_review → approved → expired (terminal)
    --              submitted → under_review → rejected (terminal for this submission)
    status       btc_kyc_submission_status NOT NULL DEFAULT 'submitted',

    -- When the submission was initiated on our side.
    submitted_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- When a human reviewer completed the review. NULL until reviewed.
    reviewed_at      TIMESTAMPTZ,

    -- The reviewer (internal admin or system). SET NULL if reviewer's account is deleted.
    reviewer_id      UUID        REFERENCES users(id) ON DELETE SET NULL,

    -- Required when status = 'rejected'. Documents why the submission failed.
    rejection_reason TEXT,

    -- When this KYC approval expires and requires re-submission. NULL = no known expiry.
    -- The application should alert vendors approaching expiry.
    expires_at       TIMESTAMPTZ,

    -- One submission per (provider, provider_ref_id) — prevents duplicate webhook inserts.
    CONSTRAINT uq_kyc_provider_ref UNIQUE (provider, provider_ref_id),

    -- Coherence: rejection_reason required when status = 'rejected'.
    -- A rejected submission without a reason prevents the vendor from understanding
    -- what corrective action to take.
    CONSTRAINT chk_kyc_rejection_coherent
        CHECK (status != 'rejected' OR rejection_reason IS NOT NULL)
);

-- Per-vendor KYC history: "show all submissions for vendor X, most recent first."
CREATE INDEX idx_kyc_vendor ON kyc_submissions(vendor_id, submitted_at DESC);

-- LOW-1: Cross-vendor admin pending queue: "show all submissions awaiting KYC review."
-- The per-vendor idx_kyc_pending index cannot serve admin queries across all vendors.
-- Without this, an admin's "show all pending" query does a full table scan.
CREATE INDEX idx_kyc_admin_pending
    ON kyc_submissions(submitted_at DESC)
    WHERE status IN ('submitted', 'under_review');

-- Pending queue: "which vendors are waiting for KYC review?"

-- Expiry alert: "which approved submissions are approaching or past expiry?"
-- Partial: only approved submissions have a meaningful expiry.
CREATE INDEX idx_kyc_expiring ON kyc_submissions(expires_at)
    WHERE expires_at IS NOT NULL AND status = 'approved';

COMMENT ON TABLE kyc_submissions IS
    'KYC/AML submission lifecycle records. Minimum viable schema (COMP-01). '
    'Vendor effective KYC status = status of latest submission row for that vendor. '
    'UNIQUE (provider, provider_ref_id) prevents duplicate webhook replays. '
    'Document storage refs and provider webhooks are in future kyc_documents / kyc_provider_webhooks tables.';
COMMENT ON COLUMN kyc_submissions.provider_ref_id IS
    'Provider''s own reference ID. UNIQUE per (provider, ref_id) to prevent duplicate submissions.';
COMMENT ON COLUMN kyc_submissions.expires_at IS
    'When this KYC approval expires. NULL = no expiry. '
    'Application should alert vendors approaching expiry (typically 2 years from approval).';

-- Active submission queue (submitted + under_review need worker attention).
CREATE INDEX idx_kyc_pending
    ON kyc_submissions(vendor_id, submitted_at DESC)
    WHERE status IN ('submitted', 'under_review');

COMMENT ON INDEX idx_kyc_pending IS
    'Covers both submitted and under_review — both need provider polling.';

/* ═════════════════════════════════════════════════════════════
   FATF TRAVEL RULE RECORDS
   ═════════════════════════════════════════════════════════════ */

/*
 * FATF (Financial Action Task Force) Travel Rule compliance records for payouts
 * above the jurisdictional threshold.
 *
 * The Travel Rule (FATF Recommendation 16) requires VASPs (Virtual Asset Service
 * Providers) to collect and transmit originator and beneficiary information for
 * transfers above a threshold:
 *   USA (FinCEN): $1,000  |  EU (TFR Regulation): €1,000  |  Others: varies
 *
 * This platform is a VASP when it sweeps vendor payouts. For each qualifying payout:
 *   originator = the vendor receiving the payout (our customer)
 *   beneficiary = the owner of the destination Bitcoin address
 *   beneficiary_vasp = the exchange or wallet service controlling that address
 *
 * TIMING: This record must be created BEFORE or AT the same time as the payout broadcast.
 * The application checks for a qualifying fatf_travel_rule_records row before allowing
 * the constructing → broadcast transition for payouts above the threshold.
 *
 * compliance_status tracks whether the originator/beneficiary data was successfully
 * transmitted to the receiving VASP (via TRISA, OpenVASP, or similar protocol).
 *
 * beneficiary_address must match payout_records.destination_address.
 * The application enforces this at creation time. A trigger could also enforce it but
 * the cross-table check adds latency to every payout record INSERT.
 */
CREATE TABLE fatf_travel_rule_records (
    id               UUID        PRIMARY KEY DEFAULT uuidv7(),

    -- The payout this Travel Rule record covers. One record per qualifying payout.
    -- RESTRICT: payout cannot be deleted while a compliance record exists.
    payout_record_id UUID        NOT NULL REFERENCES payout_records(id) ON DELETE RESTRICT,

    -- The jurisdictional threshold that was exceeded, in satoshis.
    -- Recorded at creation time so the record is self-contained even if thresholds change.
    threshold_sat    BIGINT      NOT NULL,

    -- ── Originator (our vendor — the sender) ──────────────────────────────────

    -- Full legal name of the originator (vendor).
    originator_name  TEXT        NOT NULL,

    -- This platform's VASP identifier in the relevant VASP directory.
    -- NULL if not yet registered or if jurisdiction doesn't require VASP identification.
    originator_vasp  TEXT,

    -- ── Beneficiary (owner of the destination address — the receiver) ──────────

    -- Full legal name or entity name of the beneficiary.
    beneficiary_name TEXT        NOT NULL,

    -- Receiving VASP's identifier (e.g. the exchange where the destination address is held).
    -- NULL if the beneficiary is self-custodying (no known receiving VASP).
    beneficiary_vasp TEXT,

    -- The Bitcoin address receiving the payout. Must match payout_records.destination_address.
    -- Enforced by the application at creation. Recorded here for self-contained audit.
    beneficiary_address TEXT     NOT NULL,

    -- ── Compliance transmission state ─────────────────────────────────────────

    -- Whether the Travel Rule data has been transmitted to the receiving VASP.
    -- pending   = not yet transmitted (payout may proceed; transmission is async)
    -- sent      = transmission initiated
    -- acknowledged = receiving VASP confirmed receipt
    -- failed    = transmission failed after retries; requires manual follow-up
    compliance_status TEXT       NOT NULL DEFAULT 'pending'
                      CHECK (compliance_status IN (
                          'pending', 'sent', 'acknowledged', 'failed'
                      )),

    -- When the transmission was initiated. NULL while compliance_status = 'pending'.
    submitted_at     TIMESTAMPTZ,

    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- One Travel Rule record per payout. If payout is split, each output gets its own record.
    CONSTRAINT uq_ftr_payout UNIQUE (payout_record_id),

    CONSTRAINT chk_ftr_threshold_positive
        CHECK (threshold_sat > 0),

    -- Coherence: submitted_at required once transmission has been initiated.
    CONSTRAINT chk_ftr_submitted_coherent
        CHECK (compliance_status = 'pending' OR submitted_at IS NOT NULL)
);

-- Compliance monitoring: "which Travel Rule records are pending or failed transmission?"
-- Partial index keeps this focused on actionable records.
CREATE INDEX idx_ftr_status ON fatf_travel_rule_records(compliance_status, created_at DESC)
    WHERE compliance_status IN ('pending', 'failed');

/* ═════════════════════════════════════════════════════════════
   GDPR ERASURE REQUESTS
   ═════════════════════════════════════════════════════════════ */

/*
 * GDPR Article 17 (right to erasure) request tracking.
 *
 * PII columns subject to erasure across the BTC schema:
 *   invoices.buyer_refund_address           — nullify after retention window
 *   vendor_wallet_config.bridge_destination_address — nullify
 *   payout_records.destination_address      — nullify
 *   sse_token_issuances.source_ip_hash      — nullify + set erased=TRUE
 *   vendor_wallet_config_history.*          — nullify old_value / new_value where PII
 *
 * financial_audit_events.actor_label CANNOT be erased (immutable table, DELETE blocked).
 * Mitigation: store HMAC-SHA256(email, server_secret) instead of raw email at write time.
 * See COMP-02 in todo.md for the full mitigation strategy.
 *
 * tables_processed: array of table names where erasure was applied for this request.
 * Used by the erasure worker to resume incomplete runs after a crash without
 * re-processing tables that were already handled.
 *
 * Status lifecycle: pending → in_progress → completed or rejected.
 */
CREATE TABLE gdpr_erasure_requests (
    id               UUID        PRIMARY KEY DEFAULT uuidv7(),

    -- The user requesting erasure. SET NULL if the account is purged before erasure completes.
    user_id          UUID        REFERENCES users(id) ON DELETE SET NULL,

    -- When the erasure request was received.
    requested_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- When all erasure steps were completed. Required when status = 'completed'.
    completed_at     TIMESTAMPTZ,

    -- Which tables have been processed by the erasure worker so far.
    -- Updated incrementally so a crashed worker can resume without re-processing.
    -- Example: ARRAY['invoices', 'vendor_wallet_config', 'sse_token_issuances']
    tables_processed TEXT[],

    -- Current erasure workflow position.
    status           TEXT        NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'in_progress', 'completed', 'rejected')),

    -- Required when status = 'rejected'. Documents why erasure was denied.
    -- Example: "Legal hold active — erasure blocked pending litigation."
    rejection_reason TEXT,

    -- Coherence: completed_at required when status = 'completed'.
    CONSTRAINT chk_ger_completed_coherent
        CHECK (status != 'completed' OR completed_at IS NOT NULL),
    -- Coherence: rejection_reason required when status = 'rejected'.
    CONSTRAINT chk_ger_rejected_coherent
        CHECK (status != 'rejected' OR rejection_reason IS NOT NULL)
);

-- Active request queue: "show all pending/in-progress erasure requests."
CREATE INDEX idx_ger_pending ON gdpr_erasure_requests(requested_at DESC)
    WHERE status IN ('pending', 'in_progress');

-- MED-4: Prevent multiple concurrent active erasure requests for the same user.
-- Two concurrent workers processing different request rows for the same user interfere
-- with each other's tables_processed resume state and can cause double-nullification.
-- Partial index: once a request reaches 'completed' or 'rejected' it is terminal and
-- a new request may be created.
CREATE UNIQUE INDEX uq_ger_one_active_per_user
    ON gdpr_erasure_requests(user_id)
    WHERE user_id IS NOT NULL AND status IN ('pending', 'in_progress');

COMMENT ON TABLE gdpr_erasure_requests IS
    'GDPR Article 17 erasure request tracking. '
    'tables_processed enables crash recovery without re-processing completed tables. '
    'NOTE: financial_audit_events.actor_label cannot be erased (immutable table). '
    'Mitigation: store HMAC hash at write time. See COMP-02 in todo.md.';
COMMENT ON COLUMN gdpr_erasure_requests.tables_processed IS
    'Tables where erasure steps have been applied. Updated incrementally for crash recovery. '
    'The erasure worker checks this before processing each table to avoid duplicate work.';



-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS gdpr_erasure_requests    CASCADE;
DROP TABLE IF EXISTS fatf_travel_rule_records CASCADE;
DROP TABLE IF EXISTS kyc_submissions          CASCADE;

-- +goose StatementEnd
