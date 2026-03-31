import { NextResponse } from "next/server";
import { extractCookieValue, readSetCookieHeaders } from "@/lib/api/http/cookies";

interface LoginResponse {
  access_token: string;
  // refresh_token is no longer in the JSON body (F-02 fix).
  // The Go backend delivers it exclusively via Set-Cookie.
  refresh_expiry: string;
  expires_in: number;
  scheduled_deletion_at?: string;
}

interface ErrorResponse {
  code?: string;
  message?: string;
}

const API_BASE = process.env.API_BASE_URL ?? "http://localhost:8080/api/v1";

export async function POST(request: Request) {
  try {
    const body = await request.json();
    const upstream = await fetch(`${API_BASE}/auth/login`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
      cache: "no-store",
    });

    const payload = (await upstream.json()) as LoginResponse | ErrorResponse;
    if (!upstream.ok) {
      const res = NextResponse.json(payload, { status: upstream.status });
      const retryAfter = upstream.headers.get("retry-after");
      if (retryAfter) res.headers.set("Retry-After", retryAfter);
      return res;
    }

    const data = payload as LoginResponse;

    const res = NextResponse.json({ ok: true }, { status: 200 });

    // Promote access token to session cookie (HttpOnly, browser-scoped).
    res.cookies.set("session", data.access_token, {
      httpOnly: true,
      path: "/",
      maxAge: data.expires_in ?? 900,
      sameSite: "lax",
    });

    // The refresh token is no longer in the JSON body — read it from the
    // Set-Cookie header on the upstream response and re-issue it on the
    // frontend origin.
    const upstreamSetCookie = readSetCookieHeaders(upstream.headers);
    const refreshToken = extractCookieValue(upstreamSetCookie, "refresh_token");
    
    if (refreshToken) {
      // Compute MaxAge from refresh_expiry so the cookie lifetime matches the
      // server-side TTL exactly, rather than using a hardcoded 30-day constant.
      const expiryMs = new Date(data.refresh_expiry).getTime() - Date.now();
      const maxAge = Math.max(Math.floor(expiryMs / 1000), 0);
      res.cookies.set("refresh_token", refreshToken, {
        httpOnly: true,
        path: "/",
        maxAge: maxAge || 60 * 60 * 24 * 30, // fallback to 30d if expiry parse fails
        sameSite: "lax",
      });
      return res;
    }

    // If we get here, the refresh token was not found in the upstream Set-Cookie headers
    // Log this for debugging (in development only)
    if (process.env.NODE_ENV === "development") {
      console.error("[Login] Refresh token missing from upstream response", {
        setcookieCount: upstreamSetCookie.length,
        setCookieHeaders: upstreamSetCookie,
      });
    }

    const failure = NextResponse.json(
      { code: "refresh_cookie_missing", message: "Upstream login did not provide a refresh cookie." },
      { status: 502 },
    );
    failure.cookies.set("session", "", {
      path: "/",
      maxAge: 0,
      httpOnly: true,
      sameSite: "lax",
    });
    return failure;
  } catch (e) {
    return proxyError(e);
  }
}

function proxyError(e: unknown): NextResponse {
  return NextResponse.json(
    { code: "upstream_unavailable", message: "Service temporarily unavailable." },
    { status: 502 },
  );
}
