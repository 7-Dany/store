# Scheduler Feature — Behavior & Design Rationale

> **What this file is:** A plain-language description of how scheduled jobs fire,
> the difference between cron and interval schedules, multi-instance safety, and
> what `skip_if_running` prevents. Read this before the technical file.
>
> **Companion:** `scheduler-technical.md` — poll loop, due-schedule SQL, cron
> parsing, next_run_at computation, test inventory.
> **Schema reference:** `sql/schema/007_jobqueue.sql` (`job_schedules` table).

---

## What the ScheduleWatcher is

The ScheduleWatcher is a single goroutine that fires scheduled jobs. It polls the
`job_schedules` table every 10 seconds, finds rows whose `next_run_at` is in the
past, inserts a job row for each, and updates `next_run_at` to the next fire time.

There is **one goroutine for all schedules**, not one per schedule. Fifty schedules
cost no more goroutines than one. This replaces the V1 design where each
`ScheduleEntry` had its own goroutine, which was wasteful and made multi-instance
deployments unsafe.

---

## Two schedule types

**Interval** — a fixed number of seconds between fires. An interval of 3600 means
the job fires every hour, measured from the last enqueue time. Simple and predictable.

**Cron** — a standard 5-field cron expression (e.g. `"0 * * * *"` for every hour at
minute 0). Parsed using `robfig/cron`. Useful when jobs need to fire at specific
wall-clock times (e.g. midnight UTC) rather than a fixed duration after the last run.

Both types go through the same poll loop. The only difference is how `next_run_at`
is computed after each enqueue.

---

## Multi-instance safety

Multiple application instances each run their own ScheduleWatcher. Without
coordination, they would both detect the same due schedule and both insert a job,
resulting in duplicate execution.

Safety is handled at the database level: the due-schedule query uses
`FOR UPDATE SKIP LOCKED` on the `job_schedules` rows. The first instance to acquire
the lock enqueues the job and updates `next_run_at`. The second instance, arriving
a few milliseconds later, either finds the row already locked (skips it) or finds
that `next_run_at` is now in the future (not due). Either way, only one job is
enqueued per schedule per fire time.

---

## skip_if_running

When `skip_if_running = true`, the ScheduleWatcher checks whether a job of this kind
is already `pending` or `running` before inserting a new one. If one exists, the
enqueue is skipped and `next_run_at` is still advanced.

This prevents job pile-up for slow-running handlers. Without it, an hourly cleanup
job that takes 90 minutes to complete would accumulate a backlog of pending instances
that would each run in sequence, potentially holding the queue for hours.

---

## What happens when a schedule fires but Redis is down

The ScheduleWatcher inserts the job row into Postgres first. Then it publishes to
Redis as a best-effort wake signal. If the publish fails (Redis is down), the
failure is logged at WARN and the method returns. The job row is already safely in
Postgres. Workers will claim it on their next 10-second ticker poll. No job is lost.

---

## Startup schedule seeding

Built-in schedules (like `purge_accounts_hourly` and `purge_completed_jobs_daily`)
are created via `Manager.EnsureSchedule()`, called during `server.New`. This is
idempotent — it uses `ON CONFLICT DO NOTHING` on the schedule `name` unique
constraint. Running `EnsureSchedule` on an already-existing schedule is safe and
produces no change.

---

## Schedule deactivation

Setting `is_active = false` on a schedule (via `PUT /admin/jobqueue/schedules/:id`)
causes the ScheduleWatcher to skip it. The schedule row is preserved for history and
can be reactivated without re-creation. This is different from deleting a schedule,
which removes it permanently.
