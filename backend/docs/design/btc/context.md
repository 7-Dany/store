# Bitcoin ZMQ — Resolved Context

**Section:** New domain — `internal/platform/bitcoin/` + `internal/domain/bitcoin/`
**Status:** Stage 0 approved

## Resolved paths

> **NOTE:** This file reflects the initial design context. The authoritative path
> reference is `1-zmq-system.md §5.2`. The `wallet/` path below was superseded by
> `watch/` during design review. Use `watch/` everywhere.
> The original `1-zmq-infrastructure-pre-audit.md` has been split into:
> - `1-zmq-system.md` — system design (HTTP contract, ADRs, package structure)
> - `1-zmq-technical.md` — implementation guide organized by stage
> The original file is preserved in Git history if needed.

- ZMQ platform: `internal/platform/bitcoin/zmq/` (subscriber.go, event.go)
- RPC platform:  `internal/platform/bitcoin/rpc/` (client.go, types.go)
- Domain root:   `internal/domain/bitcoin/routes.go`
- Shared errors: `internal/domain/bitcoin/shared/errors.go`
- Feature:       `internal/domain/bitcoin/watch/`  ~~(`wallet/` — old name, superseded)~~
  - handler.go, service.go, store.go, routes.go
  - ssetoken.go, models.go, requests.go, errors.go, validators.go
- Config:        `internal/config/config.go` (BitcoinEnabled, RPC*, ZMQ*, Network, and all additions in `prerequisites/config.md`)
- Deps:          `internal/app/deps.go` (BitcoinZMQ, BitcoinRPC, BitcoinNetwork, BitcoinRedis — see `prerequisites/app-deps.md`)
- bitcoin.conf:  `D:\bitcoin\data\bitcoin.conf` (zmqpubhashblock, zmqpubhashtx)

## Key decisions

- D-02: `go-zeromq/zmq4` (pure Go, no CGo, cross-platform)
- D-03: `hashblock` + `hashtx` topics only (not raw*)
- D-04: Handler registration pattern for platform→domain fan-out
- D-05: Address matching in domain via RPC getrawtransaction
- D-06: In-memory sync.Map watch list per userID (V1, no DB)
- D-07: SSE (not WebSocket) for event stream
- D-08: `?token=` query param for SSE auth
- D-10: New `internal/domain/bitcoin/` domain (never imports other domains)
- D-12: Feature opt-in via `BTC_ENABLED=false` default

## New audit events

- EventBitcoinAddressWatched = "bitcoin_address_watched"
- EventBitcoinTxDetected     = "bitcoin_tx_detected"

## New sentinel errors

- ErrZMQNotRunning     — bitcoinshared/errors.go
- ErrInvalidAddress    — bitcoinshared/errors.go
- ErrUnsupportedNetwork — bitcoinshared/errors.go

## Rate-limit prefixes

> **NOTE:** Paths updated to `watch/` (see resolved paths note above).
> Full prefix list in `1-zmq-infrastructure-pre-audit.md §10.1`.

- btc:watch:ip:    — POST /bitcoin/watch        (10/min IP)
- btc:token:ip:    — POST /bitcoin/events/token  (5/min IP)
- btc:events:ip:   — GET  /bitcoin/events         (5/min IP)
- btc:status:ip:   — GET  /bitcoin/status          (20/min IP)
- btc:sse:conn:    — per-user SSE connection counter (ConnectionCounter key prefix)
- btc:global:watch_count — cross-instance watch count estimate (advisory)

## Test case IDs

- S-layer: T-01 to T-09
- H-layer: T-10 to T-22
- I-layer: T-23 (placeholder — no DB in V1)

---

# Bitcoin Transaction System — Stage 2 Context

**Status:** Design complete — ready for Stage 2 (schema design)
**Design docs:** Split into per-package files under `docs/design/btc/`. See package
map below.

---

## Stage 2 Implementation Order

Stage 2 is split into three sub-stages gated by financial risk. Each stage ships and
stabilises before the next begins.

### Stage 2a — `invoice` package: detection only, nothing moves
- Creates invoices (both-step RPC: `getnewaddress` + `getaddressinfo`)
- Immediately registers new addresses with ZMQ subscriber
- Initialises DB-backed `invoice_address_monitoring` and `btc_outage_log` tables
- Tracks invoice status through `pending` → `detected` → `confirming`
- **Financial risk: Zero** — no money moves, no fee calculations, no payout records

### Stage 2b — `settlement` package: accounting only, no on-chain transaction
- Atomic settlement with Phase 1 pre-claim checks (tolerance check BEFORE settling claim)
- Underpaid re-settlement via `underpaid → settling` atomic claim
- Handles reorg rollback, backfill on recovery, hybrid mode auto-sweep threshold check
- **Financial risk: DB accounting only.** Ships when `bitcoin_balance_drift_satoshis`
  = 0 for ≥ 1 week on testnet4.

### Stage 2c — `sweep` package: real Bitcoin moves
- Full PSBT flow: `walletcreatefundedpsbt` → `walletprocesspsbt` → `finalizepsbt` →
  DB update (`constructing → broadcast` with txid) → `sendrawtransaction` with
  `maxfeerate` guard.
  **DB update must commit before `sendrawtransaction` is called — this is a hard invariant.**
- Batch consolidation (max 100 outputs), sweep confirmation at 3 blocks, UTXO management
- Default in beta: `BTC_AUTO_SWEEP_ENABLED=false`
- **Financial risk: Real Bitcoin leaves the platform**

---

## Integration with Existing Platform Systems

| System | How the Bitcoin system uses it |
|--------|-------------------------------|
| **RBAC roles** | Tier assignment changes the vendor's RBAC role |
| **Approval workflow** | Withdrawals above threshold enter the existing approval pipeline |
| **Billing system** | Subscription fee debit requests; rate-staleness deferral response flows back |
| **Job queue** | Settlement, sweep, reconciliation, UTXO consolidation, address expiry monitoring, fee estimation caching, mempool watchdog, `constructing` stale watchdog, reorg monitor, wallet backup monitor, underpaid-aging monitor, held-aging monitor, stale-outage-log cleaner, fee-floor re-evaluation |
| **ZMQ infrastructure (Stage 0)** | Settlement engine receives payment detection and block events |
| **Financial audit trail** | Append-only trail, indefinite retention |
| **Product listing system** | Enforces bridge-mode address requirement and active-product check |
| **Bitcoin Core wallet (RPC)** | Address generation (P2WPKH bech32, label="invoice"), derivation index retrieval, sweep construction, signing, broadcast, fee estimation, backup, address ownership check (`getaddressinfo`) |

---

## Prerequisites

Read before writing any Stage 2 code:

| File | Purpose |
|------|---------|
| `prerequisites/config.md` | All `BTC_*` env variables, types, defaults, validation ranges |
| `prerequisites/audit.md` | Platform audit package extensions — new `EventType` constants, `AllEvents()`, test cases |
| `prerequisites/schemas.md` | Index of every DB table, owner package, DDL location, known gaps |
| `prerequisites/contracts.md` | Cross-package interface boundaries — what each package promises to others |
| `prerequisites/app-deps.md` | `app.Deps` new fields, constructor invariant for Redis wiring |
| `prerequisites/kvstore.md` | kvstore interface extensions (`RefreshTTL`, `AtomicCounterStore`, `ListStore`, `PubSubStore`) |
| `prerequisites/ratelimit.md` | `ConnectionCounter` type — acquire/release/heartbeat for SSE connection ceilings |
| `prerequisites/rbac.md` | New `PermBitcoin*` constants; Stage 2 DB seed migration checklist |
| `prerequisites/token.md` | `token.Sign` low-level primitive for SSE token generation |

---

## Package Map

Each package has a `{name}-feature.md` (behavior, rules, edge cases) and a
`{name}-technical.md` (schemas, state machines, implementation contracts, test
inventory). Read the feature file first.

| Package | Feature doc | Technical doc | Primary content |
|---------|-------------|---------------|-----------------|
| `invoice/` | invoice-feature.md | invoice-technical.md | Vendor wallet modes, invoice creation, address lifecycle, expiry rules, monitoring table schema |
| `payment/` | payment-feature.md | payment-technical.md | Payment detection, confirmation depths, mempool drop watchdog, btc_outage_log schema |
| `settlement/` | settlement-feature.md | settlement-technical.md | Settlement phases, underpay/overpay/hybrid, invoice state machine, payout state machine, atomicity contracts |
| `sweep/` | sweep-feature.md | sweep-technical.md | Fee system, sweep models, PSBT broadcast sequence, RBF, batch integrity, UTXO consolidation |
| `rate/` | rate-feature.md | rate-technical.md | BTC/fiat rate cache, deviation policy, failure behavior, stale subscription debits |
| `resilience/` | resilience-feature.md | resilience-technical.md | Degraded mode, reorg rollback, post-outage backfill, HandleRecovery flow |
| `vendor/` | vendor-feature.md | vendor-technical.md | Tier config, vendor lifecycle, regulatory context, step-up auth (TOTP) |
| `audit/` | audit-feature.md | audit-technical.md | Financial audit trail, reconciliation formula (monitoring events → see monitoring doc) |
| `wallet-backup/` | wallet-backup-feature.md | wallet-backup-technical.md | Backup layers, recovery scenarios (A/B/C/D), keypool cursor advance, pre-mainnet checklist |

Packages from Stage 0 (unchanged):

| Package | Docs |
|---------|------|
| `zmq/` | zmq-feature.md, zmq-technical.md |
| `rpc/` | rpc-feature.md, rpc-technical.md |
| `events/` | events-feature.md, events-technical.md |
| `txstatus/` | txstatus-feature.md, txstatus-technical.md |
| `watch/` | watch-feature.md, watch-technical.md |

**Monitoring:** `../../monitoring/bitcoin-monitoring.md` — Prometheus metrics,
alert rules, dashboards, runbooks, and the canonical financial monitoring events
inventory (§11).

---

## Open Items (Blocking Mainnet)

| # | Item | Blocks |
|----|------|--------|
| O-01 | Jurisdiction determination for platform wallet mode | Platform wallet mode launch |
| O-02 | Legal ToS review for platform wallet mode | Platform wallet mode launch |
