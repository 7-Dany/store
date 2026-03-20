# Telemetry Instrumentation Checklist

> Use this every time you add telemetry to a package.
> Work through each gate in order — stop as soon as you reach a "No".

---

## Gate 1 — Logging (always yes)

Every package gets a package-level logger. No decision needed.

```go
var log = telemetry.New("package-name")
```

**Terminal?** Yes — every level (Debug/Info/Warn/Error) writes JSON to stdout.
**Prometheus?** No — logging alone touches nothing in Prometheus.
**Dashboard?** No.

---

## Gate 2 — Error wrapping (always yes for returned errors)

Every error returned up the call stack gets wrapped with the right constructor.

```go
return telemetry.Store("GetOrder.query", err)
return telemetry.Service("PlaceOrder.validate", err)
```

**Terminal?** Only when a `log.Error` call logs it.
**Prometheus?** Automatically — `TelemetryHandler` fires `app_errors_total`
on every `log.Error`. No registration needed, no field to add.
**Dashboard?** Already covered by the existing "Errors by component" bar chart
and `app_errors_total` queries. Nothing to add.

---

## Gate 3 — Should this event have its own metric?

Ask: **"Would I page someone specifically because of this signal, or investigate
it independently of the HTTP error rate?"**

| Answer | Action |
|--------|--------|
| No — a 500 error rate spike already captures it | Stop here. Gate 2 is enough. |
| Yes — it needs its own counter/gauge | Continue to Gate 4. |

**Examples that stop at Gate 2:**
- A database query fails → `app_errors_total{component="orders", layer="store"}` covers it.
- A handler returns 422 bad request → `http_requests_total{status="422"}` covers it.
- A background job crashes → `app_errors_total{component="worker.purge"}` covers it.

**Examples that need their own metric:**
- User places an order → business throughput signal, not an error.
- Login fails with wrong password → security signal, not an infrastructure error.
- Bitcoin invoice detected → financial event, zero-tolerance for missed detections.
- Password reset requested for non-existent email → enumeration attack signal.

---

## Gate 4 — Which family does this metric belong to?

| The event is about… | Family | Prefix |
|---------------------|--------|--------|
| HTTP request shape | 1 — HTTP | `http_` — **already covered automatically** |
| An error in any package | 2 — App Errors | `app_errors_` — **already covered automatically** |
| Auth / account / session | 3 — Auth | `auth_` |
| DB pool / Redis / goroutines / process | 4 — Infra | `db_pool_` / `redis_pool_` / `process_` — **already covered automatically** |
| Job queue lifecycle | 5 — Job Queue | `jobqueue_` — **already covered automatically** |
| Bitcoin / payments | 6 — Bitcoin | `bitcoin_` |
| Something else (new domain) | New family — justify it | new prefix |

If the family is covered automatically (families 1, 2, 4, 5) and no
independent signal is needed → **stop here**. You already have the signal.

---

## Gate 5 — What kind of Prometheus instrument?

| The event… | Instrument | Example |
|------------|------------|---------|
| Counts occurrences (never goes down) | `CounterVec` | login attempts, orders placed |
| Measures current state | `Gauge` | pool size, active connections, drift satoshis |
| Measures duration / distribution | `HistogramVec` | job duration, settlement latency |

**Counter naming rule:** always end in `_total`.
**Histogram naming rule:** always end in `_seconds`, `_bytes`, or the unit.

---

## Gate 6 — Does this metric need a label?

Ask: **"Do I need to split this metric by a dimension to make it actionable?"**

| Answer | Action |
|--------|--------|
| No — the total count is enough | Use a plain `prometheus.Counter`, not a Vec. |
| Yes — I need to filter by X | Add `{x}` as a label. |

**Label safety rules (mandatory):**
- Label values must come from a **bounded constant set** — never from user input,
  URL path segments, error messages, UUIDs, or email addresses.
- Document the allowed values as Go constants next to the interface method.
- Estimated cardinality = product of all label value counts. Keep it under 100
  per metric. Check `monitoring-technical.md §21` for the budget.

```go
// Good — bounded set, documented
const (
    OrderMethodCard   = "card"
    OrderMethodBTC    = "bitcoin"
    OrderMethodWallet = "wallet"
)

// Bad — unbounded, never do this
r.ordersPlaced.WithLabelValues(req.UserID).Inc()   // one series per user
r.ordersPlaced.WithLabelValues(err.Error()).Inc()   // unbounded string
```

---

## Gate 7 — Where does the code live?

| What | Where |
|------|-------|
| Interface method declaration | `domain/<domain>/shared/recorder.go` |
| Noop implementation | same file, `NoopXxxRecorder` struct |
| Counter/gauge field on Registry | `internal/platform/telemetry/metrics.go` |
| Hook method on `*Registry` | `internal/platform/telemetry/<domain>_hooks.go` |
| Registration in `NewRegistry()` | `internal/platform/telemetry/metrics.go` |
| Local narrow interface in sub-package | `domain/<domain>/<subpkg>/service.go` |
| Recorder call at the event site | same service file |

Sub-packages **never** import `domain/<domain>` — they define a local narrow
interface containing only the methods they call. `*telemetry.Registry` satisfies
it structurally.

---

## Gate 8 — Does this metric need an alert?

Ask: **"If this metric spikes or crosses a threshold at 3am, does someone need
to wake up or investigate within the hour?"**

| Answer | Severity | Channel |
|--------|----------|---------|
| No — visible on dashboard is enough | none | Dashboard only |
| Investigate within the hour | `warning` | Slack `#alerts` |
| Security/fraud event | `warning` + `team: security` | Slack `#security-alerts` |
| SLO breached or funds at risk | `critical` | PagerDuty |
| Any single occurrence is unacceptable | `critical`, `for: 0m` | PagerDuty |

Add the alert YAML to `monitoring-technical.md §20`.

---

## Gate 9 — Does this metric belong on the frontend dashboard?

Ask: **"Would an engineer monitoring the system want to see this in real time,
and is it not already covered by the existing panels?"**

**Already covered — no frontend change needed:**
- HTTP error rate, request rate, latency → `MetricAreaChart` panels
- App errors by component → `ErrorBarChart`
- Service health (DB, Redis, mailer, jobs) → `ServiceGrid`
- Any anomaly that has an alert rule → `AnomalyFeed` (add a case in
  `computeAnomalies()` in `prometheus.ts`)

**Needs a frontend change:**
- A new stat you want as a number card → add to `fetchSecuritySnapshot()` in
  `prometheus.ts` and add a `StatCard` in `security-dashboard.tsx`.
- A new time-series chart → add a `rangeQuery` call and a `MetricAreaChart`.
- A new anomaly detection rule → add a case in `computeAnomalies()` only —
  no new Prometheus query needed.

---

## Summary — the full decision in one table

| You are adding… | Terminal log | `app_errors_total` | New metric | Alert | Dashboard |
|---|:---:|:---:|:---:|:---:|:---:|
| `var log = telemetry.New(...)` | ✅ | — | — | — | — |
| `telemetry.Store/Service(...)` wrap | on `log.Error` | on `log.Error` | — | — | — |
| `log.Warn(...)` | ✅ | — | — | — | — |
| `log.Error(...)` | ✅ | ✅ auto | — | via existing alert | via existing chart |
| New recorder method (business event) | — | — | ✅ | if needed | if needed |
| New alert rule | — | — | — | ✅ | `computeAnomalies()` |

---

## Quick reference — what you touch per scenario

### Scenario A: New API route, no special business event
```
✅ var log = telemetry.New("orders")          ← Gate 1
✅ return telemetry.Store("GetOrder.q", err)  ← Gate 2
   log.Error / log.Warn at call sites
   ─────────────────────────────────────────
   Nothing else. HTTP + error metrics are free.
```

### Scenario B: New business event worth counting (e.g. order placed)
```
✅ Gate 1-2 as above
✅ domain/orders/shared/recorder.go     → add OnOrderPlaced(method string)
✅ telemetry/metrics.go                 → add ordersPlaced *prometheus.CounterVec field
✅ telemetry/orders_hooks.go            → implement OnOrderPlaced on *Registry
✅ telemetry/metrics.go NewRegistry()   → register the counter
✅ domain/orders/service.go             → local narrow recorder interface + call site
   ─────────────────────────────────────────
   Optional: alert in monitoring-technical.md §20
   Optional: stat card / chart in prometheus.ts + security-dashboard.tsx
```

### Scenario C: New security signal (e.g. failed 2FA attempts)
```
✅ Everything in Scenario B
✅ Alert with team: security label in monitoring-technical.md §20
✅ Case in computeAnomalies() in prometheus.ts
✅ Stat card in security-dashboard.tsx
```

### Scenario D: New infrastructure dependency (e.g. new external API)
```
✅ Gate 1-2 as above
✅ New gauge/counter in metrics.go for connectivity / error rate
✅ InfraPoller or hook depending on whether it's polled or event-driven
✅ ServiceHealth entry in deriveServices() in prometheus.ts
✅ Warning alert if it can go down
```
