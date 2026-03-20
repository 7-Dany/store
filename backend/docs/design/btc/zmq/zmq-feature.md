# ZMQ Subscriber — Behavior & Edge Cases

> **What this file is:** Plain-language description of what the ZMQ subscriber
> does, every guarantee it makes and does not make, and every edge case it handles.
> Read this to understand the platform contract before touching any implementation.
>
> **Package:** `internal/platform/bitcoin/zmq/`
> **Rule:** no domain imports — pure platform.
> **Companion:** `zmq-technical.md` — constructor, event types, worker pool,
> panic recovery, endpoint validation, shutdown, test inventory.

---

## What this package does

Bitcoin Core exposes two ZMQ PUSH sockets that fire on network activity:

- `hashblock` — fires every time a new block is added to the chain. Delivers
  the 32-byte block hash and a sequence number.
- `hashtx` — fires every time a transaction enters the mempool. Delivers the
  32-byte txid and a sequence number.

The ZMQ subscriber connects to both sockets, decodes the raw bytes into typed
Go structs (`BlockEvent` and `TxEvent`), and fans them out to registered handler
callbacks. It is the single point of entry for all Bitcoin network events in the
application. Nothing above it knows about raw ZMQ bytes.

---

## What ZMQ guarantees and what it does not

ZMQ is a signal, not a source of truth. This is the most important thing to
understand about this package.

**What ZMQ guarantees within a single connection:** events arrive in order, each
event has a monotonically increasing sequence number, and delivery is reliable
while the socket is connected.

**What ZMQ does not guarantee:**
- It provides no persistence. A message dropped while the subscriber is
  disconnected is gone permanently — Bitcoin Core will not re-send it.
- It provides no replay. There is no way to ask "give me all events since
  sequence N."
- It does not guarantee delivery across a reconnect. Events emitted by Bitcoin
  Core between a disconnect and reconnect are silently lost.
- High-water mark (HWM) overflow silently drops messages when the internal
  buffer is full. HWM is set to 5000 on both publisher and subscriber sides;
  the effective queue depth is the minimum of the two.

This is why settlement correctness must never depend on ZMQ delivery. The
settlement engine uses RPC-based block scanning with a DB cursor as its
authoritative source. A ZMQ miss cannot cause a missed payment.

---

## Handler registration — the two-handler rule

Four handler types can be registered:

- `RegisterBlockHandler` — called on every new block.
- `RegisterDisplayTxHandler` — called on every mempool transaction, for the
  SSE display path.
- `RegisterSettlementTxHandler` — called on every mempool transaction, for
  the settlement path.
- `RegisterRecoveryHandler` — called after reconnect, before event delivery
  resumes.

SSE and settlement always register via separate methods. They are called in
separate fan-out loops and never share a goroutine, channel, or failure domain.
A slow or panicking settlement handler cannot affect SSE delivery, and vice
versa.

---

## How events are delivered to handlers

The subscriber uses a fixed worker pool, not goroutine-per-message. There are
20 block workers and 20 tx workers running at all times. Decoded events are
placed into buffered internal channels (`blockCh` and `txCh`, each buffered at
`DefaultSubscriberHWM × 2 = 10,000`). Workers read from these channels and
call `safeInvoke` for each registered handler.

This design has two important consequences:

**Backpressure:** if all 20 workers are occupied (every worker is inside a slow
handler), new events pile up in the 10,000-slot channel buffer. If the buffer
fills completely, the ZMQ read loop drops the incoming message and increments
`dropped_zmq_messages_total{reason="hwm"}`. This is a non-blocking drop — the
read loop never stalls.

**Ordering within a handler:** a single worker processes events one at a time,
sequentially. Events for different workers may interleave, but a single worker
never processes two events concurrently.

---

## Panic and timeout isolation

Every handler callback runs inside `safeInvoke`, which:

1. Creates a child context with a timeout of `BTC_HANDLER_TIMEOUT_MS`.
2. Launches a goroutine tracked by the subscriber's WaitGroup.
3. Wraps the goroutine body in `recover()`.

A panicking handler logs the panic at ERROR with a stack trace, increments
`bitcoin_handler_panics_total`, and returns. It does not crash the process,
does not stall other workers, and does not affect the WaitGroup drain.

A handler that does not panic but simply takes too long has its context cancelled
after `BTC_HANDLER_TIMEOUT_MS`. The goroutine is still tracked by the WaitGroup
and continues running until it respects the context cancellation. Handlers MUST
check `ctx.Done()`. A handler that ignores context cancellation will eventually
exhaust all workers.

The `recover()` call is inside the spawned goroutine, not in the calling frame.
A `recover()` in the calling frame cannot catch panics in a different goroutine.

---

## Reconnect behavior

The subscriber handles reconnects internally. `Run()` does not return on
transient errors — it reconnects with exponential backoff (1 second initial,
60 second ceiling). The application does not need to restart the subscriber
on a connection loss.

Before resuming event delivery after a reconnect, the subscriber fires
`RecoveryEvent`. This carries the reconnect timestamp and the last sequence
number that was seen before the disconnect. Domain handlers use this to trigger
any gap-filling logic (e.g. the settlement reconciliation loop).

The subscriber fires `RecoveryEvent` before delivering the first post-reconnect
event. This ordering guarantee means domain handlers will never see a block
event arrive before their recovery handler has had a chance to run.

---

## Sequence gap detection

Every `BlockEvent` and `TxEvent` carries a `Sequence` field. The subscriber
tracks the last seen sequence for each topic (`hashblock` and `hashtx`
independently). If the incoming sequence number is not exactly `last + 1`,
the subscriber logs a WARNING, increments
`dropped_zmq_messages_total{reason="sequence_gap"}`, and fires `RecoveryEvent`.

A sequence gap means messages were dropped at the ZMQ layer (HWM overflow,
network issue, or Bitcoin Core restart). It is not an error in the subscriber —
it is a signal that the domain layer needs to recover.

---

## Byte order — the most common source of bugs

ZMQ delivers block and transaction hashes in internal byte order (little-endian
display). Bitcoin RPC expects big-endian display, which is also what block
explorers show.

If you pass raw ZMQ bytes directly to any RPC method, you will get "Block not
found" or "No such transaction" every time, with no other error message. The
bytes are technically valid hex — they just represent the hash in the wrong order.

Both `BlockEvent.HashHex()` and `TxEvent.HashHex()` reverse the byte order
before encoding to hex. Every RPC caller must use these methods. Direct use of
`hex.EncodeToString(event.Hash[:])` is a bug and is forbidden in the bitcoin
package tree by CI lint rule.

---

## ZMQ security model

ZMQ has no authentication or encryption. The subscriber only accepts connections
from loopback TCP (`127.0.0.1` or `[::1]`) or Unix sockets. This is enforced by
`requireZMQEndpoint` at startup — any other endpoint format causes a panic before
the server starts. There is no runtime fallback.

This means the ZMQ port must never be exposed outside the machine running Bitcoin
Core. Firewall rules should block inbound connections to ZMQ ports from any
external address.

---

## Overflow behavior per path

When the subscriber's internal channels are full, messages are dropped. But the
two domain paths handle overflow differently:

**SSE path:** inside the display_tx handler, each connected SSE client has its
own unbuffered channel. Sending to a full client channel is a non-blocking drop.
The event is lost for that client. The client reconciles via REST polling on
reconnect.

**Settlement path:** inside the settlement_tx handler, overflow pushes the block
hash to `btc:settlement:overflow` in Redis (payload format `"0:{hashHex}"`). A
drain goroutine retries delivery up to 5 times before moving the hash to a
dead-letter list. Redis unavailability at push time is logged at ERROR with a
funds-risk label and increments `dropped_zmq_messages_total{reason="settlement_overflow_redis_fail"}`.

The asymmetry is intentional and deliberate: SSE events can be lost because the
display path is best-effort. Settlement hashes must be durably queued because
missing a settlement trigger delays payment processing.

---

## What happens on graceful shutdown

`Shutdown()` must be called after cancelling the `Run()` context. It waits up
to 30 seconds for all in-flight handler goroutines to finish (tracked by the
subscriber's WaitGroup). After 30 seconds it logs an error and proceeds.
Socket cleanup happens after the drain.

The application shutdown sequence is strictly ordered:
1. HTTP server shutdown (cancels SSE handler contexts).
2. `sub.Shutdown()` — drain ZMQ handler goroutines.
3. `svc.Shutdown()` — drain domain goroutines (TTL, reconciliation, overflow drain).
4. Close DB pool and Redis connections.

Calling `sub.Shutdown()` before the HTTP server shuts down means the SSE
handler's block-event goroutine may still be running while the subscriber tries
to drain — leading to a 30-second timeout. The ordering matters.

---

## What this package does NOT do

- It does not decode raw block or transaction data. It only handles `hashblock`
  and `hashtx` topics, which deliver 32-byte hashes. Full block/tx decoding
  is the responsibility of the RPC client.
- It does not know about Bitcoin addresses, invoices, users, or sessions.
- It does not persist anything. State only exists while the process is running.
- It does not implement the `rawblock` or `rawtx` ZMQ topics. Those would
  eliminate the need for RPC calls to decode transactions but would significantly
  increase memory and bandwidth requirements.
- It does not detect reorgs directly. Reorg detection is the responsibility of
  the settlement engine, which compares stored block hashes against the current
  chain via RPC.
