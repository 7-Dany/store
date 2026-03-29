# Bitcoin Design Stages

This document splits Bitcoin frontend design into stages that match the backend code and schema shape.

The rule is simple:

- Design the implemented surfaces as real product now.
- Design the later surfaces as extension points, not as separate products.

## Stage 0: Implemented foundation

Backend source:

- `backend/internal/platform/bitcoin/zmq/`
- `backend/internal/platform/bitcoin/rpc/`
- `backend/internal/domain/bitcoin/watch/`
- `backend/internal/domain/bitcoin/events/`
- `backend/internal/domain/bitcoin/txstatus/`
- `backend/internal/domain/bitcoin/block/`

Frontend design scope now:

- connection health and event stream intake
- event watcher and recent signal inspection
- address watch registration and active watched-address management
- transaction status lookup
- block detail inspection

Current frontend reality:

- the existing Bitcoin page is a testing harness
- it validates backend connectivity, states, and actions
- it should not be treated as the final navigation, composition, or visual system

UI modules now:

- `Operations > Live Events`
- `Operations > Watch Addresses`
- `Operations > Tx Lookup`
- `Operations > Block Details`

Primary UX goal:

- Make Bitcoin feel like an operator workspace, not a crypto marketing page.
- replace the current test-oriented shell with a durable product shell that later stages can join

## Stage 1: Invoice and payment intake

Schema anchors:

- `012_btc_invoices.sql`
- `014_btc_infrastructure.sql`

What enters the product:

- invoices
- invoice addresses
- invoice payments
- invoice address monitoring
- outage-aware expiry behavior
- block history as payment-processing support

Frontend surfaces to plan:

- invoice list
- invoice details
- payment timeline
- address lifecycle panel
- late payment / expired with payment flows
- outage-aware status explanation

Navigation cluster:

- `Money Flow > Invoices`
- `Money Flow > Payments`

## Stage 2: Settlement and payout operations

Schema anchors:

- `010_btc_core.sql`
- `015_btc_payouts.sql`

What enters the product:

- tier configuration effects
- vendor wallet configuration
- vendor balances
- platform config
- sync state
- payout records
- held / queued / constructing / broadcast / confirmed lifecycle

Frontend surfaces to plan:

- payout queue
- held payout review
- sweep batch monitor
- vendor balance view
- tier and fee visibility
- operational controls around sweep hold mode

Navigation cluster:

- `Money Flow > Settlements`
- `Money Flow > Payouts`
- `Operations > Sweep Control`

## Stage 3: Reconciliation and operational history

Schema anchors:

- `017_btc_audit.sql`
- `023_btc_history.sql`

What enters the product:

- financial audit trail
- operational audit log
- tier config history
- vendor wallet config history
- reconciliation run history
- SSE token issuance history
- ZMQ dead letters

Frontend surfaces to plan:

- reconciliation dashboard
- audit timeline
- config history compare view
- dead-letter review
- operator incident timeline

Navigation cluster:

- `Operations > Reconciliation`
- `Governance > Audit`
- `Governance > History`

## Stage 4: Compliance and external delivery

Schema anchors:

- `019_btc_compliance.sql`
- `021_btc_webhooks.sql`
- `022_btc_disputes.sql`

What enters the product:

- KYC submissions
- FATF Travel Rule records
- GDPR erasure requests
- webhook config and deliveries
- dispute lifecycle

Frontend surfaces to plan:

- KYC review queue
- compliance dossier
- webhook configuration and delivery logs
- dispute case management
- policy-gated action flows

Navigation cluster:

- `Governance > KYC`
- `Governance > Compliance`
- `Governance > Webhooks`
- `Governance > Disputes`

## Stage 5: Platform-wallet and recurring billing

Schema anchors:

- `026_btc_subscription_debits.sql`
- `027_btc_platform_wallet.sql`

What enters the product:

- subscription debit tracking
- stale-rate deferrals
- platform-wallet withdrawal requests
- approval queue for withdrawals

Frontend surfaces to plan:

- subscription debit ledger
- stale-rate recovery state
- withdrawal request flow
- withdrawal approval queue

Navigation cluster:

- `Money Flow > Subscription Debits`
- `Money Flow > Withdrawals`

## IA guidance across all stages

Keep new Bitcoin features grouped under three long-term clusters:

- `Operations`
  - live events, watch, block inspection, sweep control, reconciliation
- `Money Flow`
  - invoices, payments, settlements, payouts, debits, withdrawals
- `Governance`
  - audit, history, compliance, KYC, disputes, webhooks

Do not let every backend package become a first-level nav item.
