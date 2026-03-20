// Package telemetry is the single observability boundary for the store backend.
//
// Any package that needs structured logging, error classification, or Prometheus
// metrics imports this package and nothing else. It never imports
// prometheus/client_golang, log/slog, or any other observability library
// directly — telemetry owns that surface entirely.
//
// # Signal taxonomy
//
// Six metric families cover the full observability surface:
//
//   - Family 1 — HTTP: request counts, latency histograms, in-flight gauge, error counters.
//     Recorded automatically by [RequestMiddleware]; zero domain code required.
//   - Family 2 — Application Errors: app_errors_total fires on every [Logger.Error] call.
//     Recorded automatically by [TelemetryHandler]; zero domain code required.
//   - Family 3 — Auth Business Events: login/registration/OAuth signals.
//     Recorded explicitly via authshared.AuthRecorder call sites.
//   - Family 4 — Infrastructure: DB pool, Redis pool, goroutine/memory gauges.
//     Recorded automatically by [Registry.StartInfraPoller]; zero domain code required.
//   - Family 5 — Job Queue: job lifecycle counters and duration histograms.
//     Recorded automatically via jobqueue.MetricsRecorder structural satisfaction.
//   - Family 6 — Bitcoin Domain: ZMQ/RPC connectivity, balance drift, settlement metrics.
//     Recorded explicitly via bitcoinshared.BitcoinRecorder call sites.
//
// # Usage
//
//	// Declare one logger per package at package level:
//	var log = telemetry.New("login")
//
//	// Wrap errors at the correct layer:
//	return nil, telemetry.Store("GetUser.query", err)
//	return nil, telemetry.Service("Login.tx", err)
//
//	// Log errors — TelemetryHandler auto-enriches with fault attribution:
//	log.Error(ctx, "login: store error", "error", err)
//
// # Wiring (server.New)
//
//	registry := telemetry.NewRegistry()
//	telemetry.SetDefault(registry)
//	registry.StartInfraPoller(ctx, pool, redisStore, 15*time.Second)
//	deps.Metrics = registry
package telemetry
