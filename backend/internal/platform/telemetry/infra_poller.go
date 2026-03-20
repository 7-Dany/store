package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// DBStatsProvider is satisfied by *pgxpool.Pool. Defined here so packages that
// wire the poller do not need to import pgxpool directly.
type DBStatsProvider interface {
	Stat() *pgxpool.Stat
	Ping(ctx context.Context) error
}

// RedisStatsProvider is satisfied by *kvstore.RedisStore.
//
// PoolStats exposes connection pool counters for Prometheus gauges.
// Ping is called by the InfraPoller on every tick to actively probe Redis;
// this ensures failures are detected within one poll cycle (15 s) regardless
// of whether any request traffic is hitting Redis at the time.
type RedisStatsProvider interface {
	PoolStats() *redis.PoolStats
	Ping(ctx context.Context) error
}

// StartInfraPoller launches a background goroutine that polls DB pool stats,
// Redis pool stats, and process metrics every interval and writes them into
// the Family 4 gauges.
//
//   - db must not be nil.
//   - redisProvider may be nil; when nil, Redis gauges are not updated.
//   - interval is typically 15 * time.Second.
//
// The goroutine exits cleanly when ctx is cancelled. A recovered panic from
// within the poller is logged as an ERROR (fires app_errors_total) and the
// poller stops — triggering the InfraPollerDown alert within 60 seconds.
func (r *Registry) StartInfraPoller(
	ctx context.Context,
	db DBStatsProvider,
	redisProvider RedisStatsProvider,
	interval time.Duration,
) {
	go func() {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			// Log so TelemetryHandler fires app_errors_total and the
			// InfraPollerDown alert triggers within one scrape interval.
			slog.ErrorContext(ctx, "InfraPoller panicked and stopped — infra gauges are stale",
				"error",     &Fault{Op: "poller.tick", Layer: LayerWorker, Err: fmt.Errorf("panic: %v", rec)},
				"component", "infra_poller",
				"stack",     string(debug.Stack()),
			)
			// Do NOT restart: a crashing poller may have a resource leak or a
			// broken DB/Redis reference. The alert will page on-call.
		}()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.pollDB(ctx, db)
				if redisProvider != nil {
					r.pollRedis(ctx, redisProvider)
				}
				r.pollProcess()
				// Heartbeat — used by InfraPollerDown alert.
				r.infraPollerLastRunSeconds.SetToCurrentTime()
			}
		}
	}()
}

var infraLog = New("infra_poller")

// redisLog kept as alias so existing references compile without churn.
var redisLog = infraLog

func (r *Registry) pollDB(ctx context.Context, db DBStatsProvider) {
	// Active ping — db_up flips to 0 within one poll cycle when DB goes down.
	// Without this, the pool utilization drops to 0% on disconnection (nobody
	// can acquire connections), which looks "healthy" under the old >= 100% check.
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := db.Ping(pingCtx); err != nil {
		r.dbUp.Set(0)
		infraLog.Error(ctx, "DB ping failed", "error", Store("pollDB.ping", err))
	} else {
		r.dbUp.Set(1)
	}

	s := db.Stat()
	r.dbPoolTotal.Set(float64(s.TotalConns()))
	r.dbPoolIdle.Set(float64(s.IdleConns()))
	r.dbPoolAcquired.Set(float64(s.AcquiredConns()))
	r.dbPoolMax.Set(float64(s.MaxConns()))
}

func (r *Registry) pollRedis(ctx context.Context, rs RedisStatsProvider) {
	// Active ping with a tight deadline.
	// redis_up flips to 0 within one poll cycle when Redis goes down and back
	// to 1 within one poll cycle when it recovers — no time-window dependency.
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := rs.Ping(pingCtx); err != nil {
		r.redisUp.Set(0)
		// Also emit an error log so app_errors_total increments (secondary signal).
		redisLog.Error(ctx, "Redis ping failed", "error", KVStore("pollRedis.ping", err))
	} else {
		r.redisUp.Set(1)
	}

	// Pool stats — read after the ping so StaleConns reflects any connection
	// that was marked stale by the failed ping attempt above.
	s := rs.PoolStats()
	r.redisPoolTotal.Set(float64(s.TotalConns))
	r.redisPoolIdle.Set(float64(s.IdleConns))
	r.redisPoolStale.Set(float64(s.StaleConns))
}

func (r *Registry) pollProcess() {
	r.processGoroutines.Set(float64(runtime.NumGoroutine()))
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	r.processMemAlloc.Set(float64(ms.Alloc))
}
