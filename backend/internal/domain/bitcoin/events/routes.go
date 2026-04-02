// Package events registers the bitcoin SSE endpoints.
package events

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/bitcoin/rpc"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
)

// rpcHealthChecker wraps rpc.Client to satisfy the RPCHealthChecker interface
// required by Service.SetRPCHealthCheck. It issues a GetBlockchainInfo call
// with a short per-call timeout so the /events/status handler is never blocked
// by a slow or unresponsive Bitcoin Core node.
type rpcHealthChecker struct {
	client  rpc.Client
	timeout time.Duration
}

// IsRPCHealthy returns true when GetBlockchainInfo succeeds within the timeout.
// Any error (network, timeout, RPC) maps to false — the caller treats this as
// a transient disconnection and logs the metric accordingly.
func (h *rpcHealthChecker) IsRPCHealthy() bool {
	ctx, cancel := context.WithTimeout(context.Background(), h.timeout)
	defer cancel()
	_, err := h.client.GetBlockchainInfo(ctx)
	return err == nil
}

// Routes registers the bitcoin events endpoints on r:
//
//	POST /events/token   — issue one-time SSE JWT (JWTAuth required)
//	GET  /events         — open SSE stream (cookie auth, origin-gated)
//	GET  /events/status  — health snapshot (JWTAuth required)
//
// Panics at startup if deps.KVStore does not implement kvstore.OnceStore or
// kvstore.AtomicCounterStore — both are required and only satisfied by the
// Redis backend. This is the correct failure mode; bitcoin requires Redis.
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	if !deps.BitcoinEnabled {
		return
	}

	// Security guard: the SSE cookie must be Secure on mainnet.
	// A non-Secure cookie on mainnet would transmit the SSE JWT in plaintext.
	if !deps.SecureCookies && deps.BitcoinNetwork == "mainnet" {
		panic("bitcoin/events: BTC_SECURE_COOKIES must be true when network=mainnet")
	}

	// Build the allowed-origins map, filtering out blank entries.
	// The panic guard is placed AFTER filtering so a slice of all-empty strings
	// (e.g. ALLOWED_ORIGINS="") is caught just as reliably as a nil slice.
	allowedOrigins := make(map[string]struct{}, len(deps.AllowedOrigins))
	for _, o := range deps.AllowedOrigins {
		if o != "" {
			allowedOrigins[o] = struct{}{}
		}
	}
	if len(allowedOrigins) == 0 {
		panic("bitcoin/events: AllowedOrigins contains no non-empty entries — set ALLOWED_ORIGINS")
	}

	// Startup guard: warn when on mainnet with no trusted proxy CIDRs configured.
	// Without TrustedProxyRealIP middleware the IP rate limiters key on r.RemoteAddr
	// (the reverse-proxy address), making per-IP limits ineffective and allowing
	// a single client to exhaust all rate-limit buckets via a shared proxy IP.
	if deps.BitcoinNetwork == "mainnet" && len(deps.TrustedProxyCIDRs) == 0 {
		log.Warn(ctx, "bitcoin/events: no TrustedProxyCIDRs configured on mainnet — "+
			"IP rate limiters will key on RemoteAddr; set TrustedProxyCIDRs to fix")
	}

	// Type-assert KVStore to the interfaces Store/Service require.
	// Both are implemented by *kvstore.RedisStore; panics at startup if Redis
	// is not configured, consistent with the watch domain pattern.
	onceKV := deps.KVStore.(kvstore.OnceStore)
	counterKV := deps.KVStore.(kvstore.AtomicCounterStore)

	// Single shared Store instance for both Service and MempoolTracker.
	// Both components need the same Redis client and DB pool; a single Store
	// avoids the duplicate db.Queries allocation that two NewStore calls create.
	eventsStore := NewStore(onceKV, deps.Pool)

	cfg := EventsConfig{
		TokenTTL:                 deps.BitcoinSSETokenTTL,
		SigningSecret:            deps.BitcoinSSESigningSecret,
		SessionSecret:            deps.BitcoinSessionSecret,
		ServerSecret:             deps.BitcoinServerSecret,
		DailyRotationKey:         deps.BitcoinDailyRotationKey,
		Network:                  deps.BitcoinNetwork,
		BindIP:                   deps.BitcoinSSETokenBindIP,
		MaxSSEPerUser:            deps.BitcoinMaxSSEPerUser,
		MaxSSEProcess:            deps.BitcoinMaxSSEProcess,
		BlockRPCTimeout:          time.Duration(deps.BitcoinBlockRPCTimeoutSeconds) * time.Second,
		PendingMempoolMaxSize:    deps.BitcoinPendingMempoolMaxSize,
		MempoolPendingMaxAgeDays: deps.BitcoinMempoolPendingMaxAgeDays,
	}

	broker := NewBroker(cfg.MaxSSEProcess, deps.Metrics)

	connCounter := ratelimit.NewConnectionCounter(
		counterKV,
		ratelimit.DefaultBTCSSEConnKeyPrefix,
		cfg.MaxSSEPerUser,
		2*time.Hour, // safety TTL — auto-expires counter on process crash
		deps.Metrics,
	)

	svc := NewService(ctx, eventsStore, broker, connCounter, deps.Metrics, deps.BitcoinZMQ, cfg)

	// Wire the RPC health checker so Service.Status can populate RPCConnected.
	// Cap the per-call timeout at 2s so a slow node does not block the status handler.
	healthTimeout := cfg.BlockRPCTimeout
	if healthTimeout == 0 || healthTimeout > 2*time.Second {
		healthTimeout = 2 * time.Second
	}
	svc.SetRPCHealthCheck(&rpcHealthChecker{client: deps.BitcoinRPC, timeout: healthTimeout})

	// ── Mempool tracker ───────────────────────────────────────────────────────
	// Re-use the same eventsStore rather than creating a second identical Store.
	// Pass deps.Metrics so the tracker can emit pendingMempool size / drop / prune
	// metrics directly.
	tracker := NewMempoolTracker(ctx, eventsStore, broker, deps.BitcoinRPC, deps.Metrics, cfg)
	// Register ZMQ handlers BEFORE btcSub.Run(ctx) launches in server.go.
	deps.BitcoinZMQ.RegisterRawTxHandler(tracker.HandleRawTxEvent)
	deps.BitcoinZMQ.RegisterBlockHandler(tracker.HandleBlockEvent)

	// Expose a combined shutdown func so server.go can drain both the service
	// goroutines and the mempool tracker's pruning goroutine in the correct order
	// (after ZMQ drain, before pool close).
	deps.BitcoinEventsShutdown = func() {
		svc.Shutdown()
		tracker.Shutdown()
	}

	h := NewHandler(svc, deps.Metrics, allowedOrigins, deps.BitcoinNetwork, deps.SecureCookies, cfg)

	// Rate limiters (events-technical.md §12).
	// NOTE: TrustedProxyRealIP middleware (applied at the root router level in
	// server.go) rewrites r.RemoteAddr to the real client IP before these
	// limiters run, so per-IP buckets correctly key on the originating client
	// rather than the reverse-proxy address.
	// Cleanup goroutines are started per-limiter; they exit when ctx is cancelled.
	tokenLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "btc:token:ip:", 5.0/60, 5, 1*time.Minute)
	eventsLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "btc:events:ip:", 5.0/60, 5, 1*time.Minute)
	statusLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "btc:status:ip:", 20.0/60, 20, 1*time.Minute)
	go tokenLimiter.StartCleanup(ctx)
	go eventsLimiter.StartCleanup(ctx)
	go statusLimiter.StartCleanup(ctx)

	// POST /events/token — rate limit BEFORE JWTAuth: IP rejection is cheaper
	// than JWT parsing and prevents exhausting JWT validation budget.
	r.With(tokenLimiter.Limit, deps.JWTAuth).Post("/events/token", h.IssueToken)

	// GET /events — rate limit only; cookie auth is handled inside the handler
	// (step 3 of the guard sequence). Browser EventSource cannot send headers.
	r.With(eventsLimiter.Limit).Get("/events", h.Events)

	// GET /events/status — rate limit BEFORE JWTAuth.
	r.With(statusLimiter.Limit, deps.JWTAuth).Get("/events/status", h.Status)
}
