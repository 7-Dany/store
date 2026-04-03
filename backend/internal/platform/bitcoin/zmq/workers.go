package zmq

import (
	"context"
	"fmt"
	"runtime"

	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

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
				// Capture stack trace for debugging production panics
				stack := make([]byte, 4096)
				n := runtime.Stack(stack, false)
				// T2: add "error" key so TelemetryHandler increments app_errors_total.
				panicErr := telemetry.ZMQ("invokeHandler.panic",
					fmt.Errorf("handler %q panicked: %v\nstack:\n%s", name, r, stack[:n]))
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
