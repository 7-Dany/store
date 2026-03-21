/**
 * POST /api/auth/refresh
 * Rotates the refresh token and issues a new access token + refresh token pair.
 * Reads the httpOnly refresh_token cookie, calls the Go backend via serverApi
 * (Axios), and sets the new session and refresh_token cookies on the frontend
 * domain.
 *
 * The refresh token is no longer in the Go backend's JSON response body (F-02
 * fix). It is delivered exclusively via Set-Cookie. Both handlers extract it
 * from the Axios response headers["set-cookie"] array.
 *
 * GET /api/auth/refresh?from=<path>
 * Server-side recovery used by DashboardLayout when the session cookie has
 * expired but a refresh_token cookie still exists. Attempts the rotation and
 * redirects to `from` (default /dashboard) on success, or /login on failure.
 * The `from` param is validated to be a same-origin relative path.
 */
import { NextResponse } from "next/server";
import { cookies } from "next/headers";
import { isAxiosError } from "axios";
import { serverApi } from "@/lib/api/http/server";

interface RefreshResponse {
  access_token: string;
  // refresh_token is no longer in the JSON body (F-02 fix).
  // The Go backend delivers it exclusively via Set-Cookie.
  refresh_expiry: string;
  expires_in: number;
}

/**
 * Extracts the refresh_token value from the Set-Cookie header array on an
 * Axios response. In Node.js, Axios exposes Set-Cookie as string[] on
 * response.headers["set-cookie"].
 *
 * The Go backend sets:
 *   refresh_token=<value>; Path=/api/v1/auth; HttpOnly; SameSite=Strict; ...
 */
function extractRefreshToken(
  setCookieHeaders: string | string[] | undefined,
): string | null {
  const headers = Array.isArray(setCookieHeaders)
    ? setCookieHeaders
    : setCookieHeaders
      ? [setCookieHeaders]
      : [];
  for (const header of headers) {
    const match = header.match(/^refresh_token=([^;]+)/);
    if (match) return match[1];
  }
  return null;
}

function computeMaxAge(refreshExpiry: string): number {
  const expiryMs = new Date(refreshExpiry).getTime() - Date.now();
  const secs = Math.floor(expiryMs / 1000);
  // Fall back to 30 days if the expiry timestamp is unparseable.
  return secs > 0 ? secs : 60 * 60 * 24 * 30;
}

export async function POST() {
  const cookieStore = await cookies();
  const refreshToken = cookieStore.get("refresh_token")?.value;

  if (!refreshToken) {
    return NextResponse.json(
      { code: "missing_token", message: "No refresh token." },
      { status: 401 },
    );
  }

  try {
    const { data, headers } = await serverApi.post<RefreshResponse>(
      "/auth/refresh",
      null,
      { headers: { Cookie: `refresh_token=${refreshToken}` } },
    );

    const out = NextResponse.json({ ok: true }, { status: 200 });

    out.cookies.set("session", data.access_token, {
      httpOnly: true,
      path: "/",
      maxAge: data.expires_in ?? 900,
      sameSite: "lax",
    });

    const newRefreshToken = extractRefreshToken(
      headers["set-cookie"] as string | string[] | undefined,
    );
    if (newRefreshToken) {
      out.cookies.set("refresh_token", newRefreshToken, {
        httpOnly: true,
        path: "/",
        maxAge: computeMaxAge(data.refresh_expiry),
        sameSite: "lax",
      });
    }

    return out;
  } catch (e) {
    if (isAxiosError(e) && e.response) {
      const out = NextResponse.json(e.response.data, {
        status: e.response.status,
      });
      // On reuse-detection / account issues — clear both cookies so the client
      // knows there is no valid session to fall back to.
      if ([401, 403, 423].includes(e.response.status)) {
        out.cookies.set("session", "", { path: "/", maxAge: 0 });
        out.cookies.set("refresh_token", "", { path: "/", maxAge: 0 });
      }
      return out;
    }
    return NextResponse.json(
      { code: "upstream_unavailable", message: "Service temporarily unavailable." },
      { status: 502 },
    );
  }
}

// ─── GET handler: server-side session recovery ────────────────────────────────
//
// Called by DashboardLayout when the session cookie has expired but the
// refresh_token cookie still has a long TTL. Attempts the rotation and
// redirects back to `from` so the user lands on the page they were going to
// rather than being dropped at /dashboard or /login unnecessarily.

export async function GET(request: Request) {
  const url = new URL(request.url);

  // Validate `from` — must be a relative same-origin path to prevent open redirect.
  const raw = url.searchParams.get("from") ?? "";
  const from =
    raw.startsWith("/") && !raw.startsWith("//") && !raw.includes(":")
      ? raw
      : "/dashboard";

  const cookieStore = await cookies();
  const refreshToken = cookieStore.get("refresh_token")?.value;

  if (!refreshToken) {
    return NextResponse.redirect(new URL("/login", request.url));
  }

  try {
    const { data, headers } = await serverApi.post<RefreshResponse>(
      "/auth/refresh",
      null,
      { headers: { Cookie: `refresh_token=${refreshToken}` } },
    );

    const dest = NextResponse.redirect(new URL(from, request.url));

    dest.cookies.set("session", data.access_token, {
      httpOnly: true,
      path: "/",
      maxAge: data.expires_in ?? 900,
      sameSite: "lax",
    });

    const newRefreshToken = extractRefreshToken(
      headers["set-cookie"] as string | string[] | undefined,
    );
    if (newRefreshToken) {
      dest.cookies.set("refresh_token", newRefreshToken, {
        httpOnly: true,
        path: "/",
        maxAge: computeMaxAge(data.refresh_expiry),
        sameSite: "lax",
      });
    }

    return dest;
  } catch (e) {
    const dest = NextResponse.redirect(new URL("/login", request.url));
    // Hard failures: reuse detected, locked, inactive — clear cookies.
    if (isAxiosError(e) && e.response && [401, 403, 423].includes(e.response.status)) {
      dest.cookies.set("session", "", { path: "/", maxAge: 0 });
      dest.cookies.set("refresh_token", "", { path: "/", maxAge: 0 });
    }
    // Backend unreachable or other error — redirect to login without clearing
    // cookies (might recover once the backend comes back up).
    return dest;
  }
}
