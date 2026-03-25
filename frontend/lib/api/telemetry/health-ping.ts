/**
 * Active health pings — real service probes that run in parallel with
 * the Prometheus queries on every /api/telemetry request.
 *
 * GET /api/v1/health?ping=true returns a structured body:
 *
 *   {
 *     "status":   "ok" | "degraded",
 *     "pong":     true,
 *     "services": [
 *       { "name": "Database",    "status": "up"|"down", "latency_ms": 4,  "detail": "…" },
 *       { "name": "Redis",       "status": "up"|"down", "latency_ms": 1,  "detail": "…" },
 *       { "name": "Bitcoin ZMQ", "status": "up"|"down",                   "detail": "…" },
 *       { "name": "Bitcoin RPC", "status": "up"|"down", "latency_ms": 12, "detail": "…" }
 *     ]
 *   }
 *
 * The backend runs all probes concurrently under a 2 s deadline.
 * Bitcoin services are only included when BitcoinEnabled=true.
 *
 * Infra Poller health is derived from the infra_poller_last_run_timestamp_seconds
 * Prometheus gauge in buildDerivedPings() — it cannot be probed via HTTP.
 *
 * Server-only. Never imported by Client Components.
 */

const BACKEND_URL = process.env.BACKEND_URL ?? "http://localhost:8080";
const PING_TIMEOUT_MS = 3_000;

// ─── Types ────────────────────────────────────────────────────────────────────

export interface ServicePingResult {
  name: string;
  status: "up" | "down" | "unknown";
  latencyMs: number | null;
  detail: string;
}

// Shape of one entry in the backend's "services" array.
interface BackendServiceProbe {
  name: string;
  status: "up" | "down";
  latency_ms?: number;
  detail?: string;
}

// Shape of the full backend health response body.
interface BackendHealthBody {
  status: "ok" | "degraded";
  pong: true;
  services: BackendServiceProbe[];
}

// ─── Active HTTP probe ────────────────────────────────────────────────────────

/**
 * Pings the Backend API at GET /api/v1/health?ping=true.
 *
 * On success the backend returns { pong: true, services: [...] } with real
 * probe results for Database, Redis, Bitcoin ZMQ, and Bitcoin RPC.
 * Those results are mapped into ServicePingResults and returned alongside a
 * synthetic "Backend API" entry representing the HTTP round-trip itself.
 *
 * On any failure (timeout, non-200, bad JSON, missing pong) every service is
 * returned as "down" with an explanatory detail string.
 *
 * Never throws — all failures are returned as { status: "down" }.
 */
export async function pingActiveServices(): Promise<ServicePingResult[]> {
  const url = `${BACKEND_URL}/api/v1/health?ping=true`;
  const start = Date.now();

  let res: Response;
  try {
    res = await fetch(url, {
      method: "GET",
      signal: AbortSignal.timeout(PING_TIMEOUT_MS),
      cache: "no-store",
    });
  } catch (err) {
    const latencyMs = Date.now() - start;
    const detail =
      err instanceof Error
        ? err.name === "TimeoutError"
          ? `Timed out after ${PING_TIMEOUT_MS}ms`
          : err.message
        : String(err);
    // Backend unreachable — all services unknown.
    return [
      { name: "Backend API", status: "down", latencyMs, detail },
      { name: "Database",    status: "unknown", latencyMs: null, detail: "Backend unreachable" },
      { name: "Redis",       status: "unknown", latencyMs: null, detail: "Backend unreachable" },
    ];
  }

  const latencyMs = Date.now() - start;

  if (!res.ok) {
    const detail = `HTTP ${res.status} — unexpected response`;
    return [
      { name: "Backend API", status: "down", latencyMs, detail },
      { name: "Database",    status: "unknown", latencyMs: null, detail: "Backend unreachable" },
      { name: "Redis",       status: "unknown", latencyMs: null, detail: "Backend unreachable" },
    ];
  }

  // Parse and validate the pong body.
  let body: BackendHealthBody;
  try {
    const raw = await res.json();
    if (
      typeof raw !== "object" ||
      raw === null ||
      raw.pong !== true ||
      !Array.isArray(raw.services)
    ) {
      throw new Error("pong missing or services not an array");
    }
    body = raw as BackendHealthBody;
  } catch (err) {
    const detail = `HTTP ${res.status} — invalid response: ${err instanceof Error ? err.message : String(err)}`;
    return [
      { name: "Backend API", status: "down", latencyMs, detail },
      { name: "Database",    status: "unknown", latencyMs: null, detail: "Bad health response" },
      { name: "Redis",       status: "unknown", latencyMs: null, detail: "Bad health response" },
    ];
  }

  // Backend API is "up" even when some sub-services are "down" — it responded
  // correctly and the HTTP probe succeeded.
  const backendEntry: ServicePingResult = {
    name: "Backend API",
    status: "up",
    latencyMs,
    detail: `HTTP ${res.status} — pong ✓`,
  };

  // Map each backend service probe into a ServicePingResult.
  const serviceEntries: ServicePingResult[] = body.services.map(
    (svc): ServicePingResult => ({
      name: svc.name,
      status: svc.status === "up" ? "up" : "down",
      latencyMs: typeof svc.latency_ms === "number" ? svc.latency_ms : null,
      detail: svc.detail ?? "",
    }),
  );

  return [backendEntry, ...serviceEntries];
}

// ─── Derived ping from Prometheus (Infra Poller only) ────────────────────────

/**
 * Builds a ServicePingResult for the Infra Poller — the only service whose
 * health cannot be probed via a direct network call. Its liveness is reflected
 * by the infra_poller_last_run_timestamp_seconds Prometheus gauge, which is
 * already queried in the main Promise.all of fetchSecuritySnapshot().
 *
 * Called AFTER the Promise.all resolves so there is no extra waterfall.
 * Database, Redis, ZMQ, and RPC are now real probes from the backend — they
 * no longer need Prometheus-derived entries here.
 */
export function buildDerivedPings(p: {
  infraPollerAgeSeconds: number;
}): ServicePingResult[] {
  const pollerStale = p.infraPollerAgeSeconds > 60;
  return [
    {
      name: "Infra Poller",
      status: pollerStale ? "down" : "up",
      latencyMs: null,
      detail: pollerStale
        ? `Poller stuck — last heartbeat ${Math.round(p.infraPollerAgeSeconds)}s ago`
        : `Last heartbeat ${Math.round(p.infraPollerAgeSeconds)}s ago`,
    },
  ];
}
