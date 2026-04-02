package zmq

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

// ── Reconnect loop constants ──────────────────────────────────────────────────

// recoveryHandlerTimeout bounds how long a single recovery handler call may
// block the reader goroutine. With N handlers (typically 1–2), the worst-case
// stall is N × recoveryHandlerTimeout before event delivery resumes.
const recoveryHandlerTimeout = 5 * time.Second

// receiveLoopFn is the function type used by runReconnectLoop to delegate the
// per-connection receive logic. Both receiveLoop (hashblock/hashtx) and
// rawTxReceiveLoop (rawtx) satisfy this type as method values.
type receiveLoopFn func(ctx context.Context, cfg readerConfig, state *readerState, conn *zmtpConn)

// Run blocks until ctx is cancelled, returning ctx.Err() on normal shutdown.
// Run never returns on transient errors — it reconnects with exponential
// backoff (1 s initial, 60 s ceiling, ±50% jitter) and never surfaces
// transient failures to the caller.
//
// Run starts defaultWorkerCount block workers and defaultWorkerCount tx workers
// before entering the reader loops. Workers are drained by Shutdown() after
// ctx is cancelled.
//
// Run panics if called more than once.
//
// Launch in a goroutine and cancel the context to initiate shutdown:
//
//	ctx, cancel := context.WithCancel(context.Background())
//	go func() {
//	    if err := sub.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
//	        slog.Error("zmq: subscriber exit", "error", err)
//	        appCancelFn()
//	    }
//	}()
//	defer sub.Shutdown()
func (s *subscriber) Run(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		panic("zmq: Run: must not be called more than once")
	}

	// Create a cancel context so reader panics can signal critical failures.
	// This context is used only for panic recovery — normal shutdown still
	// uses the caller's ctx.
	ctx, cancel := context.WithCancelCause(ctx)
	s.cancelCauseFn = cancel
	defer cancel(nil)

	logger.Debug(ctx, "zmq: starting worker pool",
		"block_workers", defaultWorkerCount,
		"rawtx_workers", defaultWorkerCount,
		"settle_workers", defaultWorkerCount)
	s.startWorkers(ctx)

	logger.Debug(ctx, "zmq: launching reader goroutines",
		"block_endpoint", s.blockEndpoint, "tx_endpoint", s.txEndpoint)
	var readersWg sync.WaitGroup
	readersWg.Go(func() {
		defer func() {
			if r := recover(); r != nil {
				// T1: add "error" key so TelemetryHandler increments app_errors_total.
				panicErr := telemetry.ZMQ("Run.block_reader_panic",
					fmt.Errorf("reader goroutine panicked: %v", r))
				logger.Error(ctx, "zmq: block reader goroutine panicked -- subscriber will not recover without restart",
					"error", panicErr, "panic", r)
				// Signal critical failure: cancel the context with the panic error.
				s.cancelCauseFn(panicErr)
			}
		}()
		var state readerState
		s.runReader(ctx, s.blockReaderConfig(), &state)
	})
	// Settlement path: hashtx → TxEvent → txCh
	readersWg.Go(func() {
		defer func() {
			if r := recover(); r != nil {
				panicErr := telemetry.ZMQ("Run.settlement_reader_panic",
					fmt.Errorf("reader goroutine panicked: %v", r))
				logger.Error(ctx, "zmq: settlement-tx reader goroutine panicked -- subscriber will not recover without restart",
					"error", panicErr, "panic", r)
				// Signal critical failure: cancel the context with the panic error.
				s.cancelCauseFn(panicErr)
			}
		}()
		var state readerState
		s.runReader(ctx, s.txReaderConfig(), &state)
	})
	// SSE display path: rawtx → RawTxEvent → rawTxCh
	readersWg.Go(func() {
		defer func() {
			if r := recover(); r != nil {
				panicErr := telemetry.ZMQ("Run.rawtx_reader_panic",
					fmt.Errorf("reader goroutine panicked: %v", r))
				logger.Error(ctx, "zmq: rawtx reader goroutine panicked -- subscriber will not recover without restart",
					"error", panicErr, "panic", r)
				// Signal critical failure: cancel the context with the panic error.
				s.cancelCauseFn(panicErr)
			}
		}()
		s.runRawTxReader(ctx)
	})
	readersWg.Wait()

	// Check if we exited due to a reader panic (context cancelled with a cause).
	// If so, return the panic error instead of the original ctx error.
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return ctx.Err()
}

// Shutdown drains all in-flight handler goroutines, then returns. It blocks
// until every goroutine calls wg.Done() or shutdownDrainTimeout elapses,
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
	bg := context.Background()

	drained := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(drained)
	}()

	t := time.NewTimer(s.shutdownDrainTimeout)
	defer t.Stop()

	select {
	case <-drained:
		logger.Info(bg, "zmq: subscriber drained — all handler goroutines finished")
	case <-t.C:
		// T4: add "error" key so TelemetryHandler increments app_errors_total.
		drainErr := telemetry.ZMQ("Shutdown.drain_timeout",
			fmt.Errorf("drain timeout after %v: some goroutines did not exit", s.shutdownDrainTimeout))
		logger.Error(bg, "zmq: subscriber shutdown timed out — some goroutines may still be running",
			"error", drainErr, "timeout", s.shutdownDrainTimeout)
	}
}

// IsReady reports whether all required ZMQ subscriptions are currently dialled.
// It does NOT issue any network call and intentionally ignores the age of the
// last seen block so callers can treat quiet-chain periods as transport-ready.
func (s *subscriber) IsReady() bool {
	if !s.blockDialOK.Load() || !s.hashtxDialOK.Load() || !s.rawtxDialOK.Load() {
		return false
	}
	return true
}

// IsConnected reports whether the subscriber appears healthy based on local
// liveness signals. It does NOT issue any network call.
//
// Returns false when any required subscription is not dialled, or when a block
// was received but more than idleTimeout ago. Returns true on fresh startup
// (all subscriptions established, no block received yet) — this prevents
// spurious "disconnected" alerts immediately after deployment.
func (s *subscriber) IsConnected() bool {
	if !s.IsReady() {
		return false
	}
	p := s.live.Load()
	if p == nil {
		// All subscriptions up but no block received yet — treat as connected.
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

// LastHashTime returns the Unix nanosecond timestamp of the most recently
// received block. Returns 0 before the first block is received, consistent
// with the H-04 invariant.
//
// Thread-safe: reads the same atomic.Pointer[liveness] as LastSeenHash().
// Both methods always observe a consistent snapshot — hash and timestamp are
// stored together in a single atomic Store in blockReaderConfig.onEvent,
// so the value returned here is always the timestamp for the hash returned
// by LastSeenHash() at the moment of the same atomic load.
func (s *subscriber) LastHashTime() int64 {
	p := s.live.Load()
	if p == nil {
		return 0
	}
	return p.at.UnixNano()
}

// ── Worker pool ───────────────────────────────────────────────────────────────

// startWorkers launches defaultWorkerCount block workers and defaultWorkerCount
// tx workers. All workers are tracked by s.wg so Shutdown() can drain them.
//
// NOTE: Events buffered in the channels (blockCh, rawTxCh, txCh) at the moment
// ctx is cancelled may be dropped. Each worker's select statement is non-deterministic,
// so if both ctx.Done() and the channel have data, either case may be chosen.
// This is acceptable for a streaming event system where reconnection and recovery
// handle redelivery guarantees. Domain callers must not rely on all buffered
// events being processed — only on the strong ordering of delivered events.
func (s *subscriber) startWorkers(ctx context.Context) {
	// Block workers: one goroutine per slot. Each goroutine processes one
	// BlockEvent at a time, calling all registered block handlers sequentially.
	for range defaultWorkerCount {
		s.wg.Go(func() {
			for {
				select {
				case e := <-s.blockCh:
					for _, h := range s.blockHandlers {
						invokeHandler(ctx, s, h, e, "block")
					}
				case <-ctx.Done():
					return
				}
			}
		})
	}

	// RawTx workers: dispatch decoded RawTxEvent to SSE display handlers.
	// These run independently of settlement workers (ADR-BTC-01) — a slow or
	// panicking settlement handler cannot stall SSE display delivery.
	for range defaultWorkerCount {
		s.wg.Go(func() {
			for {
				select {
				case e := <-s.rawTxCh:
					for _, h := range s.rawTxHandlers {
						invokeHandler(ctx, s, h, e, "display_rawtx")
					}
				case <-ctx.Done():
					return
				}
			}
		})
	}

	// Settlement Tx workers: dispatch TxEvent (hashtx topic) to settlement handlers.
	// The settlement path uses hashtx + GetTransaction (wallet RPC) which works
	// on pruned nodes via the wallet index — no txindex required.
	for range defaultWorkerCount {
		s.wg.Go(func() {
			for {
				select {
				case e := <-s.txCh:
					for _, h := range s.settleTxHandlers {
						invokeHandler(ctx, s, h, e, "settlement_tx")
					}
				case <-ctx.Done():
					return
				}
			}
		})
	}
}

// ── Reader configs ────────────────────────────────────────────────────────────

// blockReaderConfig returns the readerConfig for the hashblock endpoint.
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
		onEvent: func(ctx context.Context, hash [32]byte, seq uint32) {
			event := BlockEvent{Hash: hash, Sequence: seq}
			// Single atomic Store: IsConnected() and LastSeenHash() always read
			// a consistent snapshot — hash and timestamp are never torn.
			s.live.Store(&liveness{hash: event.HashHex(), at: time.Now()})
			s.recorder.SetChannelDepth("block", len(s.blockCh), cap(s.blockCh))
			logger.Debug(ctx, "zmq: block event dispatched",
				"hash", event.HashHex(), "seq", event.Sequence)
			select {
			case s.blockCh <- event:
				s.recorder.SetChannelDepth("block", len(s.blockCh), cap(s.blockCh))
			default:
				// Buffer full — drop and meter. The read loop must never block
				// or it stalls delivery for the entire block socket.
				// Note: a dropped message still advances the sequence counter on the
				// publisher side. The sequence-gap detector in processFrame will fire
				// a recovery event for any gap caused by an HWM drop.
				logger.Warn(ctx, "zmq: blockCh full -- dropping block event (HWM)",
					"hash", event.HashHex(), "channel_cap", cap(s.blockCh))
				s.recorder.OnMessageDropped("hwm")
				s.recorder.SetChannelDepth("block", len(s.blockCh), cap(s.blockCh))
			}
		},
	}
}

// txReaderConfig returns the readerConfig for the tx endpoint.
//
// A separate ZMTP connection is established for the rawtx topic; see rawTxReaderConfig.
//
// The tx reader does not drive the SetZMQConnected gauge — the block stream
// is the authoritative liveness signal.
//
// NOTE: The readerConfig.onEvent callback is used for the settlement hashtx path
// only. The rawtx path uses a separate rawTxReceiveLoop started in Run().
func (s *subscriber) txReaderConfig() readerConfig {
	return readerConfig{
		endpoint:   s.txEndpoint,
		topic:      []byte("hashtx"),
		onDialOK:   func() { s.hashtxDialOK.Store(true) },
		onDialFail: func() { s.hashtxDialOK.Store(false) },
		onRecvErr:  func() { s.hashtxDialOK.Store(false) },
		onEvent: func(ctx context.Context, hash [32]byte, seq uint32) {
			event := TxEvent{Hash: hash, Sequence: seq}
			s.recorder.SetChannelDepth("tx", len(s.txCh), cap(s.txCh))
			select {
			case s.txCh <- event:
				s.recorder.SetChannelDepth("tx", len(s.txCh), cap(s.txCh))
			default:
				s.recorder.OnMessageDropped("hwm")
				s.recorder.SetChannelDepth("tx", len(s.txCh), cap(s.txCh))
			}
		},
	}
}

// rawTxReaderConfig returns the readerConfig for the rawtx topic on the tx endpoint.
// This reader shares the same TCP endpoint as the hashtx reader but subscribes
// to the "rawtx" topic, which delivers full serialized transaction bytes.
//
// The onEvent callback is not used for rawtx (the hash-only signature doesn't
// carry the raw bytes), so this config uses a custom processRawTxFrame path
// invoked from rawTxReceiveLoop.
func (s *subscriber) rawTxReaderConfig() readerConfig {
	return readerConfig{
		endpoint:   s.txEndpoint,
		topic:      []byte("rawtx"),
		onDialOK:   func() { s.rawtxDialOK.Store(true) },
		onDialFail: func() { s.rawtxDialOK.Store(false) },
		onRecvErr:  func() { s.rawtxDialOK.Store(false) },
		// onEvent is not used — rawTxReceiveLoop calls processRawTxFrame directly.
		onEvent: func(context.Context, [32]byte, uint32) {},
	}
}

// ── Unified reconnect loop (H5) ───────────────────────────────────────────────

// runReconnectLoop is the shared reconnect-and-dispatch backbone used by both
// runReader and runRawTxReader. It eliminates the ~55-line duplication that
// previously existed between them.
//
// Connection lifecycle per iteration:
//  1. dialZMTP: TCP + ZMTP 3.1 NULL handshake + SUBSCRIBE — returns ready conn.
//  2. Fire recovery before first post-reconnect event (if ever connected before).
//  3. loop(ctx, cfg, state, conn): receive loop for one connection session.
//  4. On ctx cancellation: close conn and return.
//  5. On transient error: close conn, log, backoff, reconnect.
//
// State persists across reconnects so sequence-gap detection works correctly
// after re-establishing the connection.
func (s *subscriber) runReconnectLoop(
	ctx context.Context,
	cfg readerConfig,
	state *readerState,
	loop receiveLoopFn,
) {
	backoff := reconnectBase
	everConnected := false
	attempt := 0

	for {
		if ctx.Err() != nil {
			logger.Debug(ctx, "zmq: runReconnectLoop: context cancelled, exiting",
				"topic", string(cfg.topic))
			return
		}

		attempt++
		logger.Debug(ctx, "zmq: runReconnectLoop: dial attempt",
			"topic", string(cfg.topic), "endpoint", cfg.endpoint, "attempt", attempt, "backoff", backoff)

		conn, err := dialZMTP(ctx, cfg.endpoint, cfg.topic)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			cfg.onDialFail()
			// T5: wrap with telemetry.ZMQ at the logging call site.
			logger.Warn(ctx, "zmq: connection failed -- retrying",
				"topic", string(cfg.topic), "endpoint", cfg.endpoint,
				"backoff", backoff, "attempt", attempt,
				"error", telemetry.ZMQ("runReconnectLoop.dial", err))
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}

		cfg.onDialOK()
		logger.Debug(ctx, "zmq: runReconnectLoop: connected",
			"topic", string(cfg.topic), "endpoint", cfg.endpoint, "attempt", attempt)

		// Fire recovery before delivering the first post-reconnect event so
		// handlers can fill any gap before new events arrive. Skip on the very
		// first connection — no gap is possible before any message is received.
		if everConnected {
			logger.Debug(ctx, "zmq: runReconnectLoop: firing recovery after reconnect",
				"topic", string(cfg.topic), "last_seq", state.lastSeq)
			s.fireRecovery(ctx, string(cfg.topic), state.lastSeq)
		}
		everConnected = true
		backoff = reconnectBase // reset after a successful connection

		loop(ctx, cfg, state, conn)

		if ctx.Err() != nil {
			return
		}
		logger.Debug(ctx, "zmq: runReconnectLoop: session ended, will reconnect",
			"topic", string(cfg.topic), "next_backoff", backoff)
		if !sleepCtx(ctx, backoff) {
			return
		}
		backoff = nextBackoff(backoff)
	}
}

// runReader connects to the endpoint described by cfg and reads until ctx is
// cancelled, reconnecting with exponential backoff on any transient error.
// State persists across reconnects so sequence gap detection works correctly.
func (s *subscriber) runReader(ctx context.Context, cfg readerConfig, state *readerState) {
	s.runReconnectLoop(ctx, cfg, state, s.receiveLoop)
}

// receiveLoop runs the blocking receive loop for one established connection,
// closing conn on return regardless of exit reason. It is a helper for
// runReconnectLoop; callers must not reuse conn after receiveLoop returns.
func (s *subscriber) receiveLoop(ctx context.Context, cfg readerConfig, state *readerState, conn *zmtpConn) {
	defer func() {
		if err := conn.Close(); err != nil {
			// Only log as Warn if it's not a normal close (net.ErrClosed is expected
			// when ctx cancels the connection). Other errors indicate a problem.
			if err != net.ErrClosed {
				logger.Warn(ctx, "zmq: receiveLoop close failed unexpectedly", "topic", string(cfg.topic), "error", err)
			} else {
				logger.Debug(ctx, "zmq: receiveLoop closed normally", "topic", string(cfg.topic))
			}
		}
	}()
	for {
		frames, err := conn.RecvMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				logger.Debug(ctx, "zmq: receiveLoop: context cancelled",
					"topic", string(cfg.topic))
				return
			}
			cfg.onRecvErr()
			// T5: wrap with telemetry.ZMQ at the logging call site.
			logger.Warn(ctx, "zmq: receive error -- reconnecting",
				"topic", string(cfg.topic),
				"error", telemetry.ZMQ("receiveLoop.recv", err))
			return
		}
		logger.Debug(ctx, "zmq: receiveLoop: got message",
			"topic", string(cfg.topic), "frame_count", len(frames))
		if err := s.processFrame(ctx, frames, cfg.topic, state, cfg.onEvent); err != nil {
			logger.Warn(ctx, "zmq: frame rejected",
				"topic", string(cfg.topic), "error", err)
		}
	}
}

// ── RawTx reader ────────────────────────────────────────────────────────────

// runRawTxReader connects to the rawtx topic with exponential backoff and reads
// until ctx is cancelled. Uses the shared runReconnectLoop.
//
// Note: state is intentionally local (owned by this function) rather than
// passed as a parameter. This allows state to persist across reconnects via
// the pointer passed to runReconnectLoop, while keeping the state local to
// avoid accidentally sharing state with runReader or other readers. This is
// the "correct" pattern for the reconnect loop architecture.
func (s *subscriber) runRawTxReader(ctx context.Context) {
	cfg := s.rawTxReaderConfig()
	var state readerState
	s.runReconnectLoop(ctx, cfg, &state, s.rawTxReceiveLoop)
}

// rawTxReceiveLoop runs the receive loop for one established rawtx connection,
// closing conn on return regardless of exit reason.
func (s *subscriber) rawTxReceiveLoop(ctx context.Context, cfg readerConfig, state *readerState, conn *zmtpConn) {
	defer func() {
		if err := conn.Close(); err != nil {
			if err != net.ErrClosed {
				logger.Warn(ctx, "zmq: rawTxReceiveLoop close failed unexpectedly", "endpoint", cfg.endpoint, "error", err)
			} else {
				logger.Debug(ctx, "zmq: rawTxReceiveLoop closed normally", "endpoint", cfg.endpoint)
			}
		}
	}()
	for {
		frames, err := conn.RecvMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				logger.Debug(ctx, "zmq: rawTxReceiveLoop: context cancelled")
				return
			}
			cfg.onRecvErr()
			// T5: wrap with telemetry.ZMQ at the logging call site.
			logger.Warn(ctx, "zmq: rawtx receive error -- reconnecting",
				"endpoint", cfg.endpoint,
				"error", telemetry.ZMQ("rawTxReceiveLoop.recv", err))
			return
		}
		logger.Debug(ctx, "zmq: rawTxReceiveLoop: got message", "frame_count", len(frames))
		if err := s.processRawTxFrame(ctx, frames, state); err != nil {
			logger.Warn(ctx, "zmq: rawtx frame rejected", "error", err)
		}
	}
}

// processRawTxFrame decodes one rawtx multipart ZMQ message.
//
// Frame layout: [topic="rawtx"][raw_tx_bytes][4-byte_sequence_LE]
//
// The sequence number drives gap detection (identical logic to processFrame).
// ParseRawTx decodes the raw bytes into a RawTxEvent using the subscriber's
// configured network HRP (set from the network parameter in New()). A parse
// failure is escalated to Error level — it indicates a protocol violation or
// data corruption, not a transient network condition.
func (s *subscriber) processRawTxFrame(ctx context.Context, msg [][]byte, state *readerState) error {
	if len(msg) != 3 {
		return fmt.Errorf("zmq.processRawTxFrame: expected 3 frames, got %d", len(msg))
	}
	if !bytes.Equal(msg[0], []byte("rawtx")) {
		return nil // wrong topic — ignore silently
	}
	if len(msg[2]) != 4 {
		return fmt.Errorf("zmq.processRawTxFrame: expected 4-byte sequence frame, got %d bytes", len(msg[2]))
	}

	seq := binary.LittleEndian.Uint32(msg[2])

	// Sequence gap detection — same logic as processFrame.
	if state.lastSeqSeen && seq != state.lastSeq+1 {
		logger.Warn(ctx, "zmq: rawtx sequence gap -- frames were dropped at the ZMQ layer",
			"expected", state.lastSeq+1, "got", seq, "dropped", seq-state.lastSeq-1)
		s.recorder.OnMessageDropped("sequence_gap")
		s.fireRecovery(ctx, "rawtx", state.lastSeq)
	} else if state.lastSeqSeen {
		logger.Debug(ctx, "zmq: rawtx processFrame: seq OK", "seq", seq)
	}
	state.lastSeq = seq
	state.lastSeqSeen = true

	event, err := ParseRawTx(msg[1], s.hrp)
	if err != nil {
		// T3: parse failure is NOT a transient network event — it indicates a
		// Bitcoin Core protocol change, encoding error, or data corruption.
		// Escalate to Error so it increments app_errors_total and is visible
		// in Prometheus dashboards.
		logger.Error(ctx, "zmq: rawtx parse failed -- dropping frame",
			"error", telemetry.ZMQ("processRawTxFrame.parse", err),
			"seq", seq, "raw_len", len(msg[1]))
		s.recorder.OnMessageDropped("parse_error")
		return nil
	}
	event.Sequence = seq

	logger.Debug(ctx, "zmq: rawtx frame decoded",
		"txid", event.TxIDHex(), "seq", seq,
		"inputs", len(event.Inputs), "outputs", len(event.Outputs),
		"raw_len", len(msg[1]))

	s.recorder.SetChannelDepth("rawtx", len(s.rawTxCh), cap(s.rawTxCh))
	select {
	case s.rawTxCh <- event:
		s.recorder.SetChannelDepth("rawtx", len(s.rawTxCh), cap(s.rawTxCh))
		logger.Debug(ctx, "zmq: rawtx event dispatched to rawTxCh", "txid", event.TxIDHex())
	default:
		logger.Warn(ctx, "zmq: rawTxCh full -- dropping rawtx event (HWM)",
			"txid", event.TxIDHex(), "channel_cap", cap(s.rawTxCh))
		s.recorder.OnMessageDropped("hwm")
		s.recorder.SetChannelDepth("rawtx", len(s.rawTxCh), cap(s.rawTxCh))
	}
	return nil
}

// ── Frame processing ──────────────────────────────────────────────────────────

// processFrame decodes one raw multipart message, validates its frame structure,
// detects sequence gaps, and calls onEvent with the decoded hash and sequence
// number for the caller to dispatch to the appropriate channel.
//
// Messages whose topic frame does not match topic are silently dropped (nil
// returned) — unexpected topics on the wrong socket are not errors.
//
// State is per-session and persists across calls. The zero value is correct for
// the first call after a (re)connect: lastSeqSeen=false prevents a false gap
// on the very first message when there is no valid baseline sequence to compare.
func (s *subscriber) processFrame(
	ctx context.Context,
	msg [][]byte,
	topic []byte,
	state *readerState,
	onEvent func(context.Context, [32]byte, uint32),
) error {
	if len(msg) != 3 {
		return telemetry.ZMQ("processFrame.validate",
			fmt.Errorf("expected 3 frames, got %d", len(msg)))
	}

	// bytes.Equal avoids the string allocation that string(msg[0]) would cause
	// on every message — important on the tx hot path at ~100 msg/s.
	if !bytes.Equal(msg[0], topic) {
		return nil
	}

	if len(msg[1]) != 32 {
		return telemetry.ZMQ("processFrame.validate",
			fmt.Errorf("expected 32-byte hash frame, got %d bytes", len(msg[1])))
	}
	if len(msg[2]) != 4 {
		return telemetry.ZMQ("processFrame.validate",
			fmt.Errorf("expected 4-byte sequence frame, got %d bytes", len(msg[2])))
	}

	seq := binary.LittleEndian.Uint32(msg[2])

	// uint32 wrap-around (seq = 0 after MaxUint32) is handled correctly:
	// state.lastSeq+1 also wraps to 0, so seq == state.lastSeq+1 and no gap
	// is reported.
	if state.lastSeqSeen && seq != state.lastSeq+1 {
		logger.Warn(ctx, "zmq: sequence gap -- frames were dropped at the ZMQ layer",
			"topic", string(topic), "expected", state.lastSeq+1, "got", seq, "dropped", seq-state.lastSeq-1)
		s.recorder.OnMessageDropped("sequence_gap")
		s.fireRecovery(ctx, string(topic), state.lastSeq)
	} else if state.lastSeqSeen {
		logger.Debug(ctx, "zmq: processFrame: seq OK",
			"topic", string(topic), "seq", seq)
	}

	state.lastSeq = seq
	state.lastSeqSeen = true

	var hash [32]byte
	copy(hash[:], msg[1])

	logger.Debug(ctx, "zmq: processFrame: dispatching event",
		"topic", string(topic), "seq", seq)
	onEvent(ctx, hash, seq)

	return nil
}

// ── Recovery ──────────────────────────────────────────────────────────────────

// fireRecovery delivers a snapshot of event to all registered handlers.
// event is passed by value so handlers cannot mutate shared state.
// Each handler is bounded by recoveryHandlerTimeout (H7) so a slow or hung
// handler cannot stall the reader goroutine indefinitely.
//
// Handlers are dispatched in parallel goroutines (tracked by s.wg) rather than
// running synchronously. This prevents a slow or blocking handler from stalling
// the reader while others complete. The function blocks the caller until ALL
// handler goroutines have completed (either normally or via timeout), ensuring
// that recovery is fully processed before event delivery resumes.
//
// With N recovery handlers each bounded at recoveryHandlerTimeout, the
// worst-case stall is N × recoveryHandlerTimeout (typically 1-2 handlers × 5 s).
func (s *subscriber) fireRecovery(ctx context.Context, topic string, lastSeq uint32) {
	if len(s.recoveryHandlers) == 0 {
		return
	}
	logger.Debug(ctx, "zmq: fireRecovery",
		"topic", topic, "last_seq", lastSeq, "handler_count", len(s.recoveryHandlers))
	event := RecoveryEvent{
		ReconnectedAt:    time.Now(),
		Topic:            topic,
		LastSeenSequence: lastSeq,
	}

	// Use a local WaitGroup to wait for all recovery handlers to complete.
	// This differs from invokeHandler (which blocks a worker), but recovery
	// is not on the hot path and must complete before event delivery resumes.
	var recWg sync.WaitGroup
	for _, h := range s.recoveryHandlers {
		// Capture h in a local variable to avoid the loop variable closure bug.
		handler := h
		recWg.Add(1)
		// Dispatch each handler in its own goroutine (tracked by s.wg) so they
		// run in parallel and don't block each other, but we wait below for all
		// to complete before returning.
		s.wg.Go(func() {
			defer recWg.Done()
			// Detach from the parent ctx (which may be cancelled at this instant),
			// then apply the recovery timeout. This ensures handlers get
			// recoveryHandlerTimeout seconds to complete their work, even if
			// the application is shutting down.
			detached := context.WithoutCancel(ctx)
			rCtx, cancel := context.WithTimeout(detached, recoveryHandlerTimeout)
			defer cancel()
			defer func() {
				if r := recover(); r != nil {
					logger.Error(rCtx, "zmq: recovery handler panic",
						"error", telemetry.ZMQ("fireRecovery.panic",
							fmt.Errorf("recovery handler panicked: %v", r)),
						"panic", r)
				}
			}()
			handler(rCtx, event)
		})
	}
	// Block until all handlers complete or timeout.
	recWg.Wait()
	logger.Debug(ctx, "zmq: fireRecovery complete")
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
func invokeHandler[E any](parentCtx context.Context, s *subscriber, h func(context.Context, E), e E, name string) {
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

		// Defers run LIFO: panic recovery (innermost, registered last) runs first,
		// then inflight decrement (registered second), then close(done) (outermost,
		// registered first) runs last. This ensures the inflight counter is decremented
		// BEFORE close(done) unblocks the caller — no race between the caller reading
		// inflightGoroutines and this goroutine's final decrement.
		defer close(done)
		defer func() {
			remaining := s.inflightGoroutines.Add(-1)
			s.recorder.SetHandlerGoroutines(int(remaining))
		}()
		defer func() {
			// recover() MUST live inside the spawned goroutine; a recover() in
			// the calling frame cannot catch panics from a different goroutine.
			if r := recover(); r != nil {
				// T2: add "error" key so TelemetryHandler increments app_errors_total.
				panicErr := telemetry.ZMQ("invokeHandler.panic",
					fmt.Errorf("handler %q panicked: %v", name, r))
				logger.Error(hCtx, "zmq: handler panic recovered",
					"error", panicErr, "handler", name, "panic", r)
				s.recorder.OnHandlerPanic(name) // domain-specific counter
			}
		}()

		logger.Debug(hCtx, "zmq: invokeHandler: starting", "handler", name)
		h(hCtx, e)
		logger.Debug(hCtx, "zmq: invokeHandler: done", "handler", name)
	})

	select {
	case <-done:
		// Handler completed within the deadline — normal path.
	case <-hCtx.Done():
		// Deadline expired. The goroutine is still tracked by s.wg and runs
		// until it observes hCtx.Done(). The calling worker is released
		// immediately to process the next queued event.
		// T2: add "error" key so TelemetryHandler increments app_errors_total.
		timeoutErr := telemetry.ZMQ("invokeHandler.timeout",
			fmt.Errorf("handler %q timed out after %v", name, s.handlerTimeout))
		logger.Error(hCtx, "zmq: handler timeout — goroutine still tracked by WaitGroup",
			"error", timeoutErr, "handler", name, "timeout", s.handlerTimeout)
		s.recorder.OnHandlerTimeout(name) // domain-specific counter
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
	//nolint:gosec // Exponential-backoff jitter is non-cryptographic.
	jitter := time.Duration(rand.Int64N(jitterRange))
	return min(doubled+jitter, reconnectCeiling)
}

// requireLoopbackTCP panics if endpoint is not a well-formed loopback TCP
// address. IPC endpoints are always rejected — use tcp://127.0.0.1:<port>.
//
// This is a panic, not a returned error, so a misconfigured endpoint fails at
// startup rather than at the first connection attempt. The ZMQ port must never
// be reachable from outside the machine running Bitcoin Core.
func requireLoopbackTCP(endpoint, envName string) {
	if strings.HasPrefix(endpoint, "ipc://") {
		panic(fmt.Sprintf("zmq: %s: ipc:// endpoints are not supported; use tcp://127.0.0.1:<port>", envName))
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

// compile-time assertion that *subscriber satisfies Subscriber.
var _ Subscriber = (*subscriber)(nil)

// ── Network HRP configuration ─────────────────────────────────────────────────

// networkToHRP converts a Bitcoin network name to its bech32 human-readable part.
// The network must be one of: "mainnet", "testnet", "testnet4", "signet", "regtest".
// Any other value is an error — unknown networks are not silently defaulted.
//
// Mapping:
//   - "mainnet"    → "bc" (mainnet)
//   - "testnet", "testnet4", "signet"  → "tb" (all testnet variants)
//   - "regtest"    → "bcrt" (regtest)
func networkToHRP(network string) (string, error) {
	switch network {
	case "mainnet":
		return "bc", nil
	case "testnet", "testnet4", "signet":
		return "tb", nil
	case "regtest":
		return "bcrt", nil
	default:
		return "", fmt.Errorf("zmq: unknown network %q (must be mainnet, testnet, testnet4, signet, or regtest)", network)
	}
}

// ── ParseRawTx ────────────────────────────────────────────────────────────────

// ParseRawTx decodes a Bitcoin transaction from its wire-format byte slice and
// returns a RawTxEvent with the txid, inputs, and outputs populated.
//
// hrp is the bech32 human-readable part for the target network ("bc" mainnet,
// "tb" testnet4/signet, "bcrt" regtest). It is used for address extraction in
// P2WPKH, P2WSH, and P2TR outputs. Passing the correct hrp ensures that decoded
// addresses match what bitcoinshared.ValidateAndNormalise expects.
//
// The txid is computed per BIP 141:
//   - For legacy transactions: SHA256d of the full raw bytes.
//   - For SegWit transactions: SHA256d of the non-witness serialization
//     (version + inputs + outputs + locktime, excluding the 0x00 0x01 marker/flag
//     and all witness stacks). This matches RPC getrawtransaction and block
//     explorers. The full-bytes hash (including witness) is the wtxid.
//
// Only the fields needed by the SSE display path are decoded:
//   - Input prevouts (txid + vout) — for O(1) RBF detection via spentOutpoints
//   - Output values (satoshis) and addresses — for watch-address matching
//
// Script and witness data is read but not decoded beyond address extraction.
// This function supports both legacy and SegWit (BIP 141) transactions.
//
// Returns a non-nil error if the byte slice is truncated or structurally invalid.
// Never panics on malformed input — all reads use io.ReadFull with explicit
// bounds checks.
func ParseRawTx(raw []byte, hrp string) (RawTxEvent, error) {
	if len(raw) < 10 {
		return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: too short (%d bytes)", len(raw))
	}

	r := newPushBackReader(raw)

	// Version: 4 bytes LE (value not validated — any version is accepted)
	if _, err := readUint32LE(r); err != nil {
		return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: version: %w", err)
	}

	// SegWit detection: peek at the next two bytes.
	// BIP 141: if marker=0x00 and flag=0x01 → SegWit format.
	// Otherwise → legacy format; push both bytes back.
	isSegWit := false
	// L3: use stack allocation instead of heap to avoid garbage per call.
	var peek [2]byte
	if n, err := io.ReadFull(r, peek[:]); err != nil || n < 2 {
		return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: peek marker/flag: %w", err)
	}
	if peek[0] == 0x00 && peek[1] == 0x01 {
		isSegWit = true
	} else {
		r.pushBack(peek[0], peek[1])
	}

	// Input count
	inputCount, err := readVarInt(r)
	if err != nil {
		return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: input count: %w", err)
	}
	if inputCount > 100_000 {
		return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: implausible input count %d", inputCount)
	}

	inputs := make([]RawTxInput, 0, inputCount)
	for i := range inputCount {
		input, parseErr := parseTxInput(r)
		if parseErr != nil {
			return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: input[%d]: %w", i, parseErr)
		}
		inputs = append(inputs, input)
	}

	// outputCount is read after inputCount; both use the same varint encoding.
	// An excessively large count is bounded by the enclosing zmtpMaxFrameBody
	// cap on the raw transaction frame.
	outputCount, err := readVarInt(r)
	if err != nil {
		return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: output count: %w", err)
	}
	if outputCount > 100_000 {
		return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: implausible output count %d", outputCount)
	}

	outputs := make([]RawTxOutput, 0, outputCount)
	for i := range outputCount {
		out, err := parseTxOutput(r, uint32(i), hrp)
		if err != nil {
			return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: output[%d]: %w", i, err)
		}
		outputs = append(outputs, out)
	}

	// Witness data: one stack per input for SegWit transactions. Skip entirely.
	if isSegWit {
		for i := range inputCount {
			stackCount, err := readVarInt(r)
			if err != nil {
				return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: witness[%d] stack count: %w", i, err)
			}
			for j := range stackCount {
				itemLen, err := readVarInt(r)
				if err != nil {
					return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: witness[%d][%d] len: %w", i, j, err)
				}
				if err := skipN(r, itemLen); err != nil {
					return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: witness[%d][%d] data: %w", i, j, err)
				}
			}
		}
	}

	// Locktime: 4 bytes LE — skip
	if _, err := readUint32LE(r); err != nil {
		return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: locktime: %w", err)
	}

	// Compute txid: for legacy transactions, hash the full raw bytes.
	// For SegWit transactions, hash only the non-witness serialization
	// (version + inputs + outputs + locktime), excluding the 0x00 0x01 marker/flag
	// and all witness stacks. This matches BIP 141 and RPC behavior.
	var txid [32]byte
	if isSegWit {
		var err error
		txid, err = txidSegWit(raw)
		if err != nil {
			return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: txid (segwit): %w", err)
		}
	} else {
		txid = doubleSHA256(raw)
	}

	return RawTxEvent{
		TxIDBytes: txid,
		Inputs:    inputs,
		Outputs:   outputs,
	}, nil
}

// txidSegWit computes the txid of a SegWit transaction (BIP 141) as SHA256d of
// the non-witness serialization: version + inputs + outputs + locktime, excluding
// the 0x00 0x01 marker/flag and all witness stacks.
//
// The input raw must be a valid SegWit transaction (marker=0x00, flag=0x01).
// This is guaranteed by the caller (ParseRawTx checks isSegWit before calling).
func txidSegWit(raw []byte) ([32]byte, error) {
	// Non-witness serialization: version (4) + inputs + outputs + locktime (4)
	// We reconstruct by reading from the original wire format and skipping witness data.
	r := newPushBackReader(raw)

	// Buffer for the non-witness bytes. Start with a reasonable capacity
	// and let it grow as needed — most transactions are < 1 KB.
	buf := bytes.NewBuffer(make([]byte, 0, len(raw)/2))

	// Version: 4 bytes LE
	var ver [4]byte
	if _, err := io.ReadFull(r, ver[:]); err != nil {
		return [32]byte{}, fmt.Errorf("read version: %w", err)
	}
	buf.Write(ver[:])

	// Skip marker (0x00) and flag (0x01)
	var skip [2]byte
	if _, err := io.ReadFull(r, skip[:]); err != nil {
		return [32]byte{}, fmt.Errorf("read marker/flag: %w", err)
	}

	// Input count and inputs
	inputCount, err := readVarInt(r)
	if err != nil {
		return [32]byte{}, fmt.Errorf("read input count: %w", err)
	}
	writeVarInt(buf, inputCount)

	for i := range inputCount {
		// Prevout: 32 bytes (txid) + 4 bytes (vout)
		var prevout [36]byte
		if _, err := io.ReadFull(r, prevout[:]); err != nil {
			return [32]byte{}, fmt.Errorf("read input[%d] prevout: %w", i, err)
		}
		buf.Write(prevout[:])

		// scriptSig
		scriptLen, err := readVarInt(r)
		if err != nil {
			return [32]byte{}, fmt.Errorf("read input[%d] scriptSig length: %w", i, err)
		}
		writeVarInt(buf, scriptLen)
		scriptBytes := make([]byte, scriptLen)
		if _, err := io.ReadFull(r, scriptBytes); err != nil {
			return [32]byte{}, fmt.Errorf("read input[%d] scriptSig: %w", i, err)
		}
		buf.Write(scriptBytes)

		// sequence: 4 bytes LE
		var seq [4]byte
		if _, err := io.ReadFull(r, seq[:]); err != nil {
			return [32]byte{}, fmt.Errorf("read input[%d] sequence: %w", i, err)
		}
		buf.Write(seq[:])
	}

	// Output count and outputs
	outputCount, err := readVarInt(r)
	if err != nil {
		return [32]byte{}, fmt.Errorf("read output count: %w", err)
	}
	writeVarInt(buf, outputCount)

	for i := range outputCount {
		// value: 8 bytes LE
		var val [8]byte
		if _, err := io.ReadFull(r, val[:]); err != nil {
			return [32]byte{}, fmt.Errorf("read output[%d] value: %w", i, err)
		}
		buf.Write(val[:])

		// scriptPubKey
		scriptLen, err := readVarInt(r)
		if err != nil {
			return [32]byte{}, fmt.Errorf("read output[%d] scriptPubKey length: %w", i, err)
		}
		writeVarInt(buf, scriptLen)
		scriptBytes := make([]byte, scriptLen)
		if _, err := io.ReadFull(r, scriptBytes); err != nil {
			return [32]byte{}, fmt.Errorf("read output[%d] scriptPubKey: %w", i, err)
		}
		buf.Write(scriptBytes)
	}

	// Locktime: 4 bytes LE
	var locktime [4]byte
	if _, err := io.ReadFull(r, locktime[:]); err != nil {
		return [32]byte{}, fmt.Errorf("read locktime: %w", err)
	}
	buf.Write(locktime[:])

	// Hash the non-witness serialization
	return doubleSHA256(buf.Bytes()), nil
}

// writeVarInt writes a variable-length integer to buf using Bitcoin's varint encoding.
func writeVarInt(buf *bytes.Buffer, n uint64) {
	if n < 0xFD {
		buf.WriteByte(byte(n))
	} else if n <= 0xFFFF {
		buf.WriteByte(0xFD)
		var b [2]byte
		binary.LittleEndian.PutUint16(b[:], uint16(n))
		buf.Write(b[:])
	} else if n <= 0xFFFFFFFF {
		buf.WriteByte(0xFE)
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], uint32(n))
		buf.Write(b[:])
	} else {
		buf.WriteByte(0xFF)
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], n)
		buf.Write(b[:])
	}
}

// ── Wire-format field parsers ─────────────────────────────────────────────────

// parseTxInput reads one transaction input from r.
func parseTxInput(r *pushBackReader) (RawTxInput, error) {
	// Prevout txid: 32 bytes LE on the wire
	var prevLE [32]byte
	if _, err := io.ReadFull(r, prevLE[:]); err != nil {
		return RawTxInput{}, fmt.Errorf("prevout txid: %w", err)
	}

	// Prevout vout: 4 bytes LE
	prevVout, err := readUint32LE(r)
	if err != nil {
		return RawTxInput{}, fmt.Errorf("prevout vout: %w", err)
	}

	// scriptSig: skip
	scriptLen, err := readVarInt(r)
	if err != nil {
		return RawTxInput{}, fmt.Errorf("scriptSig len: %w", err)
	}
	if err := skipN(r, scriptLen); err != nil {
		return RawTxInput{}, fmt.Errorf("scriptSig data: %w", err)
	}

	// Sequence: 4 bytes LE — skip
	if _, err := readUint32LE(r); err != nil {
		return RawTxInput{}, fmt.Errorf("sequence: %w", err)
	}

	// Coinbase detection: the coinbase sentinel is all-zero prevout txid AND
	// vout == 0xFFFFFFFF. Check isCoinbase first so we don't reverse the bytes
	// of a coinbase input into a misleading hex string.
	isCoinbase := prevVout == 0xFFFFFFFF
	if isCoinbase {
		for _, b := range prevLE {
			if b != 0x00 {
				isCoinbase = false
				break
			}
		}
	}
	if isCoinbase {
		return RawTxInput{PrevTxIDHex: "", PrevVout: prevVout}, nil
	}

	// Reverse LE → BE for RPC-compatible hex
	var prevBE [32]byte
	for i, b := range prevLE {
		prevBE[31-i] = b
	}
	return RawTxInput{
		PrevTxIDHex: hex.EncodeToString(prevBE[:]),
		PrevVout:    prevVout,
	}, nil
}

// parseTxOutput reads one transaction output from r and extracts its address.
// hrp is the bech32 human-readable part used for witness address encoding.
func parseTxOutput(r *pushBackReader, n uint32, hrp string) (RawTxOutput, error) {
	// Value: 8 bytes LE (satoshis)
	var valueBuf [8]byte
	if _, err := io.ReadFull(r, valueBuf[:]); err != nil {
		return RawTxOutput{}, fmt.Errorf("value: %w", err)
	}
	valueSatU64 := binary.LittleEndian.Uint64(valueBuf[:])
	// Maximum valid bitcoin value: 21,000,000 BTC = 2.1e15 satoshis, well below int64 max.
	// But check for overflow on 32-bit platforms where int64 is still 64-bit.
	const maxValidValue uint64 = 2_100_000_000_000_000
	if valueSatU64 > maxValidValue {
		return RawTxOutput{}, fmt.Errorf("value overflows valid bitcoin range: %d", valueSatU64)
	}
	valueSat := int64(valueSatU64)

	// scriptPubKey
	scriptLen, err := readVarInt(r)
	if err != nil {
		return RawTxOutput{}, fmt.Errorf("scriptPubKey len: %w", err)
	}
	if scriptLen > 10_000 {
		return RawTxOutput{}, fmt.Errorf("implausible scriptPubKey length %d", scriptLen)
	}
	script := make([]byte, scriptLen)
	if _, err := io.ReadFull(r, script); err != nil {
		return RawTxOutput{}, fmt.Errorf("scriptPubKey data: %w", err)
	}

	return RawTxOutput{
		ValueSat: valueSat,
		N:        n,
		Address:  extractAddress(script, hrp),
	}, nil
}

// ── Address extraction ────────────────────────────────────────────────────────

// extractAddress returns the human-readable address for standard scriptPubKey
// patterns, or "" for OP_RETURN, multisig, and other non-standard scripts.
//
// The output encoding matches bitcoinshared.ValidateAndNormalise:
//   - P2WPKH / P2WSH  → bech32  (lowercase, witness version 0)
//   - P2TR            → bech32m (lowercase, witness version 1)
//   - P2PKH           → base58check (mixed-case, version byte 0x00 mainnet / 0x6F testnet)
//   - P2SH            → base58check (mixed-case, version byte 0x05 mainnet / 0xC4 testnet)
//
// hrp selects the network prefix: "bc" mainnet, "tb" testnet4/signet, "bcrt" regtest.
// The P2PKH/P2SH version bytes are derived from the hrp for correct encoding.
func extractAddress(script []byte, hrp string) string {
	switch {
	// P2WPKH: OP_0 PUSH20 <20-byte key hash>  →  0x00 0x14 <20 bytes>
	case len(script) == 22 && script[0] == 0x00 && script[1] == 0x14:
		return bech32EncodeWitness(hrp, 0, script[2:22])

	// P2WSH: OP_0 PUSH32 <32-byte script hash>  →  0x00 0x20 <32 bytes>
	case len(script) == 34 && script[0] == 0x00 && script[1] == 0x20:
		return bech32EncodeWitness(hrp, 0, script[2:34])

	// P2TR: OP_1 PUSH32 <32-byte tweaked pubkey>  →  0x51 0x20 <32 bytes>
	case len(script) == 34 && script[0] == 0x51 && script[1] == 0x20:
		return bech32EncodeWitness(hrp, 1, script[2:34])

	// P2PKH: OP_DUP OP_HASH160 PUSH20 <20 bytes> OP_EQUALVERIFY OP_CHECKSIG
	//        0x76  0xa9         0x14   ...         0x88           0xac
	case len(script) == 25 &&
		script[0] == 0x76 && script[1] == 0xa9 && script[2] == 0x14 &&
		script[23] == 0x88 && script[24] == 0xac:
		ver := p2pkhVersion(hrp)
		return base58CheckEncode(ver, script[3:23])

	// P2SH: OP_HASH160 PUSH20 <20 bytes> OP_EQUAL
	//       0xa9        0x14   ...         0x87
	case len(script) == 23 &&
		script[0] == 0xa9 && script[1] == 0x14 && script[22] == 0x87:
		ver := p2shVersion(hrp)
		return base58CheckEncode(ver, script[2:22])

	default:
		return ""
	}
}

// p2pkhVersion returns the P2PKH version byte for the given HRP.
// Mainnet=0x00, testnet/regtest=0x6F.
func p2pkhVersion(hrp string) byte {
	if hrp == "bc" {
		return 0x00
	}
	return 0x6F
}

// p2shVersion returns the P2SH version byte for the given HRP.
// Mainnet=0x05, testnet/regtest=0xC4.
func p2shVersion(hrp string) byte {
	if hrp == "bc" {
		return 0x05
	}
	return 0xC4
}

// ── Bech32 / Bech32m encoding (BIP 173 / BIP 350) ────────────────────────────
//
// Stdlib-only implementation. No external dependency on btcutil or any bech32 library.

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

// bech32EncodeWitness encodes a witness program as a bech32 (version 0) or
// bech32m (version 1+) address string, matching Bitcoin Core's output format.
func bech32EncodeWitness(hrp string, witVersion byte, program []byte) string {
	// Convert 8-bit program bytes → 5-bit groups (base32), prepend witness version.
	data := make([]byte, 0, 1+(len(program)*8+4)/5)
	data = append(data, witVersion) // witness version as-is (already 0–16)

	acc, bits := 0, 0
	for _, b := range program {
		acc = (acc << 8) | int(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			data = append(data, byte((acc>>bits)&0x1f))
		}
	}
	if bits > 0 {
		data = append(data, byte((acc<<(5-bits))&0x1f))
	}

	useBech32m := witVersion != 0
	chk := bech32Checksum(hrp, data, useBech32m)

	var sb strings.Builder
	sb.WriteString(hrp)
	sb.WriteByte('1') // separator
	for _, b := range data {
		sb.WriteByte(bech32Charset[b])
	}
	for _, b := range chk {
		sb.WriteByte(bech32Charset[b])
	}
	return sb.String()
}

// bech32Checksum computes the 6-character bech32/bech32m checksum.
// UseBech32m=true selects the BIP 350 constant; false selects BIP 173.
func bech32Checksum(hrp string, data []byte, useBech32m bool) [6]byte {
	// Build the values slice: HRP expanded + data + 6 zero bytes for checksum slot.
	vals := make([]byte, 0, len(hrp)*2+1+len(data)+6)
	for _, c := range hrp {
		vals = append(vals, byte(c>>5))
	}
	vals = append(vals, 0)
	for _, c := range hrp {
		vals = append(vals, byte(c&0x1f))
	}
	vals = append(vals, data...)
	vals = append(vals, 0, 0, 0, 0, 0, 0)

	var constant uint32 = 1
	if useBech32m {
		constant = 0x2bc830a3
	}
	poly := bech32Polymod(vals) ^ constant

	var chk [6]byte
	for i := range chk {
		//nolint:gosec // G115: uint8→int shift is safe; all inputs are ASCII
		//              // printable characters validated by the bech32 charset.
		chk[i] = byte((poly >> uint(5*(5-i))) & 0x1f)
	}
	return chk
}

// bech32Polymod computes the BCH polynomial checksum per BIP 173.
func bech32Polymod(values []byte) uint32 {
	gen := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := uint32(1)
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i, g := range gen {
			if (top>>uint(i))&1 != 0 {
				chk ^= g
			}
		}
	}
	return chk
}

// ── Base58Check encoding ──────────────────────────────────────────────────────

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// base58CheckEncode encodes a version byte + payload as a Bitcoin Base58Check
// address string. Used for P2PKH (version 0x00/0x6F) and P2SH (0x05/0xC4).
func base58CheckEncode(version byte, payload []byte) string {
	// Prepend version byte
	full := make([]byte, 0, 1+len(payload)+4)
	full = append(full, version)
	full = append(full, payload...)

	// Append 4-byte checksum = first 4 bytes of SHA256(SHA256(full))
	chk := doubleSHA256(full)
	full = append(full, chk[0], chk[1], chk[2], chk[3])

	// Count leading zero bytes → one leading '1' per zero byte
	leadingOnes := 0
	for _, b := range full {
		if b != 0x00 {
			break
		}
		leadingOnes++
	}

	// Big-integer base58 encoding via repeated division
	// digits accumulates base-58 digits in reverse (least-significant first)
	digits := make([]byte, 0, len(full)*136/100+1)
	for _, b := range full {
		carry := int(b)
		for i := range digits {
			carry += 256 * int(digits[i])
			digits[i] = byte(carry % 58)
			carry /= 58
		}
		for carry > 0 {
			digits = append(digits, byte(carry%58))
			carry /= 58
		}
	}

	// Build final string: leading '1's first, then digits in reverse order
	out := make([]byte, leadingOnes, leadingOnes+len(digits))
	for i := range out {
		out[i] = '1'
	}
	for i := len(digits) - 1; i >= 0; i-- {
		out = append(out, base58Alphabet[digits[i]])
	}
	return string(out)
}

// ── SHA-256 helpers ───────────────────────────────────────────────────────────

// doubleSHA256 computes SHA256(SHA256(b)), the digest used for Bitcoin
// transaction IDs (txid) and script hashes per BIP-141.
func doubleSHA256(data []byte) [32]byte {
	h1 := sha256.Sum256(data)
	return sha256.Sum256(h1[:])
}

// ── io.Reader helpers ─────────────────────────────────────────────────────────

// pushBackReader is a minimal buffered reader that supports pushing back at most
// 2 bytes. Used to "un-read" the SegWit marker/flag bytes when the transaction
// turns out to be a legacy (non-SegWit) format.
type pushBackReader struct {
	data   []byte
	pos    int
	pushed [2]byte
	nPush  int
}

func newPushBackReader(data []byte) *pushBackReader {
	return &pushBackReader{data: data}
}

func (r *pushBackReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	// Drain pushback buffer first.
	if r.nPush > 0 {
		n := copy(p, r.pushed[:r.nPush])
		// Shift remaining pushed bytes left.
		if n < r.nPush {
			copy(r.pushed[:], r.pushed[n:r.nPush])
		}
		r.nPush -= n
		return n, nil
	}
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

// pushBack puts at most 2 bytes back into the read buffer.
// Panics if more than 2 bytes are pushed — this is an internal invariant.
func (r *pushBackReader) pushBack(b ...byte) {
	if len(b)+r.nPush > 2 {
		panic("pushBackReader: pushback buffer overflow (max 2 bytes)")
	}
	// Insert at front: shift existing bytes right, prepend new ones.
	for i := r.nPush - 1; i >= 0; i-- {
		r.pushed[i+len(b)] = r.pushed[i]
	}
	copy(r.pushed[:], b)
	r.nPush += len(b)
}

// Skip advances the read position by n bytes without allocating a throwaway
// buffer. This is an O(1) operation for all callers since pushBackReader is
// an in-memory slice — all data is already in RAM.
//
// Skip panics if there are any pushed-back bytes (nPush > 0). The invariant
// is that Skip is only called after all pushed-back bytes have been consumed
// by a prior read. This is currently guaranteed: pushBack is called once
// (to un-read the SegWit marker/flag), and the pushed bytes are fully consumed
// by the next readVarInt before any skipN call. However, documenting and
// enforcing this invariant protects against future regressions.
func (r *pushBackReader) Skip(n uint64) error {
	if r.nPush > 0 {
		panic("pushBackReader.Skip: called with buffered pushback bytes (must consume with Read first)")
	}
	if n == 0 {
		return nil
	}
	end := r.pos + int(n)
	if end > len(r.data) {
		return io.ErrUnexpectedEOF
	}
	r.pos = end
	return nil
}

// readUint32LE reads a 4-byte little-endian uint32.
func readUint32LE(r io.Reader) (uint32, error) {
	var buf [4]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(buf[:]), nil
}

// readVarInt reads a Bitcoin compact-size (variable-length) integer.
//
// Encoding:
//   - 0x00–0xFC  → single byte value
//   - 0xFD       → followed by 2-byte LE uint16
//   - 0xFE       → followed by 4-byte LE uint32
//   - 0xFF       → followed by 8-byte LE uint64
func readVarInt(r io.Reader) (uint64, error) {
	var first [1]byte
	if _, err := io.ReadFull(r, first[:]); err != nil {
		return 0, fmt.Errorf("varint prefix: %w", err)
	}
	switch first[0] {
	case 0xfd:
		var buf [2]byte
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return 0, fmt.Errorf("varint uint16: %w", err)
		}
		return uint64(binary.LittleEndian.Uint16(buf[:])), nil
	case 0xfe:
		var buf [4]byte
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return 0, fmt.Errorf("varint uint32: %w", err)
		}
		return uint64(binary.LittleEndian.Uint32(buf[:])), nil
	case 0xff:
		var buf [8]byte
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return 0, fmt.Errorf("varint uint64: %w", err)
		}
		return binary.LittleEndian.Uint64(buf[:]), nil
	default:
		return uint64(first[0]), nil
	}
}

// skipN advances past n bytes in r without allocating any buffer.
// The maxSkip guard is applied here so pushBackReader.Skip stays a simple
// bounds check reusable independently of the guard.
//
// H1: changed to accept *pushBackReader instead of io.Reader to eliminate the
// throwaway heap allocation that io.ReadFull(r, make([]byte, n)) caused on
// every scriptSig, witness-item, and sequence-field skip.
func skipN(r *pushBackReader, n uint64) error {
	const maxSkip = 4 << 20 // 4 MiB safety guard
	if n > maxSkip {
		return fmt.Errorf("skip size %d exceeds 4 MiB guard", n)
	}
	return r.Skip(n)
}
