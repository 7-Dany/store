/**
 * Dev-only mock snapshots for the security dashboard.
 *
 * getMockSnapshot(scenario) returns a fully-formed SecuritySnapshot without
 * hitting Prometheus. Returns null in production or for unknown scenario names.
 *
 * Available scenarios
 * ───────────────────
 *   critical              — full meltdown: all critical anomalies firing at once
 *   warning               — soft degradation: every warning-level signal active
 *   dead_jobs             — only the dead-jobs anomaly
 *   login_spike           — credential-stuffing simulation
 *   balance_drift         — bitcoin accounting drift + recon hold CRITICAL
 *   bitcoin_rpc_down      — bitcoin_rpc_connected=0 CRITICAL
 *   invoice_detection_slow — invoice P95 > 120s WARNING
 *   jobs_failed           — 5 failed + 2 requeued + P95 job duration 280s
 *   infra_poller_down     — infraPollerAgeSeconds=90 (poller stuck) CRITICAL
 *   oauth_unlink          — OAuth unlink spike (account takeover signal)
 *   zmq_down              — Bitcoin ZMQ disconnected
 *   zmq_dropped           — Bitcoin ZMQ message drops (HWM / sequence gap)
 *   prometheus_down       — monitoring backend unreachable
 */

import type { SecuritySnapshot } from "./prometheus";

const HEALTHY_SERVICES: SecuritySnapshot["services"] = [
  { name: "API",              status: "healthy", detail: "All routes responding normally" },
  { name: "Database",         status: "healthy", detail: "Pool healthy and responsive" },
  { name: "Cache / Sessions", status: "healthy", detail: "Redis responding normally" },
  { name: "Email delivery",   status: "healthy", detail: "Delivering normally" },
  { name: "Job queue",        status: "healthy", detail: "All jobs processing normally" },
];

// Baseline values shared by every scenario — only differing fields are overridden.
const BASE: Omit<SecuritySnapshot, "anomalies" | "overall" | "services"> = {
  // HTTP
  requestsPerMin: 240,
  errorRatePct:   0,

  // Auth
  loginFailuresPerMin:           0,
  loginFailureRatePct:           0,
  accountLocksLastHour:          0,
  tokenValidationFailuresPerMin: 0,
  oauthUnlinksLastHour:          0,
  passwordResetDeniedLastHour:   0,
  registrationsLastHour:         4,
  tokenRefreshesPerMin:          12,
  sessionRevocationsLastHour:    0,

  // Infrastructure
  dbPoolUtilPct:       45,
  dbPoolIdlePct:       55,
  // null = metric not yet published (backend not restarted). Scenarios that
  // care about DB/Redis ping override to 1 (healthy) or 0 (down).
  dbUp:                null,
  redisUp:             1,
  redisStaleConnections: 0,
  redisErrLastHour:    0,
  redisPoolIdlePct:    60,
  processMemAllocMB:   128,
  infraPollerAgeSeconds: 8,
  goroutines:          80,

  // Job queue
  deadJobsTotal:          0,
  jobsSubmittedLastHour:  42,
  jobsFailedLastHour:     0,
  jobsRequeuedLastHour:   0,
  jobDurationP95Sec:      4.2,

  // Bitcoin — null = disabled on this deployment (Bitcoin section hidden in UI).
  // Override to 1 (connected) or 0 (disconnected) in bitcoin scenarios.
  zmqConnected:              null,
  zmqDroppedLastHour:        0,
  zmqLastMessageAgeSec:      null,
  zmqHandlerPanicsLastHour:  0,
  zmqHandlerTimeoutsLastHour: 0,
  zmqHandlerGoroutines:      0,
  rpcConnected:              null,
  keypoolSize:               null,
  rpcCallErrorsLastHour:     0,
  balanceDriftSatoshis:      null,
  reconciliationHoldActive:  false,
  reorgDetectedLastDay:      0,
  payoutFailuresLastHour:    0,
  sweepStuckLastHour:        0,
  sseConnectionsActive:      0,
  rateFeedStalenessSec:      null,
  reconciliationLagBlocks:   null,
  walletBackupAgeSec:        null,
  utxoCount:                 null,
  feeEstimate1Block:         null,
  feeEstimate6Block:         null,
  invoiceDetectionP50Sec:    null,
  invoiceDetectionP95Sec:    null,
  invoiceStatePending:       0,
  invoiceStateConfirmed:     0,
  invoiceStateExpired:       0,

  // Ping results — empty in BASE; each scenario fills as needed
  pingResults: [
    { name: "Backend API", status: "up",  latencyMs: 12,   detail: "HTTP 200" },
    { name: "Database",    status: "up",  latencyMs: null, detail: "DB ping succeeded" },
    { name: "Redis",       status: "up",  latencyMs: null, detail: "Redis ping succeeded" },
    { name: "Infra Poller",status: "up",  latencyMs: null, detail: "Last heartbeat 8s ago" },
  ],

  // Charts — empty series render fine (filled with zeros by chart components)
  loginFailureSeries: [],
  errorRateSeries:    [],
  requestRateSeries:  [],
  errorsByComponent:  [],

  fetchedAt:           Date.now(),
  prometheusReachable: true,
};

// Baseline Bitcoin fields for scenarios where Bitcoin IS enabled
const BITCOIN_HEALTHY_BASE: Partial<SecuritySnapshot> = {
  zmqConnected:          1,
  zmqLastMessageAgeSec:  5,
  rpcConnected:          1,
  keypoolSize:           250,
  rpcCallErrorsLastHour: 0,
  balanceDriftSatoshis:  0,
  reconciliationHoldActive: false,
  sseConnectionsActive:  3,
  rateFeedStalenessSec:  12,
  reconciliationLagBlocks: 0,
  walletBackupAgeSec:    3_600,
  utxoCount:             47,
  feeEstimate1Block:     18,
  feeEstimate6Block:     11,
  invoiceDetectionP50Sec: 4.2,
  invoiceDetectionP95Sec: 18,
  invoiceStatePending:   5,
  invoiceStateConfirmed: 148,
  invoiceStateExpired:   3,
  pingResults: [
    { name: "Backend API",  status: "up",  latencyMs: 12,   detail: "HTTP 200" },
    { name: "Bitcoin RPC",  status: "up",  latencyMs: 34,   detail: "HTTP 200" },
    { name: "Database",     status: "up",  latencyMs: null, detail: "DB ping succeeded" },
    { name: "Redis",        status: "up",  latencyMs: null, detail: "Redis ping succeeded" },
    { name: "Bitcoin ZMQ",  status: "up",  latencyMs: null, detail: "Last message 5s ago" },
    { name: "Infra Poller", status: "up",  latencyMs: null, detail: "Last heartbeat 8s ago" },
  ],
};

export function getMockSnapshot(scenario: string): SecuritySnapshot | null {
  if (process.env.NODE_ENV === "production") return null;

  const now = Date.now();

  switch (scenario) {

    // ── Full meltdown ──────────────────────────────────────────────────────────
    case "critical":
      return {
        ...BASE,
        ...BITCOIN_HEALTHY_BASE,
        fetchedAt: now,
        overall: "critical",
        errorRatePct:                  3.5,
        loginFailuresPerMin:           14,
        loginFailureRatePct:           72,
        accountLocksLastHour:          25,
        tokenValidationFailuresPerMin: 1.2,
        dbUp:                          1,
        dbPoolUtilPct:                 100,
        deadJobsTotal:                 3,
        // Bitcoin financial crisis
        balanceDriftSatoshis:          1500,
        reconciliationHoldActive:      true,
        rpcConnected:                  0,
        pingResults: [
          { name: "Backend API",  status: "up",   latencyMs: 12,   detail: "HTTP 200" },
          { name: "Bitcoin RPC",  status: "down", latencyMs: 2001, detail: "Timed out after 2000ms" },
          { name: "Database",     status: "up",   latencyMs: null, detail: "DB ping succeeded" },
          { name: "Redis",        status: "up",   latencyMs: null, detail: "Redis ping succeeded" },
          { name: "Bitcoin ZMQ",  status: "up",   latencyMs: null, detail: "Last message 5s ago" },
          { name: "Infra Poller", status: "up",   latencyMs: null, detail: "Last heartbeat 8s ago" },
        ],
        services: [
          { name: "API",               status: "down",     detail: "3.5% of requests returning 5xx" },
          { name: "Database",          status: "down",     detail: "Pool exhausted — requests blocking" },
          { name: "Cache / Sessions",  status: "healthy",  detail: "Redis responding normally" },
          { name: "Email delivery",    status: "healthy",  detail: "Delivering normally" },
          { name: "Job queue",         status: "degraded", detail: "3 dead jobs — exhausted retries" },
          { name: "Bitcoin ZMQ",       status: "healthy",  detail: "Connected to Bitcoin Core" },
          { name: "Bitcoin RPC",       status: "down",     detail: "RPC client disconnected from Bitcoin Core" },
          { name: "Bitcoin integrity", status: "down",     detail: "Balance drift: 1500 sat" },
        ],
        anomalies: [
          { id: "http_error_critical",          severity: "critical", title: "HTTP error rate critical",          detail: "3.50% of requests are returning 5xx — SLO-1 actively breaching.",                    detectedAt: now },
          { id: "db_pool_exhausted",            severity: "critical", title: "DB pool exhausted",                detail: "Connection pool is 100% utilized — all new DB requests are blocked.",                detectedAt: now },
          { id: "login_failure_spike",          severity: "critical", title: "Login failure spike",              detail: "14.0/min — possible credential stuffing attack.",                                    detectedAt: now },
          { id: "high_failure_rate",            severity: "critical", title: "Critical login failure rate",      detail: "72% of login attempts failing — HighInvalidCredentialRate alert threshold exceeded.", detectedAt: now },
          { id: "account_lockout_accumulation", severity: "critical", title: "Account lockout accumulation",     detail: "25 accounts auto-locked in the past hour — possible distributed brute-force.",        detectedAt: now },
          { id: "balance_drift_nonzero",        severity: "critical", title: "Bitcoin balance drift detected",   detail: "bitcoin_balance_drift_satoshis = 1500 sat. Accounting integrity violated.",           detectedAt: now },
          { id: "reconciliation_hold_active",   severity: "critical", title: "Reconciliation hold active",      detail: "Sweep hold mode is active — all outbound Bitcoin transactions are paused.",           detectedAt: now },
          { id: "rpc_disconnected",             severity: "critical", title: "Bitcoin RPC disconnected",        detail: "RPC client cannot reach Bitcoin Core — fee estimation unavailable.",                  detectedAt: now },
          { id: "token_validation_spike",       severity: "warning",  title: "Token validation failures",       detail: "1.20/min — possible token replay or JWT signature attack.",                          detectedAt: now },
          { id: "dead_jobs",                    severity: "warning",  title: "Dead jobs accumulating",          detail: "3 jobs exhausted all retries.",                                                      detectedAt: now },
        ],
      };

    // ── All warning signals ────────────────────────────────────────────────────
    case "warning":
      return {
        ...BASE,
        ...BITCOIN_HEALTHY_BASE,
        fetchedAt: now,
        overall: "degraded",
        errorRatePct:                  0.3,
        loginFailuresPerMin:           5,
        loginFailureRatePct:           30,
        accountLocksLastHour:          8,
        tokenValidationFailuresPerMin: 0.7,
        oauthUnlinksLastHour:          7,
        passwordResetDeniedLastHour:   12,
        dbUp:                          1,
        dbPoolUtilPct:                 88,
        redisStaleConnections:         6,
        goroutines:                    520,
        deadJobsTotal:                 1,
        jobsRequeuedLastHour:          2,
        sweepStuckLastHour:            1,
        rateFeedStalenessSec:          360,
        reconciliationLagBlocks:       8,
        walletBackupAgeSec:            90_000,
        invoiceDetectionP95Sec:        75,
        services: [
          { name: "API",               status: "degraded", detail: "0.30% error rate over last 5 min" },
          { name: "Database",          status: "degraded", detail: "88% pool utilization" },
          { name: "Cache / Sessions",  status: "degraded", detail: "6 stale connections" },
          { name: "Email delivery",    status: "healthy",  detail: "Delivering normally" },
          { name: "Job queue",         status: "degraded", detail: "1 dead job — exhausted retries" },
          { name: "Bitcoin ZMQ",       status: "healthy",  detail: "Connected to Bitcoin Core" },
          { name: "Bitcoin RPC",       status: "degraded", detail: "8 blocks behind chain tip" },
          { name: "Bitcoin integrity", status: "degraded", detail: "1 stuck sweep detected" },
        ],
        anomalies: [
          { id: "http_error_elevated",        severity: "warning", title: "HTTP error rate elevated",       detail: "0.30% error rate over last 5 min.",                                                 detectedAt: now },
          { id: "db_pool_saturation",         severity: "warning", title: "DB pool near saturation",       detail: "Pool at 88% — requests may begin queuing.",                                         detectedAt: now },
          { id: "login_failure_elevated",     severity: "warning", title: "Elevated login failures",       detail: "5.0/min — above normal baseline.",                                                  detectedAt: now },
          { id: "account_lockout_elevated",   severity: "warning", title: "Elevated account lockouts",     detail: "8 accounts locked in the past hour.",                                               detectedAt: now },
          { id: "redis_stale",                severity: "warning", title: "Redis connectivity degraded",   detail: "6 stale connections detected.",                                                     detectedAt: now },
          { id: "goroutine_leak",             severity: "warning", title: "Possible goroutine leak",       detail: "520 goroutines — sustained high count indicates a leak.",                            detectedAt: now },
          { id: "token_validation_spike",     severity: "warning", title: "Token validation failures",     detail: "0.70/min — possible token replay or JWT signature attack.",                         detectedAt: now },
          { id: "oauth_unlink_spike",         severity: "warning", title: "OAuth unlink spike",            detail: "7 providers unlinked in the past hour — possible account takeover campaign.",       detectedAt: now },
          { id: "password_reset_enumeration", severity: "warning", title: "User enumeration attempt",      detail: "12 password resets for non-existent accounts in the past hour.",                   detectedAt: now },
          { id: "dead_jobs",                  severity: "warning", title: "Dead jobs accumulating",        detail: "1 job exhausted all retries.",                                                      detectedAt: now },
          { id: "jobs_requeued",              severity: "warning", title: "Jobs requeued by stall detector", detail: "2 jobs requeued in the past hour — a worker crashed or timed out.",               detectedAt: now },
          { id: "sweep_stuck",                severity: "warning", title: "Bitcoin sweep stuck",           detail: "1 sweep detected as stuck in the past hour.",                                       detectedAt: now },
          { id: "rate_feed_stale",            severity: "warning", title: "Bitcoin rate feed stale",       detail: "Exchange rate last updated 360s ago (> 5 min threshold).",                         detectedAt: now },
          { id: "reconciliation_lag",         severity: "warning", title: "Reconciliation lag",            detail: "8 blocks behind chain tip (~80 min) — exceeds 6-block threshold.",                 detectedAt: now },
          { id: "wallet_backup_stale",        severity: "warning", title: "Wallet backup stale",          detail: "Last backup 25h ago — exceeds 24 h retention target.",                             detectedAt: now },
          { id: "invoice_detection_slow",     severity: "warning", title: "Invoice detection slow",       detail: "P95 detection latency is 75s — exceeds 60s threshold.",                            detectedAt: now },
        ],
      };

    // ── Dead jobs only ─────────────────────────────────────────────────────────
    case "dead_jobs":
      return {
        ...BASE,
        fetchedAt: now,
        overall: "degraded",
        dbUp: 1,
        deadJobsTotal: 5,
        services: [
          ...HEALTHY_SERVICES.slice(0, 4),
          { name: "Job queue", status: "degraded", detail: "5 dead jobs — exhausted retries" },
        ],
        anomalies: [
          { id: "dead_jobs", severity: "warning", title: "Dead jobs accumulating", detail: "5 jobs exhausted all retries.", detectedAt: now },
        ],
      };

    // ── Credential-stuffing attack ─────────────────────────────────────────────
    case "login_spike":
      return {
        ...BASE,
        fetchedAt: now,
        overall: "critical",
        dbUp: 1,
        loginFailuresPerMin:  18,
        loginFailureRatePct:  85,
        accountLocksLastHour: 30,
        services: HEALTHY_SERVICES,
        anomalies: [
          { id: "login_failure_spike",          severity: "critical", title: "Login failure spike",          detail: "18.0/min — possible credential stuffing attack.",                                      detectedAt: now },
          { id: "high_failure_rate",            severity: "critical", title: "Critical login failure rate",  detail: "85% of login attempts failing — HighInvalidCredentialRate alert threshold exceeded.",   detectedAt: now },
          { id: "account_lockout_accumulation", severity: "critical", title: "Account lockout accumulation", detail: "30 accounts auto-locked in the past hour — possible distributed brute-force.",          detectedAt: now },
        ],
      };

    // ── Bitcoin balance drift + recon hold ────────────────────────────────────
    case "balance_drift":
      return {
        ...BASE,
        ...BITCOIN_HEALTHY_BASE,
        fetchedAt: now,
        overall: "critical",
        dbUp: 1,
        balanceDriftSatoshis:     1500,
        reconciliationHoldActive: true,
        services: [
          ...HEALTHY_SERVICES,
          { name: "Bitcoin ZMQ",       status: "healthy", detail: "Connected to Bitcoin Core" },
          { name: "Bitcoin RPC",       status: "healthy", detail: "Connected to Bitcoin Core" },
          { name: "Bitcoin integrity", status: "down",    detail: "Balance drift: 1500 sat" },
        ],
        anomalies: [
          { id: "balance_drift_nonzero",      severity: "critical", title: "Bitcoin balance drift detected", detail: "bitcoin_balance_drift_satoshis = 1500 sat. Accounting integrity violated.", detectedAt: now },
          { id: "reconciliation_hold_active", severity: "critical", title: "Reconciliation hold active",    detail: "Sweep hold mode is active — all outbound Bitcoin transactions are paused.",  detectedAt: now },
        ],
      };

    // ── Bitcoin RPC down ──────────────────────────────────────────────────────
    case "bitcoin_rpc_down":
      return {
        ...BASE,
        ...BITCOIN_HEALTHY_BASE,
        fetchedAt: now,
        overall: "critical",
        dbUp: 1,
        rpcConnected: 0,
        pingResults: [
          { name: "Backend API",  status: "up",   latencyMs: 12,   detail: "HTTP 200" },
          { name: "Bitcoin RPC",  status: "down", latencyMs: 2001, detail: "Timed out after 2000ms" },
          { name: "Database",     status: "up",   latencyMs: null, detail: "DB ping succeeded" },
          { name: "Redis",        status: "up",   latencyMs: null, detail: "Redis ping succeeded" },
          { name: "Bitcoin ZMQ",  status: "up",   latencyMs: null, detail: "Last message 5s ago" },
          { name: "Infra Poller", status: "up",   latencyMs: null, detail: "Last heartbeat 8s ago" },
        ],
        services: [
          ...HEALTHY_SERVICES,
          { name: "Bitcoin ZMQ",       status: "healthy", detail: "Connected to Bitcoin Core" },
          { name: "Bitcoin RPC",       status: "down",    detail: "RPC client disconnected from Bitcoin Core" },
          { name: "Bitcoin integrity", status: "healthy", detail: "Balance drift zero — accounting clean" },
        ],
        anomalies: [
          { id: "rpc_disconnected", severity: "critical", title: "Bitcoin RPC disconnected", detail: "RPC client cannot reach Bitcoin Core — fee estimation and broadcasting are unavailable.", detectedAt: now },
        ],
      };

    // ── Invoice detection slow ────────────────────────────────────────────────
    case "invoice_detection_slow":
      return {
        ...BASE,
        ...BITCOIN_HEALTHY_BASE,
        fetchedAt: now,
        overall: "degraded",
        dbUp: 1,
        invoiceDetectionP50Sec: 45,
        invoiceDetectionP95Sec: 128,
        services: [
          ...HEALTHY_SERVICES,
          { name: "Bitcoin ZMQ",       status: "healthy", detail: "Connected to Bitcoin Core" },
          { name: "Bitcoin RPC",       status: "healthy", detail: "Connected to Bitcoin Core" },
          { name: "Bitcoin integrity", status: "healthy", detail: "Balance drift zero — accounting clean" },
        ],
        anomalies: [
          { id: "invoice_detection_slow", severity: "warning", title: "Invoice detection slow", detail: "P95 detection latency is 128s — exceeds 60s threshold.", detectedAt: now },
        ],
      };

    // ── Jobs failed ───────────────────────────────────────────────────────────
    case "jobs_failed":
      return {
        ...BASE,
        fetchedAt: now,
        overall: "degraded",
        dbUp: 1,
        deadJobsTotal:         5,
        jobsFailedLastHour:    5,
        jobsRequeuedLastHour:  2,
        jobDurationP95Sec:     280,
        services: [
          ...HEALTHY_SERVICES.slice(0, 4),
          { name: "Job queue", status: "degraded", detail: "5 dead jobs — exhausted retries" },
        ],
        anomalies: [
          { id: "dead_jobs",     severity: "warning", title: "Dead jobs accumulating",          detail: "5 jobs exhausted all retries.",                                               detectedAt: now },
          { id: "jobs_requeued", severity: "warning", title: "Jobs requeued by stall detector", detail: "2 jobs requeued in the past hour — a worker crashed or timed out.",           detectedAt: now },
        ],
      };

    // ── Infra poller stuck ────────────────────────────────────────────────────
    case "infra_poller_down":
      return {
        ...BASE,
        fetchedAt: now,
        overall: "critical",
        dbUp: 1,
        infraPollerAgeSeconds: 90,
        pingResults: [
          { name: "Backend API",  status: "up",   latencyMs: 12,   detail: "HTTP 200" },
          { name: "Database",     status: "up",   latencyMs: null, detail: "DB ping succeeded" },
          { name: "Redis",        status: "up",   latencyMs: null, detail: "Redis ping succeeded" },
          { name: "Infra Poller", status: "down", latencyMs: null, detail: "Poller stuck — last heartbeat 90s ago" },
        ],
        services: HEALTHY_SERVICES,
        anomalies: [
          { id: "infra_poller_stale", severity: "critical", title: "Infra poller stopped", detail: "Last heartbeat 90s ago — DB/Redis health gauges are now stale.", detectedAt: now },
        ],
      };

    // ── OAuth unlink spike ────────────────────────────────────────────────────
    case "oauth_unlink":
      return {
        ...BASE,
        fetchedAt: now,
        overall: "degraded",
        dbUp: 1,
        oauthUnlinksLastHour: 15,
        services: HEALTHY_SERVICES,
        anomalies: [
          { id: "oauth_unlink_spike", severity: "warning", title: "OAuth unlink spike", detail: "15 providers unlinked in the past hour — possible account takeover campaign.", detectedAt: now },
        ],
      };

    // ── Bitcoin ZMQ disconnected ───────────────────────────────────────────────
    case "zmq_down":
      return {
        ...BASE,
        ...BITCOIN_HEALTHY_BASE,
        fetchedAt: now,
        overall: "critical",
        dbUp: 1,
        zmqConnected:     0,
        zmqLastMessageAgeSec: 900,
        pingResults: [
          { name: "Backend API",  status: "up",   latencyMs: 12,   detail: "HTTP 200" },
          { name: "Bitcoin RPC",  status: "up",   latencyMs: 34,   detail: "HTTP 200" },
          { name: "Database",     status: "up",   latencyMs: null, detail: "DB ping succeeded" },
          { name: "Redis",        status: "up",   latencyMs: null, detail: "Redis ping succeeded" },
          { name: "Bitcoin ZMQ",  status: "down", latencyMs: null, detail: "ZMQ subscriber disconnected from Bitcoin Core" },
          { name: "Infra Poller", status: "up",   latencyMs: null, detail: "Last heartbeat 8s ago" },
        ],
        services: [
          ...HEALTHY_SERVICES,
          { name: "Bitcoin ZMQ",       status: "down",    detail: "Disconnected — block and tx events paused" },
          { name: "Bitcoin RPC",       status: "healthy", detail: "Connected to Bitcoin Core" },
          { name: "Bitcoin integrity", status: "healthy", detail: "Balance drift zero — accounting clean" },
        ],
        anomalies: [
          { id: "zmq_disconnected", severity: "critical", title: "Bitcoin ZMQ disconnected", detail: "ZMQ subscriber lost connection to Bitcoin Core — block and tx events have stopped.", detectedAt: now },
        ],
      };

    // ── Bitcoin ZMQ message drops ──────────────────────────────────────────────
    case "zmq_dropped":
      return {
        ...BASE,
        ...BITCOIN_HEALTHY_BASE,
        fetchedAt: now,
        overall: "degraded",
        dbUp: 1,
        zmqDroppedLastHour: 42,
        services: [
          ...HEALTHY_SERVICES,
          { name: "Bitcoin ZMQ",       status: "healthy", detail: "Connected to Bitcoin Core" },
          { name: "Bitcoin RPC",       status: "healthy", detail: "Connected to Bitcoin Core" },
          { name: "Bitcoin integrity", status: "healthy", detail: "Balance drift zero — accounting clean" },
        ],
        anomalies: [
          { id: "zmq_messages_dropped", severity: "warning", title: "Bitcoin ZMQ messages dropped", detail: "42 ZMQ messages dropped in the past hour (HWM overflow or sequence gap).", detectedAt: now },
        ],
      };

    // ── Prometheus unreachable ─────────────────────────────────────────────────
    case "prometheus_down":
      return {
        ...BASE,
        fetchedAt: now,
        overall: "degraded",
        prometheusReachable: false,
        pingResults: [],
        services: [
          { name: "Monitoring backend", status: "down", detail: "Prometheus is not reachable. Check PROMETHEUS_URL env var." },
        ],
        anomalies: [
          { id: "prometheus_down", severity: "critical", title: "Monitoring backend unreachable", detail: "Prometheus is not reachable from the Next.js server. Ensure PROMETHEUS_URL is set correctly.", detectedAt: now },
        ],
      };

    default:
      return null;
  }
}
