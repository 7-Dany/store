package server

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	// go get github.com/rs/cors
	"github.com/rs/cors"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/domain/auth"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
	"github.com/7-Dany/store/backend/internal/platform/respond"
)

// newRouter wires global middleware and mounts every versioned API sub-router.
// ctx is the application root context; it is threaded into domain assemblers
// so they can start cleanup goroutines that respect graceful shutdown (RULES §2.6).
// Domain routes receive deps and are responsible for their own feature-level
// middleware (content-type, rate-limiting, JWT auth).
func newRouter(ctx context.Context, deps *app.Deps) http.Handler {
	r := chi.NewRouter()

	// ── Global middleware ─────────────────────────────────────────────────
	r.Use(
		chimiddleware.RequestID, // injects X-Request-ID into every request
		// Security: TrustedProxyRealIP sets r.RemoteAddr from X-Forwarded-For only
		// when the TCP peer matches a configured trusted-proxy CIDR. This prevents
		// internet clients from forging their IP to bypass rate limiting.
		ratelimit.TrustedProxyRealIP(deps.TrustedProxyCIDRs),
		chimiddleware.Logger,    // structured request/response logging
		chimiddleware.Recoverer, // turns panics into 500 responses
	)

	// Security: defence-in-depth response headers. Applied unconditionally so
	// every response from this server carries them regardless of route or auth state.
	//   X-Content-Type-Options: prevents MIME-sniffing attacks where browsers
	//     reinterpret a JSON response as an executable type.
	//   X-Frame-Options: prevents clickjacking by disallowing this app from being
	//     embedded in a <frame> or <iframe> on a third-party origin.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			next.ServeHTTP(w, r)
		})
	})

	// Security: CORS middleware restricts cross-origin requests to the configured
	// allow-list. Must be applied before route registration so preflight OPTIONS
	// requests are handled globally.
	c := cors.New(cors.Options{
		AllowedOrigins:   deps.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type"},
		AllowCredentials: true,
		MaxAge:           300,
	})
	r.Use(c.Handler)

	// Security: Strict-Transport-Security enforces TLS for all future requests.
	// Only injected when cfg.HTTPSEnabled is true to avoid breaking plain-HTTP
	// development environments.
	if deps.HTTPSEnabled {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
				next.ServeHTTP(w, r)
			})
		})
	}

	// ── Health ───────────────────────────────────────────────────────────
	// Rate limit: 3 req / min per IP, burst=3.
	// Protects the health endpoint from being used as a free amplifier or
	// enumeration vector while remaining invisible to legitimate monitoring
	// tools (load balancers, uptime checkers) that poll at most once every
	// few seconds.
	healthLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "health:ip:", 3.0/60, 3, 5*time.Minute)
	go healthLimiter.StartCleanup(ctx)
	r.With(healthLimiter.Limit).Get("/health", func(w http.ResponseWriter, r *http.Request) {
		respond.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// ── Docs (gated) ──────────────────────────────────────────────────────
	if deps.DocsEnabled {
		// TODO(#1): serve docs/openapi.yaml and docs/index.html once files exist.
		r.Get("/docs/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, "docs/openapi.yaml")
		})
		r.Get("/docs", func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, "docs/index.html")
		})
	}

	// ── API v1 ────────────────────────────────────────────────────────────
	r.Route("/api/v1", func(r chi.Router) {
		r.Mount("/auth", auth.Routes(ctx, deps))
	})

	return r
}
