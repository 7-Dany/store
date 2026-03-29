# Bitcoin Frontend Surfaces

This file translates backend packages and schema slices into frontend surfaces.

## Current design surfaces

These should be treated as immediate design work because the backend already exists.
The current frontend implementation around them is only a testing shell.

### 1. Live Events workspace

Backend references:

- `backend/internal/domain/bitcoin/events/`
- `backend/internal/platform/bitcoin/zmq/`
- `backend/internal/platform/bitcoin/rpc/`

Required UI areas:

- connection indicator
- connection details popover
- event watcher sheet
- recent signals list
- block detail drill-down
- stream error and recovery states

Recommended structure:

- compact page header
- recent signals as primary canvas
- watcher as optional sheet, not permanent chrome
- block details as stacked sheet or inspector depending on density
- do not preserve the current test layout unless it directly supports the final workspace

### 2. Watch Addresses workspace

Backend references:

- `backend/internal/domain/bitcoin/watch/`

Required UI areas:

- add address flow
- validation feedback
- capacity / limits
- watched address table
- remove / retire actions
- re-registration guidance when watches expire

Recommended structure:

- form + active registry
- clear distinction between registration action and watched inventory
- use the current implementation as behavior reference only

### 3. Tx Lookup workspace

Backend references:

- `backend/internal/domain/bitcoin/txstatus/`

Required UI areas:

- single tx lookup
- batch tx lookup
- result states
- partial failures
- empty and loading states

Recommended structure:

- query entry on top
- results below
- batch mode should feel operational, not like a generic textarea tool
- use the current implementation as behavior reference only

### 4. Block Details surface

Backend references:

- `backend/internal/domain/bitcoin/block/`

Required UI areas:

- selected block hash
- height
- confirmations
- tx count
- mined time
- difficulty
- bits

Recommended structure:

- secondary surface invoked from events, not a top-level workspace
- use the current implementation as contract reference only

## Near-term surfaces to prepare for

These are not implemented in backend handlers yet, but schema and design docs already define their shape.

### Invoices

Schema references:

- `012_btc_invoices.sql`
- `014_btc_infrastructure.sql`

Likely surfaces:

- invoice list
- invoice details
- payment attempts
- invoice address lifecycle
- expiry and outage compensation explanation

### Settlement and payouts

Schema references:

- `010_btc_core.sql`
- `015_btc_payouts.sql`

Likely surfaces:

- payout queue
- held payouts
- sweep batches
- vendor balances
- platform config controls

### Governance and evidence

Schema references:

- `017_btc_audit.sql`
- `019_btc_compliance.sql`
- `021_btc_webhooks.sql`
- `022_btc_disputes.sql`
- `023_btc_history.sql`

Likely surfaces:

- audit event ledger
- KYC review
- compliance case records
- webhook delivery log
- dispute review
- config history and operational history

### Platform-wallet and recurring money movement

Schema references:

- `026_btc_subscription_debits.sql`
- `027_btc_platform_wallet.sql`

Likely surfaces:

- debit ledger
- stale-rate deferral state
- withdrawal request flow
- approval queue

## Layout rules for the frontend team

- Keep one Bitcoin workspace shell and let modules plug into it.
- Avoid separate visual languages for each package.
- Prefer sheets, dialogs, and inspectors for secondary detail.
- Keep monitoring and action surfaces distinct.
- Treat `watch`, `events`, `txstatus`, and `block` as one operational cluster.
- Reserve denser review tables and audit surfaces for later Governance modules.
