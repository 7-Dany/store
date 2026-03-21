// Package server builds the HTTP server and owns every shared infrastructure
// object (database pool, KV store, mailer, mail queue). It is the only place
// in the application where those objects are created and their lifecycles
// managed.
package server

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/config"
	"github.com/7-Dany/store/backend/internal/db"
	"github.com/7-Dany/store/backend/internal/platform/bitcoin/zmq"
	"github.com/7-Dany/store/backend/internal/platform/crypto"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/mailer"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
	"github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// New constructs the shared infrastructure, wires every domain router, and
// returns a ready-to-serve *http.Server together with a cleanup function.
//
// The caller must invoke cleanup after the HTTP server has stopped accepting
// new requests (i.e. after srv.Shutdown returns) to flush the mail queue and
// release the database pool and KV store.
//
//	srv, cleanup, err := server.New(ctx, cfg)
//	defer cleanup()
//	srv.ListenAndServe()
func New(ctx context.Context, cfg *config.Config) (*http.Server, func(), error) {
	// ── Telemetry ─────────────────────────────────────────────────────────
	// Created first so SetDefault replaces the slog global before any
	// infrastructure error can be logged without fault enrichment.
	registry := telemetry.NewRegistry()
	telemetry.SetDefault(registry)

	// ── Database pool ─────────────────────────────────────────────────────
	pool, err := newPool(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("server: database pool: %w", err)
	}

	// ── KV store (rate-limiters + JTI blocklist) ──────────────────────────
	kvStore, err := newKVStore(ctx, cfg)
	if err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("server: kv store: %w", err)
	}
	// StartCleanup blocks until ctx is cancelled; run it in the background.
	go kvStore.StartCleanup(ctx)
	// Close the store once ctx is cancelled (graceful shutdown).
	go func() {
		<-ctx.Done()
		kvStore.Close() //nolint:errcheck — best-effort on shutdown
	}()

	// The KV store doubles as the JTI blocklist when the backend supports it
	// (Redis does; the in-memory fallback does not).
	var blocklist kvstore.TokenBlocklist
	if bl, ok := kvStore.(kvstore.TokenBlocklist); ok {
		blocklist = bl
	}

	// ── Infra poller ──────────────────────────────────────────────────────
	// Type-assert to RedisStatsProvider. When the in-memory fallback is active,
	// redisStats is nil and the poller skips Redis gauge updates — correct
	// behaviour; no false alert fires on a planned Redis fallback.
	var redisStats telemetry.RedisStatsProvider
	if rs, ok := kvStore.(telemetry.RedisStatsProvider); ok {
		redisStats = rs
	}
	registry.StartInfraPoller(ctx, pool, redisStats, 15*time.Second)

	// ── Mailer ────────────────────────────────────────────────────────────
	m, err := mailer.New(mailer.Config{
		Host:            cfg.SMTPHost,
		Port:            cfg.SMTPPort,
		Username:        cfg.SMTPUsername,
		Password:        cfg.SMTPPassword,
		From:            cfg.SMTPFrom,
		AppName:         cfg.AppName,
		OTPValidMinutes: cfg.OTPValidMinutes,
	})
	if err != nil {
		pool.Close()
		kvStore.Close() //nolint:errcheck
		return nil, nil, fmt.Errorf("server: mailer: %w", err)
	}

	// ── Mail queue ────────────────────────────────────────────────────────
	q := mailer.NewQueue()
	if err := q.Start(cfg.MailWorkers); err != nil {
		pool.Close()
		kvStore.Close() //nolint:errcheck
		return nil, nil, fmt.Errorf("server: mail queue: %w", err)
	}

	// ── Trusted proxy CIDRs ──────────────────────────────────────────────
	trustedCIDRs, err := ratelimit.ParseTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		q.Shutdown()
		pool.Close()
		kvStore.Close() //nolint:errcheck
		return nil, nil, fmt.Errorf("server: trusted proxies: %w", err)
	}

	// ── Token encryptor ───────────────────────────────────────────────────
	keyBytes, err := hex.DecodeString(cfg.TokenEncryptionKey)
	if err != nil {
		q.Shutdown()
		pool.Close()
		kvStore.Close() //nolint:errcheck
		return nil, nil, fmt.Errorf("server: decode token encryption key: %w", err)
	}
	encryptor, err := crypto.New(keyBytes)
	if err != nil {
		q.Shutdown()
		pool.Close()
		kvStore.Close() //nolint:errcheck
		return nil, nil, fmt.Errorf("server: crypto encryptor: %w", err)
	}

	// ── JWT ───────────────────────────────────────────────────────────────
	jwtCfg := token.JWTConfig{
		JWTAccessSecret:  cfg.JWTAccessSecret,
		JWTRefreshSecret: cfg.JWTRefreshSecret,
		AccessTTL:        cfg.AccessTokenTTL,
		SecureCookies:    !cfg.HTTPSDisabled,
	}
	// Validate at startup so misconfigurations (short/identical secrets, TTL ceiling,
	// insecure cookies in production) surface before the server accepts traffic.
	// isDev=cfg.HTTPSDisabled: HTTP-only local environments may use insecure cookies.
	if err := token.ValidateJWTConfig(jwtCfg, cfg.HTTPSDisabled); err != nil {
		q.Shutdown()
		pool.Close()
		kvStore.Close() //nolint:errcheck
		return nil, nil, fmt.Errorf("server: jwt config: %w", err)
	}

	// ── RBAC checker ─────────────────────────────────────────────────────
	rbacChecker := rbac.NewChecker(db.New(pool))

	// ── Bitcoin ZMQ subscriber ────────────────────────────────────────────
	// Constructed only when BTC_ENABLED=true. Run() is launched here so the
	// subscriber is live before the HTTP server accepts traffic — domain
	// routes register handlers during wiring and events are delivered as soon
	// as Bitcoin Core connects. btcSub is nil when bitcoin is disabled, and
	// all bitcoin dep fields in app.Deps are guarded by BitcoinEnabled.
	var btcSub zmq.Subscriber
	if cfg.BitcoinEnabled {
		var zmqErr error
		btcSub, zmqErr = newBitcoinZMQ(cfg, registry)
		if zmqErr != nil {
			q.Shutdown()
			pool.Close()
			kvStore.Close() //nolint:errcheck
			return nil, nil, fmt.Errorf("server: bitcoin zmq: %w", zmqErr)
		}
		// Run blocks until ctx is cancelled. Launch in a background goroutine
		// and log abnormal exits — do NOT call os.Exit or log.Fatal here;
		// the cleanup func must still run to drain the queue and close the pool.
		go func() {
			if err := btcSub.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("server: ZMQ subscriber abnormal exit", "error", err)
			}
		}()
	}

	// ── Shared deps ───────────────────────────────────────────────────────
	deps := &app.Deps{
		Pool:                pool,
		KVStore:             kvStore,
		Blocklist:           blocklist,
		Mailer:              m,
		MailQueue:           q,
		MailDeliveryTimeout: cfg.MailDeliveryTimeout,
		JWTConfig:           jwtCfg,
		JWTAuth:             token.Auth(cfg.JWTAccessSecret, blocklist, kvStore),
		SecureCookies:       !cfg.HTTPSDisabled,
		AllowedOrigins:      cfg.AllowedOrigins,
		TrustedProxyCIDRs:   trustedCIDRs,
		HTTPSEnabled:        cfg.HTTPSEnabled,
		OTPTokenTTL:         time.Duration(cfg.OTPValidMinutes) * time.Minute,
		Encryptor:           encryptor,
		RBAC:                rbacChecker,
		BootstrapSecret:     cfg.BootstrapSecret,
		Metrics:             registry,
		// ApprovalSubmitter: assigned in Phase 10 when requests domain is ready.
		OAuth: app.OAuthConfig{
			GoogleClientID:     cfg.GoogleClientID,
			GoogleClientSecret: cfg.GoogleClientSecret,
			GoogleRedirectURI:  cfg.GoogleRedirectURI,
			SuccessURL:         cfg.OAuthSuccessURL,
			ErrorURL:           cfg.OAuthErrorURL,
			TelegramBotToken:   cfg.TelegramBotToken,
		},
		BitcoinEnabled: cfg.BitcoinEnabled,
		BitcoinZMQ:     btcSub,
		BitcoinNetwork: cfg.BitcoinNetwork,
	}

	// ── HTTP server ───────────────────────────────────────────────────────
	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      newRouter(ctx, deps, registry),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Shutdown order must not be changed — see zmq-technical.md §9:
	//   1. btcSub.Shutdown() — drain ZMQ handler goroutines (30 s ceiling).
	//      In-flight handlers may hold pool and KV store references; draining
	//      first prevents use-after-close panics in those handlers.
	//   2. q.Shutdown() — drain the mail queue. In-flight mailer goroutines
	//      may call DB-backed audit writes, so the pool must still be open.
	//   3. pool.Close() — safe only after the queue is drained.
	//
	// kvStore.Close() is driven by the ctx.Done() goroutine above and may fire
	// concurrently with cleanup(); that is intentional because KV store ops
	// (rate limiters) are not part of in-flight request work at this point.
	//
	// This func is called by main.go AFTER srv.Shutdown() returns, so all
	// HTTP handler contexts are already cancelled before it executes.
	cleanup := func() {
		if btcSub != nil {
			btcSub.Shutdown()
		}
		q.Shutdown()
		pool.Close()
	}

	return srv, cleanup, nil
}

// ── Private helpers ───────────────────────────────────────────────────────────

func newPool(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	poolCfg.MaxConns = cfg.DBMaxConns
	poolCfg.MinConns = cfg.DBMinConns
	poolCfg.MaxConnLifetime = cfg.DBMaxConnLifetime
	poolCfg.MaxConnIdleTime = cfg.DBMaxConnIdle
	poolCfg.HealthCheckPeriod = cfg.DBHealthCheck

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}

func newKVStore(ctx context.Context, cfg *config.Config) (kvstore.Store, error) {
	if cfg.RedisURL != "" {
		s, err := kvstore.NewRedisStore(cfg.RedisURL)
		if err == nil {
			return s, nil
		}
		// SetDefault has already been called, so slog.Default() IS the
		// TelemetryHandler — this warning gets request_id enrichment automatically.
		slog.WarnContext(ctx, "server: Redis unavailable, falling back to in-memory KV store", "error", err)
	}
	return kvstore.NewInMemoryStore(5 * time.Minute), nil
}

// newBitcoinZMQ constructs the ZMQ subscriber from config. It translates
// BTC_ZMQ_IDLE_TIMEOUT=0 to a network-appropriate default before calling
// zmq.New(), which rejects zero as a programming error (the default must be
// resolved here, not inside the platform package).
func newBitcoinZMQ(cfg *config.Config, recorder zmq.ZMQRecorder) (zmq.Subscriber, error) {
	// Translate idle timeout 0 → network default.
	// 0 means "use the default" at the config layer; zmq.New() rejects it
	// explicitly so misconfigured callers are caught immediately.
	idleTimeout := time.Duration(cfg.BitcoinZMQIdleTimeout) * time.Second
	if idleTimeout == 0 {
		if cfg.BitcoinNetwork == "mainnet" {
			// Mainnet produces one block roughly every 10 minutes.
			// 600 s gives a full block interval before the liveness gauge flips.
			idleTimeout = 600 * time.Second
		} else {
			// testnet4 produces blocks faster; 120 s is sufficient.
			idleTimeout = 120 * time.Second
		}
	}
	return zmq.New(cfg.BitcoinZMQBlock, cfg.BitcoinZMQTx, idleTimeout, recorder)
}
