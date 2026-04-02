package zmq

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

// ── Constants ─────────────────────────────────────────────────────────────────

// DefaultSubscriberHWM is the sizing basis for the internal event channels
// (buffered at HWM×2 = 10,000 slots). Overflow protection is provided
// entirely by the non-blocking channel select in onEvent — messages that
// arrive when the channel is full are dropped and metered.
const DefaultSubscriberHWM = 5000

const (
	// DefaultChannelDepth is the capacity of blockCh and txCh.
	//
	// BlockCh: mainnet averages one block every 10 min — this will never fill.
	//
	// TxCh: mainnet averages ~7 tx/s with bursts up to ~100 tx/s. At 100 tx/s,
	// 10,000 slots provide ~100 s of headroom, well above the handler timeout,
	// preventing cascade overflow under sustained load.
	defaultChannelDepth = DefaultSubscriberHWM * 2 // 10,000

	// DefaultWorkerCount is the number of pool goroutines for each event type
	// (block, tx, and rawtx). 20 workers per type × 3 types = 60 total.
	//
	// Sizing rationale: at a per-handler invocation timeout of 30 s and expected
	// p99 handler latency of ~100 ms, each worker can process ~300 events/s
	// in the best case (one event per timeout period). With 20 workers, the pool
	// can sustain ~6,000 events/s. Above this, events queue in the channel buffer
	// (defaultChannelDepth = 10,000 slots). If sustained load exceeds this capacity,
	// in-flight handlers will complete, freeing workers, and processing will
	// eventually catch up. Under sustained 10,000+ events/s, the channel fills
	// and events are dropped (see onEvent select with default case).
	//
	// At 30 s timeout + 100,000 slots, we can buffer ~3,333 events/s for 30 s,
	// providing a reasonable grace period for brief traffic spikes without
	// dropping events to application-layer subscribers.
	defaultWorkerCount = 20

	// DefaultHandlerTimeout is the per-handler invocation deadline when none
	// is configured. Matches the BTC_HANDLER_TIMEOUT_MS config default (30 s).
	defaultHandlerTimeout = 30 * time.Second

	// ShutdownTimeout is the maximum time Shutdown() waits for in-flight
	// handler goroutines before logging an error and returning. Tests assert
	// this value.
	shutdownTimeout = 30 * time.Second

	// ReconnectBase and reconnectCeiling bound the exponential backoff used
	// when a connection attempt or receive fails.
	reconnectBase    = 1 * time.Second
	reconnectCeiling = 60 * time.Second
)

// logger is the package-level structured logger. All records carry component="zmq".
var logger = telemetry.New("zmq")

// ── Internal types ────────────────────────────────────────────────────────────

// liveness is an immutable snapshot of the most recently received block.
// Stored via atomic.Pointer so IsConnected() and LastSeenHash() always read a
// consistent pair — the hash and timestamp are never torn across two separate
// atomic operations.
type liveness struct {
	hash string
	at   time.Time
}

// readerState is the per-session state for a single ZMQ reader loop. It persists
// across reconnects so the subscriber can detect sequence gaps after a reconnect.
// The zero value is the correct initial state (no message received yet).
type readerState struct {
	lastSeq     uint32 // sequence number of the most recently received message
	lastSeqSeen bool   // false until the first message in this session
}

// readerConfig parameterises a single reader loop. All callbacks are required
// and must not be nil.
type readerConfig struct {
	endpoint   string                                  // TCP endpoint, e.g. "tcp://127.0.0.1:28332"
	topic      []byte                                  // subscription topic, e.g. []byte("hashblock")
	onDialOK   func()                                  // called after each successful connect + subscribe
	onDialFail func()                                  // called after each failed connect
	onRecvErr  func()                                  // called after each failed receive
	onEvent    func(context.Context, [32]byte, uint32) // called for each valid frame
}

// ── Subscriber ───────────────────────────────────────────────────────────────

// Subscriber is the public contract for a ZMQ event subscriber.
// Depend on this interface in domain packages — never on *subscriber directly.
// This keeps domain packages decoupled from the ZMQ implementation and makes
// them trivially testable with a mock.
type Subscriber interface {
	// RegisterBlockHandler registers h to be called on every new block event.
	// Must be called before Run(). Panics if h is nil or if Run() has already started.
	RegisterBlockHandler(func(context.Context, BlockEvent))

	// RegisterRawTxHandler registers h for mempool transactions on the SSE
	// display path. The handler receives a fully decoded RawTxEvent with inputs
	// and outputs already parsed from the rawtx wire bytes — no GetRawTransaction
	// RPC call is needed. Must be called before Run(). Panics if h is nil or if
	// Run() has already started.
	RegisterRawTxHandler(func(context.Context, RawTxEvent))

	// RegisterSettlementTxHandler registers h for mempool transactions on the
	// settlement path. Must be called before Run(). Panics if h is nil or if Run() has already started.
	RegisterSettlementTxHandler(func(context.Context, TxEvent))

	// RegisterRecoveryHandler registers h to be called after each reconnect or
	// sequence gap, before event delivery resumes. Must be called before Run().
	// Panics if h is nil or if Run() has already started.
	RegisterRecoveryHandler(func(context.Context, RecoveryEvent))

	// Run blocks until ctx is cancelled, returning ctx.Err() on normal shutdown.
	// Run never returns on transient errors — it reconnects with exponential backoff
	// and never surfaces transient failures to the caller. Panics if called more than once.
	//
	// Run in a goroutine and cancel the context to initiate shutdown:
	//
	//	go func() {
	//	    if err := sub.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
	//	        slog.Error("zmq: subscriber exit", "error", err)
	//	        appCancelFn()
	//	    }
	//	}()
	//	defer sub.Shutdown()
	Run(context.Context) error

	// Shutdown drains all in-flight handler goroutines, then returns. It blocks
	// for up to 30 s. Must be called after cancelling the ctx passed to Run().
	Shutdown()

	// IsReady reports whether all required ZMQ subscriptions are currently
	// dialled and ready to deliver events. This excludes age-based liveness so
	// callers can distinguish transport failure from a quiet chain.
	IsReady() bool

	// IsConnected reports whether the subscriber appears healthy based on local
	// liveness signals. Returns false if either dial has failed or the last block
	// is older than the configured idle timeout.
	IsConnected() bool

	// LastSeenHash returns the most recently received block hash in the same hex
	// form used by RPC and block explorers. Returns "" before the first block is
	// received.
	LastSeenHash() string

	// LastHashTime returns the Unix nanosecond timestamp of the most recently
	// received block. Returns 0 before the first block is received, consistent
	// with the H-04 invariant (prevents spurious liveness gauge flips on
	// fresh startup before Bitcoin Core delivers its first block).
	//
	// The value is derived from the same atomic.Pointer[liveness] as
	// LastSeenHash() — both methods always read a consistent snapshot.
	LastHashTime() int64
}

// subscriber is the concrete ZMTP implementation of the Subscriber interface.
// It manages three ZMTP 3.1 SUB connections — one for hashblock events, one for
// hashtx events, and one for rawtx events — decodes raw frames into typed Go
// events, and fans them out to registered handlers via a fixed worker pool.
//
// ZMQ topics used:
//   - hashblock: delivers the 32-byte block hash when a new block is mined.
//   - rawtx:     delivers the full serialized transaction bytes when a new
//     transaction enters the mempool. Used instead of hashtx to
//     eliminate the GetRawTransaction RPC call and the race condition
//     it creates on pruned nodes without txindex=1.
//
// Zero external dependencies: this package owns its own ZMTP implementation
// in transport.go. No third-party ZMQ library is required.
//
// Zero domain imports: this is a pure platform concern. Domain packages register
// handlers via the Subscriber interface and never interact with raw frames.
//
// Typical usage:
//
//	sub, err := zmq.New(blockEndpoint, txEndpoint, idleTimeout, deps.Metrics)
//	sub.RegisterBlockHandler(myBlockHandler)
//	sub.RegisterSettlementTxHandler(mySettlementHandler)
//	sub.RegisterRawTxHandler(myDisplayHandler)
//
//	go func() {
//	    if err := sub.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
//	        slog.Error("zmq: subscriber exit", "error", err)
//	        appCancelFn()
//	    }
//	}()
//	defer sub.Shutdown()
type subscriber struct {
	// Endpoints — set at construction, read-only after.
	blockEndpoint string
	txEndpoint    string

	// hrp is the Bech32 human-readable part for the configured network
	// (e.g., "bc" for mainnet, "tb" for testnet, "bcrt" for regtest).
	// Set at construction, read-only after. Used by ParseRawTx.
	hrp string

	// Handler slices — registered before Run(), read-only after.
	// Data race protection: started.CompareAndSwap enforces the "before Run"
	// invariant in all Register* methods.
	blockHandlers    []func(context.Context, BlockEvent)
	rawTxHandlers    []func(context.Context, RawTxEvent)
	settleTxHandlers []func(context.Context, TxEvent)
	recoveryHandlers []func(context.Context, RecoveryEvent)

	// Internal delivery channels between reader goroutines and the worker pool.
	blockCh chan BlockEvent
	// rawTxCh carries decoded RawTxEvent values from the rawtx reader to display
	// handler workers. Buffered at the same depth as blockCh/txCh.
	rawTxCh chan RawTxEvent
	// txCh carries TxEvent values for the settlement path (hashtx topic).
	// The settlement handler still uses hashtx + GetTransaction (wallet RPC)
	// which works on pruned nodes via the wallet index.
	txCh chan TxEvent

	// live is an atomically-updated liveness snapshot set on every BlockEvent.
	// nil until the first block is received, which prevents spurious
	// "disconnected" alerts on fresh startup before Bitcoin Core delivers its
	// first block (H-04 fix).
	live atomic.Pointer[liveness]

	// Dial health — each reader maintains its own flag so readiness/liveness
	// reflect the true state of all subscribed streams, not just whichever tx
	// reader most recently updated a shared flag.
	blockDialOK  atomic.Bool
	hashtxDialOK atomic.Bool
	rawtxDialOK  atomic.Bool

	// started is set to true by Run(). Prevents double-start and late handler
	// registration, both of which would be data races.
	started atomic.Bool

	// wg tracks all pool workers and every in-flight handler goroutine.
	// Shutdown() waits on this WaitGroup before returning.
	wg sync.WaitGroup

	// inflightGoroutines is the current count of handler goroutines executing.
	// Incremented/decremented atomically inside each goroutine; reported to
	// recorder.SetHandlerGoroutines on every change.
	inflightGoroutines atomic.Int64

	// idleTimeout is the maximum age of the last received block before
	// IsConnected() considers the subscriber stale.
	idleTimeout time.Duration

	// handlerTimeout is the per-invocation deadline for every handler call.
	// A handler that exceeds this deadline has its context cancelled; it is
	// still tracked by wg and must honour ctx.Done() to release the goroutine.
	handlerTimeout time.Duration

	// shutdownDrainTimeout is the maximum time Shutdown() waits before logging
	// an error and returning. Defaults to shutdownTimeout (30 s). Overridable
	// in tests via direct field assignment before the first Shutdown() call.
	shutdownDrainTimeout time.Duration

	// recorder receives all observability calls. Never nil after construction.
	recorder ZMQRecorder
}

// New constructs a Subscriber backed by two ZMTP 3.1 SUB connections and
// returns the Subscriber interface — callers depend on the interface, not
// the concrete type.
//
// Panics if either endpoint is not a loopback TCP address — the ZMQ port must
// never be reachable from outside the machine running Bitcoin Core. IPC
// endpoints are not supported; use tcp://127.0.0.1:<port>.
//
// Returns an error if idleTimeout is outside [30s, 3600s]. Zero is not
// accepted — server.go must translate BTC_ZMQ_IDLE_TIMEOUT=0 to a
// network-appropriate default before calling New():
//
//	idleTimeout := time.Duration(cfg.BitcoinZMQIdleTimeout) * time.Second
//	if idleTimeout == 0 {
//	    if cfg.BitcoinNetwork == "mainnet" {
//	        idleTimeout = 600 * time.Second // one full block interval
//	    } else {
//	        idleTimeout = 120 * time.Second // testnet4 produces blocks faster
//	    }
//	}
//	sub, err := zmq.New(cfg.BitcoinZMQBlock, cfg.BitcoinZMQTx, cfg.BitcoinNetwork, idleTimeout, deps.Metrics)
//
// network must be one of "mainnet", "testnet", "testnet4", "signet", or "regtest".
// Returns an error if network is not recognized — unknown values are not silently
// defaulted. The network determines the Bech32 HRP used by ParseRawTx for address
// encoding.
//
// recorder may be nil; a no-op recorder is substituted automatically.
func New(blockEndpoint, txEndpoint, network string, idleTimeout time.Duration, recorder ZMQRecorder) (Subscriber, error) {
	// Security: panic, not error, so a misconfigured endpoint fails at startup
	// and never silently degrades to an insecure configuration at runtime.
	requireLoopbackTCP(blockEndpoint, "BTC_ZMQ_BLOCK")
	requireLoopbackTCP(txEndpoint, "BTC_ZMQ_TX")

	if idleTimeout < 30*time.Second || idleTimeout > 3600*time.Second {
		return nil, telemetry.ZMQ("New.validate",
			fmt.Errorf("idleTimeout must be between 30s and 3600s (got %v); "+
				"translate BTC_ZMQ_IDLE_TIMEOUT=0 to a network default in server.go before calling New()", idleTimeout))
	}

	hrp, err := networkToHRP(network)
	if err != nil {
		return nil, telemetry.ZMQ("New.network", err)
	}

	if recorder == nil {
		recorder = noopRecorder{}
	}

	return &subscriber{
		blockEndpoint:        blockEndpoint,
		txEndpoint:           txEndpoint,
		hrp:                  hrp,
		blockCh:              make(chan BlockEvent, defaultChannelDepth),
		rawTxCh:              make(chan RawTxEvent, defaultChannelDepth),
		txCh:                 make(chan TxEvent, defaultChannelDepth),
		idleTimeout:          idleTimeout,
		handlerTimeout:       defaultHandlerTimeout,
		shutdownDrainTimeout: shutdownTimeout,
		recorder:             recorder,
	}, nil
}

// ── Handler registration ──────────────────────────────────────────────────────

// RegisterBlockHandler registers h to be called on every new block event.
// Multiple handlers may be registered; all are called sequentially per worker.
// Must be called before Run(). Panics if h is nil or if Run() has already started.
func (s *subscriber) RegisterBlockHandler(h func(context.Context, BlockEvent)) {
	s.mustRegister("RegisterBlockHandler", h != nil)
	s.blockHandlers = append(s.blockHandlers, h)
}

// RegisterRawTxHandler registers h for mempool transactions on the SSE display
// path. The handler receives a RawTxEvent with inputs and outputs already decoded
// from the rawtx wire bytes — no GetRawTransaction RPC call required.
//
// Always use this for SSE display handlers — never RegisterSettlementTxHandler.
// RawTx and settlement handlers run in separate fan-out loops (ADR-BTC-01) so a
// slow or panicking settlement handler cannot stall SSE delivery.
//
// Must be called before Run(). Panics if h is nil or if Run() has already started.
func (s *subscriber) RegisterRawTxHandler(h func(context.Context, RawTxEvent)) {
	s.mustRegister("RegisterRawTxHandler", h != nil)
	s.rawTxHandlers = append(s.rawTxHandlers, h)
}

// RegisterSettlementTxHandler registers h for mempool transactions on the
// settlement path. Must be called before Run(). Panics if h is nil or if Run()
// has already started.
func (s *subscriber) RegisterSettlementTxHandler(h func(context.Context, TxEvent)) {
	s.mustRegister("RegisterSettlementTxHandler", h != nil)
	s.settleTxHandlers = append(s.settleTxHandlers, h)
}

// RegisterRecoveryHandler registers h to be called after each reconnect or
// sequence gap, before event delivery resumes. The settlement engine registers
// here to trigger gap-fill reconciliation. Must be called before Run(). Panics
// if h is nil or if Run() has already started.
func (s *subscriber) RegisterRecoveryHandler(h func(context.Context, RecoveryEvent)) {
	s.mustRegister("RegisterRecoveryHandler", h != nil)
	s.recoveryHandlers = append(s.recoveryHandlers, h)
}

// mustRegister is the shared guard for all Register* methods. It panics if
// Run() has already been called (to prevent data races on handler slices) or
// if the caller passed a nil handler.
func (s *subscriber) mustRegister(method string, nonNil bool) {
	if !nonNil {
		panic("zmq: " + method + ": handler must not be nil")
	}
	if s.started.Load() {
		panic("zmq: " + method + ": must not be called after Run()")
	}
}

// ZMQRecorder is the narrow observability interface required by Subscriber.
// It is defined here (in the platform package) rather than in the domain packages
// because the ZMQ platform owns both the data source and the observability concerns.
// Domain packages depend on this interface and receive metrics via the Subscriber.
//
// *telemetry.Registry satisfies this interface via the hook methods in
// internal/platform/telemetry/bitcoin_hooks.go — pass deps.Metrics directly;
// no factory method is needed. Pass nil to silence all metrics.
//
//nolint:revive // Kept for consistency with package-level Bitcoin metric naming.
type ZMQRecorder interface {
	// SetZMQConnected sets the ZMQ connectivity gauge (1=connected, 0=disconnected).
	// Driven by the block socket only — the block stream is the primary liveness signal.
	SetZMQConnected(connected bool)

	// OnHandlerPanic increments bitcoin_handler_panics_total for the named handler.
	OnHandlerPanic(handler string)

	// OnHandlerTimeout increments bitcoin_handler_timeouts_total for the named
	// handler. Called when the handler context deadline expires before it returns;
	// the goroutine continues running until it honours ctx.Done().
	OnHandlerTimeout(handler string)

	// SetHandlerGoroutines records the current count of in-flight handler
	// goroutines in bitcoin_handler_goroutines_inflight.
	SetHandlerGoroutines(count int)

	// OnMessageDropped increments dropped_zmq_messages_total{reason}.
	// Known reasons: "hwm" (channel full), "sequence_gap" (ZMQ layer dropped).
	OnMessageDropped(reason string)

	// SetChannelDepth records the current depth (items in buffer) and capacity
	// of a ZMQ event channel. channel is one of "block", "tx", or "rawtx".
	SetChannelDepth(channel string, depth, capacity int)
}

// compile-time check that *telemetry.Registry satisfies ZMQRecorder.
var _ ZMQRecorder = (*telemetry.Registry)(nil)

// noopRecorder discards all metric calls. Substituted when New() receives nil.
type noopRecorder struct{}

// SetZMQConnected implements ZMQRecorder.
func (noopRecorder) SetZMQConnected(bool) {}

// OnHandlerPanic implements ZMQRecorder.
func (noopRecorder) OnHandlerPanic(string) {}

// OnHandlerTimeout implements ZMQRecorder.
func (noopRecorder) OnHandlerTimeout(string) {}

// SetHandlerGoroutines implements ZMQRecorder.
func (noopRecorder) SetHandlerGoroutines(int) {}

// OnMessageDropped implements ZMQRecorder.
func (noopRecorder) OnMessageDropped(string) {}

// SetChannelDepth implements ZMQRecorder.
func (noopRecorder) SetChannelDepth(string, int, int) {}

