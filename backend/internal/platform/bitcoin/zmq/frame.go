package zmq

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"runtime"
	"sync"

	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

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

// ── Recovery ──────────────────────────────────────────────────────────────────

// fireRecovery delivers a snapshot of event to all registered handlers.
// Event is passed by value so handlers cannot mutate shared state.
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
		ReconnectedAt:    s.clockFn(),
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
					// Capture stack trace for debugging production panics
					stack := make([]byte, 4096)
					n := runtime.Stack(stack, false)
					logger.Error(rCtx, "zmq: recovery handler panic",
						"error", telemetry.ZMQ("fireRecovery.panic",
							fmt.Errorf("recovery handler panicked: %v\nstack:\n%s", r, stack[:n])),
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
