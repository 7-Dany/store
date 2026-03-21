// Package config loads and validates all application configuration from environment variables.
package config

import (
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Config holds every tunable setting for the application.
// Required fields are validated in Load; missing values cause a descriptive error.
// Optional fields carry documented defaults that are applied when the variable is absent.
type Config struct {
	// ── Server ────────────────────────────────────────────────
	// AppEnv is the deployment environment: "development", "staging", or
	// "production". Default: "development". Used to gate the Redis requirement.
	AppEnv string
	// Addr is the TCP address the HTTP server listens on. Default: ":8080".
	Addr string
	// AppName is the human-readable product name shown in emails and email subjects.
	// Sourced from APP_NAME (env). Default: "Store".
	// Leading/trailing whitespace and surrounding ASCII double-quotes are stripped
	// on load so APP_NAME="Vend" and APP_NAME=Vend both produce "Vend".
	// Must be non-empty after trimming; max 64 characters.
	AppName string
	// HTTPSEnabled enables the Strict-Transport-Security header.
	// Set true only when the app runs behind a TLS-terminating reverse proxy.
	HTTPSEnabled bool
	// HTTPSDisabled is the opt-out flag for secure cookies. When true, the
	// refresh-token cookie is sent without the Secure attribute — only set this
	// in known development/test environments. The default (false) keeps cookies
	// Secure-only, which is the safe production default.
	HTTPSDisabled bool
	// TrustedProxies is the raw comma-separated CIDR string forwarded to the
	// TrustedProxyRealIP middleware. An empty string means no proxy is trusted.
	TrustedProxies string
	// AllowedOrigins is the parsed list of CORS-allowed origins.
	// Sourced from ALLOWED_ORIGINS (comma-separated). Required.
	AllowedOrigins []string
	// MailWorkers is the number of async mail-delivery goroutines. Default: 4.
	MailWorkers int
	// MailDeliveryTimeout is the maximum time allowed for a single mail delivery
	// attempt. When an async mail worker or synchronous fallback sends email, the
	// operation is bounded by this timeout — if SMTP is unresponsive, the goroutine
	// is not blocked indefinitely. Default: 30s.
	MailDeliveryTimeout time.Duration

	// ── Redis ─────────────────────────────────────────────────
	// RedisURL is the Redis connection string used by rate-limiters in
	// multi-instance deployments (e.g. "redis://:password@localhost:6379/0").
	// Required when AppEnv != "development"; optional otherwise.
	RedisURL string

	// ── Database ──────────────────────────────────────────────
	// DatabaseURL is the full pgx connection string. Required.
	DatabaseURL string
	// DBMaxConns is the pool maximum. Default: 20.
	DBMaxConns int32
	// DBMinConns is the pool minimum. Default: 2.
	DBMinConns int32
	// DBMaxConnLifetime is how long a connection may be reused. Default: 30m.
	DBMaxConnLifetime time.Duration
	// DBMaxConnIdle is how long an idle connection is kept open. Default: 5m.
	DBMaxConnIdle time.Duration
	// DBHealthCheck is how often the pool pings idle connections. Default: 1m.
	DBHealthCheck time.Duration

	// ── SMTP ──────────────────────────────────────────────────
	// SMTPHost is the mail server hostname. Required.
	SMTPHost string
	// SMTPPort is 587 (STARTTLS), 465 (implicit TLS), or 25 (relay). Default: 587.
	SMTPPort int
	// SMTPUsername is the SMTP auth login. Required.
	SMTPUsername string
	// SMTPPassword is the SMTP auth secret. Required.
	SMTPPassword string
	// SMTPFrom is the RFC 5321 envelope sender address. Required.
	SMTPFrom string
	// OTPValidMinutes is the TTL shown in OTP emails AND used as the database
	// token expiry when creating one_time_tokens rows (email_verification,
	// password_reset, account_unlock). Default: 15.
	//
	// IMPORTANT: this value is also baked into the sqlc-generated SQL queries
	// (CreateEmailVerificationToken, CreatePasswordResetToken, CreateUnlockToken).
	// Changing it here without regenerating those queries only affects the email
	// display text — the DB TTL stays at whatever INTERVAL is in auth.sql.
	// After changing this value, update auth.sql and run `make sqlc`.
	//
	// Must not exceed 30 (the operational cap; the DB schema enforces 15 minutes
	// for email_verification tokens via chk_ott_ev_ttl_max, but password_reset
	// and account_unlock tokens accept up to 30 minutes).
	OTPValidMinutes int

	// ── JWT ───────────────────────────────────────────────────
	// JWTAccessSecret is the HMAC-SHA256 signing key exclusively for access tokens.
	// Must be at least 32 bytes of high-entropy random data in production.
	// Generate with: openssl rand -hex 32. Must differ from JWTRefreshSecret.
	// Required.
	JWTAccessSecret string
	// JWTRefreshSecret is the HMAC-SHA256 signing key exclusively for refresh tokens.
	// Keeping it separate from JWTAccessSecret means a compromised access-token key
	// cannot be used to forge refresh tokens, and each key can be rotated independently.
	// Must be at least 32 bytes and differ from JWTAccessSecret. Required.
	JWTRefreshSecret string
	// AccessTokenTTL is how long an access token remains valid. Default: 15m.
	// Keep short — access tokens are not server-side revokable.
	AccessTokenTTL time.Duration

	// ── Security ──────────────────────────────────────────────
	// TokenEncryptionKey is the 32-byte AES-256-GCM key used to encrypt
	// OAuth tokens at rest. Must be exactly 64 valid hex characters (32 bytes decoded).
	// Generate with: openssl rand -hex 32. Required.
	//
	// NOTE: This field pre-provisions the upcoming OAuth token-storage flow.
	// deps.Encryptor is wired at startup but is not yet consumed by any active route.
	// Operators must supply a valid key on every deployment in preparation for
	// that feature going live.
	TokenEncryptionKey string

	// ── RBAC ──────────────────────────────────────────────────
	// BootstrapSecret is the one-time passphrase that gates POST /owner/bootstrap.
	// A caller must supply this value in the request body to claim the owner role.
	// Use a high-entropy random string; generate with: openssl rand -hex 32.
	// Required (non-empty). The handler rejects every bootstrap attempt with 403
	// when the value does not match, but a missing env var is caught here at startup.
	BootstrapSecret string

	// ── OAuth ─────────────────────────────────────────────────
	// GoogleClientID is the OAuth 2.0 client ID issued by the Google Cloud Console.
	// Required.
	GoogleClientID string
	// GoogleClientSecret is the OAuth 2.0 client secret. Keep out of version control.
	// Required.
	GoogleClientSecret string
	// GoogleRedirectURI is the callback URL registered in the Google Cloud Console.
	// Must exactly match one of the authorised redirect URIs for the client.
	// Example: http://localhost:8080/api/v1/oauth/google/callback
	// Required.
	GoogleRedirectURI string
	// OAuthSuccessURL is the frontend URL the callback redirects to on success
	// (login, register, or link). Session cookies are set on the API domain before
	// the redirect — no query params are appended.
	// Example: http://localhost:3000/dashboard
	// Required.
	OAuthSuccessURL string
	// OAuthErrorURL is the frontend URL the callback redirects to on failure.
	// The handler appends ?error=<code> so the SPA can display a user-facing message.
	// Example: http://localhost:3000/login
	// Required.
	OAuthErrorURL string
	// TelegramBotToken is the Telegram Bot API token used to verify HMAC-SHA256
	// signatures on Login Widget payloads. Required when Telegram OAuth is enabled.
	// Generate at https://t.me/BotFather. Keep out of version control.
	TelegramBotToken string

	// ── Bitcoin ──────────────────────────────────────────────────────────────────────
	// BitcoinEnabled gates all bitcoin wiring. When false (default), no bitcoin
	// validation runs and all bitcoin deps are nil.
	BitcoinEnabled bool // BTC_ENABLED
	// BitcoinRPCHost is the Bitcoin Core RPC host. Default: "127.0.0.1".
	BitcoinRPCHost string // BTC_RPC_HOST
	// BitcoinRPCPort is the Bitcoin Core RPC port. Validated numeric 1–65535.
	BitcoinRPCPort string // BTC_RPC_PORT
	// BitcoinRPCUser is the RPC username. Required when bitcoin enabled.
	BitcoinRPCUser string // BTC_RPC_USER
	// BitcoinRPCPass is the RPC password. Required. NEVER log raw.
	BitcoinRPCPass string // BTC_RPC_PASS
	// BitcoinZMQBlock is the ZMQ endpoint for hashblock events. Default: "tcp://127.0.0.1:28332".
	BitcoinZMQBlock string // BTC_ZMQ_BLOCK
	// BitcoinZMQTx is the ZMQ endpoint for hashtx events. Default: "tcp://127.0.0.1:28333".
	BitcoinZMQTx string // BTC_ZMQ_TX
	// BitcoinZMQIdleTimeout is the ZMQ idle timeout seconds. 0 = use network default; valid range 30–3600.
	BitcoinZMQIdleTimeout int // BTC_ZMQ_IDLE_TIMEOUT
	// BitcoinNetwork is "testnet4" or "mainnet".
	BitcoinNetwork string // BTC_NETWORK
	// BitcoinSSETokenTTL is the SSE one-time token TTL in seconds. Range 1–300; default 60.
	BitcoinSSETokenTTL int // BTC_SSE_TOKEN_TTL
	// BitcoinSSETokenBindIP binds the SSE token to the issuing IP. Default: true.
	BitcoinSSETokenBindIP bool // BTC_SSE_TOKEN_BIND_IP
	// BitcoinSessionSecret is the HMAC key for bitcoin session cookies. ≥32 bytes.
	BitcoinSessionSecret string // BTC_SESSION_SECRET
	// BitcoinSSESigningSecret is the HMAC key for SSE tokens. ≥32 bytes; must differ from session secret.
	BitcoinSSESigningSecret string // BTC_SSE_SIGNING_SECRET
	// BitcoinMaxSSEPerUser is the per-user SSE connection cap. Range 1–10; default 3.
	BitcoinMaxSSEPerUser int // BTC_MAX_SSE_PER_USER
	// BitcoinMaxSSEProcess is the process-wide SSE connection cap. Range 10–10000; default 100.
	BitcoinMaxSSEProcess int // BTC_MAX_SSE_PROCESS
	// BitcoinMaxWatchPerUser is the per-user address watch cap. Range 1–1000; default 100.
	BitcoinMaxWatchPerUser int // BTC_MAX_WATCH_PER_USER
	// BitcoinCacheTTL is the block-data cache TTL in seconds. Range 1–60; default 5.
	BitcoinCacheTTL int // BTC_CACHE_TTL
	// BitcoinBlockRPCTimeoutSeconds is the per-RPC call timeout. Range 2–60; default 10.
	BitcoinBlockRPCTimeoutSeconds int // BTC_BLOCK_RPC_TIMEOUT_SECONDS
	// BitcoinHandlerTimeoutMs is the HTTP handler timeout in ms. Range 100–120000; default 30000.
	// Must satisfy: HandlerTimeoutMs > 2×BlockRPCTimeoutSeconds×1000 + 2000.
	BitcoinHandlerTimeoutMs int // BTC_HANDLER_TIMEOUT_MS
	// BitcoinPendingMempoolMaxSize is the max mempool pending tx count. Range 100–100000; default 10000.
	BitcoinPendingMempoolMaxSize int // BTC_PENDING_MEMPOOL_MAX_SIZE
	// BitcoinMempoolPendingMaxAgeDays is the max pending tx age in days. Range 1–90; default 14.
	BitcoinMempoolPendingMaxAgeDays int // BTC_MEMPOOL_PENDING_MAX_AGE_DAYS
	// BitcoinFallbackAuditLog is the path for the Bitcoin fallback audit log. Default: "" (stdout JSON).
	BitcoinFallbackAuditLog string // BTC_FALLBACK_AUDIT_LOG
	// BitcoinReconciliationStartHeight is the block height for reconciliation. ≥0; default 0.
	BitcoinReconciliationStartHeight int // BTC_RECONCILIATION_START_HEIGHT
	// BitcoinReconciliationCheckpointInterval is the checkpoint interval in blocks. Range 1–500; default 100.
	BitcoinReconciliationCheckpointInterval int // BTC_RECONCILIATION_CHECKPOINT_INTERVAL
	// BitcoinReconciliationAllowGenesisScan allows scanning from genesis on mainnet. Default: false.
	BitcoinReconciliationAllowGenesisScan bool // BTC_RECONCILIATION_ALLOW_GENESIS_SCAN
	// BitcoinAuditHMACKey is the HMAC key for audit PII obfuscation. ≥32 bytes; must differ from secrets.
	BitcoinAuditHMACKey string // BTC_AUDIT_HMAC_KEY
}

// Load reads every environment variable, applies defaults, validates required
// fields, and returns a populated *Config.
// Call this once at startup, immediately after godotenv.Load().
// Returns a descriptive error listing every missing required variable so the
// operator can fix them all in one restart.
func Load() (*Config, error) {
	cfg := &Config{
		// Server defaults
		AppEnv:              getEnv("APP_ENV", "development"),
		Addr:                getEnv("ADDR", ":8080"),
		AppName:             trimAppName(getEnv("APP_NAME", "Store")),
		HTTPSEnabled:        parseBoolEnv("HTTPS_ENABLED"),
		HTTPSDisabled:       parseBoolEnv("HTTPS_DISABLED"),
		TrustedProxies:      os.Getenv("TRUSTED_PROXIES"),
		MailWorkers:         getEnvInt("MAIL_WORKERS", 4),
		MailDeliveryTimeout: getEnvDuration("MAIL_DELIVERY_TIMEOUT", 30*time.Second),

		// Redis
		RedisURL: os.Getenv("REDIS_URL"),

		// Database defaults
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		DBMaxConns:        getEnvInt32("DB_MAX_CONNS", 20),
		DBMinConns:        getEnvInt32("DB_MIN_CONNS", 2),
		DBMaxConnLifetime: getEnvDuration("DB_MAX_CONN_LIFETIME", 30*time.Minute),
		DBMaxConnIdle:     getEnvDuration("DB_MAX_CONN_IDLE", 5*time.Minute),
		DBHealthCheck:     getEnvDuration("DB_HEALTH_CHECK", 1*time.Minute),

		// SMTP defaults
		SMTPHost:        os.Getenv("SMTP_HOST"),
		SMTPPort:        getEnvInt("SMTP_PORT", 587),
		SMTPUsername:    os.Getenv("SMTP_USERNAME"),
		SMTPPassword:    os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:        os.Getenv("SMTP_FROM"),
		OTPValidMinutes: getEnvInt("OTP_VALID_MINUTES", 15),

		// JWT defaults
		JWTAccessSecret:  os.Getenv("JWT_ACCESS_SECRET"),
		JWTRefreshSecret: os.Getenv("JWT_REFRESH_SECRET"),
		AccessTokenTTL:   getEnvDuration("ACCESS_TOKEN_TTL", 15*time.Minute),

		// Security
		TokenEncryptionKey: os.Getenv("TOKEN_ENCRYPTION_KEY"),

		// RBAC
		BootstrapSecret: os.Getenv("BOOTSTRAP_SECRET"),

		// OAuth
		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURI:  os.Getenv("GOOGLE_REDIRECT_URI"),
		OAuthSuccessURL:    os.Getenv("OAUTH_SUCCESS_URL"),
		OAuthErrorURL:      os.Getenv("OAUTH_ERROR_URL"),
		TelegramBotToken:   os.Getenv("TELEGRAM_BOT_TOKEN"),
	}

	// Bitcoin
	cfg.BitcoinEnabled = parseBoolEnv("BTC_ENABLED")
	cfg.BitcoinRPCHost = getEnv("BTC_RPC_HOST", "127.0.0.1")
	cfg.BitcoinRPCPort = getEnv("BTC_RPC_PORT", "8332")
	cfg.BitcoinRPCUser = os.Getenv("BTC_RPC_USER")
	cfg.BitcoinRPCPass = os.Getenv("BTC_RPC_PASS")
	cfg.BitcoinZMQBlock = getEnv("BTC_ZMQ_BLOCK", "tcp://127.0.0.1:28332")
	cfg.BitcoinZMQTx = getEnv("BTC_ZMQ_TX", "tcp://127.0.0.1:28333")
	cfg.BitcoinZMQIdleTimeout = getEnvInt("BTC_ZMQ_IDLE_TIMEOUT", 0)
	cfg.BitcoinNetwork = os.Getenv("BTC_NETWORK")
	cfg.BitcoinSSETokenTTL = getEnvInt("BTC_SSE_TOKEN_TTL", 60)
	cfg.BitcoinSSETokenBindIP = parseBoolEnvDefault("BTC_SSE_TOKEN_BIND_IP", true)
	cfg.BitcoinSessionSecret = os.Getenv("BTC_SESSION_SECRET")
	cfg.BitcoinSSESigningSecret = os.Getenv("BTC_SSE_SIGNING_SECRET")
	cfg.BitcoinMaxSSEPerUser = getEnvInt("BTC_MAX_SSE_PER_USER", 3)
	cfg.BitcoinMaxSSEProcess = getEnvInt("BTC_MAX_SSE_PROCESS", 100)
	cfg.BitcoinMaxWatchPerUser = getEnvInt("BTC_MAX_WATCH_PER_USER", 100)
	cfg.BitcoinCacheTTL = getEnvInt("BTC_CACHE_TTL", 5)
	cfg.BitcoinBlockRPCTimeoutSeconds = getEnvInt("BTC_BLOCK_RPC_TIMEOUT_SECONDS", 10)
	cfg.BitcoinHandlerTimeoutMs = getEnvInt("BTC_HANDLER_TIMEOUT_MS", 30000)
	cfg.BitcoinPendingMempoolMaxSize = getEnvInt("BTC_PENDING_MEMPOOL_MAX_SIZE", 10000)
	cfg.BitcoinMempoolPendingMaxAgeDays = getEnvInt("BTC_MEMPOOL_PENDING_MAX_AGE_DAYS", 14)
	cfg.BitcoinFallbackAuditLog = getEnv("BTC_FALLBACK_AUDIT_LOG", "")
	cfg.BitcoinReconciliationStartHeight = getEnvInt("BTC_RECONCILIATION_START_HEIGHT", 0)
	cfg.BitcoinReconciliationCheckpointInterval = getEnvInt("BTC_RECONCILIATION_CHECKPOINT_INTERVAL", 100)
	cfg.BitcoinReconciliationAllowGenesisScan = parseBoolEnv("BTC_RECONCILIATION_ALLOW_GENESIS_SCAN")
	cfg.BitcoinAuditHMACKey = os.Getenv("BTC_AUDIT_HMAC_KEY")

	// Parse ALLOWED_ORIGINS before validation so the required-field check
	// can include it.
	raw := os.Getenv("ALLOWED_ORIGINS")
	if raw != "" {
		for o := range strings.SplitSeq(raw, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				cfg.AllowedOrigins = append(cfg.AllowedOrigins, o)
			}
		}
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// TestDatabaseURL returns the DSN for the test database.
// Returns "" when TEST_DATABASE_URL is unset so TestMain guards of the form
// "if dsn != """ correctly skip integration tests in environments without a database.
//
// This function exists so test files never call os.Getenv directly.
// It must not be called from Load() or any production code path.
func TestDatabaseURL() string {
	return os.Getenv("TEST_DATABASE_URL")
}

// TestRedisURL returns the Redis URL for integration tests.
// It reads TEST_REDIS_URL first, then falls back to REDIS_URL.
// Returns an empty string when neither variable is set — callers should
// skip the test with t.Skip when the returned value is empty.
//
// This function exists so test files never call os.Getenv directly.
// It must not be called from Load() or any production code path.
func TestRedisURL() string {
	if v := os.Getenv("TEST_REDIS_URL"); v != "" {
		return v
	}
	return os.Getenv("REDIS_URL")
}

// validate checks that every required field is non-empty / non-zero.
// It accumulates all problems so the operator can fix them in one restart.
func (c *Config) validate() error {
	type required struct {
		name  string
		value string
	}
	fields := []required{
		{"DATABASE_URL", c.DatabaseURL},
		{"SMTP_HOST", c.SMTPHost},
		{"SMTP_USERNAME", c.SMTPUsername},
		{"SMTP_PASSWORD", c.SMTPPassword},
		{"SMTP_FROM", c.SMTPFrom},
		{"ALLOWED_ORIGINS", strings.Join(c.AllowedOrigins, ",")},
		{"JWT_ACCESS_SECRET", c.JWTAccessSecret},
		{"JWT_REFRESH_SECRET", c.JWTRefreshSecret},
		{"TOKEN_ENCRYPTION_KEY", c.TokenEncryptionKey},
		{"BOOTSTRAP_SECRET", c.BootstrapSecret},
		{"GOOGLE_CLIENT_ID", c.GoogleClientID},
		{"GOOGLE_CLIENT_SECRET", c.GoogleClientSecret},
		{"GOOGLE_REDIRECT_URI", c.GoogleRedirectURI},
		{"OAUTH_SUCCESS_URL", c.OAuthSuccessURL},
		{"OAUTH_ERROR_URL", c.OAuthErrorURL},
		{"TELEGRAM_BOT_TOKEN", c.TelegramBotToken},
	}

	var missing []string
	for _, f := range fields {
		if f.value == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf(
			"config: missing required environment variables: %s",
			strings.Join(missing, ", "),
		)
	}

	// AppEnv must be one of the three known deployment environments.
	// Rejecting unknown values catches typos like "prod" or "Staging" early,
	// before they silently bypass the Redis requirement or other env-gated logic.
	validEnvs := []string{"development", "staging", "production"}
	if !slices.Contains(validEnvs, c.AppEnv) {
		return fmt.Errorf(
			"config: APP_ENV must be one of %s, got %q",
			strings.Join(validEnvs, ", "), c.AppEnv,
		)
	}

	// Security: wildcard origin + AllowCredentials is a browser security hole.
	// rs/cors silently drops credentials when it sees '*'; catching it here
	// surfaces the misconfiguration at startup rather than at runtime.
	if slices.Contains(c.AllowedOrigins, "*") {
		return fmt.Errorf(
			"config: ALLOWED_ORIGINS must not contain '*' — " +
				"wildcard origins are forbidden because AllowCredentials is enabled; " +
				"list explicit origins instead (e.g. http://localhost:3000)",
		)
	}

	// AppName must be non-empty and within a sane display length.
	const appNameMaxLen = 64
	if c.AppName == "" {
		return fmt.Errorf("config: APP_NAME must not be empty — set it in the environment (e.g. APP_NAME=Acme)")
	}
	if len(c.AppName) > appNameMaxLen {
		return fmt.Errorf(
			"config: APP_NAME must not exceed %d characters; got %d",
			appNameMaxLen, len(c.AppName),
		)
	}

	if c.AppEnv != "development" && c.RedisURL == "" {
		return fmt.Errorf("config: REDIS_URL is required when APP_ENV=%q", c.AppEnv)
	}

	// Security: cookies without the Secure flag are transmitted over plain HTTP,
	// making refresh-token theft trivial. Reject this configuration at startup
	// rather than silently running insecure in production.
	if c.AppEnv == "production" && c.HTTPSDisabled {
		return fmt.Errorf("config: HTTPS_DISABLED must not be true when APP_ENV=production — " +
			"refresh-token cookies require TLS in production")
	}

	// SMTPPort must be one of the standard well-known mail submission ports.
	// 587 = STARTTLS (recommended), 465 = implicit TLS, 25 = relay (no auth).
	validSMTPPorts := []int{25, 465, 587}
	if !slices.Contains(validSMTPPorts, c.SMTPPort) {
		return fmt.Errorf("config: SMTP_PORT must be one of 25, 465, 587; got %d", c.SMTPPort)
	}

	// OTPValidMinutes must be positive and must not exceed the operational cap of
	// 30 minutes. Note that the DB schema enforces a stricter 15-minute cap for
	// email_verification tokens (chk_ott_ev_ttl_max); operators should keep
	// OTP_VALID_MINUTES ≤ 15 when email verification tokens are in use.
	const otpMaxMinutes = 30
	if c.OTPValidMinutes < 1 || c.OTPValidMinutes > otpMaxMinutes {
		return fmt.Errorf(
			"config: OTP_VALID_MINUTES must be between 1 and %d (the chk_ott_ev_ttl_max cap); got %d",
			otpMaxMinutes, c.OTPValidMinutes,
		)
	}

	if len(c.JWTAccessSecret) < 32 {
		return fmt.Errorf("config: JWT_ACCESS_SECRET must be at least 32 bytes")
	}
	if len(c.JWTRefreshSecret) < 32 {
		return fmt.Errorf("config: JWT_REFRESH_SECRET must be at least 32 bytes")
	}
	if c.JWTAccessSecret == c.JWTRefreshSecret {
		return fmt.Errorf("config: JWT_ACCESS_SECRET and JWT_REFRESH_SECRET must be distinct")
	}

	// Security: contradictory HTTPS flags would produce a server that both sends HSTS
	// headers (promising TLS) and issues non-Secure cookies — reject this combination.
	if c.HTTPSEnabled && c.HTTPSDisabled {
		return fmt.Errorf("config: HTTPS_ENABLED and HTTPS_DISABLED cannot both be true")
	}

	// Security: detect low-entropy JWT secrets. A secret whose characters are all
	// identical (e.g. 32 × 'a') satisfies the length check but is trivially
	// brute-forceable against HMAC-SHA256. Generate safe secrets with:
	//   openssl rand -hex 32
	if isLowEntropySecret(c.JWTAccessSecret) {
		return fmt.Errorf("config: JWT_ACCESS_SECRET has dangerously low entropy; generate with: openssl rand -hex 32")
	}
	if isLowEntropySecret(c.JWTRefreshSecret) {
		return fmt.Errorf("config: JWT_REFRESH_SECRET has dangerously low entropy; generate with: openssl rand -hex 32")
	}

	if c.MailWorkers < 1 {
		return fmt.Errorf("config: MAIL_WORKERS must be >= 1, got %d", c.MailWorkers)
	}
	if c.MailDeliveryTimeout <= 0 {
		return fmt.Errorf("config: MAIL_DELIVERY_TIMEOUT must be positive, got %s", c.MailDeliveryTimeout)
	}
	if c.AccessTokenTTL <= 0 {
		return fmt.Errorf("config: ACCESS_TOKEN_TTL must be positive, got %s", c.AccessTokenTTL)
	}
	if c.DBMaxConns < 1 {
		return fmt.Errorf("config: DB_MAX_CONNS must be >= 1, got %d", c.DBMaxConns)
	}
	if c.DBMinConns < 0 {
		return fmt.Errorf("config: DB_MIN_CONNS must be >= 0, got %d", c.DBMinConns)
	}
	if c.DBMinConns > c.DBMaxConns {
		return fmt.Errorf(
			"config: DB_MIN_CONNS (%d) must not exceed DB_MAX_CONNS (%d)",
			c.DBMinConns, c.DBMaxConns,
		)
	}
	if c.DBMaxConnLifetime <= 0 {
		return fmt.Errorf("config: DB_MAX_CONN_LIFETIME must be positive, got %s", c.DBMaxConnLifetime)
	}
	if c.DBMaxConnIdle <= 0 {
		return fmt.Errorf("config: DB_MAX_CONN_IDLE must be positive, got %s", c.DBMaxConnIdle)
	}
	if c.DBHealthCheck <= 0 {
		return fmt.Errorf("config: DB_HEALTH_CHECK must be positive, got %s", c.DBHealthCheck)
	}

	// Security: CRLF characters in the envelope sender address enable SMTP header
	// injection. Reject at startup rather than sanitising silently.
	if strings.ContainsAny(c.SMTPFrom, "\r\n") {
		return fmt.Errorf("config: SMTP_FROM must not contain CR or LF characters")
	}

	// Security: TOKEN_ENCRYPTION_KEY must decode to exactly 32 bytes for AES-256-GCM.
	// 32 bytes hex-encoded = 64 hex characters. Catching this at startup prevents a
	// silent runtime failure inside crypto.New the first time a token is encrypted.
	if len(c.TokenEncryptionKey) != 64 {
		return fmt.Errorf(
			"config: TOKEN_ENCRYPTION_KEY must be exactly 64 hex characters (32 bytes); "+
				"got %d characters — generate with: openssl rand -hex 32",
			len(c.TokenEncryptionKey),
		)
	}
	// Verify every character in TOKEN_ENCRYPTION_KEY is valid hexadecimal.
	// A 64-char string containing non-hex bytes (e.g. 'g', 'z') would pass the
	// length check above but fail later in server.New when hex.DecodeString is
	// called, breaking the fail-fast guarantee of validate().
	if _, err := hex.DecodeString(c.TokenEncryptionKey); err != nil {
		return fmt.Errorf(
			"config: TOKEN_ENCRYPTION_KEY contains non-hex characters — generate with: openssl rand -hex 32",
		)
	}

	// TRUSTED_PROXIES must contain valid CIDR notation so failures are surfaced
	// here at Load() rather than later in server.New when ratelimit.ParseTrustedProxies
	// is called.
	if c.TrustedProxies != "" {
		for part := range strings.SplitSeq(c.TrustedProxies, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if _, _, err := net.ParseCIDR(part); err != nil {
				return fmt.Errorf("config: TRUSTED_PROXIES contains invalid CIDR %q: %w", part, err)
			}
		}
	}

	if c.BitcoinEnabled {
		if err := c.validateBitcoin(); err != nil {
			return err
		}
	}

	return nil
}

// validateBitcoin checks all bitcoin-specific configuration.
// Only called when BitcoinEnabled is true.
func (c *Config) validateBitcoin() error {
	if c.BitcoinRPCUser == "" {
		return fmt.Errorf("config: BTC_RPC_USER is required when BTC_ENABLED=true")
	}
	if c.BitcoinRPCPass == "" {
		return fmt.Errorf("config: BTC_RPC_PASS is required when BTC_ENABLED=true")
	}
	port, portErr := strconv.Atoi(c.BitcoinRPCPort)
	if portErr != nil || port < 1 || port > 65535 {
		return fmt.Errorf("config: BTC_RPC_PORT must be a numeric port in range 1–65535, got %q", c.BitcoinRPCPort)
	}
	if c.BitcoinNetwork != "testnet4" && c.BitcoinNetwork != "mainnet" {
		return fmt.Errorf("config: BTC_NETWORK must be \"testnet4\" or \"mainnet\", got %q", c.BitcoinNetwork)
	}
	if len(c.BitcoinSessionSecret) < 32 {
		return fmt.Errorf("config: BTC_SESSION_SECRET must be at least 32 bytes")
	}
	if len(c.BitcoinSSESigningSecret) < 32 {
		return fmt.Errorf("config: BTC_SSE_SIGNING_SECRET must be at least 32 bytes")
	}
	if c.BitcoinSessionSecret == c.BitcoinSSESigningSecret {
		return fmt.Errorf("config: BTC_SESSION_SECRET and BTC_SSE_SIGNING_SECRET must be distinct")
	}
	if len(c.BitcoinAuditHMACKey) < 32 {
		return fmt.Errorf("config: BTC_AUDIT_HMAC_KEY must be at least 32 bytes")
	}
	if c.BitcoinAuditHMACKey == c.BitcoinSessionSecret {
		return fmt.Errorf("config: BTC_AUDIT_HMAC_KEY must differ from BTC_SESSION_SECRET")
	}
	if c.BitcoinAuditHMACKey == c.BitcoinSSESigningSecret {
		return fmt.Errorf("config: BTC_AUDIT_HMAC_KEY must differ from BTC_SSE_SIGNING_SECRET")
	}
	type rc struct{ name string; val, lo, hi int }
	for _, r := range []rc{
		{"BTC_SSE_TOKEN_TTL", c.BitcoinSSETokenTTL, 1, 300},
		{"BTC_MAX_WATCH_PER_USER", c.BitcoinMaxWatchPerUser, 1, 1000},
		{"BTC_MAX_SSE_PER_USER", c.BitcoinMaxSSEPerUser, 1, 10},
		{"BTC_MAX_SSE_PROCESS", c.BitcoinMaxSSEProcess, 10, 10000},
		{"BTC_CACHE_TTL", c.BitcoinCacheTTL, 1, 60},
		{"BTC_BLOCK_RPC_TIMEOUT_SECONDS", c.BitcoinBlockRPCTimeoutSeconds, 2, 60},
		{"BTC_HANDLER_TIMEOUT_MS", c.BitcoinHandlerTimeoutMs, 100, 120000},
		{"BTC_PENDING_MEMPOOL_MAX_SIZE", c.BitcoinPendingMempoolMaxSize, 100, 100000},
		{"BTC_MEMPOOL_PENDING_MAX_AGE_DAYS", c.BitcoinMempoolPendingMaxAgeDays, 1, 90},
		{"BTC_RECONCILIATION_CHECKPOINT_INTERVAL", c.BitcoinReconciliationCheckpointInterval, 1, 500},
	} {
		if r.val < r.lo || r.val > r.hi {
			return fmt.Errorf("config: %s must be between %d and %d, got %d", r.name, r.lo, r.hi, r.val)
		}
	}
	if c.BitcoinReconciliationStartHeight < 0 {
		return fmt.Errorf("config: BTC_RECONCILIATION_START_HEIGHT must be ≥0, got %d", c.BitcoinReconciliationStartHeight)
	}
	if c.BitcoinZMQIdleTimeout != 0 && (c.BitcoinZMQIdleTimeout < 30 || c.BitcoinZMQIdleTimeout > 3600) {
		return fmt.Errorf("config: BTC_ZMQ_IDLE_TIMEOUT must be 0 or in range 30–3600, got %d", c.BitcoinZMQIdleTimeout)
	}
	minHandlerMs := 2*c.BitcoinBlockRPCTimeoutSeconds*1000 + 2000
	if c.BitcoinHandlerTimeoutMs <= minHandlerMs {
		return fmt.Errorf(
			"config: BTC_HANDLER_TIMEOUT_MS (%d) must be > 2×BTC_BLOCK_RPC_TIMEOUT_SECONDS×1000+2000 (%d)",
			c.BitcoinHandlerTimeoutMs, minHandlerMs)
	}
	if c.BitcoinNetwork == "mainnet" && c.BitcoinReconciliationStartHeight == 0 {
		if !c.BitcoinReconciliationAllowGenesisScan {
			return fmt.Errorf("config: BTC_RECONCILIATION_START_HEIGHT=0 on mainnet requires BTC_RECONCILIATION_ALLOW_GENESIS_SCAN=true")
		}
		slog.Error("config: scanning from bitcoin genesis on mainnet — ensure this is intentional")
	}
	if c.BitcoinNetwork == "testnet4" && c.BitcoinRPCPort == "8332" {
		slog.Warn("config: BTC_RPC_PORT=8332 is the mainnet default; did you mean 48332 for testnet4?")
	}
	if c.BitcoinNetwork == "testnet4" &&
		(c.BitcoinZMQBlock == "tcp://127.0.0.1:28332" || c.BitcoinZMQTx == "tcp://127.0.0.1:28333") {
		slog.Warn("config: BTC_ZMQ_BLOCK/BTC_ZMQ_TX are set to mainnet default ports; verify testnet4 ZMQ config")
	}
	if c.BitcoinZMQBlock == c.BitcoinZMQTx {
		return fmt.Errorf("config: BTC_ZMQ_BLOCK and BTC_ZMQ_TX must be different endpoints, both are %q", c.BitcoinZMQBlock)
	}
	return nil
}

// ── private helpers ───────────────────────────────────────────────────────────

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		slog.Warn("config: malformed integer env var; using default",
			"key", key, "value", v, "default", fallback)
		return fallback
	}
	return n
}

func getEnvInt32(key string, fallback int32) int32 {
	v := getEnvInt(key, int(fallback))
	if v > math.MaxInt32 || v < math.MinInt32 {
		slog.Warn("config: int32 overflow for env var; using default",
			"key", key, "value", v, "default", fallback)
		return fallback
	}
	return int32(v)
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		slog.Warn("config: malformed duration env var; using default",
			"key", key, "value", v, "default", fallback)
		return fallback
	}
	return d
}

// parseBoolEnv parses a boolean environment variable using strconv.ParseBool,
// which accepts "1", "t", "T", "TRUE", "true", "True", "0", "f", "F",
// "FALSE", "false", "False". An unrecognised value defaults to false — the
// safe production default — and a warning is logged so operators catch typos
// (e.g. HTTPS_ENABLED=YES) that would otherwise silently disable HSTS.
func parseBoolEnv(key string) bool {
	v := os.Getenv(key)
	if v == "" {
		return false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		slog.Warn("config: unrecognised boolean env var; defaulting to false",
			"key", key, "value", v)
		return false
	}
	return b
}

// parseBoolEnvDefault returns defaultVal when the env var is absent.
// parseBoolEnv always returns false for absent vars; this helper is needed for
// fields that default to true (e.g. BTC_SSE_TOKEN_BIND_IP).
// Logs a WARNING with accepted values listed when an unrecognised value is given.
func parseBoolEnvDefault(key string, defaultVal bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		slog.Warn("config: unrecognised boolean env var; using default",
			"key", key, "value", v, "default", defaultVal,
			"accepted_values", "1, t, T, TRUE, true, True, 0, f, F, FALSE, false, False")
		return defaultVal
	}
	return b
}

// isLowEntropySecret returns true when s has dangerously low Shannon entropy,
// indicating a weak or patterned secret unsuitable for HMAC-SHA256 signing.
//
// The check uses a two-stage approach:
//  1. Fast path: rejects all-identical character strings (e.g. "aaaa...").
//     This is zero-allocation for the common sentinel/placeholder case.
//  2. Shannon entropy: for strings >= 32 characters, computes
//     H = -sum(p_i * log2(p_i)) where p_i is the frequency of each unique byte
//     divided by the total length. Secrets with entropy below 3.5 bits per
//     character are rejected.
//
// The 3.5 bits/char threshold catches weak patterns while accepting legitimate
// hex-encoded secrets. A 64-char hex string (32 random bytes) has ~4.0 bits/char;
// repeated words like "password_password_..." fall below 3.5.
//
// Generate safe secrets with: openssl rand -hex 32
func isLowEntropySecret(s string) bool {
	if len(s) == 0 {
		return false // empty is caught by the required-field check
	}

	// Fast path: reject all-identical characters (e.g. "aaaaaaa...")
	// This avoids allocating the frequency map for the common placeholder case.
	first := s[0]
	allSame := true
	for i := 1; i < len(s); i++ {
		if s[i] != first {
			allSame = false
			break
		}
	}
	if allSame {
		return true
	}

	// Shannon entropy only applies to strings >= 32 chars (minimum JWT secret length).
	// Short strings are caught by the length validation anyway.
	if len(s) < 32 {
		return false
	}

	freq := make(map[byte]int, 32)
	for i := 0; i < len(s); i++ {
		freq[s[i]]++
	}

	var entropy float64
	length := float64(len(s))
	for _, count := range freq {
		if count > 0 {
			p := float64(count) / length
			entropy -= p * logBase2(p)
		}
	}

	const minEntropyBitsPerChar = 3.5
	return entropy < minEntropyBitsPerChar
}

// logBase2 computes log2(x) via natural logarithm.
// Precondition: x > 0.
func logBase2(x float64) float64 {
	const ln2 = 0.6931471805599453
	return math.Log(x) / ln2
}

// trimAppName strips surrounding whitespace and a single layer of ASCII
// double-quotes from s. This lets operators write APP_NAME="Acme" or
// APP_NAME=Acme in their .env file and get the same result: "Acme".
func trimAppName(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	return strings.TrimSpace(s)
}
