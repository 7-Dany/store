package server

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/bitcoin/rpc"
	"github.com/7-Dany/store/backend/internal/platform/bitcoin/zmq"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/respond"
)

// healthCheckTimeout is the per-probe deadline. All probes run in parallel
// so the overall handler latency is bounded by this value, not multiplied by
// the number of services checked.
const healthCheckTimeout = 2 * time.Second

// ── Wire types ────────────────────────────────────────────────────────────────

// serviceProbe is the JSON shape for one service in the health response.
// Field names match the frontend's ServicePingResult interface exactly so the
// health-ping.ts parser can unmarshal without any field mapping.
type serviceProbe struct {
	Name      string `json:"name"`
	Status    string `json:"status"`               // "up" | "down"
	LatencyMs *int64 `json:"latency_ms,omitempty"` // nil for in-process checks (ZMQ)
	Detail    string `json:"detail,omitempty"`
}

// healthBody is the full JSON response body for GET /api/v1/health?ping=true.
type healthBody struct {
	Status   string         `json:"status"` // "ok" | "degraded"
	Pong     bool           `json:"pong"`
	Services []serviceProbe `json:"services"`
}

// ── Handler factory ───────────────────────────────────────────────────────────

// handleHealth returns the health check handler.
//
// Without ?ping=true it responds with {"status":"ok"} for load-balancer probes
// that do not need individual service detail.
//
// With ?ping=true it runs all service probes in parallel (bounded by
// healthCheckTimeout) and returns the full serviceProbe list. The overall HTTP
// status is 200 when all services are up, 503 when any is down so that
// load-balancers that check the status code get the correct signal.
func handleHealth(deps *app.Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Fast path: load-balancer / uptime probe without service detail.
		if r.URL.Query().Get("ping") != "true" {
			respond.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
			return
		}

		// All probes share one deadline so the handler never blocks longer
		// than healthCheckTimeout regardless of how many services are checked.
		ctx, cancel := context.WithTimeout(r.Context(), healthCheckTimeout)
		defer cancel()

		probes := runProbes(ctx, deps)

		allUp := true
		for _, p := range probes {
			if p.Status == "down" {
				allUp = false
				break
			}
		}

		status := "ok"
		httpCode := http.StatusOK
		if !allUp {
			status = "degraded"
			httpCode = http.StatusServiceUnavailable
		}

		respond.JSON(w, httpCode, healthBody{
			Status:   status,
			Pong:     true,
			Services: probes,
		})
	}
}

// ── Parallel probe runner ─────────────────────────────────────────────────────

// runProbes executes every enabled probe concurrently and returns the results
// in a deterministic order: Database → Redis → Bitcoin ZMQ → Bitcoin RPC.
func runProbes(ctx context.Context, deps *app.Deps) []serviceProbe {
	type task struct {
		idx int
		fn  func() serviceProbe
	}

	tasks := []task{
		{0, func() serviceProbe { return probeDB(ctx, deps.Pool) }},
		{1, func() serviceProbe { return probeRedis(ctx, deps.KVStore) }},
	}
	if deps.BitcoinEnabled && deps.BitcoinZMQ != nil {
		tasks = append(tasks, task{2, func() serviceProbe { return probeZMQ(deps.BitcoinZMQ) }})
	}
	if deps.BitcoinEnabled && deps.BitcoinRPC != nil {
		tasks = append(tasks, task{3, func() serviceProbe { return probeRPC(ctx, deps.BitcoinRPC) }})
	}

	out := make([]serviceProbe, len(tasks))
	var wg sync.WaitGroup
	wg.Add(len(tasks))
	for i, t := range tasks {
		i, t := i, t
		go func() {
			defer wg.Done()
			out[i] = t.fn()
		}()
	}
	wg.Wait()
	return out
}

// ── Individual probes ─────────────────────────────────────────────────────────

// probeDB pings the PostgreSQL pool with a single round-trip.
// pgxpool.Pool.Ping acquires a connection, issues a lightweight query, and
// returns it — latency reflects end-to-end DB connectivity from the server.
func probeDB(ctx context.Context, pool *pgxpool.Pool) serviceProbe {
	start := time.Now()
	err := pool.Ping(ctx)
	ms := time.Since(start).Milliseconds()
	if err != nil {
		return serviceProbe{Name: "Database", Status: "down", LatencyMs: &ms, Detail: err.Error()}
	}
	return serviceProbe{Name: "Database", Status: "up", LatencyMs: &ms}
}

// probeRedis issues a lightweight Exists call against a sentinel key.
//
// ErrNotFound means Redis replied (key absent → healthy).
// Any other error means Redis is unreachable or returned an unexpected response.
//
// If deps.KVStore is InMemoryStore (Redis disabled), Exists always succeeds,
// which correctly reflects that the in-process store is always available.
func probeRedis(ctx context.Context, store kvstore.Store) serviceProbe {
	start := time.Now()
	_, err := store.Exists(ctx, "_health_probe")
	ms := time.Since(start).Milliseconds()
	if err != nil && err != kvstore.ErrNotFound {
		return serviceProbe{Name: "Redis", Status: "down", LatencyMs: &ms, Detail: err.Error()}
	}
	return serviceProbe{Name: "Redis", Status: "up", LatencyMs: &ms}
}

// probeZMQ queries the subscriber's in-process liveness state — no network I/O.
// IsConnected returns false when either connection's last dial failed or the
// most recently received block is older than the configured idle timeout.
//
// LastSeenHash is surfaced in Detail so the dashboard can show the chain tip
// without an extra RPC call.
func probeZMQ(sub zmq.Subscriber) serviceProbe {
	if !sub.IsConnected() {
		return serviceProbe{
			Name:   "Bitcoin ZMQ",
			Status: "down",
			Detail: "ZMQ subscriber disconnected or idle",
		}
	}
	detail := "Connected to Bitcoin Core"
	if h := sub.LastSeenHash(); h != "" {
		detail = "Last block: " + h[:8] + "…"
	}
	return serviceProbe{Name: "Bitcoin ZMQ", Status: "up", Detail: detail}
}

// probeRPC calls GetBlockchainInfo — the canonical RPC connectivity probe used
// at startup and by the InfraPoller. This call also flips the
// bitcoin_rpc_connected Prometheus gauge inside the client so telemetry stays
// consistent with the health response at zero extra cost.
func probeRPC(ctx context.Context, client rpc.Client) serviceProbe {
	start := time.Now()
	info, err := client.GetBlockchainInfo(ctx)
	ms := time.Since(start).Milliseconds()
	if err != nil {
		return serviceProbe{Name: "Bitcoin RPC", Status: "down", LatencyMs: &ms, Detail: err.Error()}
	}
	detail := "Chain: " + info.Chain
	if info.Blocks > 0 {
		detail += " · block " + intToStr(info.Blocks)
	}
	return serviceProbe{Name: "Bitcoin RPC", Status: "up", LatencyMs: &ms, Detail: detail}
}

// intToStr converts a non-negative int to its decimal string without pulling in
// strconv for a single use in this file.
func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
