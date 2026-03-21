package zmq

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-zeromq/zmq4"

	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

// ── Constants ─────────────────────────────────────────────────────────────────

// DefaultSubscriberHWM is the high-water mark set on both ZMQ SUB sockets
// (zmq4.WithHWM) and the sizing basis for the internal event channels
// (buffered at HWM×2). Raising this value increases memory use on both the
// Bitcoin Core side (send buffer) and here (receive buffer + channel).
const DefaultSubscriberHWM = 5000

const (
	// defaultChannelDepth is the capacity of blockCh and txCh.
	//
	// blockCh: mainnet averages one block every 10 min — this will never fill.
	//
	// txCh: mainnet averages ~7 tx/s with bursts up to ~100 tx/s. At 100 tx/s,
	// 10,000 slots provide ~100 s of headroom, well above the handler timeout,
	// preventing cascade overflow under sustained load.
	defaultChannelDepth = DefaultSubscriberHWM * 2 // 10,000

	// defaultWorkerCount is the number of pool goroutines for each event type
	// (block and tx). 20 block workers + 20 tx workers = 40 total.
	defaultWorkerCount = 20

	// defaultHandlerTimeout is the per-handler invocation deadline when none
	// is configured. Matches the BTC_HANDLER_TIMEOUT_MS config default (30 s).
	defaultHandlerTimeout = 30 * time.Second

	// shutdownTimeout is the maximum time Shutdown() waits for in-flight
	// handler goroutines before logging an error and returning. Tests assert
	// this value.
	shutdownTimeout = 30 * time.Second

	// reconnectBase and reconnectCeiling bound the exponential backoff used
	// when a ZMQ socket dial or recv fails.
	reconnectBase    = 1 * time.Second
	reconnectCeiling = 60 * time.Second
)

// ── ZMQRecorder ──────────────────────────────────────────────────────────────

// ZMQRecorder is the narrow observability interface required by Subscriber.
// *telemetry.Registry satisfies this interface via the hook methods in
// internal/platform/telemetry/bitcoin_hooks.go — pass deps.Metrics directly;
// no factory method is needed. Pass nil to silence all metrics.
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
}

// noopRecorder discards all metric calls. Substituted when New() receives nil.
type noopRecorder struct{}

func (noopRecorder) SetZMQConnected(bool)     {}
func (noopRecorder) OnHandlerPanic(string)    {}
func (noopRecorder) OnHandlerTimeout(string)  {}
func (noopRecorder) SetHandlerGoroutines(int) {}
func (noopRecorder) OnMessageDropped(string)  {}

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

// readerConfig parameterises a single ZMQ reader loop. All four callbacks are
// required and must not be nil.
type readerConfig struct {
	endpoint   string                          // ZMQ TCP endpoint, e.g. "tcp://127.0.0.1:28332"
	topic      []byte                          // ZMQ subscription topic, e.g. []byte("hashblock")
	onDialOK   func()                          // called after each successful Dial + Subscribe
	onDialFail func()                          // called after each failed Dial or Subscribe
	onRecvErr  func()                          // called after each failed Recv
	onEvent    func(hash [32]byte, seq uint32) // called for each valid frame
}

// ── Subscriber ───────────────────────────────────────────────────────────────

// Subscriber is the public contract for a ZMQ event subscriber.
// Depend on this interface in domain packages — never on *subscriber directly.
// This keeps domain packages decoupled from the ZMQ implementation and makes
// them trivially testable with a mock.
type Subscriber interface {
	RegisterBlockHandler(func(context.Context, BlockEvent))
	RegisterDisplayTxHandler(func(context.Context, TxEvent))
	RegisterSettlementTxHandler(func(context.Context, TxEvent))
	RegisterRecoveryHandler(func(context.Context, RecoveryEvent))
	Run(context.Context) error
	Shutdown()
	IsConnected() bool
	LastSeenHash() string
}

// subscriber is the concrete ZMQ implementation of the Subscriber interface.
// It manages two ZMQ SUB sockets — one for hashblock events, one for hashtx
// events — decodes raw frames into typed Go events, and fans them out to
// registered handlers via a fixed worker pool.
//
// Zero domain imports: this is a pure platform concern. Domain packages register
// handlers via the Subscriber interface and never interact with ZMQ frames.
//
// Typical usage:
//
//	sub, err := zmq.New(blockEndpoint, txEndpoint, idleTimeout, deps.Metrics)
//	sub.RegisterBlockHandler(myBlockHandler)
//	sub.RegisterSettlementTxHandler(mySettlementHandler)
//	sub.RegisterDisplayTxHandler(myDisplayHandler)
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

	// Handler slices — registered before Run(), read-only after.
	// Data race protection: started.CompareAndSwap enforces the "before Run"
	// invariant in all Register* methods.
	blockHandlers     []func(context.Context, BlockEvent)
	displayTxHandlers []func(context.Context, TxEvent)
	settleTxHandlers  []func(context.Context, TxEvent)
	recoveryHandlers  []func(context.Context, RecoveryEvent)

	// Internal delivery channels between ZMQ reader goroutines and the worker pool.
	blockCh chan BlockEvent
	txCh    chan TxEvent

	// live is an atomically-updated liveness snapshot set on every BlockEvent.
	// nil until the first block is received, which prevents spurious
	// "disconnected" alerts on fresh startup before Bitcoin Core delivers its
	// first block (H-04 fix).
	live atomic.Pointer[liveness]

	// Dial health — each reader maintains its own flag so IsConnected() can
	// distinguish a healthy block socket from a healthy tx socket.
	blockDialOK atomic.Bool
	txDialOK    atomic.Bool

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
	// IsConnected() considers the connection stale.
	idleTimeout time.Duration

	// handlerTimeout is the per-invocation deadline for every handler call.
	// A handler that exceeds this deadline has its context cancelled; it is
	// still tracked by wg and must honour ctx.Done() to release the goroutine.
	handlerTimeout time.Duration

	// recorder receives all observability calls. Never nil after construction.
	recorder ZMQRecorder
}

// New constructs a Subscriber backed by two ZMQ SUB sockets and returns the
// Subscriber interface — callers depend on the interface, not the concrete type.
//
// Panics if either endpoint is not a loopback TCP address — the ZMQ port must
// never be reachable from outside the machine running Bitcoin Core. IPC
// endpoints are not supported on Windows; use tcp://127.0.0.1:<port>.
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
//	sub, err := zmq.New(cfg.BitcoinZMQBlock, cfg.BitcoinZMQTx, idleTimeout, deps.Metrics)
//
// recorder may be nil; a no-op recorder is substituted automatically.
func New(blockEndpoint, txEndpoint string, idleTimeout time.Duration, recorder ZMQRecorder) (Subscriber, error) {
	// Security: panic, not error, so a misconfigured endpoint fails at startup
	// and never silently degrades to an insecure configuration at runtime.
	requireLoopbackTCP(blockEndpoint, "BTC_ZMQ_BLOCK")
	requireLoopbackTCP(txEndpoint, "BTC_ZMQ_TX")

	if idleTimeout < 30*time.Second || idleTimeout > 3600*time.Second {
		return nil, telemetry.ZMQ("New.validate",
			fmt.Errorf("idleTimeout must be between 30s and 3600s (got %v); "+
				"translate BTC_ZMQ_IDLE_TIMEOUT=0 to a network default in server.go before calling New()", idleTimeout))
	}

	if recorder == nil {
		recorder = noopRecorder{}
	}

	return &subscriber{
		blockEndpoint:  blockEndpoint,
		txEndpoint:     txEndpoint,
		blockCh:        make(chan BlockEvent, defaultChannelDepth),
		txCh:           make(chan TxEvent, defaultChannelDepth),
		idleTimeout:    idleTimeout,
		handlerTimeout: defaultHandlerTimeout,
		recorder:       recorder,
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

// RegisterDisplayTxHandler registers h for mempool transactions on the SSE
// display path. Always use this for SSE handlers — never RegisterSettlementTxHandler.
// Display and settlement handlers run in separate fan-out loops (ADR-BTC-01)
// so a slow or panicking settlement handler cannot stall SSE delivery.
// Must be called before Run(). Panics if h is nil or if Run() has already started.
func (s *subscriber) RegisterDisplayTxHandler(h func(context.Context, TxEvent)) {
	s.mustRegister("RegisterDisplayTxHandler", h != nil)
	s.displayTxHandlers = append(s.displayTxHandlers, h)
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

// ── Lifecycle ─────────────────────────────────────────────────────────────────

// Run blocks until ctx is cancelled, returning ctx.Err() on normal shutdown.
// Run never returns on transient ZMQ errors — it reconnects with exponential
// backoff (1s initial, 60s ceiling, ±50% jitter) and never surfaces transient
// failures to the caller.
//
// Run starts defaultWorkerCount block workers and defaultWorkerCount tx workers
// before entering the ZMQ reader loops. Workers are drained by Shutdown() after
// ctx is cancelled.
//
// Run panics if called more than once.
func (s *subscriber) Run(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		panic("zmq: Run: must not be called more than once")
	}

	s.startWorkers(ctx)

	var readersWg sync.WaitGroup
	readersWg.Go(func() {
		var state readerState
		s.runReader(ctx, s.blockReaderConfig(), &state)
	})
	readersWg.Go(func() {
		var state readerState
		s.runReader(ctx, s.txReaderConfig(), &state)
	})
	readersWg.Wait()
	return ctx.Err()
}

// Shutdown drains all in-flight handler goroutines, then returns. It blocks
// until every goroutine calls wg.Done() or shutdownTimeout (30 s) elapses,
// whichever comes first. On timeout it logs an error and returns — it never
// blocks indefinitely.
//
// MUST be called after cancelling the ctx passed to Run(). Calling Shutdown()
// before cancellation blocks indefinitely because pool workers loop on ctx.Done().
//
// Shutdown order in server.go:
//  1. HTTP server shutdown (cancels SSE handler contexts).
//  2. sub.Shutdown() — drain ZMQ handler goroutines.
//  3. svc.Shutdown() — drain domain goroutines.
//  4. Close DB pool and Redis connections.
func (s *subscriber) Shutdown() {
	// context.Background() is intentional: the run ctx has already been
	// cancelled by this point, so we use a background context for shutdown logs.
	// Trace context is not available here.
	bg := context.Background()

	drained := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(drained)
	}()

	t := time.NewTimer(shutdownTimeout)
	defer t.Stop()

	select {
	case <-drained:
		logger.Info(bg, "zmq: subscriber drained — all handler goroutines finished")
	case <-t.C:
		logger.Error(bg, "zmq: subscriber shutdown timed out — some goroutines may still be running",
			"timeout", shutdownTimeout)
	}
}

// IsConnected reports whether the subscriber appears healthy based on local
// liveness signals. It does NOT issue any network call.
//
// Returns false when either socket's last dial attempt failed, or when a block
// was received but more than idleTimeout ago. Returns true on fresh startup
// (both sockets dialled successfully, no block received yet) — this prevents
// spurious "disconnected" alerts immediately after deployment.
func (s *subscriber) IsConnected() bool {
	if !s.blockDialOK.Load() || !s.txDialOK.Load() {
		return false
	}
	p := s.live.Load()
	if p == nil {
		// Dial succeeded but no block received yet — treat as connected.
		return true
	}
	return time.Since(p.at) < s.idleTimeout
}

// LastSeenHash returns the most recently received block hash in RPC-compatible
// big-endian hex encoding. Returns "" before the first block is received (H-04
// fix: prevents the liveness goroutine from spuriously flipping the
// bitcoin_zmq_connected gauge to 0 on fresh startup).
func (s *subscriber) LastSeenHash() string {
	p := s.live.Load()
	if p == nil {
		return ""
	}
	return p.hash
}

// ── Worker pool ───────────────────────────────────────────────────────────────

// startWorkers launches defaultWorkerCount block workers and defaultWorkerCount
// tx workers. All workers are tracked by s.wg so Shutdown() can drain them.
func (s *subscriber) startWorkers(ctx context.Context) {
	// Block workers: one goroutine per slot. Each goroutine processes one
	// BlockEvent at a time, calling all registered block handlers sequentially.
	for range defaultWorkerCount {
		s.wg.Go(func() {
			for {
				select {
				case e := <-s.blockCh:
					for _, h := range s.blockHandlers {
						invokeHandler(s, ctx, h, e, "block")
					}
				case <-ctx.Done():
					return
				}
			}
		})
	}

	// Tx workers: display and settlement handlers share a single goroutine per worker slot.
	// A stall or panic in a settlement handler cannot affect SSE display delivery, and vice versa, because each type is
	// invoked separately via invokeHandler's per-call goroutine.
	for range defaultWorkerCount {
		s.wg.Go(func() {
			for {
				select {
				case e := <-s.txCh:
					for _, h := range s.displayTxHandlers {
						invokeHandler(s, ctx, h, e, "display_tx")
					}
					for _, h := range s.settleTxHandlers {
						invokeHandler(s, ctx, h, e, "settlement_tx")
					}
				case <-ctx.Done():
					return
				}
			}
		})
	}
}

// ── Reader configs ────────────────────────────────────────────────────────────

// blockReaderConfig returns the readerConfig for the hashblock ZMQ socket.
// The block reader is the primary liveness source: it updates the live atomic
// and drives the SetZMQConnected gauge.
func (s *subscriber) blockReaderConfig() readerConfig {
	return readerConfig{
		endpoint: s.blockEndpoint,
		topic:    []byte("hashblock"),
		onDialOK: func() {
			s.blockDialOK.Store(true)
			s.recorder.SetZMQConnected(true)
		},
		onDialFail: func() {
			s.blockDialOK.Store(false)
			s.recorder.SetZMQConnected(false)
		},
		onRecvErr: func() {
			s.blockDialOK.Store(false)
			s.recorder.SetZMQConnected(false)
		},
		onEvent: func(hash [32]byte, seq uint32) {
			event := BlockEvent{Hash: hash, Sequence: seq}
			// Single atomic Store: IsConnected() and LastSeenHash() always read
			// a consistent snapshot — hash and timestamp are never torn.
			s.live.Store(&liveness{hash: event.HashHex(), at: time.Now()})
			select {
			case s.blockCh <- event:
			default:
				// Buffer full — drop and meter. The read loop must never block
				// or it stalls all ZMQ delivery for this socket.
				s.recorder.OnMessageDropped("hwm")
			}
		},
	}
}

// txReaderConfig returns the readerConfig for the hashtx ZMQ socket.
// The tx reader does not drive the SetZMQConnected gauge — the block stream
// is the authoritative liveness signal.
func (s *subscriber) txReaderConfig() readerConfig {
	return readerConfig{
		endpoint:   s.txEndpoint,
		topic:      []byte("hashtx"),
		onDialOK:   func() { s.txDialOK.Store(true) },
		onDialFail: func() { s.txDialOK.Store(false) },
		onRecvErr:  func() { s.txDialOK.Store(false) },
		onEvent: func(hash [32]byte, seq uint32) {
			event := TxEvent{Hash: hash, Sequence: seq}
			select {
			case s.txCh <- event:
			default:
				s.recorder.OnMessageDropped("hwm")
			}
		},
	}
}

// ── ZMQ reader loop ───────────────────────────────────────────────────────────

// runReader connects to a single ZMQ SUB socket described by cfg and reads
// until ctx is cancelled, reconnecting with exponential backoff on any
// transient error.
//
// state persists across reconnects so sequence gap detection works correctly
// after re-establishing the connection.
func (s *subscriber) runReader(ctx context.Context, cfg readerConfig, state *readerState) {
	backoff := reconnectBase
	everConnected := false

	for {
		if ctx.Err() != nil {
			return
		}

		sockCtx, sockCancel := context.WithCancel(ctx)
		sock := zmq4.NewSub(sockCtx, zmq4.WithDialerTimeout(5*time.Second))

		if err := sock.Dial(cfg.endpoint); err != nil {
			sock.Close()
			sockCancel()
			cfg.onDialFail()
			logger.Warn(ctx, "zmq: socket dial failed — retrying",
				"topic", string(cfg.topic), "endpoint", cfg.endpoint,
				"backoff", backoff, "error", err)
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}

		// Set the receive HWM before subscribing.
		if err := sock.SetOption(zmq4.OptionHWM, DefaultSubscriberHWM); err != nil {
			sock.Close()
			sockCancel()
			cfg.onDialFail()
			logger.Warn(ctx, "zmq: socket SetOption(HWM) failed — retrying",
				"topic", string(cfg.topic), "backoff", backoff, "error", err)
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}

		if err := sock.SetOption(zmq4.OptionSubscribe, string(cfg.topic)); err != nil {
			sock.Close()
			sockCancel()
			cfg.onDialFail()
			logger.Warn(ctx, "zmq: socket subscribe failed — retrying",
				"topic", string(cfg.topic), "backoff", backoff, "error", err)
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}

		cfg.onDialOK()

		// Fire recovery before delivering the first post-reconnect event so
		// handlers can fill any gap before new events arrive. Skip on the very
		// first connection — no gap is possible before any message is received.
		if everConnected {
			s.fireRecovery(ctx, state.lastSeq)
		}
		everConnected = true
		backoff = reconnectBase // reset after a successful connection

		for {
			msg, err := sock.Recv()
			if err != nil {
				if ctx.Err() != nil {
					sock.Close()
					sockCancel()
					return
				}
				cfg.onRecvErr()
				logger.Warn(ctx, "zmq: socket recv error — reconnecting",
					"topic", string(cfg.topic), "error", err)
				break
			}
			if err := s.processFrame(ctx, msg, cfg.topic, state, cfg.onEvent); err != nil {
				logger.Warn(ctx, "zmq: frame rejected",
					"topic", string(cfg.topic), "error", err)
			}
		}

		sock.Close()
		sockCancel()

		if ctx.Err() != nil {
			return
		}
		if !sleepCtx(ctx, backoff) {
			return
		}
		backoff = nextBackoff(backoff)
	}
}

// ── Frame processing ──────────────────────────────────────────────────────────

// processFrame decodes one raw ZMQ message, validates its frame structure,
// detects sequence gaps, and calls onEvent with the decoded hash and sequence
// number for the caller to dispatch to the appropriate channel.
//
// Messages whose topic frame does not match topic are silently dropped (nil
// returned) — unexpected topics such as "rawtx" on the hashblock socket are
// not errors.
//
// state is per-session and persists across calls. The zero value is correct for
// the first call after a (re)connect: lastSeqSeen=false prevents a false gap
// on the very first message when there is no valid baseline sequence to compare.
func (s *subscriber) processFrame(
	ctx context.Context,
	msg zmq4.Msg,
	topic []byte,
	state *readerState,
	onEvent func(hash [32]byte, seq uint32),
) error {
	if len(msg.Frames) != 3 {
		return telemetry.ZMQ("processFrame.validate",
			fmt.Errorf("expected 3 frames, got %d", len(msg.Frames)))
	}

	// bytes.Equal avoids the string allocation that string(msg.Frames[0]) would
	// cause on every message — important on the tx hot path at ~100 msg/s.
	if !bytes.Equal(msg.Frames[0], topic) {
		return nil
	}

	if len(msg.Frames[1]) != 32 {
		return telemetry.ZMQ("processFrame.validate",
			fmt.Errorf("expected 32-byte hash frame, got %d bytes", len(msg.Frames[1])))
	}
	if len(msg.Frames[2]) != 4 {
		return telemetry.ZMQ("processFrame.validate",
			fmt.Errorf("expected 4-byte sequence frame, got %d bytes", len(msg.Frames[2])))
	}

	seq := binary.LittleEndian.Uint32(msg.Frames[2])

	// uint32 wrap-around (seq = 0 after MaxUint32) is handled correctly:
	// state.lastSeq+1 also wraps to 0, so seq == state.lastSeq+1 and no gap
	// is reported.
	if state.lastSeqSeen && seq != state.lastSeq+1 {
		logger.Warn(ctx, "zmq: sequence gap — frames were dropped at the ZMQ layer",
			"topic", string(topic), "expected", state.lastSeq+1, "got", seq)
		s.recorder.OnMessageDropped("sequence_gap")
		s.fireRecovery(ctx, state.lastSeq)
	}

	state.lastSeq = seq
	state.lastSeqSeen = true

	var hash [32]byte
	copy(hash[:], msg.Frames[1])
	onEvent(hash, seq)

	return nil
}

// ── Recovery ──────────────────────────────────────────────────────────────────

// fireRecovery dispatches a RecoveryEvent to all registered recovery handlers
// synchronously. This is intentional: the ordering guarantee that no
// post-reconnect BlockEvent or TxEvent arrives before recovery handlers have
// run requires synchronous dispatch. Each handler still gets its own timeout
// via invokeHandler.
//
// Note: with N recovery handlers each timing out at handlerTimeout, this method
// can block the reader goroutine for up to N×handlerTimeout in the worst case.
// During this window the ZMQ socket's internal HWM starts accumulating. Design
// recovery handlers to be fast.
func (s *subscriber) fireRecovery(ctx context.Context, lastSeq uint32) {
	if len(s.recoveryHandlers) == 0 {
		return
	}
	event := RecoveryEvent{
		ReconnectedAt:    time.Now(),
		LastSeenSequence: lastSeq,
	}
	for _, h := range s.recoveryHandlers {
		invokeHandler(s, ctx, h, event, "recovery")
	}
}

// ── Handler invocation ────────────────────────────────────────────────────────

// invokeHandler runs h(ctx, e) in a new goroutine with panic recovery and a
// per-call deadline. It blocks its caller (a pool worker) until the handler
// returns or the deadline fires — this is intentional; the worker pool is the
// concurrency budget and the backpressure mechanism.
//
// The goroutine is tracked by s.wg so Shutdown() can drain all in-flight work.
//
// A panicking handler is logged, metered, and recovered — it does not crash the
// process or stall other workers.
//
// A handler that exceeds handlerTimeout has its context cancelled and the
// calling worker is released immediately. The goroutine is still tracked by wg
// and must honour ctx.Done(); a handler that ignores cancellation will hold a
// goroutine slot until it eventually returns.
func invokeHandler[E any](s *subscriber, parentCtx context.Context, h func(context.Context, E), e E, name string) {
	// Detach from parentCtx so that application shutdown (parentCtx cancel)
	// does not immediately kill in-flight handlers — each gets its own bounded
	// window defined by handlerTimeout.
	detached := context.WithoutCancel(parentCtx)
	hCtx, cancel := context.WithTimeout(detached, s.handlerTimeout)
	defer cancel()

	done := make(chan struct{})

	// wg.Go replaces the wg.Add(1)/go/defer wg.Done() pattern (Go 1.25).
	s.wg.Go(func() {
		// Increment inside the goroutine so the counter reflects actual
		// goroutines running, not goroutines scheduled but not yet started.
		n := s.inflightGoroutines.Add(1)
		s.recorder.SetHandlerGoroutines(int(n))
		defer func() {
			remaining := s.inflightGoroutines.Add(-1)
			s.recorder.SetHandlerGoroutines(int(remaining))
		}()

		// Defers are LIFO: panic recovery (innermost) runs before close(done)
		// (outer), ensuring the parent is unblocked only after recovery is
		// complete.
		defer close(done)
		defer func() {
			// recover() MUST live inside the spawned goroutine; a recover() in
			// the calling frame cannot catch panics from a different goroutine.
			if r := recover(); r != nil {
				logger.Error(hCtx, "zmq: handler panic recovered",
					"handler", name, "panic", r)
				s.recorder.OnHandlerPanic(name)
			}
		}()

		h(hCtx, e)
	})

	select {
	case <-done:
		// Handler completed within the deadline — normal path.
	case <-hCtx.Done():
		// Deadline expired. The goroutine is still tracked by s.wg and runs
		// until it observes hCtx.Done(). The calling worker is released
		// immediately to process the next queued event.
		logger.Error(hCtx, "zmq: handler timeout — goroutine still tracked by WaitGroup",
			"handler", name, "timeout", s.handlerTimeout)
		s.recorder.OnHandlerTimeout(name)
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// sleepCtx blocks for d, returning true when the sleep completes and false when
// ctx is cancelled before d elapses. Uses time.NewTimer to avoid the timer leak
// that time.After causes when ctx is cancelled before the duration elapses.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// nextBackoff returns the next backoff duration: doubles current and adds up to
// 50% jitter to prevent thundering-herd reconnects from multiple instances,
// capped at reconnectCeiling.
func nextBackoff(current time.Duration) time.Duration {
	doubled := current * 2
	// rand.Int64N(n) panics if n <= 0; guard with max(1, ...).
	jitterRange := max(int64(current/2), 1)
	jitter := time.Duration(rand.Int64N(jitterRange))
	return min(doubled+jitter, reconnectCeiling)
}

// requireLoopbackTCP panics if endpoint is not a well-formed loopback TCP
// address. IPC endpoints are always rejected — use tcp://127.0.0.1:<port>.
//
// This is a panic, not a returned error, so a misconfigured endpoint fails at
// startup rather than at the first dial attempt. The ZMQ port must never be
// reachable from outside the machine running Bitcoin Core.
func requireLoopbackTCP(endpoint, envName string) {
	if strings.HasPrefix(endpoint, "ipc://") {
		panic(fmt.Sprintf("zmq: %s: ipc:// endpoints are not supported on Windows; use tcp://127.0.0.1:<port>", envName))
	}
	if !strings.HasPrefix(endpoint, "tcp://") {
		panic(fmt.Sprintf("zmq: %s: endpoint must be a loopback TCP address (tcp://127.0.0.1:port), got %q", envName, endpoint))
	}
	host, portStr, err := net.SplitHostPort(strings.TrimPrefix(endpoint, "tcp://"))
	if err != nil {
		panic(fmt.Sprintf("zmq: %s: invalid TCP endpoint %q: %v", envName, endpoint, err))
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		panic(fmt.Sprintf("zmq: %s: invalid port in endpoint %q (must be 1–65535)", envName, endpoint))
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		panic(fmt.Sprintf("zmq: %s: endpoint host must be a loopback address, got %q", envName, endpoint))
	}
}

// Compile-time assertion that *subscriber satisfies Subscriber.
var _ Subscriber = (*subscriber)(nil)
