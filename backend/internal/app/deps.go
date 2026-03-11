// Package app defines the shared runtime dependencies that are built once at
// startup and passed to every domain router.
//
// Keeping Deps in its own leaf package breaks the import cycle that would arise
// if domain packages imported internal/server (which imports them).
package app

import (
	"net"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/7-Dany/store/backend/internal/platform/crypto"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/mailer"
	"github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// Deps holds every shared service that domain routes may need.
// It is constructed once in server.New, with all services already started and
// ready to use. Each router reads only the fields it actually uses.
type Deps struct {
	// ── Database ──────────────────────────────────────────────────────────────
	// Pool is the shared pgx connection pool passed to every domain store.
	// Closed by the cleanup func returned from server.New, always after
	// MailQueue.Shutdown() to prevent in-flight DB-backed audit writes from failing.
	// Domain code must never call Pool.Close() directly.
	Pool *pgxpool.Pool

	// ── KV store & blocklist ──────────────────────────────────────────────────
	// KVStore is the single shared backend for all rate-limiters. Using one
	// instance means one Redis connection pool and one in-memory eviction
	// goroutine across the entire application.
	// Closed by server.New via a context-cancellation goroutine — domain code
	// must not call Close() directly.
	KVStore kvstore.Store
	// Security: a nil Blocklist means token revocation via BlockToken is silently
	// skipped; any route that calls Blocklist methods must nil-check before use
	// or risk a panic and a bypassed revocation. The in-memory backend does not
	// implement TokenBlocklist; only the Redis backend does.
	// Blocklist is derived from KVStore when the backend implements
	// kvstore.TokenBlocklist (Redis does; the in-memory fallback does not).
	// Routes that revoke tokens check for nil before use.
	Blocklist kvstore.TokenBlocklist

	// ── Mail ──────────────────────────────────────────────────────────────────
	// Mailer sends transactional email synchronously.
	// Prefer MailQueue for async delivery from request handlers.
	// Typed as *SMTPMailer so handlers can bind any Send*Email method directly
	// as a func value (e.g. deps.Mailer.SendVerificationEmail).
	Mailer *mailer.SMTPMailer
	// MailQueue is already started; handlers call Queue.Enqueue directly.
	// Shutdown is called by the cleanup func returned from server.New —
	// domain code must never call MailQueue.Shutdown() directly.
	MailQueue *mailer.Queue
	// MailDeliveryTimeout is the per-message deadline passed to the mailer;
	// zero means no timeout. Sourced from cfg.MailDeliveryTimeout in server.New.
	MailDeliveryTimeout time.Duration

	// ── JWT ───────────────────────────────────────────────────────────────────
	// JWTConfig holds the JWT secrets and TTL values used to initialise JWTAuth.
	// Both secrets are validated as non-empty, ≥32 bytes, and distinct by
	// config.validate() before Deps is assembled.
	JWTConfig token.JWTConfig
	// JWTAuth is token.Auth pre-wired with JWTConfig and Blocklist.
	// Apply as middleware on any route that requires a valid access token.
	JWTAuth func(http.Handler) http.Handler

	// ── HTTP ──────────────────────────────────────────────────────────────────
	// SecureCookies must be true in production; when false, the refresh-token
	// cookie is sent without the Secure attribute, allowing transmission over
	// plain HTTP. Derived from !cfg.HTTPSDisabled in server.New.
	// config.validate() rejects HTTPSDisabled=true when APP_ENV=production.
	SecureCookies bool
	// AllowedOrigins is the list of origins permitted by the CORS middleware.
	// Populated from cfg.AllowedOrigins in server.New().
	AllowedOrigins []string
	// TrustedProxyCIDRs is the set of proxy networks whose X-Forwarded-For
	// headers are trusted when determining the real client IP.
	TrustedProxyCIDRs []*net.IPNet
	// HTTPSEnabled controls HSTS header injection. When true, every response
	// carries Strict-Transport-Security.
	HTTPSEnabled bool
	// DocsEnabled controls whether the /docs routes are registered.
	DocsEnabled bool

	// ── OTP ───────────────────────────────────────────────────────────────────
	// OTPTokenTTL is the lifetime of every OTP token (email_verification,
	// password_reset, account_unlock). Derived from cfg.OTPValidMinutes in
	// server.New. Passed to every domain service that issues OTP tokens so that
	// OTP_VALID_MINUTES is the single source of truth — no hardcoded intervals
	// anywhere in the service or store layers.
	OTPTokenTTL time.Duration

	// ── RBAC ────────────────────────────────────────────────────────────────────
	// RBAC is the platform checker used to guard admin routes.
	// Constructed in server.New from the shared DB pool.
	RBAC *rbac.Checker
	// BootstrapSecret is the value of BOOTSTRAP_SECRET sourced from config.
	// Passed to bootstrap.Routes so the handler never reads os.Getenv directly.
	BootstrapSecret string
	// ApprovalSubmitter is nil until the requests domain is wired in Phase 10.
	// ApprovalGate returns 503 when this is nil — safe until then.
	ApprovalSubmitter rbac.ApprovalSubmitter
	// ConditionalEscalator is nil until the requests domain is wired in Phase 10.
	// Domain handlers that perform conditional escalation must nil-check before use.
	ConditionalEscalator rbac.ConditionalEscalator

	// ── Crypto ────────────────────────────────────────────────────────────────
	// Encryptor is the AES-256-GCM encryptor for OAuth tokens at rest.
	// It is pre-wired from TOKEN_ENCRYPTION_KEY at startup so the key is
	// validated on every deployment. See config.Config.TokenEncryptionKey for context.
	Encryptor *crypto.Encryptor

	// ── OAuth ─────────────────────────────────────────────────────────────────
	// OAuth holds the Google OAuth 2.0 config values threaded from config.Config.
	// Consumed by the oauth domain assembler to construct the provider and handler.
	OAuth OAuthConfig
}

// OAuthConfig holds the Google OAuth 2.0 configuration values.
type OAuthConfig struct {
	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURI  string
	SuccessURL         string
	ErrorURL           string
	// TelegramBotToken is the raw Telegram Bot API token used to derive the HMAC
	// secret key for Login Widget signature verification. Sourced from
	// TELEGRAM_BOT_TOKEN in config.Config and validated as non-empty at startup.
	TelegramBotToken string
}
