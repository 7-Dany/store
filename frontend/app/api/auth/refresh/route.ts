/**
 * POST /api/auth/refresh
 * Rotates the refresh token and issues a new access token + refresh token pair.
 * Reads the httpOnly refresh_token cookie, calls the Go backend, and sets the
 * new session and refresh_token cookies on the frontend domain.
 *
 * GET /api/auth/refresh?from=<path>
 * Server-side recovery used by DashboardLayout when the session cookie has
 * expired but a refresh_token cookie still exists. Attempts the rotation and
 * redirects to `from` (default /dashboard) on success, or /login on failure.
 * The `from` param is validated to be a same-origin relative path.
 */
import { NextResponse } from "next/server";
import { cookies } from "next/headers";

const API_BASE = process.env.API_BASE_URL ?? "http://localhost:8080/api/v1";

interface RefreshResponse {
  access_token: string;
  refresh_token: string;
  refresh_expiry: string;
  expires_in: number;
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

  let res: Response;
  try {
    res = await fetch(`${API_BASE}/auth/refresh`, {
      method: "POST",
      headers: { Cookie: `refresh_token=${refreshToken}` },
    });
  } catch {
    return NextResponse.json(
      { code: "upstream_unavailable", message: "Service temporarily unavailable." },
      { status: 502 },
    );
  }

  const text = await res.text();
  const body = text
    ? (() => { try { return JSON.parse(text); } catch { return { message: text }; } })()
    : null;

  if (!res.ok) {
    const out = NextResponse.json(body, { status: res.status });
    // On reuse-detection / account issues — clear both cookies so the client
    // knows there is no valid session to fall back to.
    if (res.status === 401 || res.status === 403 || res.status === 423) {
      out.cookies.set("session", "", { path: "/", maxAge: 0 });
      out.cookies.set("refresh_token", "", { path: "/", maxAge: 0 });
    }
    return out;
  }

  const data = body as RefreshResponse;
  const out = NextResponse.json({ ok: true }, { status: 200 });

  out.cookies.set("session", data.access_token, {
    httpOnly: true,
    path: "/",
    maxAge: data.expires_in ?? 900,
    sameSite: "lax",
  });
  out.cookies.set("refresh_token", data.refresh_token, {
    httpOnly: true,
    path: "/",
    maxAge: 60 * 60 * 24 * 30,
    sameSite: "lax",
  });

  return out;
}

// ─── GET handler: server-side session recovery ─────────────────────────────────
//
// Called by DashboardLayout when the session cookie has expired but the
// refresh_token cookie still has a long TTL. We attempt the rotation and
// redirect back to `from` so the user lands on the page they were going to
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

  let res: Response;
  try {
    res = await fetch(`${API_BASE}/auth/refresh`, {
      method: "POST",
      headers: { Cookie: `refresh_token=${refreshToken}` },
    });
  } catch {
    // Backend unreachable — send to login; don’t clear cookies (might recover).
    return NextResponse.redirect(new URL("/login", request.url));
  }

  const text = await res.text();
  const body = text
    ? (() => { try { return JSON.parse(text); } catch { return null; } })()
    : null;

  if (!res.ok) {
    const dest = NextResponse.redirect(new URL("/login", request.url));
    // Hard failures: reuse detected, locked, inactive — clear cookies.
    if (res.status === 401 || res.status === 403 || res.status === 423) {
      dest.cookies.set("session", "", { path: "/", maxAge: 0 });
      dest.cookies.set("refresh_token", "", { path: "/", maxAge: 0 });
    }
    return dest;
  }

  const data = body as RefreshResponse;
  const dest = NextResponse.redirect(new URL(from, request.url));

  dest.cookies.set("session", data.access_token, {
    httpOnly: true,
    path: "/",
    maxAge: data.expires_in ?? 900,
    sameSite: "lax",
  });
  dest.cookies.set("refresh_token", data.refresh_token, {
    httpOnly: true,
    path: "/",
    maxAge: 60 * 60 * 24 * 30,
    sameSite: "lax",
  });

  return dest;
}
