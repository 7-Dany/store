# Phases Feature — Implementation Order & Parallelization

> **What this file is:** A plain-language description of the 7 implementation
> phases, what each phase delivers, which phases can run in parallel, and what
> "done" means for each. Read this before starting any implementation work.
>
> **Companion:** `phases-technical.md` — per-phase file lists, gate SQL/commands,
> smoke test instructions.
> **Source of truth:** `../2-implementation-phases.md`.

---

## Phase map

```
Phase 1 ─────────────────────────────── DB Foundation
Phase 2 ─────────────────────────────── Redis PubSub
Phase 3 ── (needs 1 + 2) ────────────── jobqueue core (types, store, metrics)
Phase 4 ── (needs 3) ────────────────── Dispatcher + Scheduler + StallDetector + Manager
Phase 5 ── (needs 4) ────────────────── Admin API + WebSocket
Phase 6 ── (needs 3 only) ───────────── Worker handlers  [parallel with 4 + 5]
Phase 7 ── (needs 4 + 5 + 6) ────────── Wire into server
```

Phases 1 and 2 are fully independent of each other — start both simultaneously.
Phase 6 only needs Phase 3 — it can run in parallel with Phases 4 and 5.
Phase 7 is the only phase with no parallelism; it integrates everything.

---

## Phase 1 — DB Foundation

**Goal:** The four job queue tables exist in the database and the server still boots.

Apply `sql/schema/007_jobqueue.sql` and `sql/schema/008_jobqueue_functions.sql` with
goose. No Go code changes in this phase. The `request_executions` table is dropped
and `request_notifications` loses its delivery-retry columns.

**Done when:** All four tables exist, `request_executions` is gone, and the server
boots cleanly against the migrated database.

---

## Phase 2 — Redis PubSub

**Goal:** `RedisStore` in `internal/platform/kvstore/redis.go` gains `Publish` and
`Subscribe` methods and passes the `PubSub` contract test suite.

No job queue package changes yet — the `PubSub` interface does not exist until Phase
3. The `Publish`/`Subscribe` methods are added to `RedisStore` and tested in
isolation. The compile-time check `var _ jobqueue.PubSub = (*RedisStore)(nil)` is
added in Phase 3 after the interface is defined.

**Done when:** `TestRedisPubSub` passes all 5 contract cases.

---

## Phase 3 — Core Types, Store, Metrics

**Goal:** The `internal/platform/jobqueue` package compiles. `pgJobStore` works
against the real database. All contract tests are written and green.

This phase produces the data layer only — no Dispatcher, no scheduler, no API. It
is the foundation that all subsequent phases depend on.

**Done when:** `go build ./internal/platform/jobqueue/...` succeeds and
`TestPgJobStore`, `TestQueryMetricsRecorder`, and `TestNoopMetricsRecorder` all pass.

---

## Phase 4 — Dispatcher, Scheduler, StallDetector, Manager

**Goal:** Jobs can be submitted, claimed, executed, retried, and dead-lettered.
Manager lifecycle (Start/Shutdown) works. `AdminRouter()` is stubbed as an empty
router for now.

This is the execution engine. After this phase, a smoke test can verify that a
submitted job is claimed and executed within seconds.

**Done when:** Worker loop tests T-01 through T-16 and priority aging tests T-17
through T-19 pass. Manual smoke test succeeds.

---

## Phase 5 — Admin API + WebSocket

**Goal:** All 20 REST endpoints serve correct responses. The WebSocket stream
delivers real-time events. `GET /metrics` returns Prometheus text.

**Done when:** Admin API tests T-36 through T-40 and metrics tests T-47 through
T-49 pass. Manual smoke test confirms `wscat` receives `stats.tick` events.

---

## Phase 6 — Worker Handlers (parallel with Phases 4 + 5)

**Goal:** All five handler kinds exist, are tested, and implement the `Handler`
interface. The existing `PurgeWorker.Start()` goroutine is NOT removed yet — that
happens in Phase 7.

**Done when:** Handler tests T-41 through T-46 and T-WK-1 through T-WK-4 pass.

---

## Phase 7 — Wire into Server

**Goal:** The server starts with the job queue running. The `PurgeWorker` goroutine
is removed. All existing tests pass. End-to-end smoke test passes.

**Done when:** The server boots, `GET /admin/jobqueue/workers` returns the running
worker, `GET /admin/jobqueue/schedules` lists all three seeded schedules, and the
full test suite (`go test ./...`) is green.
