package telemetry

// Platform hook methods expose metrics for platform-layer primitives
// (ConnectionCounter, etc.) to callers via a narrow typed interface.
// *Registry satisfies ratelimit.ConnCounterRecorder structurally — no import
// cycle arises because ratelimit defines the interface and telemetry is never
// imported by ratelimit.
//
// All methods are nil-safe: calling them on a nil *Registry is a no-op.

// OnConnCounterReleaseFailed increments
// platform_connection_counter_release_failures_total for the given key prefix.
// Called by ConnectionCounter.Release() when the decrement fails, indicating
// that a connection slot may be permanently leaked.
func (r *Registry) OnConnCounterReleaseFailed(keyPrefix string) {
	if r == nil {
		return
	}
	r.platformConnCounterReleaseFailures.WithLabelValues(keyPrefix).Inc()
}

// OnConnCounterHeartbeatMiss increments
// platform_connection_counter_heartbeat_misses_total for the given key prefix.
// Called by ConnectionCounter.Heartbeat() when the counter key is missing,
// indicating the per-user cap may have been transiently bypassed.
func (r *Registry) OnConnCounterHeartbeatMiss(keyPrefix string) {
	if r == nil {
		return
	}
	r.platformConnCounterHeartbeatMisses.WithLabelValues(keyPrefix).Inc()
}
