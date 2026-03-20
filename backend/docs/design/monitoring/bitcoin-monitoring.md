> **Navigation note:**
> This file is the **exhaustive canonical reference** for Bitcoin monitoring —
> full metric inventory, all alert YAML, all dashboard panel specs, all runbooks,
> and the financial event inventory (§11). It is retained as the single source
> of truth. The monitoring system architecture lives in `monitoring-feature.md`
> and `monitoring-technical.md`.
>
> **Recorder pattern:** `BitcoinRecorder` is defined in `domain/bitcoin/shared/recorder.go`
> and satisfied structurally by `*telemetry.Registry` — the same pattern as
> `jobqueue.MetricsRecorder` and `authshared.AuthRecorder`. There is no
> `prometheusBitcoinRecorder` wrapper and no `Registry.Bitcoin()` factory.
> Bitcoin sub-packages (`zmq`, `invoice`, `settlement`, `sweep`, etc.) each
> define their own local narrow `recorder` interface containing only the methods
> they need. `deps.Metrics` is passed directly and satisfies each one
> structurally. See `monitoring-technical.md §12` for the full interface
> definition, noop implementation, wiring examples, and sub-package patterns.

# Bitcoin Payment System — Monitoring Design

**Status:** Active — evolves as Stage 2a / 2b / 2c ship
**Scope:** All Prometheus metrics, alert rules, dashboards, runbooks, and the
financial monitoring event inventory for the Bitcoin payment domain across all
implementation stages.
**Companion documents:**
- `monitoring-feature.md` — monitoring system architecture, signal taxonomy, SLOs, alert model
- `monitoring-technical.md` — full telemetry API; §12 covers BitcoinRecorder interface and wiring
- `../btc/zmq/zmq-technical.md` — Stage 0 ZMQ metrics detail
- `../btc/context.md` — Stage 2 feature split and package map
- `../btc/audit/audit-feature.md` — financial audit trail and reconciliation
- `../btc/audit/audit-technical.md` — reconciliation formula SQL, treasury reserve
- `../jobqueue/metrics/metrics-feature.md` — job queue metrics pattern (structural satisfaction reference)
