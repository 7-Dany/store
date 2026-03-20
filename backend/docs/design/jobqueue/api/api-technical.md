# Admin API Technical — Router, Middleware, Response Shapes, Tests

> **Read first:** `api-feature.md` — all endpoints, permissions, behavioral rules.

---

## Table of Contents

1. [AdminRouter factory](#1--adminrouter-factory)
2. [Permission middleware](#2--permission-middleware)
3. [Key response shapes](#3--key-response-shapes)
4. [Error handling](#4--error-handling)
5. [Test inventory](#5--test-inventory)

---

## §1 — AdminRouter factory

```go
// internal/platform/jobqueue/api.go

func (m *Manager) AdminRouter() chi.Router {
    r := chi.NewRouter()

    r.Use(requirePermission("job_queue:read")) // base gate for all routes

    // Workers
    r.Get("/workers",     m.handleListWorkers)
    r.Get("/workers/{id}", m.handleGetWorker)
    r.Delete("/workers/{id}", requireManage(m.handleDrainWorker))

    // Jobs
    r.Get("/jobs",                    m.handleListJobs)
    r.Get("/jobs/{id}",               m.handleGetJob)
    r.Delete("/jobs/{id}",            requireManage(m.handleCancelJob))
    r.Patch("/jobs/{id}/priority",    requireManage(m.handleUpdateJobPriority))
    r.Post("/jobs/{id}/retry",        requireManage(m.handleRetryJob))

    // Dead-letter
    r.Get("/dead",    m.handleListDeadJobs)
    r.Delete("/dead", requireManage(m.handlePurgeDeadJobs))

    // Queues
    r.Get("/queues",                    m.handleListQueues)
    r.Post("/queues/{kind}/pause",      requireManage(m.handlePauseKind))
    r.Post("/queues/{kind}/resume",     requireManage(m.handleResumeKind))

    // Schedules
    r.Get("/schedules",                   m.handleListSchedules)
    r.Post("/schedules",                  requireManage(m.handleCreateSchedule))
    r.Put("/schedules/{id}",              requireManage(m.handleUpdateSchedule))
    r.Delete("/schedules/{id}",           requireManage(m.handleDeleteSchedule))
    r.Post("/schedules/{id}/trigger",     requireManage(m.handleTriggerSchedule))

    // Stats and real-time stream
    r.Get("/stats", m.handleStats)
    r.Get("/ws",    m.handleWebSocket)

    // NOTE: No /metrics route here.
    // jobqueue_* metrics are served from the global GET /metrics endpoint
    // via registry.Handler() in routes.go. See monitoring-technical.md §17.

    return r
}
```

---

## §2 — Permission middleware

```go
// requirePermission gates all routes at the router level.
func requirePermission(perm string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            userID := token.UserIDFromContext(r.Context())
            if !rbac.UserHasPermission(r.Context(), userID, perm) {
                respond.Forbidden(w)
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}

// requireManage is applied per-route for write operations.
func requireManage(h http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        userID := token.UserIDFromContext(r.Context())
        if !rbac.UserHasPermission(r.Context(), userID, "job_queue:manage") {
            respond.Forbidden(w)
            return
        }
        h(w, r)
    }
}
```

---

## §3 — Key response shapes

**GET /jobs** response:
```json
{
  "jobs": [
    {
      "id": "...",
      "kind": "execute_request",
      "status": "pending",
      "priority": 0,
      "attempt": 0,
      "max_attempts": 5,
      "queue_name": "default",
      "run_after": "2024-01-01T00:00:00Z",
      "created_at": "2024-01-01T00:00:00Z",
      "last_error": null
    }
  ],
  "total": 142,
  "next_cursor": "..."
}
```

**GET /stats** response:
```json
{
  "pending": 14,
  "running": 3,
  "succeeded_last_hour": 847,
  "failed_last_hour": 2,
  "dead_total": 7,
  "workers_active": 2,
  "workers_idle": 1,
  "throughput_per_minute": 8.2
}
```

**GET /schedules** includes `last_schedule_error` (null on healthy schedules) to
surface cron parse failures or insertion errors without requiring log access.

---

## §4 — Error handling

All handlers use the standard `respond` package:
- `respond.NotFound(w)` — 404 when a job/worker/schedule ID does not exist.
- `respond.BadRequest(w, err)` — 400 for invalid filter parameters, malformed
  UUIDs, or priority values outside -100..100.
- `respond.Forbidden(w)` — 403 for missing permissions.
- `respond.InternalError(w)` — 500 for unexpected store errors.

`DELETE /dead` without `older_than` returns 400 (explicit constraint to prevent
accidental full purge).

---

## §5 — Test inventory

| # | Case | Layer |
|---|------|-------|
| T-API-1 | GET /stats returns correct pending/running/dead counts | I |
| T-API-2 | GET /jobs returns paginated results with filter | I |
| T-API-3 | PATCH /jobs/:id/priority updates claim order | I |
| T-API-4 | POST /jobs/:id/retry re-queues dead job | I |
| T-API-5 | DELETE /dead without older_than returns 400 | U |
| T-API-6 | POST /queues/:kind/pause prevents new claims | I |
| T-API-7 | POST /schedules creates and evaluates on next watcher tick | I |
| T-API-8 | job_queue:read user cannot access manage endpoints — 403 | I |
| T-API-9 | Unauthenticated request returns 403 | I |
| T-API-10 | AdminRouter has no /metrics route | U |
