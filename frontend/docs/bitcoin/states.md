# Bitcoin Frontend States

This file normalizes the UI states the frontend team should account for.

These states come from backend behavior and schema, not from the current frontend styling.
The current UI may expose some of them in temporary testing form only.

## Cross-surface state groups

Every Bitcoin surface should define these clearly:

- loading
- empty
- success
- warning
- error
- action-required
- disabled / unavailable

## Implemented surfaces

### Live Events

Backend references:

- `backend/internal/domain/bitcoin/events/`
- `backend/internal/platform/bitcoin/zmq/`

Core transport states:

- connecting
- connected
- reconnecting
- inactive
- error

Event-feed states:

- no events yet
- new block received
- mempool event received
- confirmed tx received
- mempool replacement received
- stream requires re-registration

Operator-facing states:

- watcher closed
- watcher open
- block selected
- block loading
- block lookup failed

### Watch Addresses

Backend references:

- `backend/internal/domain/bitcoin/watch/`

States to design:

- valid address
- invalid address
- unsupported network
- duplicate / already watched
- watch limit reached
- rate-limited
- watched successfully
- watch retired or expired
- re-registration needed

### Tx Lookup

Backend references:

- `backend/internal/domain/bitcoin/txstatus/`

States to design:

- single lookup idle
- batch lookup idle
- loading
- found
- not found
- partially failed batch
- invalid tx id input
- upstream RPC unavailable

### Block Details

Backend references:

- `backend/internal/domain/bitcoin/block/`

States to design:

- no block selected
- loading details
- details loaded
- block not found
- RPC unavailable

## Future state groups from schema

These are not all implemented in frontend yet, but the design team should keep them in mind now.

### Invoice states

From `009_btc_types.sql` and `012_btc_invoices.sql`:

- pending
- detected
- mempool_dropped
- confirming
- settling
- settled
- settlement_failed
- reorg_admin_required
- expired
- expired_with_payment
- cancelled
- cancelled_with_payment
- underpaid
- overpaid
- refunded
- manually_closed

### Payout states

From `009_btc_types.sql` and `015_btc_payouts.sql`:

- held
- queued
- constructing
- broadcast
- confirmed
- failed
- refunded
- manual_payout

### KYC states

From `009_btc_types.sql` and `019_btc_compliance.sql`:

- not_required
- pending
- approved
- rejected
- submitted
- under_review
- expired

### Dispute states

From `009_btc_types.sql` and `022_btc_disputes.sql`:

- open
- awaiting_vendor
- awaiting_buyer
- escalated
- resolved_vendor
- resolved_buyer
- resolved_platform
- withdrawn

### Withdrawal states

From `009_btc_types.sql` and `027_btc_platform_wallet.sql`:

- pending_approval
- approved
- auto_approved
- rejected
- cancelled
- completed

## Design rule

Do not invent visual states that collapse backend distinctions too early.

Good:

- one badge style system
- one state-to-color system
- different copy for `reconnecting` versus `inactive`
- different review treatment for `settlement_failed` versus `reorg_admin_required`

Bad:

- one generic `error` badge for every operational condition
- one generic `pending` label across invoices, payouts, KYC, and withdrawals
