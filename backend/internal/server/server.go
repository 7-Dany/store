// Package server builds the HTTP server and owns every shared infrastructure
// object (database pool, KV store, mailer, mail queue). It is the only place
// in the application where those objects are created and their lifecycles
// managed.
package server

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/config"
	"github.com/7-Dany/store/backend/internal/platform/crypto"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/mailer"
	"github.com/7-Dany/store/backend/internal/db"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
	"github.com/7-Dany/store/backend/internal/platform/rbac"
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
	// ── Database pool ─────────────────────────────────────────────────────
	pool, err := newPool(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("server: database pool: %w", err)
	}

	// ── KV store (rate-limiters + JTI blocklist) ──────────────────────────
	kvStore, err := newKVStore(cfg)
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

	// ── RBAC checker ─────────────────────────────────────────────────────
	rbacChecker := rbac.NewChecker(db.New(pool))

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
		// ApprovalSubmitter: assigned in Phase 10 when requests domain is ready.
		OAuth: app.OAuthConfig{
			GoogleClientID:     cfg.GoogleClientID,
			GoogleClientSecret: cfg.GoogleClientSecret,
			GoogleRedirectURI:  cfg.GoogleRedirectURI,
			SuccessURL:         cfg.OAuthSuccessURL,
			ErrorURL:           cfg.OAuthErrorURL,
			TelegramBotToken:   cfg.TelegramBotToken,
		},
	}

	// ── HTTP server ───────────────────────────────────────────────────────
	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      newRouter(ctx, deps),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Shutdown order matters and must not be changed:
	//   1. q.Shutdown() — drains the mail queue first; in-flight mailer goroutines
	//      may call DB-backed operations, so the pool must still be open.
	//   2. pool.Close() — safe only after the queue is drained.
	// kvStore.Close() is handled separately via the ctx.Done() goroutine above
	// and may fire before cleanup() is called; that is intentional because the
	// KV store is not used by in-flight pool operations.
	cleanup := func() {
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

func newKVStore(cfg *config.Config) (kvstore.Store, error) {
	if cfg.RedisURL != "" {
		s, err := kvstore.NewRedisStore(cfg.RedisURL)
		if err == nil {
			return s, nil
		}
		slog.Warn("server: Redis unavailable, falling back to in-memory KV store", "error", err)
	}
	return kvstore.NewInMemoryStore(5 * time.Minute), nil
}
