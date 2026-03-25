/**
 * Prometheus HTTP API client — single source of truth for all metric queries.
 *
 * Server-only. Never imported by Client Components.
 * PROMETHEUS_URL is a private env var — never sent to the browser.
 * The only browser-reachable path to this data is GET /api/telemetry,
 * which is session-gated (see app/api/telemetry/route.ts).
 *
 * Prometheus query API: https://prometheus.io/docs/prometheus/latest/querying/api/
 */

import {
  pingActiveServices,
  buildDerivedPings,
  type ServicePingResult,
} from "./health-ping";

export type { ServicePingResult };

const PROM = process.env.PROMETHEUS_URL ?? "http://localhost:9090";

// ─── Raw Prometheus wire types ────────────────────────────────────────────────

interface PromResponse<T> {
  status: "success" | "error";
  data: T;
  errorType?: string;
  error?: string;
}

interface PromVectorResult {
  resultType: "vector";
  result: Array<{
    metric: Record<string, string>;
    value: [number, string];
  }>;
}

interface PromMatrixResult {
  resultType: "matrix";
  result: Array<{
    metric: Record<string, string>;
    values: Array<[number, string]>;
  }>;
}

// ─── Shared public types ──────────────────────────────────────────────────────

export interface TimePoint {
  t: number; // unix timestamp (seconds)
  v: number; // metric value
}

// ─── Developer snapshot types (existing) ─────────────────────────────────────

export interface ErrorsByLayer {
  layer: string;
  cause: string;
  component: string;
  count: number;
}

export interface RouteError {
  route: string;
  layer: string;
  cause: string;
  count: number;
}

export interface MetricSnapshot {
  requestsPerMin: number;
  errorRatePct: number;
  appErrorsTotal: number;
  jobsDeadTotal: number;
  errorsByLayer: ErrorsByLayer[];
  topErrorRoutes: RouteError[];
  requestRateSeries: TimePoint[];
  errorRateSeries: TimePoint[];
}

// ─── Security snapshot types ──────────────────────────────────────────────────

export type OverallStatus = "healthy" | "degraded" | "critical";
export type ServiceStatus = "healthy" | "degraded" | "down";

export interface ServiceHealth {
  name: string;
  status: ServiceStatus;
  detail: string;
}

export interface Anomaly {
  id: string;
  severity: "critical" | "warning" | "info";
  title: string;
  detail: string;
  detectedAt: number; // unix ms (Date.now())
}

export interface ErrorBreakdown {
  name: string; // component or layer label
  value: number; // error count
}

export interface SecuritySnapshot {
  overall: OverallStatus;
  services: ServiceHealth[];

  // HTTP traffic
  requestsPerMin: number;
  errorRatePct: number; // 0-100

  // Auth security signals
  loginFailuresPerMin: number;
  loginFailureRatePct: number; // 0-100
  accountLocksLastHour: number;
  tokenValidationFailuresPerMin: number;
  oauthUnlinksLastHour: number;
  passwordResetDeniedLastHour: number;
  registrationsLastHour: number;
  tokenRefreshesPerMin: number;
  sessionRevocationsLastHour: number;

  // Infrastructure
  dbPoolUtilPct: number; // 0-100
  dbPoolIdlePct: number; // 0-100
  dbUp: number | null; // 1 = ok, 0 = failed, null = metric not yet published
  redisUp: number | null; // 1 = ok, 0 = failed, null = metric not yet published
  redisStaleConnections: number;
  redisErrLastHour: number;
  redisPoolIdlePct: number; // 0-100
  processMemAllocMB: number;
  infraPollerAgeSeconds: number;
  goroutines: number;

  // Job queue
  deadJobsTotal: number;
  jobsSubmittedLastHour: number;
  jobsFailedLastHour: number;
  jobsRequeuedLastHour: number;
  jobDurationP95Sec: number | null;

  // Bitcoin ZMQ
  zmqConnected: number | null; // 1 = connected, 0 = disconnected, null = bitcoin disabled
  zmqDroppedLastHour: number;
  zmqLastMessageAgeSec: number | null;
  zmqHandlerPanicsLastHour: number;
  zmqHandlerTimeoutsLastHour: number;
  zmqHandlerGoroutines: number;

  // Bitcoin RPC
  rpcConnected: number | null; // null = bitcoin disabled
  keypoolSize: number | null;
  // null  = bitcoin disabled (BTC_ENABLED=false)
  // -1    = bitcoin enabled, no wallet loaded yet
  // 0     = wallet exists but pool exhausted — run keypoolrefill
  // 1-99  = low — run keypoolrefill soon
  // ≥100  = healthy
  rpcCallErrorsLastHour: number; // sum of all bitcoin_rpc_errors_total in last hour

  // Bitcoin financial integrity (ZERO TOLERANCE)
  balanceDriftSatoshis: number | null; // null = bitcoin disabled
  reconciliationHoldActive: boolean;
  reorgDetectedLastDay: number;
  payoutFailuresLastHour: number;
  sweepStuckLastHour: number;

  // Bitcoin operational
  sseConnectionsActive: number;
  rateFeedStalenessSec: number | null;
  reconciliationLagBlocks: number | null;
  walletBackupAgeSec: number | null;
  utxoCount: number | null;
  feeEstimate1Block: number | null;
  feeEstimate6Block: number | null;

  // Bitcoin invoices
  invoiceDetectionP50Sec: number | null;
  invoiceDetectionP95Sec: number | null;
  invoiceStatePending: number;
  invoiceStateConfirmed: number;
  invoiceStateExpired: number;

  // Active service ping results (includes both HTTP probes and derived pings)
  pingResults: ServicePingResult[];

  // Chart series (last 30 min, 1 data point per minute)
  loginFailureSeries: TimePoint[];
  errorRateSeries: TimePoint[];
  requestRateSeries: TimePoint[];

  // Error breakdown for bar chart
  errorsByComponent: ErrorBreakdown[];

  // Computed anomalies (sorted: critical first)
  anomalies: Anomaly[];

  fetchedAt: number; // Date.now() — used for "updated X seconds ago"
  prometheusReachable: boolean;
}

// ─── Internal helpers ─────────────────────────────────────────────────────────

async function instant(query: string): Promise<PromResponse<PromVectorResult>> {
  const url = new URL(`${PROM}/api/v1/query`);
  url.searchParams.set("query", query);
  const res = await fetch(url.toString(), { cache: "no-store" });
  if (!res.ok) throw new Error(`Prometheus ${res.status}: ${query}`);
  return res.json() as Promise<PromResponse<PromVectorResult>>;
}

async function rangeQuery(
  query: string,
  startSec: number,
  endSec: number,
  stepSec: number,
): Promise<PromResponse<PromMatrixResult>> {
  const url = new URL(`${PROM}/api/v1/query_range`);
  url.searchParams.set("query", query);
  url.searchParams.set("start", String(startSec));
  url.searchParams.set("end", String(endSec));
  url.searchParams.set("step", String(stepSec));
  const res = await fetch(url.toString(), { cache: "no-store" });
  if (!res.ok) throw new Error(`Prometheus range ${res.status}: ${query}`);
  return res.json() as Promise<PromResponse<PromMatrixResult>>;
}

function scalarOf(r: PromResponse<PromVectorResult>): number {
  const v = r.data.result[0]?.value[1];
  return v ? parseFloat(v) : 0;
}

/**
 * Returns null when the metric has no data (metric never published).
 * Used for gauges representing a binary state where absence ≠ zero:
 * db_up / redis_up / bitcoin_* — null means "feature disabled / backend not
 * restarted yet", distinct from 0 = "metric published, value is zero / down".
 *
 * NEVER use `or vector(0)` on binary-state gauges — it destroys the
 * null / 0 / 1 distinction that the anomaly rules depend on.
 */
function gaugeOf(r: PromResponse<PromVectorResult>): number | null {
  const v = r.data.result[0]?.value[1];
  if (v === undefined) return null;
  const n = parseFloat(v);
  return isNaN(n) ? null : n;
}

function seriesOf(r: PromResponse<PromMatrixResult>): TimePoint[] {
  const row = r.data.result[0];
  if (!row || !Array.isArray(row.values)) return [];
  return row.values.map(([t, v]) => ({ t, v: parseFloat(v) }));
}

function round2(n: number): number {
  return Math.round(n * 100) / 100;
}

/**
 * Ensures a time series covers the full window with no gaps.
 * When Prometheus has never seen a metric, range queries return an empty result
 * even with `or vector(0)`. This fills any empty/short series with zero-valued
 * points at the expected step interval so charts always render.
 */
function fillSeries(
  series: TimePoint[],
  endSec: number,
  windowSec: number,
  stepSec: number,
): TimePoint[] {
  if (series.length > 0) return series;
  const points: TimePoint[] = [];
  const startSec = endSec - windowSec;
  for (let t = startSec; t <= endSec; t += stepSec) {
    points.push({ t, v: 0 });
  }
  return points;
}

// ─── Anomaly computation ──────────────────────────────────────────────────────

type PartialSnap = Omit<
  SecuritySnapshot,
  | "anomalies"
  | "overall"
  | "services"
  | "fetchedAt"
  | "prometheusReachable"
  | "errorsByComponent"
>;

function computeAnomalies(snap: PartialSnap): Anomaly[] {
  const now = Date.now();
  const anomalies: Anomaly[] = [];

  // ── HTTP ──────────────────────────────────────────────────────────────────
  if (snap.errorRatePct > 1) {
    anomalies.push({
      id: "http_error_critical",
      severity: "critical",
      title: "HTTP error rate critical",
      detail: `${snap.errorRatePct.toFixed(2)}% of requests are returning 5xx — SLO-1 actively breaching.`,
      detectedAt: now,
    });
  } else if (snap.errorRatePct > 0.1) {
    anomalies.push({
      id: "http_error_elevated",
      severity: "warning",
      title: "HTTP error rate elevated",
      detail: `${snap.errorRatePct.toFixed(2)}% error rate over last 5 min.`,
      detectedAt: now,
    });
  }

  // ── Auth ──────────────────────────────────────────────────────────────────
  if (snap.loginFailuresPerMin > 10) {
    anomalies.push({
      id: "login_failure_spike",
      severity: "critical",
      title: "Login failure spike",
      detail: `${snap.loginFailuresPerMin.toFixed(1)}/min — possible credential stuffing attack.`,
      detectedAt: now,
    });
  } else if (snap.loginFailuresPerMin > 3) {
    anomalies.push({
      id: "login_failure_elevated",
      severity: "warning",
      title: "Elevated login failures",
      detail: `${snap.loginFailuresPerMin.toFixed(1)}/min — above normal baseline.`,
      detectedAt: now,
    });
  }

  if (snap.loginFailureRatePct > 50) {
    anomalies.push({
      id: "high_failure_rate",
      severity: "critical",
      title: "Critical login failure rate",
      detail: `${snap.loginFailureRatePct.toFixed(0)}% of login attempts failing — HighInvalidCredentialRate alert threshold exceeded.`,
      detectedAt: now,
    });
  }

  if (snap.accountLocksLastHour > 20) {
    anomalies.push({
      id: "account_lockout_accumulation",
      severity: "critical",
      title: "Account lockout accumulation",
      detail: `${snap.accountLocksLastHour} accounts auto-locked in the past hour — possible distributed brute-force.`,
      detectedAt: now,
    });
  } else if (snap.accountLocksLastHour > 5) {
    anomalies.push({
      id: "account_lockout_elevated",
      severity: "warning",
      title: "Elevated account lockouts",
      detail: `${snap.accountLocksLastHour} accounts locked in the past hour.`,
      detectedAt: now,
    });
  }

  if (snap.tokenValidationFailuresPerMin > 0.5) {
    anomalies.push({
      id: "token_validation_spike",
      severity: "warning",
      title: "Token validation failures",
      detail: `${snap.tokenValidationFailuresPerMin.toFixed(2)}/min — possible token replay or JWT signature attack.`,
      detectedAt: now,
    });
  }

  if (snap.oauthUnlinksLastHour > 5) {
    anomalies.push({
      id: "oauth_unlink_spike",
      severity: "warning",
      title: "OAuth unlink spike",
      detail: `${snap.oauthUnlinksLastHour} providers unlinked in the past hour — possible account takeover campaign.`,
      detectedAt: now,
    });
  }

  if (snap.passwordResetDeniedLastHour > 10) {
    anomalies.push({
      id: "password_reset_enumeration",
      severity: "warning",
      title: "User enumeration attempt",
      detail: `${snap.passwordResetDeniedLastHour} password resets for non-existent accounts in the past hour.`,
      detectedAt: now,
    });
  }

  // ── Database ──────────────────────────────────────────────────────────────
  if (snap.dbUp === 0) {
    anomalies.push({
      id: "db_down",
      severity: "critical",
      title: "Database unreachable",
      detail: "DB ping failed — all database operations are failing. Service is critically impaired.",
      detectedAt: now,
    });
  } else if (snap.dbPoolUtilPct >= 100) {
    anomalies.push({
      id: "db_pool_exhausted",
      severity: "critical",
      title: "DB pool exhausted",
      detail: "Connection pool is 100% utilized — all new DB requests are blocked.",
      detectedAt: now,
    });
  } else if (snap.dbPoolUtilPct > 85) {
    anomalies.push({
      id: "db_pool_saturation",
      severity: "warning",
      title: "DB pool near saturation",
      detail: `Pool at ${snap.dbPoolUtilPct.toFixed(0)}% — requests may begin queuing.`,
      detectedAt: now,
    });
  }

  // ── Redis ─────────────────────────────────────────────────────────────────
  if (snap.redisUp === 0) {
    anomalies.push({
      id: "redis_down",
      severity: "critical",
      title: "Cache / Sessions down",
      detail: "Redis ping failed — rate-limiting and token blocklist are degraded. Auth requests fail open.",
      detectedAt: now,
    });
  } else if (snap.redisStaleConnections > 5) {
    anomalies.push({
      id: "redis_stale_high",
      severity: "critical",
      title: "Cache / Sessions degraded",
      detail: `${snap.redisStaleConnections} stale connections — Redis may have restarted or be unstable.`,
      detectedAt: now,
    });
  } else if (snap.redisStaleConnections > 0) {
    anomalies.push({
      id: "redis_stale",
      severity: "warning",
      title: "Redis connectivity degraded",
      detail: `${snap.redisStaleConnections} stale connection${snap.redisStaleConnections > 1 ? "s" : ""} — Redis may be flapping.`,
      detectedAt: now,
    });
  }

  if (snap.redisStaleConnections === 0 && snap.redisErrLastHour > 0) {
    anomalies.push({
      id: "redis_recovered",
      severity: "info",
      title: "Redis recovered — errors in past hour",
      detail: `${Math.round(snap.redisErrLastHour)} Redis error${snap.redisErrLastHour > 1 ? "s" : ""} recorded in the past hour. Service is now healthy.`,
      detectedAt: now,
    });
  }

  // ── Infrastructure ────────────────────────────────────────────────────────
  if (snap.goroutines > 500) {
    anomalies.push({
      id: "goroutine_leak",
      severity: "warning",
      title: "Possible goroutine leak",
      detail: `${snap.goroutines} goroutines — sustained high count indicates a leak.`,
      detectedAt: now,
    });
  }

  // Infra poller heartbeat — CRITICAL (fires InfraPollerDown alert within 60s)
  if (snap.infraPollerAgeSeconds > 60) {
    anomalies.push({
      id: "infra_poller_stale",
      severity: "critical",
      title: "Infra poller stopped",
      detail: `Last heartbeat ${Math.round(snap.infraPollerAgeSeconds)}s ago — DB/Redis health gauges are now stale.`,
      detectedAt: now,
    });
  }

  // ── Job queue ─────────────────────────────────────────────────────────────
  if (snap.deadJobsTotal > 0) {
    anomalies.push({
      id: "dead_jobs",
      severity: "warning",
      title: "Dead jobs accumulating",
      detail: `${snap.deadJobsTotal} job${snap.deadJobsTotal > 1 ? "s" : ""} exhausted all retries.`,
      detectedAt: now,
    });
  }

  if (snap.jobsRequeuedLastHour > 0) {
    anomalies.push({
      id: "jobs_requeued",
      severity: "warning",
      title: "Jobs requeued by stall detector",
      detail: `${snap.jobsRequeuedLastHour} job${snap.jobsRequeuedLastHour > 1 ? "s" : ""} requeued in the past hour — a worker crashed or timed out.`,
      detectedAt: now,
    });
  }

  // ── Bitcoin ZMQ ───────────────────────────────────────────────────────────
  if (snap.zmqConnected === 0) {
    anomalies.push({
      id: "zmq_disconnected",
      severity: "critical",
      title: "Bitcoin ZMQ disconnected",
      detail: "ZMQ subscriber lost connection to Bitcoin Core — block and tx events have stopped.",
      detectedAt: now,
    });
  }

  if (snap.zmqDroppedLastHour > 0) {
    anomalies.push({
      id: "zmq_messages_dropped",
      severity: "warning",
      title: "Bitcoin ZMQ messages dropped",
      detail: `${Math.round(snap.zmqDroppedLastHour)} ZMQ message${snap.zmqDroppedLastHour !== 1 ? "s" : ""} dropped in the past hour (HWM overflow or sequence gap).`,
      detectedAt: now,
    });
  }

  if (snap.zmqHandlerPanicsLastHour > 0) {
    anomalies.push({
      id: "zmq_handler_panics",
      severity: "warning",
      title: "Bitcoin ZMQ handler panics",
      detail: `${snap.zmqHandlerPanicsLastHour} handler panic${snap.zmqHandlerPanicsLastHour > 1 ? "s" : ""} in the past hour — check backend logs for stack traces.`,
      detectedAt: now,
    });
  }

  if (snap.zmqHandlerTimeoutsLastHour > 0) {
    anomalies.push({
      id: "zmq_handler_timeouts",
      severity: "warning",
      title: "Bitcoin ZMQ handler timeouts",
      detail: `${snap.zmqHandlerTimeoutsLastHour} handler timeout${snap.zmqHandlerTimeoutsLastHour > 1 ? "s" : ""} in the past hour — goroutines may be leaking.`,
      detectedAt: now,
    });
  }

  // ── Bitcoin RPC ───────────────────────────────────────────────────────────
  // rpcConnected === null means Bitcoin is disabled — do not fire
  if (snap.rpcConnected === 0) {
    anomalies.push({
      id: "rpc_disconnected",
      severity: "critical",
      title: "Bitcoin RPC disconnected",
      detail: "RPC client cannot reach Bitcoin Core — fee estimation and broadcasting are unavailable.",
      detectedAt: now,
    });
  }

  if (snap.keypoolSize === -1) {
    anomalies.push({
      id: "keypool_no_wallet",
      severity: "warning",
      title: "No Bitcoin wallet loaded",
      detail: "Bitcoin Core has no wallet. Invoice creation is unavailable. Run: bitcoin-cli createwallet \"store\"",
      detectedAt: now,
    });
  } else if (snap.keypoolSize !== null && snap.keypoolSize >= 0 && snap.keypoolSize < 10) {
    anomalies.push({
      id: "keypool_critical",
      severity: "critical",
      title: "Bitcoin keypool critically low",
      detail: `Only ${snap.keypoolSize} pre-generated address${snap.keypoolSize !== 1 ? "es" : ""} remain — invoice creation will fail imminently. Run keypoolrefill immediately.`,
      detectedAt: now,
    });
  } else if (snap.keypoolSize !== null && snap.keypoolSize < 100) {
    anomalies.push({
      id: "keypool_low",
      severity: "warning",
      title: "Bitcoin keypool running low",
      detail: `${snap.keypoolSize} addresses remaining in keypool (threshold: 100). Run keypoolrefill before it hits zero.`,
      detectedAt: now,
    });
  }

  if (snap.rpcConnected !== null && snap.rpcCallErrorsLastHour > 10) {
    anomalies.push({
      id: "rpc_errors_elevated",
      severity: "warning",
      title: "Bitcoin RPC errors elevated",
      detail: `${snap.rpcCallErrorsLastHour} RPC call error${snap.rpcCallErrorsLastHour !== 1 ? "s" : ""} in the past hour — check node connectivity and wallet state.`,
      detectedAt: now,
    });
  }

  // ── Bitcoin financial integrity (ZERO TOLERANCE) ──────────────────────────
  if (snap.balanceDriftSatoshis !== null && snap.balanceDriftSatoshis !== 0) {
    anomalies.push({
      id: "balance_drift_nonzero",
      severity: "critical",
      title: "Bitcoin balance drift detected",
      detail: `bitcoin_balance_drift_satoshis = ${snap.balanceDriftSatoshis} sat. Accounting integrity violated — reconciliation hold may be active.`,
      detectedAt: now,
    });
  }

  if (snap.reconciliationHoldActive) {
    anomalies.push({
      id: "reconciliation_hold_active",
      severity: "critical",
      title: "Reconciliation hold active",
      detail: "Sweep hold mode is active — all outbound Bitcoin transactions are paused until drift is resolved.",
      detectedAt: now,
    });
  }

  if (snap.reorgDetectedLastDay > 0) {
    anomalies.push({
      id: "reorg_detected",
      severity: "critical",
      title: "Blockchain reorganisation detected",
      detail: `${snap.reorgDetectedLastDay} reorg${snap.reorgDetectedLastDay > 1 ? "s" : ""} detected in the past 24 h — verify confirmed transactions.`,
      detectedAt: now,
    });
  }

  if (snap.payoutFailuresLastHour > 0) {
    anomalies.push({
      id: "payout_failure",
      severity: "critical",
      title: "Bitcoin payout failures",
      detail: `${snap.payoutFailuresLastHour} payout failure${snap.payoutFailuresLastHour > 1 ? "s" : ""} in the past hour — zero-tolerance threshold exceeded.`,
      detectedAt: now,
    });
  }

  if (snap.sweepStuckLastHour > 0) {
    anomalies.push({
      id: "sweep_stuck",
      severity: "warning",
      title: "Bitcoin sweep stuck",
      detail: `${snap.sweepStuckLastHour} sweep${snap.sweepStuckLastHour > 1 ? "s" : ""} detected as stuck in the past hour.`,
      detectedAt: now,
    });
  }

  // ── Bitcoin operational ───────────────────────────────────────────────────
  if (
    snap.walletBackupAgeSec !== null &&
    snap.walletBackupAgeSec > 86_400
  ) {
    anomalies.push({
      id: "wallet_backup_stale",
      severity: "warning",
      title: "Wallet backup stale",
      detail: `Last backup ${Math.round(snap.walletBackupAgeSec / 3600)}h ago — exceeds 24 h retention target.`,
      detectedAt: now,
    });
  }

  if (
    snap.rateFeedStalenessSec !== null &&
    snap.rateFeedStalenessSec > 300
  ) {
    anomalies.push({
      id: "rate_feed_stale",
      severity: "warning",
      title: "Bitcoin rate feed stale",
      detail: `Exchange rate last updated ${Math.round(snap.rateFeedStalenessSec)}s ago (> 5 min threshold).`,
      detectedAt: now,
    });
  }

  if (
    snap.reconciliationLagBlocks !== null &&
    snap.reconciliationLagBlocks > 6
  ) {
    anomalies.push({
      id: "reconciliation_lag",
      severity: "warning",
      title: "Reconciliation lag",
      detail: `${snap.reconciliationLagBlocks} blocks behind chain tip (~${Math.round(snap.reconciliationLagBlocks * 10)} min) — exceeds 6-block threshold.`,
      detectedAt: now,
    });
  }

  // ── Bitcoin invoice performance ───────────────────────────────────────────
  if (
    snap.invoiceDetectionP95Sec !== null &&
    snap.invoiceDetectionP95Sec > 60
  ) {
    anomalies.push({
      id: "invoice_detection_slow",
      severity: "warning",
      title: "Invoice detection slow",
      detail: `P95 detection latency is ${snap.invoiceDetectionP95Sec.toFixed(0)}s — exceeds 60s threshold.`,
      detectedAt: now,
    });
  }

  // ── Active pings ──────────────────────────────────────────────────────────
  const backendPing = snap.pingResults.find((p) => p.name === "Backend API");
  if (backendPing?.status === "down") {
    anomalies.push({
      id: "backend_ping_failed",
      severity: "critical",
      title: "Backend API unreachable",
      detail: `Active health probe failed: ${backendPing.detail}`,
      detectedAt: now,
    });
  }

  return anomalies.sort((a, b) => {
    const rank = { critical: 0, warning: 1, info: 2 } as const;
    return rank[a.severity] - rank[b.severity];
  });
}

// ─── Service health derivation ────────────────────────────────────────────────

function deriveServices(p: {
  dbUp: number | null;
  dbErr: number;
  redisUp: number | null;
  redisErrRecent: number;
  redisStale: number;
  mailerErr: number;
  dbPoolUtil: number;
  deadJobs: number;
  errorRatePct: number;
  zmqConnected: number | null;
  zmqLastMessageAgeSec: number | null;
  rpcConnected: number | null;
  reconciliationLagBlocks: number | null;
  balanceDriftSatoshis: number | null;
  reconciliationHoldActive: boolean;
  payoutFailuresLastHour: number;
  sweepStuckLastHour: number;
  backendPingStatus: "up" | "down" | "unknown";
}): ServiceHealth[] {
  // Backend API ─ driven by active ping result
  const apiStatus: ServiceStatus =
    p.backendPingStatus === "down"
      ? "down"
      : p.errorRatePct > 1
        ? "down"
        : p.errorRatePct > 0.1
          ? "degraded"
          : "healthy";

  const dbPingFailed = p.dbUp === 0;
  const db: ServiceStatus = dbPingFailed
    ? "down"
    : p.dbPoolUtil >= 100
      ? "down"
      : p.dbErr > 10 || p.dbPoolUtil > 85
        ? "degraded"
        : p.dbErr > 0
          ? "degraded"
          : "healthy";

  const redisPingFailed = p.redisUp === 0;
  const redis: ServiceStatus = redisPingFailed
    ? "down"
    : p.redisStale > 5 || p.redisErrRecent > 5
      ? "down"
      : p.redisStale > 0 || p.redisErrRecent > 0
        ? "degraded"
        : "healthy";

  const redisDetail = (): string => {
    if (redis === "healthy") return "Redis responding normally";
    if (redisPingFailed) return "Redis unreachable — ping failed";
    const parts: string[] = [];
    if (p.redisStale > 0)
      parts.push(`${Math.round(p.redisStale)} stale connection${p.redisStale > 1 ? "s" : ""}`);
    if (p.redisErrRecent > 0)
      parts.push(`${Math.round(p.redisErrRecent)} error${p.redisErrRecent > 1 ? "s" : ""} in last 2 min`);
    return parts.join(", ");
  };

  const mailer: ServiceStatus =
    p.mailerErr > 5 ? "down" : p.mailerErr > 0 ? "degraded" : "healthy";

  const jobs: ServiceStatus = p.deadJobs > 0 ? "degraded" : "healthy";

  const services: ServiceHealth[] = [
    {
      name: "API",
      status: apiStatus,
      detail:
        apiStatus === "healthy"
          ? "All routes responding normally"
          : p.backendPingStatus === "down"
            ? "Backend health probe failed"
            : `${p.errorRatePct.toFixed(2)}% of requests returning 5xx`,
    },
    {
      name: "Database",
      status: db,
      detail:
        db === "healthy"
          ? "Pool healthy and responsive"
          : dbPingFailed
            ? "Database unreachable — ping failed"
            : p.dbPoolUtil >= 100
              ? "Pool exhausted — requests blocking"
              : `${p.dbPoolUtil.toFixed(0)}% pool utilization${p.dbErr > 0 ? `, ${Math.round(p.dbErr)} errors/hr` : ""}`,
    },
    {
      name: "Cache / Sessions",
      status: redis,
      detail: redisDetail(),
    },
    {
      name: "Email delivery",
      status: mailer,
      detail:
        mailer === "healthy"
          ? "Delivering normally"
          : `${Math.round(p.mailerErr)} SMTP failures in the past hour`,
    },
    {
      name: "Job queue",
      status: jobs,
      detail:
        jobs === "healthy"
          ? "All jobs processing normally"
          : `${p.deadJobs} dead job${p.deadJobs > 1 ? "s" : ""} — exhausted retries`,
    },
  ];

  // Bitcoin services — only when Bitcoin is deployed (zmqConnected !== null)
  if (p.zmqConnected !== null) {
    // ZMQ: degrade if message age > 2 × idleTimeout (240 s) even when connected
    const zmqMsgStale =
      p.zmqConnected === 1 &&
      p.zmqLastMessageAgeSec !== null &&
      p.zmqLastMessageAgeSec > 240;

    const zmqStatus: ServiceStatus =
      p.zmqConnected === 0 ? "down" : zmqMsgStale ? "degraded" : "healthy";

    services.push({
      name: "Bitcoin ZMQ",
      status: zmqStatus,
      detail:
        zmqStatus === "down"
          ? "Disconnected — block and tx events paused"
          : zmqMsgStale
            ? `Idle — last message ${Math.round(p.zmqLastMessageAgeSec!)}s ago`
            : "Connected to Bitcoin Core",
    });

    // RPC: conditional on rpcConnected being published
    if (p.rpcConnected !== null) {
      const rpcLag = (p.reconciliationLagBlocks ?? 0) > 6;
      const noWallet = p.keypoolSize === -1;
      const keypoolCritical = p.keypoolSize !== null && p.keypoolSize >= 0 && p.keypoolSize < 10;
      const keypoolLow = p.keypoolSize !== null && p.keypoolSize >= 0 && p.keypoolSize < 100 && !keypoolCritical;
      const rpcStatus: ServiceStatus =
        p.rpcConnected === 0 ? "down"
        : keypoolCritical ? "down"
        : noWallet || rpcLag || keypoolLow ? "degraded"
        : "healthy";

      services.push({
        name: "Bitcoin RPC",
        status: rpcStatus,
        detail:
          p.rpcConnected === 0
            ? "RPC client disconnected from Bitcoin Core"
            : noWallet
              ? "No wallet loaded — run bitcoin-cli createwallet"
              : keypoolCritical
                ? `Keypool critical — ${p.keypoolSize} addresses left`
                : keypoolLow
                  ? `Keypool low — ${p.keypoolSize} addresses left`
                  : rpcLag
                    ? `${p.reconciliationLagBlocks} blocks behind chain tip`
                    : "Connected to Bitcoin Core",
      });
    }

    // Financial integrity (zero-tolerance entry)
    const integrityDown =
      (p.balanceDriftSatoshis !== null && p.balanceDriftSatoshis !== 0) ||
      p.reconciliationHoldActive;
    const integrityDegraded =
      !integrityDown && (p.payoutFailuresLastHour > 0 || p.sweepStuckLastHour > 0);

    const integrityStatus: ServiceStatus = integrityDown
      ? "down"
      : integrityDegraded
        ? "degraded"
        : "healthy";

    services.push({
      name: "Bitcoin integrity",
      status: integrityStatus,
      detail:
        integrityStatus === "healthy"
          ? "Balance drift zero — accounting clean"
          : (p.balanceDriftSatoshis ?? 0) !== 0
            ? `Balance drift: ${p.balanceDriftSatoshis} sat`
            : p.reconciliationHoldActive
              ? "Reconciliation hold active — sweeps paused"
              : p.payoutFailuresLastHour > 0
                ? `${p.payoutFailuresLastHour} payout failure(s) in past hour`
                : `${p.sweepStuckLastHour} stuck sweep(s) detected`,
    });
  }

  return services;
}

// ─── Prometheus unreachable fallback ──────────────────────────────────────────

function unreachableSnapshot(): SecuritySnapshot {
  return {
    overall: "degraded",
    services: [
      {
        name: "Monitoring backend",
        status: "down",
        detail: `Prometheus not reachable at ${PROM}. Is the container running? Check the Next.js terminal for details.`,
      },
    ],
    requestsPerMin: 0,
    errorRatePct: 0,
    loginFailuresPerMin: 0,
    loginFailureRatePct: 0,
    accountLocksLastHour: 0,
    tokenValidationFailuresPerMin: 0,
    oauthUnlinksLastHour: 0,
    passwordResetDeniedLastHour: 0,
    registrationsLastHour: 0,
    tokenRefreshesPerMin: 0,
    sessionRevocationsLastHour: 0,
    dbPoolUtilPct: 0,
    dbPoolIdlePct: 0,
    dbUp: null,
    redisUp: null,
    redisStaleConnections: 0,
    redisErrLastHour: 0,
    redisPoolIdlePct: 0,
    processMemAllocMB: 0,
    infraPollerAgeSeconds: 0,
    goroutines: 0,
    deadJobsTotal: 0,
    jobsSubmittedLastHour: 0,
    jobsFailedLastHour: 0,
    jobsRequeuedLastHour: 0,
    jobDurationP95Sec: null,
    zmqConnected: null,
    zmqDroppedLastHour: 0,
    zmqLastMessageAgeSec: null,
    zmqHandlerPanicsLastHour: 0,
    zmqHandlerTimeoutsLastHour: 0,
    zmqHandlerGoroutines: 0,
    rpcConnected: null,
    keypoolSize: null,
    rpcCallErrorsLastHour: 0,
    balanceDriftSatoshis: null,
    reconciliationHoldActive: false,
    reorgDetectedLastDay: 0,
    payoutFailuresLastHour: 0,
    sweepStuckLastHour: 0,
    sseConnectionsActive: 0,
    rateFeedStalenessSec: null,
    reconciliationLagBlocks: null,
    walletBackupAgeSec: null,
    utxoCount: null,
    feeEstimate1Block: null,
    feeEstimate6Block: null,
    invoiceDetectionP50Sec: null,
    invoiceDetectionP95Sec: null,
    invoiceStatePending: 0,
    invoiceStateConfirmed: 0,
    invoiceStateExpired: 0,
    pingResults: [],
    loginFailureSeries: [],
    errorRateSeries: [],
    requestRateSeries: [],
    errorsByComponent: [],
    anomalies: [
      {
        id: "prometheus_down",
        severity: "critical",
        title: "Monitoring backend unreachable",
        detail: `Cannot reach Prometheus at ${PROM}. Run: docker compose ps — check the Next.js terminal for the exact error.`,
        detectedAt: Date.now(),
      },
    ],
    fetchedAt: Date.now(),
    prometheusReachable: false,
  };
}

// ─── Public API: developer metric snapshot (existing) ────────────────────────

export async function fetchMetricSnapshot(): Promise<MetricSnapshot> {
  const now = Math.floor(Date.now() / 1000);
  const thirtyMinsAgo = now - 30 * 60;

  const [
    reqRate,
    errRate,
    appErrTotal,
    jobsDead,
    errsByLayer,
    topRoutes,
    reqRateSeries,
    errRateSeries,
  ] = await Promise.all([
    instant("sum(rate(http_requests_total[5m])) * 60"),
    instant(
      'sum(rate(http_requests_total{status=~"5.."}[5m])) / sum(rate(http_requests_total[5m])) * 100',
    ),
    instant("sum(app_errors_total)"),
    instant("sum(jobqueue_jobs_dead_total)"),
    instant("sort_desc(app_errors_total)"),
    instant("sort_desc(sum by (route, layer, cause) (http_errors_total))"),
    rangeQuery(
      "sum(rate(http_requests_total[1m])) * 60",
      thirtyMinsAgo,
      now,
      60,
    ),
    rangeQuery(
      'sum(rate(http_requests_total{status=~"5.."}[1m])) * 60',
      thirtyMinsAgo,
      now,
      60,
    ),
  ]);

  return {
    requestsPerMin: Math.round(scalarOf(reqRate)),
    errorRatePct: round2(scalarOf(errRate)),
    appErrorsTotal: Math.round(scalarOf(appErrTotal)),
    jobsDeadTotal: Math.round(scalarOf(jobsDead)),
    errorsByLayer: errsByLayer.data.result.map((r) => ({
      layer: r.metric.layer ?? "unknown",
      cause: r.metric.cause ?? "unknown",
      component: r.metric.component ?? "unknown",
      count: parseFloat(r.value[1]),
    })),
    topErrorRoutes: topRoutes.data.result.slice(0, 10).map((r) => ({
      route: r.metric.route ?? "/",
      layer: r.metric.layer ?? "unknown",
      cause: r.metric.cause ?? "unknown",
      count: parseFloat(r.value[1]),
    })),
    requestRateSeries: seriesOf(reqRateSeries),
    errorRateSeries: seriesOf(errRateSeries),
  };
}

// ─── Public API: security + health snapshot ────────────────────────────────────
// All queries and active pings run in a single Promise.all (zero waterfall).
// Safe to call from Server Components.

export async function fetchSecuritySnapshot(
  opts?: { skipPings?: boolean },
): Promise<SecuritySnapshot> {
  const now = Math.floor(Date.now() / 1000);
  const thirtyMinsAgo = now - 30 * 60;

  try {
    const [
      // ── HTTP (3) ──────────────────────────────────────────────────────────
      reqRate,
      errRateRaw,
      errorsByComp,

      // ── Auth (9) ──────────────────────────────────────────────────────────
      loginFailRate,
      loginTotalRate,
      accountLocks,
      tokenValidFail,
      oauthUnlinks,
      pwResetDenied,
      registrations,
      tokenRefreshes,
      sessionRevocations,

      // ── Infrastructure (11) ───────────────────────────────────────────────
      dbUpRaw,
      dbAcquired,
      dbMax,
      dbPoolIdleRaw,
      redisUpRaw,
      redisStale,
      redisPoolIdleRaw,
      redisPoolTotalRaw,
      processMemRaw,
      infraPollerAgeRaw,
      goroutines,

      // ── Infra error signals (4) ───────────────────────────────────────────
      dbErrHour,
      redisErrHour,
      redisErr5m,
      mailerErrHour,

      // ── Job queue (5 + dead) ──────────────────────────────────────────────
      deadJobs,
      jobsSubmittedRaw,
      jobsFailedRaw,
      jobsRequeuedRaw,
      jobDurationP95Raw,

      // ── Bitcoin ZMQ (7) ───────────────────────────────────────────────────
      zmqConnectedRaw,
      zmqDroppedRaw,
      zmqLastMsgAgeRaw,
      handlerPanicsRaw,
      handlerTimeoutsRaw,
      handlerGoroutinesRaw,
      sseConnectionsRaw,

      // ── Bitcoin RPC (3) ───────────────────────────────────────────────────
      rpcConnectedRaw,
      keypoolSizeRaw,
      rpcCallErrorsRaw,

      // ── Bitcoin financial integrity (5) ───────────────────────────────────
      balanceDriftRaw,
      reconHoldRaw,
      reorgDetectedRaw,
      payoutFailuresRaw,
      sweepStuckRaw,

      // ── Bitcoin operational (4) ───────────────────────────────────────────
      rateFeedStaleRaw,
      reconLagRaw,
      walletBackupAgeRaw,
      utxoCountRaw,

      // ── Bitcoin fee estimates (2) ─────────────────────────────────────────
      feeEst1Raw,
      feeEst6Raw,

      // ── Bitcoin invoice state (3) ─────────────────────────────────────────
      invoicePendingRaw,
      invoiceConfirmedRaw,
      invoiceExpiredRaw,

      // ── Bitcoin invoice detection histogram (2) ───────────────────────────
      invoiceDetP50Raw,
      invoiceDetP95Raw,

      // ── Active pings (runs in parallel with everything above) ─────────────
      activePings,

      // ── Chart series (3) ──────────────────────────────────────────────────
      loginFailSeries,
      errRateSeries,
      reqRateSeries,
    ] = await Promise.all([
      // HTTP
      instant("sum(rate(http_requests_total[5m])) * 60 or vector(0)"),
      instant(
        'sum(rate(http_requests_total{status=~"5.."}[5m])) / sum(rate(http_requests_total[5m])) * 100 or vector(0)',
      ),
      instant("sort_desc(sum by (component) (increase(app_errors_total[1h])))"),

      // Auth
      instant("sum(rate(auth_login_failures_total[5m])) * 60 or vector(0)"),
      instant("sum(rate(auth_logins_total[5m])) * 60 or vector(0)"),
      instant(
        'sum(increase(auth_user_locks_total{reason="auto_lockout"}[1h])) or vector(0)',
      ),
      instant(
        "sum(rate(auth_token_validation_failures_total[5m])) * 60 or vector(0)",
      ),
      instant("sum(increase(auth_oauth_unlinks_total[1h])) or vector(0)"),
      instant(
        'sum(increase(auth_password_resets_denied_total{reason="account_not_found"}[1h])) or vector(0)',
      ),
      instant(
        'sum(increase(auth_registrations_total{status="success"}[1h])) or vector(0)',
      ),
      instant("sum(rate(auth_token_refreshes_total[5m])) * 60 or vector(0)"),
      instant("increase(auth_session_revocations_total[1h]) or vector(0)"),

      // Infrastructure
      instant("db_up"),
      instant("db_pool_acquired_connections or vector(0)"),
      instant("db_pool_max_connections or vector(1)"),
      instant("db_pool_idle_connections or vector(0)"),
      instant("redis_up"),
      instant("redis_pool_stale_connections or vector(0)"),
      instant("redis_pool_idle_connections or vector(0)"),
      instant("redis_pool_total_connections or vector(1)"),
      instant("process_memory_alloc_bytes or vector(0)"),
      // infra_poller_last_run_timestamp_seconds: `or vector(0)` → age=0 at fresh deploy (poller just started, not stuck)
      instant("time() - infra_poller_last_run_timestamp_seconds or vector(0)"),
      instant("process_goroutines or vector(0)"),

      // Infra error signals
      instant(
        'sum(increase(app_errors_total{cause=~"db_error|db_pool|db_timeout"}[1h])) or vector(0)',
      ),
      instant(
        'sum(increase(app_errors_total{layer="kvstore"}[1h])) or vector(0)',
      ),
      instant(
        'sum(rate(app_errors_total{layer="kvstore"}[2m])) * 120 or vector(0)',
      ),
      instant(
        'sum(increase(app_errors_total{component="mailer"}[1h])) or vector(0)',
      ),

      // Job queue
      instant("sum(increase(jobqueue_jobs_dead_total[1h])) or vector(0)"),
      instant("sum(increase(jobqueue_jobs_submitted_total[1h])) or vector(0)"),
      instant("sum(increase(jobqueue_jobs_failed_total[1h])) or vector(0)"),
      instant("jobqueue_jobs_requeued_total or vector(0)"),
      // histogram: gaugeOf → null if no observations yet
      instant(
        "histogram_quantile(0.95, sum(rate(jobqueue_job_duration_seconds_bucket[1h])) by (le))",
      ),

      // Bitcoin ZMQ — all binary-state gauges use gaugeOf (no or vector(0))
      instant("bitcoin_zmq_connected"),
      instant("sum(increase(dropped_zmq_messages_total[1h])) or vector(0)"),
      instant("bitcoin_zmq_last_message_age_seconds"),
      instant(
        "sum(increase(bitcoin_handler_panics_total[1h])) or vector(0)",
      ),
      instant(
        "sum(increase(bitcoin_handler_timeouts_total[1h])) or vector(0)",
      ),
      instant("bitcoin_handler_goroutines_inflight or vector(0)"),
      instant("bitcoin_sse_connections_active or vector(0)"),

      // Bitcoin RPC
      instant("bitcoin_rpc_connected"),
      instant("bitcoin_keypool_size"),
      instant("sum(increase(bitcoin_rpc_errors_total[1h])) or vector(0)"),

      // Bitcoin financial integrity — binary-state gauges use gaugeOf
      instant("bitcoin_balance_drift_satoshis"),
      instant("bitcoin_reconciliation_hold_active"),
      instant(
        "sum(increase(bitcoin_reorg_detected_total[24h])) or vector(0)",
      ),
      instant("increase(bitcoin_payout_failure_total[1h]) or vector(0)"),
      instant("increase(bitcoin_sweep_stuck_total[1h]) or vector(0)"),

      // Bitcoin operational gauges — gaugeOf (null = bitcoin disabled)
      instant("bitcoin_rate_feed_staleness_seconds"),
      instant("bitcoin_reconciliation_lag_blocks"),
      instant("bitcoin_wallet_backup_age_seconds"),
      instant("bitcoin_utxo_count"),

      // Bitcoin fee estimates
      instant('bitcoin_fee_estimate_sat_per_vbyte{target_blocks="1"}'),
      instant('bitcoin_fee_estimate_sat_per_vbyte{target_blocks="6"}'),

      // Bitcoin invoice state gauges
      instant('bitcoin_invoice_state_total{status="pending"}'),
      instant('bitcoin_invoice_state_total{status="confirmed"}'),
      instant('bitcoin_invoice_state_total{status="expired"}'),

      // Bitcoin invoice detection histogram — gaugeOf (null = no observations yet)
      instant(
        "histogram_quantile(0.5, sum(rate(bitcoin_invoice_detection_duration_seconds_bucket[30m])) by (le))",
      ),
      instant(
        "histogram_quantile(0.95, sum(rate(bitcoin_invoice_detection_duration_seconds_bucket[30m])) by (le))",
      ),

      // Active HTTP pings — runs concurrently with all Prometheus queries.
      // When skipPings=true (e.g. CI), returns "unknown" stubs immediately.
      opts?.skipPings
        ? Promise.resolve([
            { name: "Backend API", status: "unknown" as const, latencyMs: null, detail: "Ping skipped (ping=skip)" },
            { name: "Database",    status: "unknown" as const, latencyMs: null, detail: "Ping skipped (ping=skip)" },
            { name: "Redis",       status: "unknown" as const, latencyMs: null, detail: "Ping skipped (ping=skip)" },
          ] satisfies ServicePingResult[])
        : pingActiveServices(),

      // Chart series
      rangeQuery(
        "sum(rate(auth_login_failures_total[1m])) * 60 or vector(0)",
        thirtyMinsAgo,
        now,
        60,
      ),
      rangeQuery(
        'sum(rate(http_requests_total{status=~"5.."}[1m])) / sum(rate(http_requests_total[1m])) * 100 or vector(0)',
        thirtyMinsAgo,
        now,
        60,
      ),
      rangeQuery(
        "sum(rate(http_requests_total[1m])) * 60 or vector(0)",
        thirtyMinsAgo,
        now,
        60,
      ),
    ]);

    // ── Shape raw results ─────────────────────────────────────────────────────

    const dbUpValue = gaugeOf(dbUpRaw);
    const acquired = scalarOf(dbAcquired);
    const max = Math.max(scalarOf(dbMax), 1);
    const idle = scalarOf(dbPoolIdleRaw);
    const dbPoolUtilPct = round2((acquired / max) * 100);
    const dbPoolIdlePct = round2((idle / max) * 100);

    const redisTotal = Math.max(scalarOf(redisPoolTotalRaw), 1);
    const redisIdleRaw = scalarOf(redisPoolIdleRaw);
    const redisPoolIdlePct = round2((redisIdleRaw / redisTotal) * 100);

    const processMemAllocMB = round2(scalarOf(processMemRaw) / 1024 / 1024);
    const infraPollerAgeSeconds = round2(scalarOf(infraPollerAgeRaw));

    const loginFailPerMin = round2(scalarOf(loginFailRate));
    const totalLoginPerMin = Math.max(scalarOf(loginTotalRate), 0.01);
    const loginFailureRatePct = round2((loginFailPerMin / totalLoginPerMin) * 100);
    const errorRatePct = round2(scalarOf(errRateRaw));

    const dbErr = scalarOf(dbErrHour);
    const mailerErr = scalarOf(mailerErrHour);
    const redisStaleCount = Math.round(scalarOf(redisStale));
    const redisUpValue = gaugeOf(redisUpRaw);
    const redisErrRecent = scalarOf(redisErr5m);
    const redisErrHourTotal = scalarOf(redisErrHour);

    const deadJobsTotal = Math.round(scalarOf(deadJobs));

    // Bitcoin — all binary-state gauges use gaugeOf (null = disabled)
    const zmqConnectedValue = gaugeOf(zmqConnectedRaw);
    const zmqLastMessageAgeSec = gaugeOf(zmqLastMsgAgeRaw);
    const rpcConnectedValue = gaugeOf(rpcConnectedRaw);
    const keypoolSizeValue = gaugeOf(keypoolSizeRaw);
    const rpcCallErrorsLastHour = Math.round(scalarOf(rpcCallErrorsRaw));
    const balanceDriftValue = gaugeOf(balanceDriftRaw);
    const reconHoldValue = gaugeOf(reconHoldRaw);
    const reconciliationHoldActive = reconHoldValue === 1;

    // Operational gauges — null means Bitcoin disabled
    const rateFeedStalenessSec = gaugeOf(rateFeedStaleRaw);
    const reconciliationLagBlocks = gaugeOf(reconLagRaw);
    const walletBackupAgeSec = gaugeOf(walletBackupAgeRaw);
    const utxoCount = gaugeOf(utxoCountRaw);
    const feeEstimate1Block = gaugeOf(feeEst1Raw);
    const feeEstimate6Block = gaugeOf(feeEst6Raw);

    // Invoice state — treat null as 0 (gauge not yet published means no invoices)
    const invoiceStatePending = gaugeOf(invoicePendingRaw) ?? 0;
    const invoiceStateConfirmed = gaugeOf(invoiceConfirmedRaw) ?? 0;
    const invoiceStateExpired = gaugeOf(invoiceExpiredRaw) ?? 0;

    // Histogram quantiles — null if no observations in window
    const invoiceDetectionP50Sec = gaugeOf(invoiceDetP50Raw);
    const invoiceDetectionP95Sec = gaugeOf(invoiceDetP95Raw);
    const jobDurationP95Sec = gaugeOf(jobDurationP95Raw);

    // ── Build ping results ────────────────────────────────────────────────────
    // activePings already contains real probe results for:
    //   Backend API, Database, Redis, Bitcoin ZMQ (if enabled), Bitcoin RPC (if enabled)
    // buildDerivedPings adds the Infra Poller — the only service whose health
    // is derived from a Prometheus gauge rather than a live network probe.
    const pingResults: ServicePingResult[] = [
      ...activePings,
      ...buildDerivedPings({ infraPollerAgeSeconds }),
    ];

    // ── Assemble partial snapshot ─────────────────────────────────────────────
    const partial: PartialSnap = {
      requestsPerMin: Math.round(scalarOf(reqRate)),
      errorRatePct,
      loginFailuresPerMin: loginFailPerMin,
      loginFailureRatePct,
      accountLocksLastHour: Math.round(scalarOf(accountLocks)),
      tokenValidationFailuresPerMin: round2(scalarOf(tokenValidFail)),
      oauthUnlinksLastHour: Math.round(scalarOf(oauthUnlinks)),
      passwordResetDeniedLastHour: Math.round(scalarOf(pwResetDenied)),
      registrationsLastHour: Math.round(scalarOf(registrations)),
      tokenRefreshesPerMin: round2(scalarOf(tokenRefreshes)),
      sessionRevocationsLastHour: Math.round(scalarOf(sessionRevocations)),
      dbPoolUtilPct,
      dbPoolIdlePct,
      dbUp: dbUpValue,
      redisUp: redisUpValue,
      redisStaleConnections: redisStaleCount,
      redisErrLastHour: redisErrHourTotal,
      redisPoolIdlePct,
      processMemAllocMB,
      infraPollerAgeSeconds,
      goroutines: Math.round(scalarOf(goroutines)),
      deadJobsTotal,
      jobsSubmittedLastHour: Math.round(scalarOf(jobsSubmittedRaw)),
      jobsFailedLastHour: Math.round(scalarOf(jobsFailedRaw)),
      jobsRequeuedLastHour: Math.round(scalarOf(jobsRequeuedRaw)),
      jobDurationP95Sec,
      zmqConnected: zmqConnectedValue,
      zmqDroppedLastHour: round2(scalarOf(zmqDroppedRaw)),
      zmqLastMessageAgeSec,
      zmqHandlerPanicsLastHour: Math.round(scalarOf(handlerPanicsRaw)),
      zmqHandlerTimeoutsLastHour: Math.round(scalarOf(handlerTimeoutsRaw)),
      zmqHandlerGoroutines: Math.round(scalarOf(handlerGoroutinesRaw)),
      rpcConnected: rpcConnectedValue,
      keypoolSize: keypoolSizeValue,
      rpcCallErrorsLastHour,
      balanceDriftSatoshis: balanceDriftValue,
      reconciliationHoldActive,
      reorgDetectedLastDay: Math.round(scalarOf(reorgDetectedRaw)),
      payoutFailuresLastHour: Math.round(scalarOf(payoutFailuresRaw)),
      sweepStuckLastHour: Math.round(scalarOf(sweepStuckRaw)),
      sseConnectionsActive: Math.round(scalarOf(sseConnectionsRaw)),
      rateFeedStalenessSec,
      reconciliationLagBlocks,
      walletBackupAgeSec,
      utxoCount,
      feeEstimate1Block,
      feeEstimate6Block,
      invoiceDetectionP50Sec,
      invoiceDetectionP95Sec,
      invoiceStatePending,
      invoiceStateConfirmed,
      invoiceStateExpired,
      pingResults,
      loginFailureSeries: fillSeries(seriesOf(loginFailSeries), now, 30 * 60, 60),
      errorRateSeries: fillSeries(seriesOf(errRateSeries), now, 30 * 60, 60),
      requestRateSeries: fillSeries(seriesOf(reqRateSeries), now, 30 * 60, 60),
    };

    const backendPingStatus =
      pingResults.find((p) => p.name === "Backend API")?.status ?? "unknown";

    const services = deriveServices({
      dbUp: dbUpValue,
      dbErr,
      redisUp: redisUpValue,
      redisErrRecent,
      redisStale: redisStaleCount,
      mailerErr,
      dbPoolUtil: dbPoolUtilPct,
      deadJobs: deadJobsTotal,
      errorRatePct,
      zmqConnected: zmqConnectedValue,
      zmqLastMessageAgeSec,
      rpcConnected: rpcConnectedValue,
      keypoolSize: keypoolSizeValue,
      reconciliationLagBlocks,
      balanceDriftSatoshis: balanceDriftValue,
      reconciliationHoldActive,
      payoutFailuresLastHour: partial.payoutFailuresLastHour,
      sweepStuckLastHour: partial.sweepStuckLastHour,
      backendPingStatus,
    });

    const hasDown = services.some((s) => s.status === "down");
    const hasDeg = services.some((s) => s.status === "degraded");
    const overall: OverallStatus = hasDown
      ? "critical"
      : hasDeg
        ? "degraded"
        : "healthy";

    const errorsByComponent: ErrorBreakdown[] = errorsByComp.data.result
      .slice(0, 8)
      .map((r) => ({
        name: r.metric.component ?? "unknown",
        value: Math.round(parseFloat(r.value[1])),
      }))
      .filter((e) => e.value > 0);

    return {
      ...partial,
      overall,
      services,
      errorsByComponent,
      anomalies: computeAnomalies(partial),
      fetchedAt: Date.now(),
      prometheusReachable: true,
    };
  } catch (err) {
    console.error(`[telemetry] Prometheus unreachable at ${PROM}:`, err);
    return unreachableSnapshot();
  }
}

/**
 * Owner-facing health summary (existing — plain language, no technical terms).
 */
export async function fetchHealthSummary(): Promise<{
  overall: "healthy" | "degraded" | "down";
  summary: string;
  services: Array<{
    name: string;
    status: "healthy" | "degraded" | "down";
    detail: string;
  }>;
  requestsToday: number;
  errorsLastHour: number;
}> {
  const now = Math.floor(Date.now() / 1000);
  const midnightToday = new Date();
  midnightToday.setHours(0, 0, 0, 0);
  const todayStart = Math.floor(midnightToday.getTime() / 1000);

  const [mailerErrors, dbErrors, redisErrors, reqToday, errLastHour] =
    await Promise.all([
      instant(`sum(increase(app_errors_total{component="mailer"}[1h]))`),
      instant(
        `sum(increase(app_errors_total{cause=~"db_error|db_pool|db_timeout"}[1h]))`,
      ),
      instant(`sum(increase(app_errors_total{component="kvstore"}[1h]))`),
      instant(`sum(increase(http_requests_total[${now - todayStart}s]))`),
      instant(`sum(increase(http_requests_total{status=~"5.."}[1h]))`),
    ]);

  const mailerErr = scalarOf(mailerErrors);
  const dbErr = scalarOf(dbErrors);
  const redisErr = scalarOf(redisErrors);

  const services = [
    {
      name: "Store & checkout",
      status: (dbErr > 10 ? "down" : dbErr > 0 ? "degraded" : "healthy") as
        | "healthy"
        | "degraded"
        | "down",
      detail:
        dbErr > 0
          ? `${Math.round(dbErr)} database errors in the last hour`
          : "Responding normally",
    },
    {
      name: "User accounts & login",
      status: "healthy" as const,
      detail: "Login and registration working normally",
    },
    {
      name: "Email notifications",
      status: (mailerErr > 5
        ? "down"
        : mailerErr > 0
          ? "degraded"
          : "healthy") as "healthy" | "degraded" | "down",
      detail:
        mailerErr > 0
          ? `SMTP delays — ${Math.round(mailerErr)} failed attempts`
          : "Delivering normally",
    },
    {
      name: "Database",
      status: (dbErr > 10 ? "down" : dbErr > 0 ? "degraded" : "healthy") as
        | "healthy"
        | "degraded"
        | "down",
      detail:
        dbErr > 0
          ? `${Math.round(dbErr)} errors detected`
          : "Responding normally",
    },
    {
      name: "Cache & sessions",
      status: (redisErr > 5
        ? "down"
        : redisErr > 0
          ? "degraded"
          : "healthy") as "healthy" | "degraded" | "down",
      detail:
        redisErr > 0
          ? "Redis connectivity issues detected"
          : "Responding normally",
    },
  ];

  const hasDown = services.some((s) => s.status === "down");
  const hasDegraded = services.some((s) => s.status === "degraded");
  const overall = hasDown ? "down" : hasDegraded ? "degraded" : "healthy";

  const summaryMap = {
    healthy: "Everything is running smoothly.",
    degraded:
      mailerErr > 0
        ? "Email delivery is experiencing delays. Orders and logins are working normally."
        : "One or more services are degraded.",
    down: "A critical service is down. Your developer has been notified automatically.",
  };

  return {
    overall,
    summary: summaryMap[overall],
    services,
    requestsToday: Math.round(scalarOf(reqToday)),
    errorsLastHour: Math.round(scalarOf(errLastHour)),
  };
}
