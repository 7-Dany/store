# WebSocket Technical — WSHub, Buffered Channels, Payload Shapes, Tests

> **What this file is:** Implementation details for the WSHub: buffered per-client
> channels, the hub goroutine, writePump and readPump goroutines, event payload
> shapes, and test inventory.
>
> **Read first:** `ws-feature.md` — event types, client commands, backpressure.

---

## Table of Contents

1. [WSHub structure](#1--wshub-structure)
2. [Hub goroutine](#2--hub-goroutine)
3. [Per-client goroutines](#3--per-client-goroutines)
4. [Event payload shapes](#4--event-payload-shapes)
5. [PubSub contract tests](#5--pubsub-contract-tests)
6. [Test inventory](#6--test-inventory)

---

## §1 — WSHub structure

```go
// ws.go

const clientSendBuf = 64 // events buffered per client before disconnect

type WSHub struct {
    clients    map[*wsClient]struct{}
    broadcast  chan []byte
    register   chan *wsClient
    unregister chan *wsClient
    mu         sync.RWMutex // protects clients map for EmitToUser filter
}

type wsClient struct {
    conn   *websocket.Conn
    send   chan []byte   // buffered — hub never blocks writing here
    hub    *WSHub
    filter WSFilter      // optional kind/queue filter from subscribe command
}

type WSFilter struct {
    Kinds  []string
    Queues []string
}
```

---

## §2 — Hub goroutine

The hub goroutine is the sole owner of the `clients` map. It never performs network
I/O — only channel operations. This means it cannot be blocked by a slow client.

```go
func (h *WSHub) Run() {
    for {
        select {
        case c := <-h.register:
            h.clients[c] = struct{}{}

        case c := <-h.unregister:
            if _, ok := h.clients[c]; ok {
                delete(h.clients, c)
                close(c.send) // signals writePump to exit
            }

        case msg := <-h.broadcast:
            for c := range h.clients {
                select {
                case c.send <- msg: // non-blocking write to buffered channel
                default:
                    // Buffer full → client too slow → disconnect
                    close(c.send)
                    delete(h.clients, c)
                }
            }
        }
    }
}
```

`Broadcast(msg []byte)` sends to `h.broadcast`. The hub goroutine distributes to all
client channels non-blockingly. Callers (Dispatcher, ScheduleWatcher) never block on
WebSocket delivery.

---

## §3 — Per-client goroutines

Each connected client has two goroutines: a `readPump` (handles client commands and
connection lifecycle) and a `writePump` (handles outbound network I/O).

```go
// writePump — slow network I/O lives here, not in the hub.
func (c *wsClient) writePump() {
    defer func() {
        c.hub.unregister <- c
        c.conn.Close()
    }()
    for msg := range c.send { // exits when channel is closed by hub
        c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
        if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
            return
        }
    }
}

// readPump — handles ping commands and connection close.
func (c *wsClient) readPump() {
    defer func() { c.hub.unregister <- c }()
    for {
        _, msg, err := c.conn.ReadMessage()
        if err != nil {
            return // connection closed
        }
        var cmd struct{ Cmd string `json:"cmd"` }
        if err := json.Unmarshal(msg, &cmd); err != nil {
            continue
        }
        switch cmd.Cmd {
        case "subscribe":
            var sub struct {
                Cmd    string   `json:"cmd"`
                Filter WSFilter `json:"filter"`
            }
            _ = json.Unmarshal(msg, &sub)
            c.filter = sub.Filter
        case "unsubscribe":
            c.filter = WSFilter{}
        case "ping":
            c.send <- []byte(`{"event":"pong"}`)
        }
    }
}
```

---

## §4 — Event payload shapes

```jsonc
// Job lifecycle
{ "event": "job.created",   "data": { "id": "...", "kind": "send_notification", "priority": 0 } }
{ "event": "job.claimed",   "data": { "id": "...", "worker_id": "...", "attempt": 1 } }
{ "event": "job.succeeded", "data": { "id": "...", "kind": "...", "duration_ms": 142 } }
{ "event": "job.failed",    "data": { "id": "...", "attempt": 2, "retry_at": "...", "error": "..." } }
{ "event": "job.dead",      "data": { "id": "...", "attempts": 5, "error": "..." } }
{ "event": "job.cancelled", "data": { "id": "..." } }

// Worker lifecycle
{ "event": "worker.online",  "data": { "id": "...", "host": "...", "concurrency": 4 } }
{ "event": "worker.idle",    "data": { "id": "..." } }
{ "event": "worker.busy",    "data": { "id": "...", "active_jobs": 3 } }
{ "event": "worker.offline", "data": { "id": "...", "reason": "graceful_shutdown" } }

// Queue management
{ "event": "queue.paused",   "data": { "kind": "send_notification", "by": "...", "reason": "..." } }
{ "event": "queue.resumed",  "data": { "kind": "send_notification" } }

// Scheduler
{ "event": "schedule.fired", "data": { "schedule_id": "...", "job_id": "...", "kind": "..." } }

// Periodic stats tick — every 5s, from Postgres, unaffected by Redis downtime
{ "event": "stats.tick", "data": { "pending": 14, "running": 3, "dead": 0, "tps": 8.2 } }

// Pong
{ "event": "pong" }
```

---

## §5 — PubSub contract tests

The `PubSub` interface (used for Redis SUBSCRIBE/PUBLISH) has its own contract test
suite parallel to the `JobStore` contract tests.

```go
// pubsub_contract_test.go
func RunPubSubContractTests(t *testing.T, newPubSub func(t *testing.T) PubSub) {
    t.Run("Publish → Subscribe receives message within 100ms", ...)
    t.Run("Multiple subscribers all receive the same publish (fan-out)", ...)
    t.Run("Publish returns error when broker is unreachable", ...)
    t.Run("Subscribe channel is closed when ctx is cancelled", ...)
    t.Run("Publish succeeds after broker recovers from downtime", ...)
}

func TestRedisPubSub(t *testing.T) {
    RunPubSubContractTests(t, func(t *testing.T) PubSub {
        return newTestRedisStore(t) // *kvstore.RedisStore satisfies PubSub
    })
}
```

| # | Case | Layer |
|---|------|-------|
| T-C1 | Publish → Subscribe receives message within 100ms | I+R |
| T-C2 | Multiple subscribers all receive the same publish (fan-out) | I+R |
| T-C3 | Publish returns non-nil error when broker is unreachable | I+R |
| T-C4 | Subscribe channel is closed when ctx is cancelled | I+R |
| T-C5 | Publish succeeds after broker recovers from downtime | I+R |

---

## §6 — Test inventory

| # | Case | Layer |
|---|------|-------|
| T-33 | Slow client buffer fills → client disconnected, others unaffected | U |
| T-34 | WS client receives job.succeeded event | I |
| T-35 | WS client reconnects and re-subscribes after disconnect | I |
| T-WS-1 | stats.tick fires every 5s | I |
| T-WS-2 | subscribe filter limits events received by client | I |
| T-WS-3 | ping command receives pong response | U |
