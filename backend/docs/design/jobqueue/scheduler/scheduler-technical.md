# Scheduler Technical — Poll Loop, SQL, Cron Parsing, Tests

> **What this file is:** Implementation details for the ScheduleWatcher: the poll
> loop, due-schedule SQL, cron and interval next_run_at computation, and test
> inventory.
>
> **Read first:** `scheduler-feature.md` — behavioral rules and design rationale.
> **Schema reference:** `sql/schema/007_jobqueue.sql` (`job_schedules`).

---

## Table of Contents

1. [ScheduleWatcher loop](#1--schedulewatcher-loop)
2. [Due-schedule SQL](#2--due-schedule-sql)
3. [next_run_at computation](#3--next_run_at-computation)
4. [skip_if_running check](#4--skip_if_running-check)
5. [last_schedule_error column](#5--last_schedule_error-column)
6. [Test inventory](#6--test-inventory)

---

## §1 — ScheduleWatcher loop

```go
// scheduler.go — single goroutine for all schedules.

func (sw *ScheduleWatcher) Run(ctx context.Context) {
    ticker := time.NewTicker(sw.cfg.ScheduleCheck) // default 10s
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            sw.tick(ctx)
        }
    }
}

func (sw *ScheduleWatcher) tick(ctx context.Context) {
    schedules, err := sw.store.ListDueSchedules(ctx, time.Now())
    if err != nil {
        sw.logger.Error("list due schedules failed", "err", err)
        return
    }
    for _, s := range schedules {
        sw.fire(ctx, s)
    }
}

func (sw *ScheduleWatcher) fire(ctx context.Context, s Schedule) {
    if s.SkipIfRunning {
        if sw.store.HasActiveJobOfKind(ctx, s.Kind) {
            sw.advanceNextRun(ctx, s) // still advance so next window is not missed
            return
        }
    }

    job, err := sw.store.InsertJob(ctx, scheduleToSubmitRequest(s))
    if err != nil {
        sw.store.SetScheduleError(ctx, s.ID, err.Error())
        sw.logger.Error("schedule fire: insert job failed", "schedule", s.Name, "err", err)
        return
    }

    sw.metrics.OnScheduleFired(s.ID, s.Kind)
    sw.hub.Broadcast(WSEvent{Event: "schedule.fired",
        Data: scheduleFiredPayload(s, job.ID)})

    // Publish is best-effort. Job row is in Postgres; workers will poll on failure.
    if err := sw.pubsub.Publish(ctx, sw.cfg.NotifyChannel, ""); err != nil {
        sw.logger.Warn("schedule fire: notify publish failed, workers will poll",
            "schedule", s.Name, "err", err)
    }

    sw.advanceNextRun(ctx, s)
}
```

---

## §2 — Due-schedule SQL

```sql
-- ListDueSchedules — called every ScheduleCheck interval.
-- FOR UPDATE SKIP LOCKED ensures multi-instance safety.
SELECT *
FROM   job_schedules
WHERE  is_active = TRUE
  AND  next_run_at <= $1    -- $1 = NOW() at tick start
ORDER  BY next_run_at ASC
FOR UPDATE SKIP LOCKED;
```

The `idx_job_schedules_due` partial index (`WHERE is_active = TRUE`) makes this scan
cheap even with hundreds of schedules. Only active, due rows are considered.

---

## §3 — next_run_at computation

```go
func nextRunAt(s Schedule, now time.Time) (time.Time, error) {
    if s.IntervalSeconds != nil {
        return now.Add(time.Duration(*s.IntervalSeconds) * time.Second), nil
    }
    // cron_expression — use robfig/cron parser.
    parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
    schedule, err := parser.Parse(s.CronExpression)
    if err != nil {
        return time.Time{}, fmt.Errorf("parse cron %q: %w", s.CronExpression, err)
    }
    return schedule.Next(now), nil
}
```

Parse errors are written to `job_schedules.last_schedule_error` and logged. The
schedule row is not deleted — an operator can correct the cron expression and the
watcher will resume on the next poll cycle.

---

## §4 — skip_if_running check

```sql
-- HasActiveJobOfKind — called before inserting when skip_if_running = true.
SELECT EXISTS (
    SELECT 1 FROM jobs
    WHERE  kind   = $1
      AND  status IN ('pending', 'running')
    LIMIT 1
);
```

If this returns `true`, the ScheduleWatcher skips insertion but still calls
`UpdateScheduleNextRun` so the schedule window advances correctly.

---

## §5 — last_schedule_error column

`job_schedules.last_schedule_error` (added in `007_jobqueue.sql`) stores the last
error encountered during schedule processing. This column is not in the original
design doc — it was added during schema authoring for operational visibility.

It is set on:
- Cron expression parse failure
- InsertJob failure during fire

It is cleared (set to NULL) on the next successful enqueue. The management API
exposes it in `GET /admin/jobqueue/schedules`.

---

## §6 — Test inventory

| # | Case | Layer |
|---|------|-------|
| T-20 | Due interval schedule fires and inserts job | I |
| T-21 | Not-yet-due schedule is skipped | I |
| T-22 | `skip_if_running` prevents duplicate job insertion | I |
| T-23 | Two watchers (multi-instance) do not double-insert | I |
| T-24 | next_run_at updated correctly after firing | I |
| T-25 | PUBLISH failure during Redis downtime does not prevent job insertion | I+R |
