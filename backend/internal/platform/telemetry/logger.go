package telemetry

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
)

// globalRegistry holds the active *Registry installed by [SetDefault].
// Logger resolves it dynamically so SetDefault is always picked up
// regardless of init order.
var globalRegistry atomic.Pointer[Registry]

// SetDefault installs registry as the active telemetry registry and replaces
// the global slog default with a [TelemetryHandler]-wrapped JSON handler that
// writes to stdout.
//
// Must be called once in server.New before any domain code runs.
// resolveLogLevel returns the slog level from the LOG_LEVEL environment
// variable. Accepts "debug", "info", "warn", "error" (case-insensitive).
// Defaults to Info when the variable is absent or unrecognised.
func resolveLogLevel() slog.Level {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL"))) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func SetDefault(registry *Registry) {
	globalRegistry.Store(registry)
	level := resolveLogLevel()
	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})
	slog.SetDefault(slog.New(newTelemetryHandler(jsonHandler, registry)))
}

// NewNoopRegistry returns a fully functional *Registry backed by its own
// scoped Prometheus registry. Two calls in tests do not collide on metric names.
//
// Logging works, error wrapping works, Prometheus metrics are registered but
// isolated per instance. Domain unit tests never need the server registry.
func NewNoopRegistry() *Registry {
	return NewRegistry()
}

// Logger is a structured logger scoped to a single component.
//
// Declare one per package at package level:
//
//	var log = telemetry.New("login")
//	var log = telemetry.New("worker.purge")
//
// [Logger.Error] is the only method that fires app_errors_total and writes to
// the request carrier. All other levels pass through transparently.
type Logger struct {
	component string
	extra     []any // pre-set key/value pairs injected into every record
}

// New returns a Logger scoped to component. The component value is injected
// into every log record as the "component" field and is used as the Prometheus
// label value for app_errors_total.
func New(component string) *Logger {
	return &Logger{component: component}
}

// With returns a new Logger that includes attrs as additional key/value pairs
// on every subsequent record. It does not mutate the receiver.
func (l *Logger) With(attrs ...slog.Attr) *Logger {
	extra := make([]any, len(l.extra), len(l.extra)+len(attrs)*2)
	copy(extra, l.extra)
	for _, a := range attrs {
		extra = append(extra, a.Key, a.Value.Any())
	}
	return &Logger{component: l.component, extra: extra}
}

// Debug emits a DEBUG-level record. No metric side-effects.
func (l *Logger) Debug(ctx context.Context, msg string, args ...any) {
	l.emit(ctx, slog.LevelDebug, msg, args...)
}

// Info emits an INFO-level record. No metric side-effects.
func (l *Logger) Info(ctx context.Context, msg string, args ...any) {
	l.emit(ctx, slog.LevelInfo, msg, args...)
}

// Warn emits a WARN-level record. No metric side-effects.
//
// Use Warn for best-effort secondary operations that fail without affecting
// the primary response (audit writes, counter resets, heartbeat failures).
// These do not fire app_errors_total and do not write to the carrier.
func (l *Logger) Warn(ctx context.Context, msg string, args ...any) {
	l.emit(ctx, slog.LevelWarn, msg, args...)
}

// Error emits an ERROR-level record. [TelemetryHandler] intercepts this record
// and:
//   - Extracts the "error" argument
//   - Adds fault_layer, fault_cause, fault_op, fault_chain log attributes
//   - Calls [Attach] to write the error into the request carrier
//   - Increments app_errors_total{component, layer, cause}
//
// Use Error for failures that represent broken behavior — the primary operation
// failed or a dependency is down.
func (l *Logger) Error(ctx context.Context, msg string, args ...any) {
	l.emit(ctx, slog.LevelError, msg, args...)
}

// emit resolves slog.Default() dynamically at call time so SetDefault changes
// are always picked up regardless of package init order.
func (l *Logger) emit(ctx context.Context, level slog.Level, msg string, args ...any) {
	// Build the final args slice: component + pre-set extras + call-site args.
	all := make([]any, 0, 2+len(l.extra)+len(args))
	all = append(all, "component", l.component)
	all = append(all, l.extra...)
	all = append(all, args...)
	slog.Default().Log(ctx, level, msg, all...)
}
