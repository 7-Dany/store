package telemetry

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// RequestMiddleware returns a chi-compatible middleware that:
//   - Injects a [carrier] into the request context before calling next.
//   - Tracks http_requests_in_flight.
//   - Records http_requests_total, http_request_duration_seconds after the handler returns.
//   - Records http_errors_total (route, layer, cause) for 5xx responses when the
//     carrier contains an error written by [TelemetryHandler].
//
// Must be added to the router BEFORE [PanicRecoveryMiddleware] so the carrier
// exists in context when a panic is recovered.
func RequestMiddleware(registry *Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if registry != nil {
				registry.httpRequestsInFlight.Inc()
				defer registry.httpRequestsInFlight.Dec()
			}

			// Inject carrier BEFORE calling next so TelemetryHandler (and
			// PanicRecoveryMiddleware) can write into it.
			ctx, c := newCarrierContext(req.Context())
			req = req.WithContext(ctx)

			start := time.Now()
			ww := newStatusRecorder(w)
			next.ServeHTTP(ww, req)

			if registry == nil {
				return
			}

			route := routePattern(req)
			status := strconv.Itoa(ww.status)
			elapsed := time.Since(start).Seconds()

			registry.httpRequestsTotal.
				WithLabelValues(req.Method, route, status).Inc()
			registry.httpRequestDuration.
				WithLabelValues(req.Method, route).Observe(elapsed)

			if ww.status >= 500 {
				if err := c.get(); err != nil {
					layer := string(LayerOf(err))
					cause := string(ClassifyCause(err))
					registry.httpErrors.
						WithLabelValues(route, layer, cause).Inc()
				}
			}
		})
	}
}

// PanicRecoveryMiddleware recovers from panics in downstream handlers, logs the
// event (which triggers [TelemetryHandler] enrichment and carrier write), and
// writes a 500 JSON response.
//
// Replaces chimiddleware.Recoverer entirely. Must be positioned AFTER
// [RequestMiddleware] in the middleware chain so the carrier is already in
// context when a panic occurs.
//
// Note: this middleware uses slog.ErrorContext directly (not a package Logger)
// because it lives inside the telemetry package and the global slog default IS
// the TelemetryHandler — the enrichment still fires.
func PanicRecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}

			panicErr := &Fault{
				Op:    "http.handler",
				Layer: LayerPanic,
				Err:   fmt.Errorf("panic: %v", rec),
			}

			// slog.ErrorContext triggers TelemetryHandler which:
			//   - enriches with fault_layer=panic, fault_cause=panic
			//   - writes panicErr into the carrier
			//   - increments app_errors_total{component="runtime", layer="panic", cause="panic"}
			slog.ErrorContext(r.Context(), "http handler panic recovered",
				"error",     panicErr,
				"component", "runtime",
				"stack",     string(debug.Stack()),
			)

			// Write 500 directly — cannot import platform/respond (import cycle).
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":"internal_error","message":"internal server error"}`))
		}()

		next.ServeHTTP(w, r)
	})
}

// ── statusRecorder ────────────────────────────────────────────────────────────

// statusRecorder wraps http.ResponseWriter and captures the status code written
// by the handler. Defaults to 200 if WriteHeader is never called.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func newStatusRecorder(w http.ResponseWriter) *statusRecorder {
	return &statusRecorder{ResponseWriter: w, status: http.StatusOK}
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

// ── routePattern ─────────────────────────────────────────────────────────────

// routePattern returns the matched chi route pattern for the request.
//
// SECURITY: must return the constant "unknown" for unmatched routes — never
// req.URL.Path. An attacker flooding 404s with unique paths would create one
// Prometheus time series per unique URL and exhaust memory (cardinality bomb).
func routePattern(r *http.Request) string {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil || rctx.RoutePattern() == "" {
		return "unknown"
	}
	return rctx.RoutePattern()
}
