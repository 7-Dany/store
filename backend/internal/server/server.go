// Package server builds the HTTP server and owns every shared infrastructure
// object (database pool, KV store, mailer, mail queue). It is the only place
// in the application where those objects are created and their lifecycles
// managed.
package server

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/config"
	"github.com/7-Dany/store/backend/internal/db"
	"github.com/7-Dany/store/backend/internal/platform/bitcoin/rpc"
	"github.com/7-Dany/store/backend/internal/platform/bitcoin/zmq"
	"github.com/7-Dany/store/backend/internal/platform/crypto"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/mailer"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
	"github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// log is the package-level structured logger. All records carry component="server".
var log = telemetry.New("server")

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
		log.Error(ctx, "database pool init failed", "error", err)
		return nil, nil, err
	}

	// ── KV store (rate-limiters + JTI blocklist) ──────────────────────────
	kvStore, err := newKVStore(ctx, cfg)
	if err != nil {
		pool.Close()
		log.Error(ctx, "kv store init failed", "error", err)
		return nil, nil, err
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
		log.Error(ctx, "mailer init failed", "error", err)
		return nil, nil, err
	}

	// ── Mail queue ────────────────────────────────────────────────────────
	q := mailer.NewQueue()
	if err := q.Start(cfg.MailWorkers); err != nil {
		pool.Close()
		kvStore.Close() //nolint:errcheck
		log.Error(ctx, "mail queue start failed", "error", err)
		return nil, nil, err
	}

	// ── Trusted proxy CIDRs ──────────────────────────────────────────────
	trustedCIDRs, err := ratelimit.ParseTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		q.Shutdown()
		pool.Close()
		kvStore.Close() //nolint:errcheck
		log.Error(ctx, "trusted proxies parse failed", "error", err)
		return nil, nil, err
	}

	// ── Token encryptor ───────────────────────────────────────────────────
	keyBytes, err := hex.DecodeString(cfg.TokenEncryptionKey)
	if err != nil {
		q.Shutdown()
		pool.Close()
		kvStore.Close() //nolint:errcheck
		log.Error(ctx, "token encryption key decode failed", "error", err)
		return nil, nil, err
	}
	encryptor, err := crypto.New(keyBytes)
	if err != nil {
		q.Shutdown()
		pool.Close()
		kvStore.Close() //nolint:errcheck
		log.Error(ctx, "crypto encryptor init failed", "error", err)
		return nil, nil, err
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
		log.Error(ctx, "JWT config validation failed", "error", err)
		return nil, nil, err
	}

	// ── RBAC checker ─────────────────────────────────────────────────────
	rbacChecker := rbac.NewChecker(db.New(pool))

	// ── Bitcoin infrastructure ────────────────────────────────────────────
	// Both the ZMQ subscriber and the RPC client are constructed only when
	// BTC_ENABLED=true. They are nil in app.Deps when bitcoin is disabled and
	// all bitcoin domain code guards on BitcoinEnabled before use.
	var btcSub zmq.Subscriber
	var btcRPC rpc.Client // interface — nil zero value is safe when !BitcoinEnabled

	if cfg.BitcoinEnabled {
		// 1. ZMQ subscriber — must be live before HTTP server accepts traffic
		//    so domain handlers can register event handlers during wiring.
		var zmqErr error
		btcSub, zmqErr = newBitcoinZMQ(cfg, registry)
		if zmqErr != nil {
			q.Shutdown()
			pool.Close()
			kvStore.Close() //nolint:errcheck
			log.Error(ctx, "bitcoin ZMQ subscriber init failed", "error", zmqErr)
			return nil, nil, zmqErr
		}

		// 2. RPC client — constructed and probed before the server accepts traffic.
		var rpcErr error
		btcRPC, rpcErr = newBitcoinRPC(ctx, cfg, registry)
		if rpcErr != nil {
			btcSub.Shutdown()
			q.Shutdown()
			pool.Close()
			kvStore.Close() //nolint:errcheck
			log.Error(ctx, "bitcoin RPC client init failed", "error", rpcErr)
			return nil, nil, rpcErr
		}
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
		MaintenanceMode:     cfg.MaintenanceMode,
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
		BitcoinEnabled:                  cfg.BitcoinEnabled,
		BitcoinZMQ:                      btcSub,
		BitcoinRPC:                      btcRPC,
		BitcoinNetwork:                  cfg.BitcoinNetwork,
		BitcoinMaxWatchPerUser:          cfg.BitcoinMaxWatchPerUser,
		BitcoinAuditHMACKey:             cfg.BitcoinAuditHMACKey,
		BitcoinSSESigningSecret:         cfg.BitcoinSSESigningSecret,
		BitcoinSessionSecret:            cfg.BitcoinSessionSecret,
		BitcoinServerSecret:             cfg.BitcoinServerSecret,
		BitcoinDailyRotationKey:         cfg.BitcoinDailyRotationKey,
		BitcoinSSETokenTTL:              time.Duration(cfg.BitcoinSSETokenTTL) * time.Second,
		BitcoinMaxSSEPerUser:            cfg.BitcoinMaxSSEPerUser,
		BitcoinMaxSSEProcess:            cfg.BitcoinMaxSSEProcess,
		BitcoinSSETokenBindIP:           cfg.BitcoinSSETokenBindIP,
		BitcoinPendingMempoolMaxSize:    cfg.BitcoinPendingMempoolMaxSize,
		BitcoinMempoolPendingMaxAgeDays: cfg.BitcoinMempoolPendingMaxAgeDays,
		BitcoinBlockRPCTimeoutSeconds:   cfg.BitcoinBlockRPCTimeoutSeconds,
	}

	// ── Startup health check ─────────────────────────────────────────────────
	// In maintenance mode, run health checks to diagnose issues.
	if cfg.MaintenanceMode {
		if err := startupHealthCheck(ctx, deps, log); err != nil {
			log.Warn(ctx, "startup health check failed in maintenance mode", "error", err)
		}
	}

	// ── HTTP server ───────────────────────────────────────────────────────
	// newRouter calls events.Routes(), which calls RegisterDisplayTxHandler and
	// RegisterBlockHandler. btcSub.Run(ctx) must start AFTER this so all handlers
	// are registered before the subscriber begins delivering events (race fix).
	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      newRouter(ctx, deps, registry),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Launch ZMQ subscriber AFTER newRouter() so all ZMQ handlers are registered.
	if btcSub != nil {
		go func() {
			if err := btcSub.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				log.Error(ctx, "ZMQ subscriber abnormal exit", "error", err)
			}
		}()
	}

	// Shutdown order must not be changed — see zmq-technical.md §9:
	//   1. btcSub.Shutdown() — drain ZMQ handler goroutines (30 s ceiling).
	//      In-flight handlers may hold pool and KV store references; draining
	//      first prevents use-after-close panics in those handlers.
	//   2. btcRPC.Close() — drain the RPC keep-alive connection pool.
	//   3. q.Shutdown() — drain the mail queue. In-flight mailer goroutines
	//      may call DB-backed audit writes, so the pool must still be open.
	//   4. pool.Close() — safe only after the queue is drained.
	//
	// kvStore.Close() is driven by the ctx.Done() goroutine above and may fire
	// concurrently with cleanup(); that is intentional because KV store ops
	// (rate limiters) are not part of in-flight request work at this point.
	//
	// This func is called by main.go AFTER srv.Shutdown() returns, so all
	// HTTP handler contexts are already cancelled before it executes.
	cleanup := func() {
		// Shutdown order (zmq-technical.md §9 + events domain):
		//   1. btcSub.Shutdown() — drain ZMQ handler goroutines first;
		//      in-flight handlers hold pool/KV references.
		if btcSub != nil {
			btcSub.Shutdown()
		}
		//   2. BitcoinEventsShutdown — drain service goroutines (liveness + heartbeat);
		//      safe now because ZMQ handlers (which call service methods) are drained.
		if deps.BitcoinEventsShutdown != nil {
			deps.BitcoinEventsShutdown()
		}
		//   3. btcRPC.Close() — drain the RPC keep-alive connection pool.
		if btcRPC != nil {
			btcRPC.Close(context.Background())
		}
		//   4. q.Shutdown() — drain mail queue (may do DB-backed audit writes).
		q.Shutdown()
		//   5. pool.Close() — safe only after the queue is drained.
		pool.Close()
	}

	return srv, cleanup, nil
}

// ── Private helpers ───────────────────────────────────────────────────────────

func newPool(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		log.Error(ctx, "parse database URL failed", "error", err)
		return nil, err
	}
	poolCfg.MaxConns = cfg.DBMaxConns
	poolCfg.MinConns = cfg.DBMinConns
	poolCfg.MaxConnLifetime = cfg.DBMaxConnLifetime
	poolCfg.MaxConnIdleTime = cfg.DBMaxConnIdle
	poolCfg.HealthCheckPeriod = cfg.DBHealthCheck

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		log.Error(ctx, "database connect failed", "error", err)
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		log.Error(ctx, "database ping failed", "error", err)
		return nil, err
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
		log.Warn(ctx, "Redis unavailable, falling back to in-memory KV store", "error", err)
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
	return zmq.New(cfg.BitcoinZMQBlock, cfg.BitcoinZMQTx, cfg.BitcoinNetwork, idleTimeout, recorder)
}

// newBitcoinRPC constructs the RPC client and runs best-effort startup probes.
//
// The server ALWAYS starts even when Bitcoin Core is unreachable — the RPC
// client reconnects on every call and the domain layer handles transient
// errors. Only one case is fatal: a chain mismatch (BTC_NETWORK configured
// for testnet4 but node reports "main" or vice versa). That is a hard
// misconfiguration — allowing it would silently operate on the wrong network.
//
// Startup probe sequence (all non-fatal except chain mismatch):
//  1. GetBlockchainInfo — verifies connectivity and chain. Node unreachable →
//     WARN + return client. Chain mismatch → ERROR + return error (only fatal).
//  2. Pruned detection — INFO only.
//  3. GetWalletInfo — drives keypool gauge. Failure → WARN, server continues.
func newBitcoinRPC(ctx context.Context, cfg *config.Config, registry *telemetry.Registry) (rpc.Client, error) {
	client, err := rpc.New(
		cfg.BitcoinRPCHost,
		cfg.BitcoinRPCPort,
		cfg.BitcoinRPCUser,
		cfg.BitcoinRPCPass,
		registry, // *telemetry.Registry satisfies rpc.RPCRecorder structurally
	)
	if err != nil {
		// Construction only fails for invalid port — a config error, not a
		// connectivity error. config.validate() should have caught this first.
		log.Error(ctx, "bitcoin RPC client construction failed", "error", err)
		return nil, err
	}

	// Probe timeout: use configured value or fall back to 10 s.
	probeTimeout := time.Duration(cfg.BitcoinBlockRPCTimeoutSeconds) * time.Second
	if probeTimeout == 0 {
		probeTimeout = 10 * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	// ── 1. Connectivity + chain match ─────────────────────────────────────
	// GetBlockchainInfo also drives the bitcoin_rpc_connected gauge via
	// SetRPCConnected inside the client — no separate call needed.
	info, err := client.GetBlockchainInfo(probeCtx)
	if err != nil {
		// Node is down or unreachable — not fatal. The client will retry on
		// every subsequent call. Domain code handles ErrNodeUnreachable paths.
		log.Warn(ctx, "bitcoin node unreachable at startup — will retry on demand",
			"host", cfg.BitcoinRPCHost,
			"port", cfg.BitcoinRPCPort,
			"error", err,
		)
		return client, nil
	}

	// Chain mismatch is the only fatal probe failure — it is a hard
	// misconfiguration, not a transient error. Operating on the wrong network
	// would silently corrupt every address lookup and block scan.
	// Bitcoin Core reports chain names as:
	//   mainnet  → "main"
	//   testnet4 → "testnet4"  (NOT "test4" — verified against Bitcoin Core v27+)
	wantChain := map[string]string{
		"mainnet":  "main",
		"testnet4": "testnet4",
	}[cfg.BitcoinNetwork]
	if info.Chain != wantChain {
		log.Error(ctx, "bitcoin node chain mismatch — wrong node or wrong network config",
			"node_chain", info.Chain,
			"btc_network", cfg.BitcoinNetwork,
			"expected_chain", wantChain,
		)
		// Release the HTTP transport before returning. cleanup() is never called
		// when startup fails, so this is the only opportunity to drain connections.
		client.Close(context.Background())
		return nil, errors.New("bitcoin: node chain does not match BTC_NETWORK")
	}

	// ── 2. Pruned node detection ──────────────────────────────────────────
	if info.Pruned {
		log.Info(ctx, "running on pruned node — wallet RPC path active; txindex not required",
			"prune_height", info.PruneHeight,
			"chain", info.Chain,
		)
	}

	// ── 3. Keypool size check ─────────────────────────────────────────────
	walletInfo, err := client.GetWalletInfo(probeCtx)
	if err != nil {
		if rpc.IsNoWalletError(err) {
			// Sentinel — wallet not loaded yet. -1 distinguishes "no wallet" from
			// "wallet exists but pool exhausted" (0) and "bitcoin disabled" (null).
			registry.SetKeypoolSize(-1)
			log.Warn(ctx, "no Bitcoin wallet loaded — create one with: bitcoin-cli createwallet \"store\"")
		} else {
			log.Warn(ctx, "GetWalletInfo failed at startup — keypool size unknown", "error", err)
		}
	} else {
		registry.SetKeypoolSize(walletInfo.KeypoolSize)
		switch {
		case walletInfo.KeypoolSize < 10:
			log.Error(ctx, "keypool critically low — invoice creation will fail soon",
				"keypool_size", walletInfo.KeypoolSize,
			)
		case walletInfo.KeypoolSize < 100:
			log.Warn(ctx, "keypool below warning threshold — run keypoolrefill",
				"keypool_size", walletInfo.KeypoolSize,
			)
		default:
			log.Info(ctx, "RPC client ready",
				"chain", info.Chain,
				"blocks", info.Blocks,
				"pruned", info.Pruned,
				"keypool_size", walletInfo.KeypoolSize,
			)
		}
	}

	return client, nil
}
