# Bitcoin Frontend Design Reference

This folder is the frontend-facing reference for Bitcoin product design.

The current Bitcoin UI is a testing and validation surface over implemented backend capabilities.
It is not the target product design baseline.

Use it to answer four questions before designing any new Bitcoin screen:

1. What is already implemented in backend code today?
2. Which schema tables and state machines shape the product?
3. Which surfaces should be designed now versus later?
4. How should future features fit the same information architecture?

## Source of truth

Use these sources in this order:

1. Implemented backend packages
   - `backend/internal/platform/bitcoin/zmq/`
   - `backend/internal/platform/bitcoin/rpc/`
   - `backend/internal/domain/bitcoin/watch/`
   - `backend/internal/domain/bitcoin/events/`
   - `backend/internal/domain/bitcoin/txstatus/`
   - `backend/internal/domain/bitcoin/block/`
   - `backend/internal/domain/bitcoin/shared/`
2. BTC design docs for future packages
   - `backend/docs/design/btc/`
3. BTC schema slices
   - `backend/sql/schema/009_btc_types.sql`
   - `backend/sql/schema/010_btc_core.sql`
   - `backend/sql/schema/012_btc_invoices.sql`
   - `backend/sql/schema/014_btc_infrastructure.sql`
   - `backend/sql/schema/015_btc_payouts.sql`
   - `backend/sql/schema/017_btc_audit.sql`
   - `backend/sql/schema/019_btc_compliance.sql`
   - `backend/sql/schema/021_btc_webhooks.sql`
   - `backend/sql/schema/022_btc_disputes.sql`
   - `backend/sql/schema/023_btc_history.sql`
   - `backend/sql/schema/026_btc_subscription_debits.sql`
   - `backend/sql/schema/027_btc_platform_wallet.sql`

## What exists now

These are the Bitcoin capabilities that should be treated as active frontend design scope:

- Live blockchain events and SSE stream handling
- Address watch registration and watch-set management
- Transaction status lookup
- Block details lookup

These are implemented enough that frontend should design against real behavior, not imagined behavior.
Design against their contracts and workflows, not against the current test-shell layout.

## What is coming next

The current backend and schema already define the shape of later modules:

- invoices and invoice address lifecycle
- payment detection and outage-aware expiry
- settlement and payout records
- sweep and broadcast operations
- reconciliation and operational health
- audit trail and history
- KYC, FATF, GDPR, disputes, and webhooks
- subscription debits and platform-wallet withdrawals

Design should keep the current Bitcoin UI small, but leave room for those domains to attach without reshaping the whole workspace.

## Files in this folder

- `stages.md`
  - staged rollout map from implemented features to full Bitcoin operations
- `surfaces.md`
  - current and future screens, panels, sheets, dialogs, and navigation clusters
- `states.md`
  - frontend state inventory for implemented modules and upcoming domains
