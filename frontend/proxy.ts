import { NextResponse } from "next/server";
import type { NextRequest } from "next/server";
import { extractCookieValue, readSetCookieHeaders } from "@/lib/api/http/cookies";
import { sanitizeRelativePath } from "@/lib/auth/redirect";

/**
 * Proxy.
 *
 * Fast, edge-side auth guard — runs before any route is rendered.
 * Only checks cookie presence; actual token validation happens in the layout
 * via a real API call (and the refresh interceptor handles expiry).
 *
 * Protected:  /dashboard/*
 * Public:     /login, /register, /verify-email, /forgot-password, /unlock, /
 *
 * If a protected route is hit without a session cookie → redirect to /login.
 * If a public route is hit WITH a session cookie → redirect to /dashboard
 * (prevents going back to login when already signed in).
 */

const PROTECTED = /^\/dashboard(\/|$)/;
const PUBLIC = /^\/(login|register|verify-email|forgot-password|unlock)(\/|$)/;
const API_BASE = process.env.API_BASE_URL ?? "http://localhost:8080/api/v1";

interface RefreshResponse {
  access_token: string;
  refresh_expiry: string;
  expires_in: number;
}

function computeMaxAge(refreshExpiry: string): number {
  const expiryMs = new Date(refreshExpiry).getTime() - Date.now();
  const secs = Math.floor(expiryMs / 1000);
  return secs > 0 ? secs : 60 * 60 * 24 * 30;
}

function setRefreshCookies(
  response: NextResponse,
  refreshToken: string,
  refreshExpiry: string,
) {
  const maxAge = computeMaxAge(refreshExpiry);

  response.cookies.set("refresh_token", refreshToken, {
    httpOnly: true,
    path: "/",
    maxAge,
    sameSite: "lax",
  });

  // Clear the backend-scoped cookie to avoid duplicates.
  response.cookies.set("refresh_token", "", {
    path: "/api/v1/auth",
    maxAge: 0,
    httpOnly: true,
    sameSite: "strict",
  });
}

function buildCookieHeader(
  request: NextRequest,
  updates: Record<string, string>,
): string {
  const cookies = new Map(
    request.cookies.getAll().map(({ name, value }) => [name, value]),
  );

  for (const [name, value] of Object.entries(updates)) {
    cookies.set(name, value);
  }

  return Array.from(cookies.entries())
    .map(([name, value]) => `${name}=${value}`)
    .join("; ");
}

async function tryRefreshSession(request: NextRequest): Promise<NextResponse | null> {
  const refreshToken = request.cookies.get("refresh_token")?.value;
  if (!refreshToken) return null;

  try {
    const upstream = await fetch(`${API_BASE}/auth/refresh`, {
      method: "POST",
      headers: {
        Cookie: `refresh_token=${refreshToken}`,
      },
      cache: "no-store",
    });

    if (!upstream.ok) {
      return null;
    }

    const data = (await upstream.json()) as RefreshResponse;
    const nextRefreshToken = extractCookieValue(
      readSetCookieHeaders(upstream.headers),
      "refresh_token",
    );
    if (!nextRefreshToken) {
      return null;
    }

    const requestHeaders = new Headers(request.headers);
    requestHeaders.set(
      "cookie",
      buildCookieHeader(request, {
        session: data.access_token,
        refresh_token: nextRefreshToken,
      }),
    );

    const response = NextResponse.next({ request: { headers: requestHeaders } });

    response.cookies.set("session", data.access_token, {
      httpOnly: true,
      path: "/",
      maxAge: data.expires_in ?? 900,
      sameSite: "lax",
    });
    setRefreshCookies(response, nextRefreshToken, data.refresh_expiry);

    return response;
  } catch {
    return null;
  }
}

export async function proxy(request: NextRequest) {
  const { pathname } = request.nextUrl;
  const hasSession = !!request.cookies.get("session")?.value;
  const from = sanitizeRelativePath(request.nextUrl.searchParams.get("from"));

  // Protected route — no cookie → send to login, preserving intended destination.
  if (PROTECTED.test(pathname) && !hasSession) {
    const refreshed = await tryRefreshSession(request);
    if (refreshed) {
      return refreshed;
    }

    const loginUrl = new URL("/login", request.url);
    loginUrl.searchParams.set("from", `${pathname}${request.nextUrl.search}`);
    return NextResponse.redirect(loginUrl);
  }

  // Public auth route — already has a cookie → send straight to dashboard.
  if (PUBLIC.test(pathname) && hasSession) {
    return NextResponse.redirect(new URL(from, request.url));
  }

  return NextResponse.next();
}

export const config = {
  matcher: [
    /*
     * Match all routes except:
     * - _next/static  (static files)
     * - _next/image   (image optimisation)
     * - favicon.ico
     * - /api/*        (API routes handle their own auth)
     */
    "/((?!_next/static|_next/image|favicon\\.ico|api/).*)",
  ],
};
