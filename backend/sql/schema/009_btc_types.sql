-- +goose Up
-- +goose StatementBegin
/*
 * 009_btc_types.sql — Bitcoin payment system ENUM type registry.
 *
 * All custom ENUM types used across the BTC schema are defined here.
 * Every other BTC migration file depends on this file.
 *
 * Types defined:
 *   btc_wallet_mode            — vendor payout delivery model (bridge / platform / hybrid)
 *   btc_sweep_schedule         — sweep frequency (weekly / daily / realtime)
 *   btc_tier_status            — tier lifecycle (active / deactivating / inactive)
 *   btc_reconciliation_result  — health-check outcome (ok / discrepancy / error)
 *   btc_settling_source        — predecessor status for settling invoices
 *   btc_invoice_status         — 16-state invoice state machine
 *   btc_payout_status          — payout lifecycle (held → queued → … → confirmed)
 *   btc_kyc_status             — KYC state on vendor_wallet_config and payout_records
 *   btc_kyc_submission_status  — per-submission workflow state
 *   btc_monitoring_status      — ZMQ address watch state (active / retired)
 *   btc_dispute_status         — dispute lifecycle (open → … → resolved_*)
 *   btc_withdrawal_status      — platform-mode withdrawal request lifecycle
 *
 * Depends on: nothing (first BTC file)
 * Continued in: 010_btc_core.sql
 */

/* ═════════════════════════════════════════════════════════════
   ENUM TYPES
   ═════════════════════════════════════════════════════════════ */

-- Vendor wallet mode — governs how a vendor's settled earnings leave the platform.
--   bridge:   every settlement triggers an immediate on-chain sweep to the vendor's address.
--   platform: earnings accumulate as an internal balance; vendor withdraws manually.
--   hybrid:   earnings accumulate until a threshold is crossed, then auto-sweep fires.
-- This value is snapshotted onto every invoice at creation time. Changing the vendor's
-- live wallet_mode after invoice creation does not affect in-flight settlements.
CREATE TYPE btc_wallet_mode AS ENUM ('bridge', 'platform', 'hybrid');

COMMENT ON TYPE btc_wallet_mode IS
    'How a vendor receives BTC earnings after invoice settlement. '
    'Snapshotted immutably onto invoices at creation; live config changes do not affect in-flight invoices.';

-- Sweep schedule — how often queued payouts are batched and broadcast on-chain.
--   weekly:   Free tier — one batch per week; highest batch amortisation.
--   daily:    Growth/Pro — one batch per day.
--   realtime: Enterprise — sweep fires as soon as a payout record is queued.
CREATE TYPE btc_sweep_schedule AS ENUM ('weekly', 'daily', 'realtime');

COMMENT ON TYPE btc_sweep_schedule IS
    'Frequency at which queued payout records are swept to vendor addresses. '
    'Snapshotted onto invoices at creation.';

-- Tier lifecycle — replaces TEXT + CHECK on btc_tier_config.status (audit decision SEC-09).
-- Using an ENUM eliminates the silent-NULL problem: a NOT NULL ENUM column is always a
-- valid state, whereas a NOT NULL TEXT column with a CHECK can still accept NULLs if
-- the NOT NULL constraint is ever dropped in a future migration.
CREATE TYPE btc_tier_status AS ENUM ('active', 'deactivating', 'inactive');

COMMENT ON TYPE btc_tier_status IS
    'Tier lifecycle. active=new invoices allowed. deactivating=pre-sweep running, no new invoices. '
    'inactive=fully wound down. ENUM replaces TEXT+CHECK to eliminate silent-NULL risk.';

-- Reconciliation result — replaces TEXT + CHECK on reconciliation_job_state.
CREATE TYPE btc_reconciliation_result AS ENUM ('ok', 'discrepancy', 'error');

COMMENT ON TYPE btc_reconciliation_result IS
    'Outcome of a reconciliation run. ENUM replaces TEXT+CHECK to eliminate silent-NULL risk.';

-- Settling source — which invoice status the settling transition came from.
-- The valid values are a strict subset of btc_invoice_status. Keeping a separate ENUM
-- instead of TEXT + CHECK means adding a new invoice status never accidentally silences
-- a constraint that should still apply here. The value is preserved in settlement_failed
-- so an admin retry can take the correct code path (the settlement logic differs depending
-- on whether the invoice arrived from confirming vs underpaid).
CREATE TYPE btc_settling_source AS ENUM ('confirming', 'underpaid');

COMMENT ON TYPE btc_settling_source IS
    'Predecessor status for a settling invoice. Preserved in settlement_failed so '
    'admin retry takes the correct settlement path. ENUM prevents CHECK drift.';

-- Invoice status — the 16-state machine that governs the full invoice lifecycle.
-- State transitions are enforced at the application layer (settlement-technical.md §3).
-- The ENUM ensures the DB rejects any unrecognised state at the type level rather than
-- relying solely on the application to produce valid strings.
-- NEVER remove a value: existing live rows would fail to decode.
-- ADD values with: ALTER TYPE btc_invoice_status ADD VALUE 'new_state';
CREATE TYPE btc_invoice_status AS ENUM (
    'pending',                -- created, waiting for a payment to appear
    'detected',               -- payment seen in the mempool; expiry timer frozen
    'mempool_dropped',        -- payment disappeared from mempool before a block confirmation
    'confirming',             -- first block confirmation received; waiting for required depth
    'settling',               -- settlement worker has claimed this invoice (transient lock)
    'settled',                -- settlement complete; payout queued or balance credited
    'settlement_failed',      -- all retries exhausted; requires admin action to retry
    'reorg_admin_required',   -- invoice was swept then hit a block reorg; admin must verify
    'expired',                -- expiry window elapsed with no confirmed payment
    'expired_with_payment',   -- late payment arrived after expiry, within 30-day window
    'cancelled',              -- vendor cancelled before any payment was detected
    'cancelled_with_payment', -- vendor cancelled, but a payment arrived within 30-day window
    'underpaid',              -- received amount was below the invoiced amount minus tolerance
    'overpaid',               -- received amount exceeded both overpayment thresholds
    'refunded',               -- on-chain refund issued and confirmed on-chain
    'manually_closed'         -- admin wrote off the invoice after investigation
);

COMMENT ON TYPE btc_invoice_status IS
    'Invoice lifecycle: 16 states, 38 permitted transitions (settlement-technical.md §3). '
    'Never remove a value — live rows would fail to decode. '
    'Add new states with ALTER TYPE … ADD VALUE.';

-- Payout status — lifecycle from settlement credit through to on-chain confirmation.
-- Transitions are enforced by the fn_pr_status_guard trigger (011_btc_functions.sql)
-- in addition to the application layer. Terminal states (confirmed, refunded, manual_payout)
-- cannot be transitioned out of at the DB level regardless of application logic.
CREATE TYPE btc_payout_status AS ENUM (
    'held',         -- net amount is below the miner fee floor; accumulating
    'queued',       -- floor cleared; waiting for the next sweep window
    'constructing', -- sweep job has claimed this record for an active batch (transient)
    'broadcast',    -- sweep TX sent to the Bitcoin network; awaiting confirmations
    'confirmed',    -- sweep output confirmed on-chain at required depth — TERMINAL
    'failed',       -- permanent sweep failure after all retries — requires admin action
    'refunded',     -- payout reversed; funds returned to buyer on-chain — TERMINAL
    'manual_payout' -- admin declared an out-of-band payment — TERMINAL
);

COMMENT ON TYPE btc_payout_status IS
    'Payout lifecycle. constructing is transient: stale records (> 10 min) are reclaimed '
    'to queued by the stuck-sweep watchdog. Terminal states: confirmed, refunded, manual_payout. '
    'fn_pr_status_guard trigger enforces the transition matrix at the DB level.';

-- KYC/AML state — used on both vendor_wallet_config and payout_records.
-- The schema supports the enum today; actual KYC logic is gated behind tier thresholds
-- that are not yet configured. kyc_submissions (010_btc_payouts.sql) holds the backing data.
CREATE TYPE btc_kyc_status AS ENUM (
    'not_required', -- default; vendor below KYC threshold for their tier
    'pending',      -- submission in progress at the KYC provider
    'approved',     -- identity verified; no restrictions
    'rejected'      -- identity check failed; payouts may be blocked
);

COMMENT ON TYPE btc_kyc_status IS
    'KYC/AML verification state. Backed by kyc_submissions (010_btc_payouts.sql). '
    'Logic is gated behind non-NULL tier thresholds — currently a placeholder.';

-- Address monitoring lifecycle — whether the ZMQ subscriber is watching a given address.
-- Retired rows are kept permanently for the audit trail.
CREATE TYPE btc_monitoring_status AS ENUM (
    'active',  -- the ZMQ subscriber must watch this address
    'retired'  -- the monitoring window has elapsed; row kept for audit, no longer watched
);

COMMENT ON TYPE btc_monitoring_status IS
    'ZMQ watch state. Retired rows are kept permanently. '
    'The partial index WHERE status = ''active'' keeps hot-path queries fast.';


/* ═════════════════════════════════════════════════════════════
   KYC SUBMISSION STATUS
   ═════════════════════════════════════════════════════════════ */

-- Per-submission KYC workflow state. Separate from btc_kyc_status which tracks the
-- aggregate KYC gate on vendor_wallet_config and payout_records.
-- Transitions: submitted → under_review → approved → expired (terminal)
--              submitted → under_review → rejected (terminal for this submission)
-- (kyc-technical.md §1)
CREATE TYPE btc_kyc_submission_status AS ENUM (
    'submitted',    -- platform initiated flow; awaiting provider acknowledgment
    'under_review', -- provider confirmed receipt and is actively reviewing
    'approved',     -- identity verified; validity window begins at approved_at
    'rejected',     -- identity check failed; re-submission allowed after cooldown
    'expired'       -- approval validity window elapsed; re-submission required
);

COMMENT ON TYPE btc_kyc_submission_status IS
    'KYC submission workflow (kyc-technical.md §1). '
    'Separate from btc_kyc_status (aggregate gate on vendor_wallet_config/payout_records). '
    'Transitions: submitted→under_review→approved/rejected, approved→expired. '
    'expired is terminal for the submission row; a new submission is required. '
    'NEVER remove a value — live rows would fail to decode.';


/* ═════════════════════════════════════════════════════════════
   DISPUTE STATUS
   ═════════════════════════════════════════════════════════════ */

-- Full dispute lifecycle state machine (dispute-technical.md §1).
-- The original schema used ('open','investigating','resolved','rejected') — wrong.
-- Correct terminal states: resolved_vendor, resolved_buyer, resolved_platform, withdrawn.
-- NEVER remove a value — live rows would fail to decode.
CREATE TYPE btc_dispute_status AS ENUM (
    'open',                 -- dispute raised; under initial review
    'awaiting_vendor',      -- platform requested evidence from vendor (7-day SLA)
    'awaiting_buyer',       -- platform requested clarification from buyer
    'resolved_vendor',      -- resolved in vendor's favour; payouts unfrozen — TERMINAL
    'resolved_buyer',       -- resolved in buyer's favour; refund issued or queued — TERMINAL
    'resolved_platform',    -- platform absorbs loss (e.g. sweep already confirmed) — TERMINAL
    'withdrawn',            -- buyer withdrew dispute; payouts unfrozen — TERMINAL
    'escalated'             -- requires legal/external review; payouts remain frozen
);

COMMENT ON TYPE btc_dispute_status IS
    'Dispute lifecycle (dispute-technical.md §1). '
    'Terminal statuses: resolved_vendor, resolved_buyer, resolved_platform, withdrawn. '
    'All terminal transitions require step-up authentication. '
    'awaiting_vendor triggers a 7-day SLA timer (vendor_deadline on dispute_records). '
    'NEVER remove a value — live rows would fail to decode.';


/* ═════════════════════════════════════════════════════════════
   WITHDRAWAL REQUEST STATUS
   ═════════════════════════════════════════════════════════════ */

-- Platform-mode vendor withdrawal request lifecycle (settlement-feature.md §9).
-- Below approval threshold → auto_approved immediately.
-- Above threshold → pending_approval → admin reviews → approved/rejected.
CREATE TYPE btc_withdrawal_status AS ENUM (
    'pending_approval',  -- above threshold; awaiting admin approval
    'approved',          -- admin approved; payout_record created; queued for sweep
    'auto_approved',     -- below threshold; auto-approved with payout_record creation
    'rejected',          -- admin rejected — TERMINAL
    'cancelled',         -- vendor cancelled before broadcast — TERMINAL
    'completed'          -- payout_record reached confirmed status — TERMINAL
);

COMMENT ON TYPE btc_withdrawal_status IS
    'Platform-mode vendor withdrawal request lifecycle (settlement-feature.md §9). '
    'Terminal statuses: rejected, cancelled, completed. '
    'Below approval threshold: auto_approved immediately with payout_record creation. '
    'NEVER remove a value — live rows would fail to decode.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TYPE IF EXISTS btc_withdrawal_status;
DROP TYPE IF EXISTS btc_dispute_status;
DROP TYPE IF EXISTS btc_kyc_submission_status;
DROP TYPE IF EXISTS btc_monitoring_status;
DROP TYPE IF EXISTS btc_kyc_status;
DROP TYPE IF EXISTS btc_payout_status;
DROP TYPE IF EXISTS btc_invoice_status;
DROP TYPE IF EXISTS btc_settling_source;
DROP TYPE IF EXISTS btc_reconciliation_result;
DROP TYPE IF EXISTS btc_tier_status;
DROP TYPE IF EXISTS btc_sweep_schedule;
DROP TYPE IF EXISTS btc_wallet_mode;

-- +goose StatementEnd
