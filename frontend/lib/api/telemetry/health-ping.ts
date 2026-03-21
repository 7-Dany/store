/**
 * Active health pings — lightweight service probes that run in parallel with
 * the Prometheus queries on every /api/telemetry request.
 *
 * Only two services receive real HTTP probes (Backend API and Bitcoin RPC).
 * The remaining four services are derived from Prometheus gauges already
 * queried in fetchSecuritySnapshot() — no separate network round-trip needed.
 *
 * Server-only. Never imported by Client Components.
 */

const BACKEND_URL = process.env.BACKEND_URL ?? "http://localhost:8080";
const PING_TIMEOUT_MS = 2_000;

// ─── Types ────────────────────────────────────────────────────────────────────

export interface ServicePingResult {
  name: string;
  status: "up" | "down" | "unknown";
  latencyMs: number | null;
  detail: string;
}

// ─── Active HTTP probes ───────────────────────────────────────────────────────

async function pingUrl(name: string, url: string): Promise<ServicePingResult> {
  const start = Date.now();
  try {
    const res = await fetch(url, {
      method: "GET",
      signal: AbortSignal.timeout(PING_TIMEOUT_MS),
      cache: "no-store",
    });
    const latencyMs = Date.now() - start;
    if (res.ok) {
      return { name, status: "up", latencyMs, detail: `HTTP ${res.status}` };
    }
    return {
      name,
      status: "down",
      latencyMs,
      detail: `HTTP ${res.status} — unexpected response`,
    };
  } catch (err) {
    const latencyMs = Date.now() - start;
    const message =
      err instanceof Error
        ? err.name === "TimeoutError"
          ? `Timed out after ${PING_TIMEOUT_MS}ms`
          : err.message
        : String(err);
    return { name, status: "down", latencyMs, detail: message };
  }
}

/**
 * Pings Backend API and Bitcoin RPC in parallel.
 * Never throws — any failure is returned as { status: "down" }.
 *
 * Called inside the main Promise.all in fetchSecuritySnapshot() so it runs
 * concurrently with all Prometheus queries (zero additional waterfall).
 */
export async function pingActiveServices(): Promise<
  [backendPing: ServicePingResult, rpcPing: ServicePingResult]
> {
  const [backend, rpc] = await Promise.all([
    pingUrl("Backend API", `${BACKEND_URL}/health`),
    pingUrl("Bitcoin RPC", `${BACKEND_URL}/internal/bitcoin/rpc-health`),
  ]);
  return [backend, rpc];
}

// ─── Derived pings from Prometheus values ─────────────────────────────────────

const ZMQ_IDLE_TIMEOUT_SEC = 120; // 2 min — degrade if no message for 2× this

/**
 * Builds ServicePingResults for services whose health is reflected in
 * Prometheus gauges already queried in the main Promise.all.
 *
 * Called AFTER the Promise.all resolves so there is no extra waterfall.
 */
export function buildDerivedPings(p: {
  dbUp: number | null;
  redisUp: number | null;
  zmqConnected: number | null;
  zmqLastMessageAgeSec: number | null;
  infraPollerAgeSeconds: number;
}): ServicePingResult[] {
  const derived: ServicePingResult[] = [];

  // Database ─ from db_up gauge (flips within one InfraPoller cycle, 15 s)
  if (p.dbUp === null) {
    derived.push({
      name: "Database",
      status: "unknown",
      latencyMs: null,
      detail: "Metric not yet published",
    });
  } else {
    derived.push({
      name: "Database",
      status: p.dbUp === 1 ? "up" : "down",
      latencyMs: null,
      detail:
        p.dbUp === 1 ? "DB ping succeeded" : "DB ping failed (db_up = 0)",
    });
  }

  // Redis ─ from redis_up gauge
  if (p.redisUp === null) {
    derived.push({
      name: "Redis",
      status: "unknown",
      latencyMs: null,
      detail: "Metric not yet published",
    });
  } else {
    derived.push({
      name: "Redis",
      status: p.redisUp === 1 ? "up" : "down",
      latencyMs: null,
      detail:
        p.redisUp === 1
          ? "Redis ping succeeded"
          : "Redis ping failed (redis_up = 0)",
    });
  }

  // Bitcoin ZMQ ─ only when Bitcoin is deployed
  if (p.zmqConnected !== null) {
    const disconnected = p.zmqConnected === 0;
    const messageStale =
      !disconnected &&
      p.zmqLastMessageAgeSec !== null &&
      p.zmqLastMessageAgeSec > ZMQ_IDLE_TIMEOUT_SEC * 2;

    const status: ServicePingResult["status"] =
      disconnected || messageStale ? "down" : "up";

    const detail = disconnected
      ? "ZMQ subscriber disconnected from Bitcoin Core"
      : messageStale
        ? `No ZMQ message for ${Math.round(p.zmqLastMessageAgeSec!)}s (idle timeout × 2)`
        : p.zmqLastMessageAgeSec !== null
          ? `Last message ${Math.round(p.zmqLastMessageAgeSec)}s ago`
          : "Connected to Bitcoin Core";

    derived.push({ name: "Bitcoin ZMQ", status, latencyMs: null, detail });
  }

  // Infra Poller ─ from infra_poller_last_run_timestamp_seconds age
  const pollerStale = p.infraPollerAgeSeconds > 60;
  derived.push({
    name: "Infra Poller",
    status: pollerStale ? "down" : "up",
    latencyMs: null,
    detail: pollerStale
      ? `Poller stuck — last heartbeat ${Math.round(p.infraPollerAgeSeconds)}s ago`
      : `Last heartbeat ${Math.round(p.infraPollerAgeSeconds)}s ago`,
  });

  return derived;
}
