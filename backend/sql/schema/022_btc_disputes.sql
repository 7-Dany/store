-- +goose Up
-- +goose StatementBegin

/*
 * 022_btc_disputes.sql — Buyer/vendor payment dispute records.
 *
 * Tables defined here:
 *   dispute_records — dispute lifecycle table
 *
 * Also defines:
 *   FK constraint on payout_records.dispute_id → dispute_records.id
 *   (payout_records was created in 015_btc_payouts.sql with dispute_id as a plain
 *    UUID column — no FK — because dispute_records didn't exist yet. Now that it does,
 *    we add the FK constraint here.)
 *
 * DESIGN NOTE — dispute_records.vendor_id is RESTRICT (not SET NULL):
 *   The vendor_id column is RESTRICT to prevent a fund-freeze trap. If vendor_id were
 *   SET NULL on deletion, the payout freeze/unfreeze job (which queries by vendor_id) could
 *   no longer locate or unfreeze the frozen payout records, permanently trapping funds.
 *   RESTRICT is correct: a vendor with an active (non-terminal) dispute cannot be deleted
 *   until the dispute is resolved. The application must enforce deletion ordering:
 *   resolve all non-terminal disputes before permitting vendor account deletion.
 *   Terminal disputes (resolved_vendor, resolved_buyer, resolved_platform, withdrawn) have
 *   already unfrozen all associated payout records, so the RESTRICT is safe to lift
 *   at the application layer once resolution is confirmed.
 *
 * DESIGN NOTE — escalated disputes require explicit resolution:
 *   Disputes in 'escalated' status freeze payout records indefinitely. The platform must
 *   manually transition them to a terminal status (resolved_*) or 'withdrawn'. An
 *   escalation_deadline column and monitoring index are provided so SLA timers can fire.
 *
 * Depends on: 015_btc_payouts.sql (payout_records), 012_btc_invoices.sql (invoices)
 *             010_btc_core.sql (vendor_wallet_config), 001_core.sql (users)
 * Continued in: 023_btc_history.sql
 */

/* ═════════════════════════════════════════════════════════════
   DISPUTE RECORDS
   ═════════════════════════════════════════════════════════════ */

/*
 * Buyer and vendor payment dispute records.
 *
 * Disputes link a raised concern to the specific financial object in question.
 * At least one of invoice_id or payout_record_id must be set (chk_dr_has_subject).
 *
 * The lifecycle is: open → investigating → resolved or rejected.
 * Resolved and rejected disputes require a resolved_at timestamp (chk_dr_resolved_coherent).
 *
 * Resolution is handled by an admin (resolved_by). The resolution_note records what action
 * was taken (e.g. "Refund issued via payout record PR-xxx", "Verified correct — no action").
 */
CREATE TABLE dispute_records (
    id               UUID        PRIMARY KEY DEFAULT uuidv7(),

    -- The invoice the dispute is about. NULL if the dispute is about a payout only.
    invoice_id       UUID        REFERENCES invoices(id) ON DELETE RESTRICT,

    -- The payout record the dispute is about. NULL if the dispute is about an invoice only.
    -- At least one of invoice_id or payout_record_id must be non-NULL.
    payout_record_id UUID        REFERENCES payout_records(id) ON DELETE RESTRICT,

    -- Who raised the dispute. RESTRICT: cannot delete user while dispute is unresolved.
    raised_by        UUID        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,

    -- Category of dispute. Determines which resolution workflow applies.
    dispute_type     TEXT        NOT NULL
                     CHECK (dispute_type IN (
                         'payment_not_credited',  -- buyer paid but invoice not settled
                         'wrong_amount',          -- incorrect amount received or charged
                         'fee_dispute',           -- vendor disputes the fee calculation
                         'refund_request',        -- buyer is requesting a refund
                         'other'                  -- does not fit standard categories
                     )),

    -- Detailed description of the dispute from the raiser's perspective.
    -- Must have visible content (chk_dr_description_not_empty).
    description      TEXT        NOT NULL,

    -- Current resolution lifecycle position.
    status       btc_dispute_status  NOT NULL DEFAULT 'open',

    -- Admin who resolved the dispute. SET NULL if resolver's account is later deleted.
    resolved_by      UUID        REFERENCES users(id) ON DELETE SET NULL,

    -- What action was taken to resolve the dispute.
    -- Required when status = resolved or rejected (application layer enforced).
    resolution_note  TEXT,

    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- When the dispute was resolved or rejected.
    -- Required when status = resolved or rejected (chk_dr_resolved_coherent).
    resolved_at      TIMESTAMPTZ,

    -- At least one financial object must be referenced.
    -- A dispute with no invoice_id and no payout_record_id cannot be investigated.
    CONSTRAINT chk_dr_has_subject
        CHECK (invoice_id IS NOT NULL OR payout_record_id IS NOT NULL),

    -- Coherence: resolved_at required when dispute is closed.
    CONSTRAINT chk_dr_resolved_coherent
        CHECK (status NOT IN ('resolved_vendor', 'resolved_buyer', 'resolved_platform', 'withdrawn') OR resolved_at IS NOT NULL),

    -- Description must have visible content.
    CONSTRAINT chk_dr_description_not_empty
        CHECK (length(trim(description)) > 0),
    -- ── Dispute party denormalization ───────────────────────────────

    -- Vendor party to the dispute. Denormalized from the invoice for freeze/unfreeze
    -- queries (WHERE vendor_id = @vendor_id).
    --
    -- RESTRICT (not SET NULL): if this were SET NULL on vendor deletion, the freeze/
    -- unfreeze job would lose the ability to locate frozen payout records for this vendor,
    -- permanently trapping funds. RESTRICT ensures the vendor cannot be deleted while an
    -- active dispute exists, forcing explicit resolution before account deletion.
    vendor_id       UUID REFERENCES users(id) ON DELETE RESTRICT,

    -- Buyer party to the dispute. SET NULL on user deletion so the dispute record survives
    -- for vendor and admin audit purposes even after the buyer's account is removed.
    buyer_id        UUID REFERENCES users(id) ON DELETE SET NULL,

    -- Deadline by which the vendor must respond when status = 'awaiting_vendor'.
    -- Set to (NOW() + 7 days) when the dispute transitions to awaiting_vendor.
    -- The auto-resolution background job queries: WHERE status = 'awaiting_vendor'
    --   AND vendor_deadline < NOW() → auto-resolve to resolved_buyer.
    -- NULL for all other statuses.
    vendor_deadline TIMESTAMPTZ,

    -- Deadline for resolving escalated disputes. Set when status transitions to
    -- 'escalated'. The SLA monitoring job queries: WHERE status = 'escalated'
    --   AND escalation_deadline < NOW() → alert ops for immediate intervention.
    -- NULL for all non-escalated statuses.
    -- NOTE: There is no auto-resolution for escalated disputes; the platform must
    -- manually transition to a terminal status after legal/external review.
    escalation_deadline TIMESTAMPTZ,

    -- SLA start anchor. Usually equals created_at but is preserved separately so
    -- the 2-business-day first-response SLA can be measured even if created_at drifts.
    opened_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Invoice dispute lookup: "are there any open disputes for invoice X?"
CREATE INDEX idx_dr_invoice ON dispute_records(invoice_id) WHERE invoice_id IS NOT NULL;

-- Open dispute queue: "show all open disputes sorted by creation date."
CREATE INDEX idx_dr_open ON dispute_records(created_at DESC) WHERE status = 'open';

-- Per-raiser history: "show all disputes raised by vendor/buyer X."
CREATE INDEX idx_dr_vendor ON dispute_records(raised_by, created_at DESC);

COMMENT ON TABLE dispute_records IS
    'Buyer/vendor payment disputes. '
    'At least one of invoice_id or payout_record_id must be set (chk_dr_has_subject). '
    'Lifecycle: open → awaiting_vendor | awaiting_buyer | escalated → resolved_* | withdrawn. '
    'vendor_id is RESTRICT (not SET NULL): vendor cannot be deleted while an active dispute exists. '
    'This prevents frozen payout records from becoming permanently unresolvable after vendor deletion.';
COMMENT ON COLUMN dispute_records.vendor_id IS
    'RESTRICT FK: vendor cannot be deleted while this dispute is open. '
    'Application must resolve all non-terminal disputes before permitting vendor account deletion. '
    'Terminal disputes (resolved_*, withdrawn) have already unfrozen associated payout records.';
COMMENT ON COLUMN dispute_records.escalation_deadline IS
    'NULL for non-escalated disputes. Set when status transitions to escalated. '
    'Monitored by idx_dr_escalated_deadline. No auto-resolution — requires manual admin action.';

-- Auto-resolution background job: "which awaiting_vendor disputes have a past deadline?"
CREATE INDEX idx_dr_awaiting_vendor_deadline
    ON dispute_records(vendor_deadline)
    WHERE status = 'awaiting_vendor' AND vendor_deadline IS NOT NULL;

COMMENT ON INDEX idx_dr_awaiting_vendor_deadline IS
    'Used by the auto-resolution job: '
    'WHERE status = ''awaiting_vendor'' AND vendor_deadline < NOW() → auto-resolve to resolved_buyer.';

-- Escalated dispute SLA monitoring: alert ops when escalated disputes exceed their deadline.
-- Unlike awaiting_vendor, escalated disputes have NO auto-resolution path — manual action only.
-- This index supports: WHERE status = 'escalated' AND escalation_deadline < NOW()
CREATE INDEX idx_dr_escalated_deadline
    ON dispute_records(escalation_deadline)
    WHERE status = 'escalated' AND escalation_deadline IS NOT NULL;

COMMENT ON INDEX idx_dr_escalated_deadline IS
    'SLA monitoring for escalated disputes: '
    'WHERE status = ''escalated'' AND escalation_deadline < NOW() → alert ops for intervention. '
    'No auto-resolution path for escalated status — manual transition to terminal state required.';


/* ═════════════════════════════════════════════════════════════
   PROMOTE payout_records.dispute_id TO A REAL FK
   ═════════════════════════════════════════════════════════════ */

/*
 * In 015_btc_payouts.sql, payout_records.dispute_id was left as a plain UUID
 * because dispute_records did not exist yet. Now that dispute_records is created
 * above, we can add the FK constraint.
 *
 * ON DELETE SET NULL: if a dispute record is ever deleted (forensics-only path;
 * disputes are never hard-deleted in normal operation), payout_records are not
 * orphaned — dispute_id becomes NULL and hold_reason = 'dispute_hold' will still
 * identify the affected records for manual review.
 */
ALTER TABLE payout_records
    ADD CONSTRAINT fk_pr_dispute_id
        FOREIGN KEY (dispute_id) REFERENCES dispute_records(id) ON DELETE SET NULL;

COMMENT ON COLUMN payout_records.dispute_id IS
    'FK to dispute_records added in 022_btc_disputes.sql (was plain UUID in 015). '
    'SET NULL on dispute deletion (forensics path only). '
    'NULL unless hold_reason = ''dispute_hold''. Sweep job checks IS NOT NULL at broadcast '
    'boundary to abort sweep for dispute-frozen records.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE payout_records DROP CONSTRAINT IF EXISTS fk_pr_dispute_id;
DROP INDEX IF EXISTS idx_dr_escalated_deadline;
DROP INDEX IF EXISTS idx_dr_awaiting_vendor_deadline;
DROP TABLE IF EXISTS dispute_records CASCADE;

-- +goose StatementEnd
