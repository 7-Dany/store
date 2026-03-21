# Prerequisites — config Extensions

> **Package:** `internal/config/`
> **Files affected:** `config.go`, `config_test.go`
>
> **Status:** Must be merged before any bitcoin domain code is written.
> **Depends on:** All platform prerequisites merged (kvstore, ratelimit, token, rbac).
> **Blocks:** `server.go` bitcoin wiring block, all bitcoin domain services.

---

## Overview

All bitcoin configuration is gated behind `BitcoinEnabled`. When `BTC_ENABLED=false`
(the default), no validation runs and no bitcoin wiring occurs. When `BTC_ENABLED=true`,
all required fields are validated at startup before any connection is made.

Three files must change together: the struct definition, `Load()` defaults, and
`validate()`. All changes live in `config.go` and `config_test.go`. No new files.

---

## §1 — New Struct Fields

Add after the `// ── OAuth` section in the `Config` struct:

```go
// ── Bitcoin ─────────────────────────────────────────────────────
BitcoinEnabled                          bool
BitcoinRPCHost                          string  // BTC_RPC_HOST; default "127.0.0.1"
BitcoinRPCPort                          string  // BTC_RPC_PORT; validated numeric 1–65535
BitcoinRPCUser                          string  // BTC_RPC_USER; required
BitcoinRPCPass                          string  // BTC_RPC_PASS; required — NEVER log raw
BitcoinZMQBlock                         string  // BTC_ZMQ_BLOCK; default "tcp://127.0.0.1:28332"
BitcoinZMQTx                            string  // BTC_ZMQ_TX; default "tcp://127.0.0.1:28333"
BitcoinZMQIdleTimeout                   int     // BTC_ZMQ_IDLE_TIMEOUT; 0=use network default; 30–3600
BitcoinNetwork                          string  // BTC_NETWORK; "testnet4" or "mainnet"
BitcoinSSETokenTTL                      int     // BTC_SSE_TOKEN_TTL; 1–300; default 60
BitcoinSSETokenBindIP                   bool    // BTC_SSE_TOKEN_BIND_IP; default true
BitcoinSessionSecret                    string  // BTC_SESSION_SECRET; ≥32 bytes
BitcoinSSESigningSecret                 string  // BTC_SSE_SIGNING_SECRET; ≥32 bytes; ≠ session secret
BitcoinAllowedOrigins                   []string // BTC_ALLOWED_ORIGINS; comma-separated; required
BitcoinTrustedProxyCIDRs                []string // BTC_TRUSTED_PROXY_CIDRS; comma-separated
BitcoinMaxSSEPerUser                    int     // BTC_MAX_SSE_PER_USER; 1–10; default 3
BitcoinMaxSSEProcess                    int     // BTC_MAX_SSE_PROCESS; 10–10000; default 100
BitcoinMaxWatchPerUser                  int     // BTC_MAX_WATCH_PER_USER; 1–1000; default 100
BitcoinCacheTTL                         int     // BTC_CACHE_TTL; 1–60; default 5
BitcoinBlockRPCTimeoutSeconds           int     // BTC_BLOCK_RPC_TIMEOUT_SECONDS; 2–60; default 10
BitcoinHandlerTimeoutMs                 int     // BTC_HANDLER_TIMEOUT_MS; 100–120000; default 30000
BitcoinPendingMempoolMaxSize            int     // BTC_PENDING_MEMPOOL_MAX_SIZE; 100–100000; default 10000
BitcoinMempoolPendingMaxAgeDays         int     // BTC_MEMPOOL_PENDING_MAX_AGE_DAYS; 1–90; default 14
BitcoinFallbackAuditLog                 string  // BTC_FALLBACK_AUDIT_LOG; default "" (stdout JSON)
BitcoinReconciliationStartHeight        int     // BTC_RECONCILIATION_START_HEIGHT; ≥0; default 0
BitcoinReconciliationCheckpointInterval int     // BTC_RECONCILIATION_CHECKPOINT_INTERVAL; 1–500; default 100
BitcoinReconciliationAllowGenesisScan   bool    // BTC_RECONCILIATION_ALLOW_GENESIS_SCAN; default false
BitcoinAuditHMACKey                     string  // BTC_AUDIT_HMAC_KEY; ≥32 bytes; ≠ other secrets
```

---

## §2 — New Helper: `parseBoolEnvDefault`

Add alongside existing `parseBoolEnv` in `config.go`:

```go
// parseBoolEnvDefault returns defaultVal when the env var is absent.
// parseBoolEnv always returns false for absent vars; this helper is needed for
// fields that default to true (e.g. BTC_SSE_TOKEN_BIND_IP).
// Logs a WARNING with accepted values listed when an unrecognised value is given.
func parseBoolEnvDefault(key string, defaultVal bool) bool
```

---

## §3 — Load() Defaults

Add inside the `cfg := &Config{...}` literal after the `// OAuth` block:

```go
// Bitcoin
BitcoinEnabled:                          parseBoolEnv("BTC_ENABLED"),
BitcoinRPCHost:                          getEnv("BTC_RPC_HOST", "127.0.0.1"),
BitcoinRPCPort:                          getEnv("BTC_RPC_PORT", "8332"),
BitcoinRPCUser:                          os.Getenv("BTC_RPC_USER"),
BitcoinRPCPass:                          os.Getenv("BTC_RPC_PASS"),
BitcoinZMQBlock:                         getEnv("BTC_ZMQ_BLOCK", "tcp://127.0.0.1:28332"),
BitcoinZMQTx:                            getEnv("BTC_ZMQ_TX", "tcp://127.0.0.1:28333"),
BitcoinZMQIdleTimeout:                   getEnvInt("BTC_ZMQ_IDLE_TIMEOUT", 0),
BitcoinNetwork:                          os.Getenv("BTC_NETWORK"),
BitcoinSSETokenTTL:                      getEnvInt("BTC_SSE_TOKEN_TTL", 60),
BitcoinSSETokenBindIP:                   parseBoolEnvDefault("BTC_SSE_TOKEN_BIND_IP", true),
BitcoinSessionSecret:                    os.Getenv("BTC_SESSION_SECRET"),
BitcoinSSESigningSecret:                 os.Getenv("BTC_SSE_SIGNING_SECRET"),
BitcoinMaxSSEPerUser:                    getEnvInt("BTC_MAX_SSE_PER_USER", 3),
BitcoinMaxSSEProcess:                    getEnvInt("BTC_MAX_SSE_PROCESS", 100),
BitcoinMaxWatchPerUser:                  getEnvInt("BTC_MAX_WATCH_PER_USER", 100),
BitcoinCacheTTL:                         getEnvInt("BTC_CACHE_TTL", 5),
BitcoinBlockRPCTimeoutSeconds:           getEnvInt("BTC_BLOCK_RPC_TIMEOUT_SECONDS", 10),
BitcoinHandlerTimeoutMs:                 getEnvInt("BTC_HANDLER_TIMEOUT_MS", 30000),
BitcoinPendingMempoolMaxSize:            getEnvInt("BTC_PENDING_MEMPOOL_MAX_SIZE", 10000),
BitcoinMempoolPendingMaxAgeDays:         getEnvInt("BTC_MEMPOOL_PENDING_MAX_AGE_DAYS", 14),
BitcoinFallbackAuditLog:                 getEnv("BTC_FALLBACK_AUDIT_LOG", ""),
BitcoinReconciliationStartHeight:        getEnvInt("BTC_RECONCILIATION_START_HEIGHT", 0),
BitcoinReconciliationCheckpointInterval: getEnvInt("BTC_RECONCILIATION_CHECKPOINT_INTERVAL", 100),
BitcoinReconciliationAllowGenesisScan:   parseBoolEnv("BTC_RECONCILIATION_ALLOW_GENESIS_SCAN"),
BitcoinAuditHMACKey:                     os.Getenv("BTC_AUDIT_HMAC_KEY"),
```

Parse slice fields inside `Load()` after the struct literal (only when bitcoin enabled):
```go
if cfg.BitcoinEnabled {
    // BTC_ALLOWED_ORIGINS — comma-separated
    // BTC_TRUSTED_PROXY_CIDRS — comma-separated
}
```

---

## §4 — validate() Bitcoin Block

All checks run only when `c.BitcoinEnabled`. Add at end of `validate()` before `return nil`.

**Required field checks:**
- `BTC_RPC_USER` empty → error
- `BTC_RPC_PASS` empty → error
- `BTC_RPC_PORT` non-numeric or outside 1–65535 → error (HIGH #6: validated at config time, not at first RPC call)
- `BTC_NETWORK` not `"testnet4"` or `"mainnet"` → error

**Secret checks:**
- `BTC_SESSION_SECRET` < 32 bytes → error
- `BTC_SSE_SIGNING_SECRET` < 32 bytes → error
- `BTC_SESSION_SECRET == BTC_SSE_SIGNING_SECRET` → error
- `BTC_AUDIT_HMAC_KEY` < 32 bytes → error
- `BTC_AUDIT_HMAC_KEY == BTC_SESSION_SECRET` or `== BTC_SSE_SIGNING_SECRET` → error

**Range checks:**
- `BTC_SSE_TOKEN_TTL`: 1–300
- `BTC_MAX_WATCH_PER_USER`: 1–1000
- `BTC_MAX_SSE_PER_USER`: 1–10
- `BTC_MAX_SSE_PROCESS`: 10–10000
- `BTC_CACHE_TTL`: 1–60
- `BTC_BLOCK_RPC_TIMEOUT_SECONDS`: 2–60
- `BTC_HANDLER_TIMEOUT_MS`: 100–120000
- `BTC_PENDING_MEMPOOL_MAX_SIZE`: 100–100000
- `BTC_MEMPOOL_PENDING_MAX_AGE_DAYS`: 1–90
- `BTC_ZMQ_IDLE_TIMEOUT`: 0 (use default) or 30–3600
- `BTC_RECONCILIATION_START_HEIGHT`: ≥0
- `BTC_RECONCILIATION_CHECKPOINT_INTERVAL`: 1–500

**Cross-field constraint:**
```
BTC_HANDLER_TIMEOUT_MS > 2 × BTC_BLOCK_RPC_TIMEOUT_SECONDS × 1000 + 2000
```
BlockEvent handler makes two sequential RPC calls each bounded by `BTC_BLOCK_RPC_TIMEOUT_SECONDS`.

**Mainnet genesis scan gate:**
- `BTC_NETWORK=mainnet` + `BTC_RECONCILIATION_START_HEIGHT=0` + `BTC_RECONCILIATION_ALLOW_GENESIS_SCAN=false` → hard error
- Same with `ALLOW_GENESIS_SCAN=true` → no error, but `slog.Error` is logged

**Allowed origins validation:**
- `BTC_ALLOWED_ORIGINS` empty → error
- Any origin is `""`, `"null"`, or contains `"*"` → error
- `BTC_NETWORK=mainnet` + any origin starts with `http://` → error
- Any origin has trailing slash → error (M-04)
- Any origin has path component after host → error (M-04)

**CIDR validation:**
- Each `BTC_TRUSTED_PROXY_CIDRS` entry must be valid via `net.ParseCIDR`

**Warnings (not errors):**
- `BTC_NETWORK=testnet4` + `BTC_RPC_PORT=8332` → warning (8332 is mainnet default)
- `BTC_NETWORK=testnet4` + default ZMQ ports (28332/28333) → warning

**Not in validate() — stays in server.go:**
- ZMQ endpoint loopback enforcement (requires syscall)
- Fallback audit log writability (requires file I/O)
- `LOG_REDACTION_VERIFIED` gate

---

## §5 — Test Inventory

**File:** `internal/config/config_test.go`

| Test | Notes |
|---|---|
| `TestConfig_Bitcoin_DisabledByDefault` | BTC_ENABLED absent → BitcoinEnabled==false, no bitcoin validation |
| `TestConfig_Bitcoin_RequiredFieldsMissing` | BTC_ENABLED=true, no other vars → error |
| `TestConfig_Bitcoin_NetworkInvalid` | BTC_NETWORK="regtest" → error |
| `TestConfig_Bitcoin_SecretsTooShort` | BTC_SESSION_SECRET=<31 bytes → error |
| `TestConfig_Bitcoin_SecretsIdentical` | both secrets same → error |
| `TestConfig_Bitcoin_CrossFieldHandlerTimeout` | BTC_HANDLER_TIMEOUT_MS=5000, BTC_BLOCK_RPC_TIMEOUT_SECONDS=10 → error |
| `TestConfig_Bitcoin_CrossFieldHandlerTimeoutDefaults` | defaults satisfy constraint |
| `TestConfig_Bitcoin_AllowedOriginsWildcard` | BTC_ALLOWED_ORIGINS=* → error |
| `TestConfig_Bitcoin_AllowedOriginsHttpMainnet` | BTC_NETWORK=mainnet + http:// origin → error |
| `TestConfig_Bitcoin_ZMQIdleTimeoutRange` | 29→error; 30→ok; 3600→ok; 3601→error |
| `TestConfig_Bitcoin_ParseBoolEnvDefault_True` | BTC_SSE_TOKEN_BIND_IP absent → true |
| `TestConfig_Bitcoin_ParseBoolEnvDefault_ExplicitFalse` | BTC_SSE_TOKEN_BIND_IP=false → false |
| `TestConfig_Bitcoin_ParseBoolEnvDefault_InvalidValue_UsesDefault` | BTC_SSE_TOKEN_BIND_IP=yes → true + warning with accepted values listed |
| `TestConfig_Bitcoin_AllowedOrigins_TrailingSlashRejects` | trailing slash → error |
| `TestConfig_Bitcoin_AllowedOrigins_PathComponentRejects` | path component → error |
| `TestConfig_Bitcoin_Testnet4WithMainnetRPCPort_EmitsWarning` | port=8332 + testnet4 → no error, warning logged |
| `TestConfig_Bitcoin_Testnet4WithMainnetZMQPorts_EmitsWarning` | default ZMQ ports + testnet4 → warning |
| `TestConfig_Bitcoin_CheckpointIntervalRange` | 0→error; 1→ok; 500→ok; 501→error |
| `TestConfig_Bitcoin_FallbackAuditLog_EmptyDefault_StdoutFallback` | default "" → stdout JSON fallback in server wiring |
| `TestConfig_Bitcoin_ReconciliationStartHeight_MainnetZero_HardError` | mainnet + height=0 + no genesis flag → error |
| `TestConfig_Bitcoin_ReconciliationStartHeight_MainnetZero_AllowGenesisScan_LogsError` | genesis flag=true → no error; slog.Error called |
| `TestConfig_Bitcoin_RPCPort_NonNumeric_ReturnsError` | BTC_RPC_PORT=abc → error |
| `TestConfig_Bitcoin_RPCPort_ZeroReturnsError` | BTC_RPC_PORT=0 → error |
| `TestConfig_Bitcoin_RPCPort_65535_Valid` | → ok |
| `TestConfig_Bitcoin_AuditHMACKey_TooShort_ReturnsError` | < 32 bytes → error |
| `TestConfig_Bitcoin_AuditHMACKey_EqualToSessionSecret_ReturnsError` | same as session secret → error |
| `TestConfig_Bitcoin_SecretsNotEqualMainAppSecret` | Stage 2 pre-blocker: when JWTSigningSecret added, BTC secrets must differ |
