package zmq

import (
	"context"
	"errors"
	"net"
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

// ── Reader configs ────────────────────────────────────────────────────────────

// blockReaderConfig returns the readerConfig for the hashblock endpoint.
// The block reader is the primary liveness source: it updates the live atomic
// and drives the SetZMQConnected gauge.
func (s *subscriber) blockReaderConfig() readerConfig {
	return readerConfig{
		endpoint: s.blockEndpoint,
		topic:    []byte("hashblock"),
		topicStr: "hashblock",
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
			s.live.Store(&liveness{hash: event.HashHex(), at: s.clockFn()})
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
		topicStr:   "hashtx",
		onDialOK:   func() { s.hashtxDialOK.Store(true) },
		onDialFail: func() { s.hashtxDialOK.Store(false) },
		onRecvErr:  func() { s.hashtxDialOK.Store(false) },
		onEvent: func(_ context.Context, hash [32]byte, seq uint32) {
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
		topicStr:   "rawtx",
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
	cfg readerConfig, //nolint:gocritic // 88 bytes, copied for isolation between reader loops
	state *readerState,
	loop receiveLoopFn,
) {
	backoff := reconnectBase
	everConnected := false
	attempt := 0

	for {
		if ctx.Err() != nil {
			logger.Debug(ctx, "zmq: runReconnectLoop: context cancelled, exiting",
				"topic", cfg.topicStr)
			return
		}

		attempt++
		logger.Debug(ctx, "zmq: runReconnectLoop: dial attempt",
			"topic", cfg.topicStr, "endpoint", cfg.endpoint, "attempt", attempt, "backoff", backoff)

		conn, err := dialZMTP(ctx, cfg.endpoint, cfg.topic)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			cfg.onDialFail()
			logger.Warn(ctx, "zmq: connection failed -- retrying",
				"topic", cfg.topicStr, "endpoint", cfg.endpoint,
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
			"topic", cfg.topicStr, "endpoint", cfg.endpoint, "attempt", attempt)

		// Fire recovery before delivering the first post-reconnect event so
		// handlers can fill any gap before new events arrive. Skip on the very
		// first connection — no gap is possible before any message is received.
		// Also skip if context is already cancelled during shutdown — recovery
		// is meaningless and handlers shouldn't be spawned as untracked goroutines.
		if everConnected && ctx.Err() == nil {
			logger.Debug(ctx, "zmq: runReconnectLoop: firing recovery after reconnect",
				"topic", cfg.topicStr, "last_seq", state.lastSeq)
			s.fireRecovery(ctx, cfg.topicStr, state.lastSeq)
		}
		everConnected = true
		backoff = reconnectBase // reset after a successful connection

		loop(ctx, cfg, state, conn)

		if ctx.Err() != nil {
			return
		}
		logger.Debug(ctx, "zmq: runReconnectLoop: session ended, will reconnect",
			"topic", cfg.topicStr, "next_backoff", backoff)
		if !sleepCtx(ctx, backoff) {
			return
		}
		backoff = nextBackoff(backoff)
	}
}

// runReader connects to the endpoint described by cfg and reads until ctx is
// cancelled, reconnecting with exponential backoff on any transient error.
// State persists across reconnects so sequence gap detection works correctly.
func (s *subscriber) runReader(ctx context.Context, cfg readerConfig, state *readerState) { //nolint:gocritic // 88 bytes, copied for isolation
	s.runReconnectLoop(ctx, cfg, state, s.receiveLoop)
}

// receiveLoop runs the blocking receive loop for one established connection,
// closing conn on return regardless of exit reason. It is a helper for
// runReconnectLoop; callers must not reuse conn after receiveLoop returns.
func (s *subscriber) receiveLoop(ctx context.Context, cfg readerConfig, state *readerState, conn *zmtpConn) { //nolint:gocritic // 88 bytes, copied for isolation
	defer func() {
		if err := conn.Close(); err != nil {
			// Only log as Warn if it's not a normal close (net.ErrClosed is expected
			// when ctx cancels the connection). Other errors indicate a problem.
			if !errors.Is(err, net.ErrClosed) {
				logger.Warn(ctx, "zmq: receiveLoop close failed unexpectedly", "topic", cfg.topicStr, "error", err)
			} else {
				logger.Debug(ctx, "zmq: receiveLoop closed normally", "topic", cfg.topicStr)
			}
		}
	}()
	for {
		frames, err := conn.RecvMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				logger.Debug(ctx, "zmq: receiveLoop: context cancelled",
					"topic", cfg.topicStr)
				return
			}
			cfg.onRecvErr()
			// T5: wrap with telemetry.ZMQ at the logging call site.
			logger.Warn(ctx, "zmq: receive error -- reconnecting",
				"topic", cfg.topicStr,
				"error", telemetry.ZMQ("receiveLoop.recv", err))
			return
		}
		logger.Debug(ctx, "zmq: receiveLoop: got message",
			"topic", cfg.topicStr, "frame_count", len(frames))
		if err := s.processFrame(ctx, frames, cfg.topic, state, cfg.onEvent); err != nil {
			logger.Warn(ctx, "zmq: frame rejected",
				"topic", cfg.topicStr, "error", err)
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
func (s *subscriber) rawTxReceiveLoop(ctx context.Context, cfg readerConfig, state *readerState, conn *zmtpConn) { //nolint:gocritic // 88 bytes, copied for isolation
	defer func() {
		if err := conn.Close(); err != nil {
			if !errors.Is(err, net.ErrClosed) {
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
