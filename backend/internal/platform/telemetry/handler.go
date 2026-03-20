package telemetry

import (
	"context"
	"log/slog"

	"github.com/go-chi/chi/v5/middleware"
)

// TelemetryHandler wraps an inner slog.Handler and adds observability
// side-effects on every log record:
//
//   - ALL levels: injects request_id from chi middleware context.
//   - ERROR level only: extracts the "error" argument, enriches the record with
//     fault_layer / fault_cause / fault_op / fault_chain attributes, calls
//     [Attach] to write the error into the request carrier, and increments
//     app_errors_total{component, layer, cause}.
//
// TelemetryHandler is installed globally by [SetDefault]. Domain code never
// interacts with it directly.
type TelemetryHandler struct {
	inner    slog.Handler
	registry *Registry // nil-safe throughout
}

// newTelemetryHandler wraps inner with a TelemetryHandler backed by registry.
// registry may be nil — all metric paths are nil-guarded.
func newTelemetryHandler(inner slog.Handler, registry *Registry) *TelemetryHandler {
	return &TelemetryHandler{inner: inner, registry: registry}
}

// Enabled delegates to the inner handler.
func (h *TelemetryHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// WithAttrs delegates to the inner handler and preserves the registry reference.
func (h *TelemetryHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &TelemetryHandler{inner: h.inner.WithAttrs(attrs), registry: h.registry}
}

// WithGroup delegates to the inner handler and preserves the registry reference.
func (h *TelemetryHandler) WithGroup(name string) slog.Handler {
	return &TelemetryHandler{inner: h.inner.WithGroup(name), registry: h.registry}
}

// Handle enriches the record before forwarding to the inner handler.
//
// For every record: injects request_id when present in ctx.
// For ERROR records only: extracts "error" and "component" args, adds fault
// attributes, calls [Attach], increments app_errors_total.
func (h *TelemetryHandler) Handle(ctx context.Context, r slog.Record) error {
	// ALL LEVELS: inject request_id from chi middleware.
	if reqID := middleware.GetReqID(ctx); reqID != "" {
		r.AddAttrs(slog.String("request_id", reqID))
	}

	// ERROR LEVEL ONLY: fault enrichment + metric increment.
	if r.Level >= slog.LevelError {
		var found error
		var component string

		r.Attrs(func(a slog.Attr) bool {
			switch a.Key {
			case "error":
				if e, ok := a.Value.Any().(error); ok {
					found = e
				}
			case "component":
				component = a.Value.String()
			}
			// Stop early once both are found.
			return found == nil || component == ""
		})

		if found != nil {
			layer := LayerOf(found)
			cause := ClassifyCause(found)
			op := OpOf(found)
			chain := FaultChain(found)

			r.AddAttrs(
				slog.String("fault_layer", string(layer)),
				slog.String("fault_cause", string(cause)),
				slog.String("fault_op", op),
				slog.Any("fault_chain", chain),
			)

			// Thread the error into the request carrier so RequestMiddleware
			// can read layer/cause for http_errors_total after the handler returns.
			Attach(ctx, found)

			// Increment app_errors_total.
			if h.registry != nil {
				h.registry.appErrors.
					WithLabelValues(component, string(layer), string(cause)).Inc()
			}
		}
	}

	return h.inner.Handle(ctx, r)
}
