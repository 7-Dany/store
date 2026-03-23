package zmq

import "github.com/7-Dany/store/backend/internal/platform/telemetry"

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

// compile-time check that *telemetry.Registry satisfies ZMQRecorder.
var _ ZMQRecorder = (*telemetry.Registry)(nil)

// noopRecorder discards all metric calls. Substituted when New() receives nil.
type noopRecorder struct{}

func (noopRecorder) SetZMQConnected(bool)     {}
func (noopRecorder) OnHandlerPanic(string)    {}
func (noopRecorder) OnHandlerTimeout(string)  {}
func (noopRecorder) SetHandlerGoroutines(int) {}
func (noopRecorder) OnMessageDropped(string)  {}
