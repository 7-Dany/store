package server

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/rs/cors"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/domain"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

// maintenanceMiddleware returns a middleware that returns 503 Service Unavailable
// when maintenance mode is enabled. Health checks are exempt.
func maintenanceMiddleware(enabled bool) func(http.Handler) http.Handler {
	if !enabled {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error":"Service temporarily unavailable for maintenance"}`))
		})
	}
}

// newRouter wires global middleware and mounts every versioned API sub-router.
// ctx is the application root context; it is threaded into domain assemblers
// so they can start cleanup goroutines that respect graceful shutdown (RULES §2.6).
// Domain routes receive deps and are responsible for their own feature-level
// middleware (content-type, rate-limiting, JWT auth).
func newRouter(ctx context.Context, deps *app.Deps, registry *telemetry.Registry) http.Handler {
	r := chi.NewRouter()

	// ── Global middleware ─────────────────────────────────────────────────
	//
	// Order is intentional:
	//   1. RequestID            — injects X-Request-ID; must be first so all
	//                             subsequent middleware/handlers see the ID.
	//   2. TrustedProxyRealIP  — rewrites RemoteAddr from X-Forwarded-For only
	//                             for trusted upstream proxies; must precede any
	//                             middleware that reads the client IP (rate limiters).
	//   3. Logger              — chi's structured access logger.
	//   4. RequestMiddleware   — injects the fault carrier, records HTTP metrics.
	//                             Must be before PanicRecoveryMiddleware so the
	//                             carrier is in context when a panic is recovered.
	//   5. PanicRecoveryMiddleware — replaces chimiddleware.Recoverer. Recovers
	//                             panics, writes them into the carrier, and returns 500.
	r.Use(
		chimiddleware.RequestID,
		ratelimit.TrustedProxyRealIP(deps.TrustedProxyCIDRs),
		chimiddleware.Logger,
		telemetry.RequestMiddleware(registry),
		telemetry.PanicRecoveryMiddleware,
	)

	// Security: defence-in-depth response headers applied unconditionally.
	//   X-Content-Type-Options: prevents MIME-sniffing attacks.
	//   X-Frame-Options: prevents clickjacking via <frame>/<iframe> embedding.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			next.ServeHTTP(w, r)
		})
	})

	// Security: CORS restricts cross-origin requests to the configured
	// allow-list. Applied before route registration so preflight OPTIONS
	// requests are handled globally.
	c := cors.New(cors.Options{
		AllowedOrigins:   deps.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type"},
		AllowCredentials: true,
		MaxAge:           300,
	})
	r.Use(c.Handler)

	// Security: HSTS enforces TLS for all future requests.
	// Only injected when HTTPSEnabled is true to avoid breaking plain-HTTP
	// development environments.
	if deps.HTTPSEnabled {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
				next.ServeHTTP(w, r)
			})
		})
	}

	// ── Metrics ───────────────────────────────────────────────────────────
	// SECURITY: GET /metrics must NOT be reachable from the public internet.
	// It exposes route enumeration, session counts, auth failure rates, infra
	// sizing, and Bitcoin connectivity state to any unauthenticated scraper.
	//
	// Production hardening options (choose one before shipping):
	//   Option A — internal port (preferred):
	//     go http.ListenAndServe(cfg.InternalMetricsAddr, registry.Handler())
	//     and remove this route entirely.
	//   Option B — bearer token gate:
	//     r.With(requireMetricsBearerToken(cfg.MetricsScrapeToken)).
	//         Handle("/metrics", registry.Handler())
	//
	// r.Handle accepts http.Handler directly — correct for promhttp.HandlerFor output.
	// TODO: add auth gate before shipping to production.
	r.Handle("/metrics", registry.Handler())

	// ── Health ────────────────────────────────────────────────────────────
	// Rate limit: 30 req / min per IP, burst=10.
	// Allows legitimate monitoring tools (Prometheus, Next.js health-ping,
	// load-balancer probes) to poll comfortably at 15–30 s intervals, even
	// with multiple server replicas, while still blocking abusive scrapers.
	healthLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "health:ip:", 30.0/60, 10, 5*time.Minute)
	go healthLimiter.StartCleanup(ctx)

	// ── API v1 ────────────────────────────────────────────────────────────
	r.Route("/api/v1", func(r chi.Router) {
		// GET /api/v1/health
		//   no params  → {"status":"ok"}                  — load-balancer fast path
		//   ?ping=true → {"pong":true, services:[...]}    — full service health check
		// The full check runs DB, Redis, ZMQ, and RPC probes in parallel (2 s
		// deadline) and returns 200 when all services are up, 503 when any is down.
		// See server/health.go for probe implementations.
		r.With(healthLimiter.Limit).Get("/health", handleHealth(deps))

		// Maintenance mode: return 503 for all API routes except health
		r.Use(maintenanceMiddleware(deps.MaintenanceMode))

		domain.Mount(ctx, r, deps)
	})

	return r
}
