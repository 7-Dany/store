# ZMQ Subscriber — Technical Implementation

> **What this file is:** Implementation details for `internal/platform/bitcoin/zmq/`.
> Covers the full constructor contract, event type definitions, worker pool
> architecture, panic recovery, endpoint validation, shutdown sequence, and the
> complete test inventory for this package.
>
> **Read first:** `zmq-feature.md` — behavioral contract and edge cases.

---

## Table of Contents

1. [Package Location & Rules](#1--package-location--rules)
2. [Constructor & Public Interface](#2--constructor--public-interface)
3. [Event Types](#3--event-types)
4. [Subscriber Internal State](#4--subscriber-internal-state)
5. [Worker Pool — Run() Architecture](#5--worker-pool--run-architecture)
6. [safeInvoke & safeInvokeBlock](#6--safeinvoke--safeinvokeblock)
7. [Shutdown](#7--shutdown)
8. [ZMQ Endpoint Validation (server.go)](#8--zmq-endpoint-validation-servergo)
9. [app.Deps Wiring & Shutdown Sequence](#9--appdeps-wiring--shutdown-sequence)
10. [Test Inventory](#10--test-inventory)

---

## §1 — Package Location & Rules

```
internal/platform/bitcoin/zmq/
├── subscriber.go      # Subscriber struct, New(), Run(), Shutdown(), IsConnected()
└── event.go           # BlockEvent, TxEvent, RecoveryEvent, HashHex()
```

**Hard rule:** zero domain imports. This package must compile without any
import from `internal/domain/`. Any domain concept (userID, address, invoice)
that needs to attach to an event belongs in a domain handler, not here.

**Go minimum version:** 1.21 — required for `context.WithoutCancel`.

**Library:** `github.com/go-zeromq/zmq4` — pure Go, no CGo, cross-platform (D-02).

---

## §2 — Constructor & Public Interface

```go
// New constructs a Subscriber managing two ZMQ sockets.
// Returns error if either endpoint fails requireZMQEndpoint validation.
// idleTimeout must be in the range 30s–3600s.
//
// H-03 fix: the caller (server.go) translates BTC_ZMQ_IDLE_TIMEOUT==0 to
// the network-appropriate default BEFORE calling New(). Passing 0 to New()
// is a programming error and returns an error.
//
// Server.go wiring pattern:
//   idleTimeout := time.Duration(cfg.BitcoinZMQIdleTimeout) * time.Second
//   if idleTimeout == 0 {
//       if cfg.BitcoinNetwork == "mainnet" {
//           idleTimeout = 600 * time.Second
//       } else {
//           idleTimeout = 120 * time.Second
//       }
//   }
//   sub, err := zmq.New(cfg.BitcoinZMQBlock, cfg.BitcoinZMQTx, idleTimeout)
func New(blockEndpoint, txEndpoint string, idleTimeout time.Duration) (*Subscriber, error)

// RegisterBlockHandler registers a callback for BlockEvent.
// Multiple handlers may be registered; all are called in sequence by each worker.
func (s *Subscriber) RegisterBlockHandler(h func(ctx context.Context, e BlockEvent))

// RegisterDisplayTxHandler registers a callback for TxEvent on the SSE path.
// Must be called via this method, never via RegisterSettlementTxHandler.
// Enforces ADR-BTC-01: SSE and settlement handlers are always separate.
func (s *Subscriber) RegisterDisplayTxHandler(h func(ctx context.Context, e TxEvent))

// RegisterSettlementTxHandler registers a callback for TxEvent on the settlement path.
func (s *Subscriber) RegisterSettlementTxHandler(h func(ctx context.Context, e TxEvent))

// RegisterRecoveryHandler registers a callback fired on reconnect before
// event delivery resumes. Settlement engine uses this to trigger reconciliation.
func (s *Subscriber) RegisterRecoveryHandler(h func(ctx context.Context, e RecoveryEvent))

// Run blocks until ctx is cancelled. Returns ctx.Err() on normal shutdown.
// Does NOT return on transient ZMQ errors — reconnects internally.
// Reconnect backoff: 1s initial, 60s ceiling.
// RecoveryHandler fires before first post-reconnect event delivery.
//
// Abnormal exit pattern (do NOT use os.Exit or log.Fatal):
//   go func() {
//       if err := sub.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
//           log.Error().Err(err).Msg("ZMQ subscriber abnormal exit")
//           appCancelFn()  // triggers graceful shutdown
//       }
//   }()
func (s *Subscriber) Run(ctx context.Context) error

// Shutdown drains all in-flight handler goroutines then closes ZMQ sockets.
// Blocks until all goroutines call wg.Done() or the 30-second ceiling is reached.
// MUST be called after cancelling the Run() context.
func (s *Subscriber) Shutdown()

// IsConnected returns true if a ZMQ message was received within idleTimeout
// AND the last dial succeeded.
func (s *Subscriber) IsConnected() bool

// lastSeenHash returns the hex hash of the most recently received block message.
// Updated atomically on every BlockEvent received inside Run().
// H-04 fix: must be defined explicitly to prevent the liveness goroutine
// comparison (info.BestBlockHash != s.lastSeenHash()) from always returning
// true on fresh startup, which would flip bitcoin_zmq_connected to 0 and
// trigger the BitcoinZMQDisconnected critical alert spuriously.
func (s *Subscriber) lastSeenHash() string

const DefaultSubscriberHWM = 5000
```

---

## §3 — Event Types

```go
// BlockEvent is what ZMQ delivers for hashblock.
// Height is NOT included — ZMQ hashblock does not send it (D-34).
// The domain layer derives height via GetBlockHeader RPC.
//
// BYTE ORDER: ZMQ delivers hashes in internal byte order (little-endian).
// Bitcoin RPC expects reversed byte order (big-endian / explorer display).
// All callers MUST use BlockEvent.HashHex(), never hex.EncodeToString(e.Hash[:]).
// Using raw bytes causes RPC to return "Block not found" with no other indication.
type BlockEvent struct {
    Hash     [32]byte
    Sequence uint32
}

// HashHex returns the block hash in RPC-compatible byte order.
func (e BlockEvent) HashHex() string {
    var rev [32]byte
    for i, b := range e.Hash { rev[31-i] = b }
    return hex.EncodeToString(rev[:])
}

// TxEvent is what ZMQ delivers for hashtx.
// Same byte-order caveat as BlockEvent — use TxEvent.HashHex() for all RPC calls.
type TxEvent struct {
    Hash     [32]byte
    Sequence uint32
}

func (e TxEvent) HashHex() string {
    var rev [32]byte
    for i, b := range e.Hash { rev[31-i] = b }
    return hex.EncodeToString(rev[:])
}

// RecoveryEvent is fired after a reconnect, before event delivery resumes.
// Does not include block height — settlement engine maintains its own
// last_processed_block_height DB cursor (Stage 2).
type RecoveryEvent struct {
    ReconnectedAt    time.Time
    LastSeenSequence uint32
}
```

---

## §4 — Subscriber Internal State

```go
type Subscriber struct {
    // Handler registrations (set once before Run(), read-only after)
    blockHandlers      []func(ctx context.Context, e BlockEvent)
    displayTxHandlers  []func(ctx context.Context, e TxEvent)
    settleTxHandlers   []func(ctx context.Context, e TxEvent)
    recoveryHandlers   []func(ctx context.Context, e RecoveryEvent)

    // Worker channels — both buffered at DefaultSubscriberHWM×2 = 10,000.
    // blockCh: mainnet sees ~1 block/10min; buffer will never fill in practice.
    // txCh:    mainnet ~7 tx/s average, burst ~100/s.
    //          10,000 entries = ~100s headroom against 30s handler timeout.
    blockCh chan BlockEvent
    txCh    chan TxEvent

    // Liveness tracking — updated atomically on every received BlockEvent.
    lastHash     atomic.Pointer[string]
    lastHashTime atomic.Int64  // Unix nanoseconds

    // Drain tracking for Shutdown().
    wg sync.WaitGroup

    // Configuration
    timeoutMs    int
    idleTimeout  time.Duration
}

// lastSeenHash() implementation:
func (s *Subscriber) lastSeenHash() string {
    p := s.lastHash.Load()
    if p == nil { return "" }
    return *p
}

// Inside Run(), after a BlockEvent is decoded:
//   h := e.HashHex()
//   s.lastHash.Store(&h)
//   s.lastHashTime.Store(time.Now().UnixNano())
```

---

## §5 — Worker Pool — Run() Architecture

`Run()` starts 20 block workers and 20 tx workers, then enters the ZMQ read loop.

```go
func (s *Subscriber) Run(ctx context.Context) error {
    for i := 0; i < 20; i++ {
        // Block worker
        s.wg.Add(1)
        go func() {
            defer s.wg.Done()
            for {
                select {
                case e, ok := <-s.blockCh:
                    if !ok { return }
                    // Calls all registered block handlers sequentially.
                    // Each call goes through safeInvokeBlock.
                    for _, h := range s.blockHandlers {
                        safeInvokeBlock(s, h, ctx, e, "block", s.timeoutMs)
                    }
                case <-ctx.Done():
                    return
                }
            }
        }()

        // Tx worker
        s.wg.Add(1)
        go func() {
            defer s.wg.Done()
            for {
                select {
                case e, ok := <-s.txCh:
                    if !ok { return }
                    // Display and settlement handlers called in separate loops —
                    // they never share a goroutine (ADR-BTC-01).
                    for _, h := range s.displayTxHandlers {
                        safeInvoke(s, h, ctx, e, "display_tx", s.timeoutMs)
                    }
                    for _, h := range s.settleTxHandlers {
                        safeInvoke(s, h, ctx, e, "settlement_tx", s.timeoutMs)
                    }
                case <-ctx.Done():
                    return
                }
            }
        }()
    }

    // ZMQ read loop — decodes raw bytes, writes to channels.
    // Channel sends are non-blocking: if the channel is full, the message
    // is dropped and the overflow metric is incremented.
    for {
        // ... receive from ZMQ sockets, decode topic + hash + sequence ...
        // ... detect sequence gaps, fire RecoveryEvent if gap detected ...

        select {
        case s.txCh <- txEvent:
        default:
            // All 10,000 buffer slots occupied. Drop and meter.
            droppedZMQMessages.WithLabelValues("hwm").Inc()
        }
        // Same non-blocking pattern for blockCh.
    }
}
```

**Throughput ceiling:** 20 workers × (1000ms / avg_handler_ms) events/s per topic.
At 100ms average handler time: 200 tx/s. At mainnet average (~7 tx/s), 20 workers
provides ~28× headroom.

**Buffer math:** 10,000-entry channel at 100 tx/s = 100 seconds before overflow.
At maximum burst (100 tx/s): 100 seconds. Both far exceed `BTC_HANDLER_TIMEOUT_MS`
(max 120s), ensuring the buffer never causes a cascade where overflow hides a
timeout problem.

**Fan-out inside handlers:**
- SSE fan-out: display_tx handler sends non-blocking to each connected SSE client
  channel; overflow drops to `dropped_zmq_messages_total{reason="sse_overflow"}`.
- Settlement fan-out: settlement_tx handler sends to settlement channel; overflow
  → `listStore.LPush(ctx, "btc:settlement:overflow", "0:{hashHex}")`.
  LPush failure → `dropped_zmq_messages_total{reason="settlement_overflow_redis_fail"}` at ERROR.
- Sequence gaps: WARNING + `dropped_zmq_messages_total{reason="sequence_gap"}` + RecoveryEvent.

---

## §6 — safeInvoke & safeInvokeBlock

```go
func safeInvoke(
    s           *Subscriber,
    h           func(ctx context.Context, e TxEvent),
    parentCtx   context.Context,
    e           TxEvent,
    handlerName string,
    timeoutMs   int,
) {
    ctx, cancel := context.WithTimeout(parentCtx,
        time.Duration(timeoutMs)*time.Millisecond)
    defer cancel()

    done := make(chan struct{})
    s.wg.Add(1)  // tracked for graceful shutdown via Shutdown()
    go func() {
        defer s.wg.Done()
        defer close(done)
        defer func() {
            // MUST be inside the spawned goroutine.
            // recover() in the calling frame cannot catch panics
            // in a different goroutine.
            if r := recover(); r != nil {
                log.Error().Str("handler", handlerName).
                    Interface("panic", r).Stack().
                    Msg("TxEvent handler panic recovered")
                bitcoinHandlerPanics.WithLabelValues(handlerName).Inc()
            }
        }()
        bitcoinHandlerGoroutinesInflight.WithLabelValues(handlerName).Inc()
        defer bitcoinHandlerGoroutinesInflight.WithLabelValues(handlerName).Dec()
        h(ctx, e)
    }()
    select {
    case <-done:
        // Handler completed within timeout.
    case <-ctx.Done():
        // Timeout expired. Goroutine is still running but tracked by wg.
        // The handler's context is now cancelled — if the handler respects
        // ctx.Done(), it will exit soon. If it doesn't, it will continue
        // running until it finishes, but it will not block other workers.
        bitcoinHandlerTimeouts.WithLabelValues(handlerName).Inc()
        log.Error().Str("handler", handlerName).
            Int("timeout_ms", timeoutMs).
            Msg("TxEvent handler timeout — context cancelled; goroutine tracked by WaitGroup")
    }
}

// safeInvokeBlock mirrors safeInvoke exactly, with BlockEvent instead of TxEvent.
```

**Key property:** `safeInvoke` blocks its caller worker for up to `timeoutMs`.
This is intentional — each worker processes one event at a time. The 20-worker
pool is the concurrency budget. Under sustained slow handlers, all workers block
and `txCh` fills. When `txCh` is full, the ZMQ read loop drops messages at the
non-blocking send. The metrics to watch before this point:
`bitcoin_handler_goroutines_inflight` and `bitcoin_handler_timeouts_total`.

---

## §7 — Shutdown

```go
func (s *Subscriber) Shutdown() {
    // wg tracks both: the 40 pool workers AND every safeInvoke goroutine
    // that is currently in-flight. All must drain before sockets are closed.
    drained := make(chan struct{})
    go func() { s.wg.Wait(); close(drained) }()
    select {
    case <-drained:
        log.Info().Msg("ZMQ subscriber: all handler goroutines drained")
    case <-time.After(30 * time.Second):
        log.Error().Msg("ZMQ subscriber: shutdown drain timed out after 30s")
    }
    // Close ZMQ sockets after drain (or timeout).
    // Closing before drain can cause in-flight handlers to read from
    // a closed socket if they attempt RPC calls that internally use ZMQ.
}
```

`Shutdown()` MUST be called after `Run()`'s context is cancelled. Calling it
before cancelling the context will block forever because the pool workers loop
on `ctx.Done()` and will never exit.

---

## §8 — ZMQ Endpoint Validation (server.go)

Lives in `server.go`, called at startup before `zmq.New()`.

```go
func requireZMQEndpoint(endpoint, envName string) {
    if strings.HasPrefix(endpoint, "tcp://") {
        hostPort := strings.TrimPrefix(endpoint, "tcp://")
        host, portStr, err := net.SplitHostPort(hostPort)
        if err != nil {
            panic(fmt.Sprintf("%s: invalid tcp endpoint %q: %v", envName, endpoint, err))
        }
        port, err := strconv.Atoi(portStr)
        if err != nil || port < 1 || port > 65535 {
            panic(fmt.Sprintf("%s: invalid port in %q", envName, endpoint))
        }
        ip := net.ParseIP(host)
        if ip == nil || (!ip.IsLoopback()) {
            panic(fmt.Sprintf("%s: must be loopback address, got %q", envName, host))
        }
        return
    }
    if strings.HasPrefix(endpoint, "ipc://") {
        // BUILD TAG REQUIREMENT:
        // zmq_endpoint_unix.go  (//go:build !windows) — IPC socket checks:
        //   resolve symlinks, restrict to BTC_ZMQ_IPC_DIR,
        //   verify UID ownership, verify not world-writable.
        // zmq_endpoint_windows.go (//go:build windows) — always panics:
        //   "ipc:// not supported on Windows; use tcp://127.0.0.1:"
        // This split prevents *syscall.Stat_t type assertion silently
        // failing on Windows CI.
        return
    }
    panic(fmt.Sprintf("%s must be loopback TCP or Unix socket, got %q",
        envName, endpoint))
}
```

Called for both `BTC_ZMQ_BLOCK` and `BTC_ZMQ_TX` before `zmq.New()`.

**Idle timeout wiring (H-03 fix):**
```go
// server.go — translate BTC_ZMQ_IDLE_TIMEOUT=0 to network default
// BEFORE calling zmq.New(). Zero is not a valid timeout.
idleTimeout := time.Duration(cfg.BitcoinZMQIdleTimeout) * time.Second
if idleTimeout == 0 {
    if cfg.BitcoinNetwork == "mainnet" {
        idleTimeout = 600 * time.Second  // 10 min — mainnet block interval
    } else {
        idleTimeout = 120 * time.Second  // 2 min — testnet4 faster blocks
    }
}
sub, err := zmq.New(cfg.BitcoinZMQBlock, cfg.BitcoinZMQTx, idleTimeout)
```

---

## §9 — app.Deps Wiring & Shutdown Sequence

```go
// app.Deps additions:
BitcoinZMQ     *zmq.Subscriber
BitcoinNetwork string
```

**Graceful shutdown sequence (server.go):**
```go
// STEP 0 — MUST BE FIRST.
// Cancel all active HTTP handler contexts. SSE handlers are long-running;
// their context cancellation is what signals the TTL goroutines to exit.
// Without this, TTL goroutines only drain via svc.ctx.Done() (step 2),
// which works but takes longer.
httpServer.Shutdown(shutdownCtx)

// STEP 1 — drain ZMQ handler goroutines (30s ceiling).
// safeInvoke goroutines that are in-flight will finish naturally.
// Timed-out goroutines may still be running but wg.Wait() covers them.
sub.Shutdown()

// STEP 2 — drain domain goroutines (TTL + reconciliation + overflow drain).
// svc.cancel() fires here; any TTL goroutine that survived HTTP shutdown
// will see svc.ctx.Done() and exit cleanly.
svc.Shutdown()  // 15s ceiling

// STEP 3 — close DB pool and Redis connections.
```

---

## §10 — Test Inventory

### ZMQ subscriber unit tests

| ID | Test | Notes |
|---|---|---|
| T-40 | `TestSubscriber_NonLoopbackBlockEndpoint_Rejected` | tcp:// non-loopback → panic at startup |
| T-41 | `TestSubscriber_NonLoopbackTxEndpoint_Rejected` | |
| T-42 | `TestSubscriber_SequenceGapEmitsRecoveryEvent` | gap in sequence numbers → RecoveryEvent fired |
| T-43 | `TestSubscriber_DisplayTxHandlerPanicIsolated` | panic in display handler → process survives |
| T-44 | `TestSubscriber_BlockHandlerPanicIsolated` | panic in block handler → process survives |
| T-45 | `TestSubscriber_ReconnectEmitsRecoveryEvent` | disconnect → reconnect → RecoveryEvent before next event |
| T-46 | `TestSubscriber_OverflowWritesRedis` | settlement channel full → LPush to overflow key |
| T-47 | `TestSubscriber_RPCFailureDoesNotFlipIsConnected` | IsConnected() based on ZMQ messages, not RPC |
| T-48 | `TestSubscriber_404SkippedGracefully` | unknown txid from RPC → no panic, no metric |
| T-63 | `TestSubscriber_BlockHandlerReceivesCorrectHash` | HashHex() matches known RPC hex |
| T-64 | `TestSubscriber_BlockHandlerCalledOnBlock` | |
| T-101 | `TestSafeInvoke_PanicInInnerGoroutine_ProcessSurvives` | recover() inside goroutine, not calling frame |
| T-108 | `TestSubscriber_Shutdown_DrainsInflightHandlers` | Shutdown() waits for in-flight goroutines |
| T-109 | `TestSubscriber_Shutdown_TimeoutAfter30s` | if handlers hang past 30s, Shutdown() logs and continues |
| T-121 | `TestBlockEvent_HashHex_IsReversed` | ZMQ raw bytes → HashHex() == known RPC block hash |
| T-122 | `TestTxEvent_HashHex_IsReversed` | same for TxEvent |
| T-151 | `TestSubscriber_ChannelDepth_IsDoubleHWM` | fill txCh with HWM×2 messages; assert all buffered; one more → hwm metric increments, no sender block |

### Startup validation tests (ZMQ-related)

| ID | Test | Notes |
|---|---|---|
| T-49 | `TestStartup_ZMQBlockNonLoopback_Rejected` | BTC_ZMQ_BLOCK=tcp://0.0.0.0:28332 → panic |
| T-50 | `TestStartup_ZMQTxNonLoopback_Rejected` | |
| T-117 | `TestStartup_ZMQPort_InvalidRejects` | BTC_ZMQ_BLOCK with port 0 or >65535 |
| T-138 | `TestStartup_ZMQIdleTimeout_ZeroUsesNetworkDefault` | BTC_NETWORK=mainnet + BTC_ZMQ_IDLE_TIMEOUT=0 → 600s; testnet4 → 120s (H-03 fix) |

### Handler timeout + panic tests

| ID | Test | Notes |
|---|---|---|
| T-89 | `TestHandlerTimeout_BlockingHandlerCancelled` | handler ignores ctx; timeout fires; worker freed — **implemented in this package** |
| T-104 | `TestSSEHandler_PanicInLoop_SlotReleased` | panic in SSE event loop → doCleanup via sync.Once — **belongs in `domain/bitcoin/events/handler_test.go`** |

### Tests that belong in other packages

These tests were specified against the ZMQ subscriber but require infrastructure
(live ZMQ server, Redis, RPC client) that this package explicitly does not import.
They are recorded here for traceability and must be implemented in the packages
that own the relevant infrastructure.

| ID | Test | Target package | Reason |
|---|---|---|---|
| T-45 | `TestSubscriber_ReconnectEmitsRecoveryEvent` | Integration test suite or `payment` package | Requires a real ZMQ server to disconnect/reconnect; unit test mocking a live socket is brittle |
| T-46 | `TestSubscriber_OverflowWritesRedis` | `domain/bitcoin/events` settlement handler tests | The overflow→Redis path lives in the settlement_tx handler, not in the ZMQ subscriber itself |
| T-47 | `TestSubscriber_RPCFailureDoesNotFlipIsConnected` | `platform/bitcoin/rpc` package tests | IsConnected() is based on ZMQ messages only; the RPC client is a separate concern |
| T-48 | `TestSubscriber_404SkippedGracefully` | `platform/bitcoin/rpc` package tests | Unknown txid handling is an RPC client concern, not a ZMQ subscriber concern |
| T-104 | `TestSSEHandler_PanicInLoop_SlotReleased` | `domain/bitcoin/events/handler_test.go` | The SSE event loop and doCleanup sync.Once live in the events handler, not here |
