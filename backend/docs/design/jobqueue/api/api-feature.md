# Admin API Feature — Endpoints & Permissions

> **What this file is:** A plain-language description of all admin API endpoints,
> what each one does, which permission it requires, and the behavioral rules around
> filters, pagination, and side effects.
>
> **Companion:** `api-technical.md` — router structure, permission middleware,
> request/response shapes, test inventory.
> **Mounted at:** `/admin/jobqueue` by `server.go`.
> **Metrics:** Job queue metrics are part of the global `GET /metrics` endpoint
> served by `registry.Handler()`. There is no separate jobqueue metrics endpoint.

---

## Permission model

Two permissions gate the entire admin API:

- `job_queue:read` — required for all read operations (GET endpoints, WebSocket).
- `job_queue:manage` — required for all write and mutation operations (POST, PUT,
  PATCH, DELETE).

Both permissions are seeded by the SQL migration into the `permissions` table. An
admin without `job_queue:manage` can observe the system but cannot change it.

---

## Endpoints

### Workers

| Method | Path | Description | Permission |
|--------|------|-------------|-----------|
| GET | /workers | List all workers with status, heartbeat, runtime counters | job_queue:read |
| GET | /workers/:id | Single worker detail + currently running jobs | job_queue:read |
| DELETE | /workers/:id | Force-drain a specific worker (graceful) | job_queue:manage |

**DELETE /workers/:id** signals the target worker to stop accepting new jobs and drain
its current work. It does not kill the process. The worker transitions to `draining`
status in the `workers` table.

### Jobs

| Method | Path | Description | Permission |
|--------|------|-------------|-----------|
| GET | /jobs | List jobs with filters | job_queue:read |
| GET | /jobs/:id | Single job detail | job_queue:read |
| DELETE | /jobs/:id | Cancel a pending job | job_queue:manage |
| PATCH | /jobs/:id/priority | Update base priority `{priority: int}` | job_queue:manage |
| POST | /jobs/:id/retry | Re-queue a dead or failed job immediately | job_queue:manage |

**GET /jobs** accepts query parameters: `kind`, `status`, `queue_name`, `from`, `to`,
`page`, `page_size`. Pagination is cursor-based (keyed on `created_at DESC, id`).

**DELETE /jobs/:id** only cancels jobs in `pending` status. A `running` job cannot
be cancelled — the handler is already executing and must complete or time out.

**POST /jobs/:id/retry** works on both `dead` and `failed` jobs. It resets `attempt`
to 0 and sets `run_after = NOW()`.

### Dead-letter queue

| Method | Path | Description | Permission |
|--------|------|-------------|-----------|
| GET | /dead | List dead-lettered jobs (paginated) | job_queue:read |
| DELETE | /dead | Purge dead jobs `?older_than=7d` | job_queue:manage |

**DELETE /dead** requires the `older_than` query parameter. The API will not purge
all dead jobs without a time constraint — this prevents accidental deletion of
recently dead jobs that an operator may still be investigating.

### Queues (pause / resume)

| Method | Path | Description | Permission |
|--------|------|-------------|-----------|
| GET | /queues | List queues with depth and paused state | job_queue:read |
| POST | /queues/:kind/pause | Pause a kind `{reason: string}` | job_queue:manage |
| POST | /queues/:kind/resume | Resume a paused kind | job_queue:manage |

Pausing a kind is durable across restarts (stored in `job_paused_kinds`). Workers on
all instances skip paused kinds immediately on the next claim cycle.

### Schedules

| Method | Path | Description | Permission |
|--------|------|-------------|-----------|
| GET | /schedules | List all schedules | job_queue:read |
| POST | /schedules | Create a schedule | job_queue:manage |
| PUT | /schedules/:id | Update a schedule | job_queue:manage |
| DELETE | /schedules/:id | Delete a schedule | job_queue:manage |
| POST | /schedules/:id/trigger | Manually trigger a schedule now | job_queue:manage |

**POST /schedules/:id/trigger** inserts a job immediately regardless of `next_run_at`.
It does not advance `next_run_at` — the schedule continues on its normal cadence.

### Stats and real-time stream

| Method | Path | Description | Permission |
|--------|------|-------------|-----------|
| GET | /stats | Aggregate stats: queue depth, throughput, error rate | job_queue:read |
| GET | /ws | WebSocket upgrade — real-time event stream | job_queue:read |

**GET /stats** returns current JSON stats sourced directly from Postgres. This is
the admin UI data feed — not a Prometheus endpoint. It answers "what is the queue
doing right now?" without needing Grafana.

**GET /ws** streams real-time job and worker events as they happen. See
`ws-feature.md` for the full event protocol.

---

## Where Prometheus metrics live

Job queue metrics (`jobqueue_*`) are registered on `*telemetry.Registry` and
served from the global `GET /metrics` endpoint alongside all other application
metrics. Prometheus scrapes one endpoint and gets everything — HTTP, errors,
auth, infrastructure, job queue, and bitcoin.

There is no `GET /admin/jobqueue/metrics` endpoint. Separating it would mean
Prometheus needs two scrape targets for one application, and jobqueue metrics
would be isolated from the recording rules and alert groups that reference other
metric families (for example, a bitcoin job dead-letter alert correlates
`jobqueue_jobs_dead_total` with `bitcoin_balance_drift_satoshis` — impossible
if they live on different endpoints).
