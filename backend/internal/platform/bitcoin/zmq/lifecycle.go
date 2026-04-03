package zmq

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

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
				// Capture stack trace for debugging production panics
				stack := make([]byte, 4096)
				n := runtime.Stack(stack, false)
				// T1: add "error" key so TelemetryHandler increments app_errors_total.
				panicErr := telemetry.ZMQ("Run.block_reader_panic",
					fmt.Errorf("reader goroutine panicked: %v\nstack:\n%s", r, stack[:n]))
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
				// Capture stack trace for debugging production panics
				stack := make([]byte, 4096)
				n := runtime.Stack(stack, false)
				panicErr := telemetry.ZMQ("Run.settlement_reader_panic",
					fmt.Errorf("reader goroutine panicked: %v\nstack:\n%s", r, stack[:n]))
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
				// Capture stack trace for debugging production panics
				stack := make([]byte, 4096)
				n := runtime.Stack(stack, false)
				panicErr := telemetry.ZMQ("Run.rawtx_reader_panic",
					fmt.Errorf("reader goroutine panicked: %v\nstack:\n%s", r, stack[:n]))
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
