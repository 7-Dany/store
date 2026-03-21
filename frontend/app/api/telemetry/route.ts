/**
 * GET /api/telemetry
 *
 * Session-gated proxy. Prometheus is server-only — this route is the only
 * path to telemetry data from the browser.
 *
 * Auth gate: session cookie must be present.
 * Cache: no-store — telemetry must always be fresh.
 *
 * Dev: append ?mock=<scenario> to bypass Prometheus and get a canned snapshot.
 * See lib/api/telemetry/mock-snapshots.ts for available scenarios.
 *
 * ?ping=skip  — skips active health pings (useful in CI/testing where
 *               BACKEND_URL is not reachable). Prometheus data is still fetched.
 *
 * Response header X-Data-Source: "prometheus" | "mock"
 * The frontend reads this to show a "MOCK" badge in the HealthBanner.
 */
import { NextResponse } from "next/server";
import { cookies } from "next/headers";
import { fetchSecuritySnapshot } from "@/lib/api/telemetry/prometheus";
import { getMockSnapshot } from "@/lib/api/telemetry/mock-snapshots";

const SHARED_HEADERS = {
  "Cache-Control": "no-store",
  "X-Content-Type-Options": "nosniff",
} as const;

export async function GET(req: Request): Promise<NextResponse> {
  const cookieStore = await cookies();
  const sessionToken = cookieStore.get("session")?.value;

  if (!sessionToken) {
    return NextResponse.json(
      { code: "unauthenticated", message: "No active session." },
      { status: 401 },
    );
  }

  const url = new URL(req.url);

  // Dev-only mock bypass — ?mock=<scenario> returns a pre-built snapshot.
  const scenario = url.searchParams.get("mock");
  if (scenario) {
    const mock = getMockSnapshot(scenario);
    if (mock) {
      return NextResponse.json(mock, {
        status: 200,
        headers: { ...SHARED_HEADERS, "X-Data-Source": "mock" },
      });
    }
    // Unknown scenario name — fall through to real Prometheus data.
  }

  // ?ping=skip bypasses active HTTP health probes. Useful in CI/testing where
  // BACKEND_URL is not reachable. Prometheus queries still run normally.
  const skipPings = url.searchParams.get("ping") === "skip";

  const snapshot = await fetchSecuritySnapshot({ skipPings });

  return NextResponse.json(snapshot, {
    status: 200,
    headers: { ...SHARED_HEADERS, "X-Data-Source": "prometheus" },
  });
}
