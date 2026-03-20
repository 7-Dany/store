# WebSocket Feature — Real-Time Event Stream

> **What this file is:** A plain-language description of what the WebSocket stream
> is, all event types, client commands, backpressure behavior, and reconnect rules.
> Read this before the technical file.
>
> **Companion:** `ws-technical.md` — WSHub internals, buffered channel design,
> writePump/readPump, test inventory.
> **Endpoint:** `GET /admin/jobqueue/ws`

---

## What the WebSocket stream is

The WebSocket stream at `/admin/jobqueue/ws` is a real-time event feed for admin
dashboards. Every significant state change in the job queue — job created, job
succeeded, worker came online, schedule fired — is broadcast as a typed JSON event
within milliseconds of occurring.

The stream is display-only. It provides visibility, not guarantees. A client that
disconnects misses events that fired during the disconnect — there is no replay buffer
and no event history. Clients that need a consistent view of the current state should
use the REST API (`GET /jobs`, `GET /stats`).

---

## Server → Client events

**Job lifecycle:**

| Event | Fires when |
|-------|-----------|
| `job.created` | A job is submitted via `Manager.Submit` |
| `job.claimed` | A worker goroutine claims a job |
| `job.succeeded` | Handler completes without error |
| `job.failed` | Handler returns an error (may retry) |
| `job.dead` | Job exhausts max_attempts |
| `job.cancelled` | Job is cancelled via the admin API |

**Worker lifecycle:**

| Event | Fires when |
|-------|-----------|
| `worker.online` | Dispatcher starts and upserts its row |
| `worker.idle` | Worker goroutine finishes a job, no new job claimed |
| `worker.busy` | Worker goroutine claims a job |
| `worker.offline` | Dispatcher shuts down or StallDetector marks offline |

**Queue management:**

| Event | Fires when |
|-------|-----------|
| `queue.paused` | `POST /queues/:kind/pause` succeeds |
| `queue.resumed` | `POST /queues/:kind/resume` succeeds |

**Scheduler:**

| Event | Fires when |
|-------|-----------|
| `schedule.fired` | ScheduleWatcher enqueues a job for a schedule |

**Periodic stats:**

| Event | Fires every |
|-------|------------|
| `stats.tick` | 5 seconds — sourced from Postgres, unaffected by Redis downtime |

---

## Client → Server commands

```jsonc
// Filter events to specific kinds or queues (sent after connect)
{ "cmd": "subscribe", "filter": { "kinds": ["execute_request"], "queues": ["default"] } }

// Remove filter — receive all events
{ "cmd": "unsubscribe" }

// Keepalive check — server replies with { "event": "pong" }
{ "cmd": "ping" }
```

Filters are advisory — they reduce traffic on high-volume deployments but do not
guarantee message ordering or delivery.

---

## Backpressure — what happens to slow clients

Each client connection has a 64-event buffered send channel. The hub goroutine that
broadcasts events never blocks on a slow client. Instead:

- If the client's channel is not full: the event is placed in the buffer. The client's
  `writePump` goroutine drains the buffer and writes to the WebSocket connection at
  the client's speed.
- If the client's channel is full: the client is disconnected. It can reconnect and
  re-subscribe immediately. A healthy client running a dashboard should never fill a
  64-event buffer.

This ensures one slow or unresponsive client can never delay event delivery to other
clients.

---

## Reconnect behavior

On reconnect the client receives a fresh connection — no catch-up of events missed
during the disconnect. The client should:

1. Re-send its `subscribe` command with the desired filter.
2. Poll `GET /stats` or `GET /jobs` via REST to reconcile current state.

The `stats.tick` event fires every 5 seconds, so a reconnected client gets a current
snapshot within 5 seconds even without explicit polling.
