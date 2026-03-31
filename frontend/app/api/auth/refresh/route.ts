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
import { extractCookieValue, extractRequestCookie, readSetCookieHeaders } from "@/lib/api/http/cookies";
import { sanitizeRelativePath } from "@/lib/auth/redirect";

interface RefreshResponse {
  access_token: string;
  // refresh_token is no longer in the JSON body (F-02 fix).
  // The Go backend delivers it exclusively via Set-Cookie.
  refresh_expiry: string;
  expires_in: number;
}

interface ErrorResponse {
  code?: string;
  message?: string;
}

const API_BASE = process.env.API_BASE_URL ?? "http://localhost:8080/api/v1";

function computeMaxAge(refreshExpiry: string): number {
  const expiryMs = new Date(refreshExpiry).getTime() - Date.now();
  const secs = Math.floor(expiryMs / 1000);
  // Fall back to 30 days if the expiry timestamp is unparseable.
  return secs > 0 ? secs : 60 * 60 * 24 * 30;
}

function readRefreshToken(request: Request): string | null {
  return extractRequestCookie(request.headers.get("cookie"), "refresh_token");
}

function setSessionCookie(response: NextResponse, accessToken: string, maxAge = 900) {
  response.cookies.set("session", accessToken, {
    httpOnly: true,
    path: "/",
    maxAge,
    sameSite: "lax",
  });
}

function clearSessionCookie(response: NextResponse) {
  response.cookies.set("session", "", {
    path: "/",
    maxAge: 0,
    httpOnly: true,
    sameSite: "lax",
  });
}

function setRefreshCookie(
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

function clearRefreshCookies(response: NextResponse) {
  response.cookies.set("refresh_token", "", {
    path: "/",
    maxAge: 0,
    httpOnly: true,
    sameSite: "lax",
  });
  response.cookies.set("refresh_token", "", {
    path: "/api/v1/auth",
    maxAge: 0,
    httpOnly: true,
    sameSite: "strict",
  });
}

function redirectToLogin(request: Request, clearCookies = false) {
  const response = NextResponse.redirect(new URL("/login", request.url));
  if (clearCookies) {
    clearSessionCookie(response);
    clearRefreshCookies(response);
  }
  return response;
}

async function requestRefresh(refreshToken: string) {
  if (!refreshToken) {
    console.error("[Refresh] No refresh token to send to backend");
    throw new Error("Missing refresh token");
  }

  if (process.env.NODE_ENV === "development") {
    console.log("[Refresh] Calling backend at:", `${API_BASE}/auth/refresh`);
    console.log("[Refresh] Token length:", refreshToken.length);
  }

  const upstream = await fetch(`${API_BASE}/auth/refresh`, {
    method: "POST",
    headers: { Cookie: `refresh_token=${refreshToken}` },
    cache: "no-store",
  });

  const payload = (await upstream.json()) as RefreshResponse | ErrorResponse;
  
  if (process.env.NODE_ENV === "development") {
    console.log("[Refresh] Backend response status:", upstream.status);
    if (!upstream.ok) {
      console.log("[Refresh] Backend error:", payload);
    }
  }

  const nextRefreshToken = extractCookieValue(
    readSetCookieHeaders(upstream.headers),
    "refresh_token",
  );

  if (process.env.NODE_ENV === "development") {
    console.log("[Refresh] Got new refresh token:", !!nextRefreshToken);
  }

  return { upstream, payload, nextRefreshToken };
}

export async function POST(request: Request) {
  const refreshToken = readRefreshToken(request);

  if (!refreshToken) {
    // Debug: log cookie info when missing
    const cookieHeader = request.headers.get("cookie");
    const hasSessionCookie = cookieHeader?.includes("session=");
    const debugInfo = {
      hasCookieHeader: !!cookieHeader,
      hasSessionCookie,
      cookieHeaderLength: cookieHeader?.length ?? 0,
      fullCookieHeader: process.env.NODE_ENV === "development" ? cookieHeader : "***",
    };
    
    if (process.env.NODE_ENV === "development") {
      console.error("[Refresh] No refresh token found in request cookies", debugInfo);
    }
    
    return NextResponse.json(
      { 
        code: "missing_token", 
        message: "No refresh token.",
        ...(process.env.NODE_ENV === "development" && { debug: debugInfo }),
      },
      { status: 401 },
    );
  }

  if (process.env.NODE_ENV === "development") {
    console.log("[Refresh] Refresh token read from browser request, length:", refreshToken.length);
  }

  try {
    const { upstream, payload, nextRefreshToken } = await requestRefresh(refreshToken);
    if (!upstream.ok) {
      const out = NextResponse.json(payload, { status: upstream.status });
      if ([401, 403, 423].includes(upstream.status)) {
        clearSessionCookie(out);
        clearRefreshCookies(out);
      }
      return out;
    }

    const data = payload as RefreshResponse;
    const out = NextResponse.json({ ok: true }, { status: 200 });
    setSessionCookie(out, data.access_token, data.expires_in ?? 900);
    if (nextRefreshToken) {
      setRefreshCookie(out, nextRefreshToken, data.refresh_expiry);
      return out;
    }

    const failure = NextResponse.json(
      { code: "refresh_cookie_missing", message: "Upstream refresh did not provide a rotated refresh cookie." },
      { status: 502 },
    );
    clearSessionCookie(failure);
    clearRefreshCookies(failure);
    return failure;
  } catch (error) {
    if (process.env.NODE_ENV === "development") {
      console.error("[Refresh] Unexpected error:", error instanceof Error ? error.message : String(error));
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
  const from = sanitizeRelativePath(url.searchParams.get("from"));

  const refreshToken = readRefreshToken(request);

  if (!refreshToken) {
    return redirectToLogin(request, true);
  }

  try {
    const { upstream, payload, nextRefreshToken } = await requestRefresh(refreshToken);

    if (!upstream.ok) {
      return redirectToLogin(request, true);
    }

    const data = payload as RefreshResponse;

    const dest = NextResponse.redirect(new URL(from, request.url));
    setSessionCookie(dest, data.access_token, data.expires_in ?? 900);
    if (nextRefreshToken) {
      setRefreshCookie(dest, nextRefreshToken, data.refresh_expiry);
      return dest;
    }

    return redirectToLogin(request, true);
  } catch {
    return redirectToLogin(request, true);
  }
}
