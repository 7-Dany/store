/**
 * Catch-all proxy for /api/bitcoin/* → backend /api/v1/bitcoin/*
 *
 * Reads the httpOnly session cookie and forwards it as a Bearer token.
 * Passes through the request body, query string, and method unchanged.
 * Forwards Retry-After header on 429 responses.
 */
import { NextResponse } from "next/server";
import { cookies } from "next/headers";
import type { NextRequest } from "next/server";

const API_BASE = process.env.API_BASE_URL ?? "http://localhost:8080/api/v1";

type Context = { params: Promise<{ path: string[] }> };

async function handler(request: NextRequest, context: Context) {
  const cookieStore = await cookies();
  const token = cookieStore.get("session")?.value;

  if (!token) {
    return NextResponse.json(
      { code: "unauthorized", message: "Not authenticated." },
      { status: 401 },
    );
  }

  const { path } = await context.params;
  const search = request.nextUrl.search;
  const backendUrl = `${API_BASE}/bitcoin/${path.join("/")}${search}`;

  let body: string | undefined;
  if (!["GET", "HEAD"].includes(request.method)) {
    body = await request.text();
  }

  let res: Response;
  try {
    res = await fetch(backendUrl, {
      method: request.method,
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${token}`,
      },
      ...(body !== undefined ? { body } : {}),
    });
  } catch {
    return NextResponse.json(
      { code: "upstream_unavailable", message: "Service temporarily unavailable." },
      { status: 502 },
    );
  }

  const text = await res.text();

  if (!text || res.status === 204 || res.status === 205) {
    const out = new NextResponse(null, { status: res.status });
    const retryAfter = res.headers.get("retry-after");
    if (retryAfter) out.headers.set("Retry-After", retryAfter);
    forwardSSECookie(res, out);
    return out;
  }

  const json = (() => { try { return JSON.parse(text); } catch { return { message: text }; } })();
  const out = NextResponse.json(json, { status: res.status });

  const retryAfter = res.headers.get("retry-after");
  if (retryAfter) out.headers.set("Retry-After", retryAfter);
  forwardSSECookie(res, out);

  return out;
}

/**
 * Forward the btc_sse_jti Set-Cookie from the backend response,
 * rewriting the Path from the backend's /api/v1/bitcoin/events to
 * the proxy path /api/bitcoin/events so the browser stores the cookie
 * against the correct origin.
 */
function forwardSSECookie(backendRes: Response, out: NextResponse) {
  // getSetCookie() returns each Set-Cookie header as a separate string
  // (avoids the comma-splitting ambiguity of res.headers.get).
  const setCookieHeaders: string[] =
    typeof (backendRes.headers as unknown as { getSetCookie?: () => string[] }).getSetCookie === "function"
      ? (backendRes.headers as unknown as { getSetCookie: () => string[] }).getSetCookie()
      : [];

  for (const raw of setCookieHeaders) {
    if (!raw.toLowerCase().startsWith("btc_sse_jti=")) continue;
    // Rewrite the backend path so the cookie is scoped to the proxy path
    const rewritten = raw.replace(
      /;\s*Path=\/api\/v1\/bitcoin\/events/i,
      "; Path=/api/bitcoin/events",
    );
    out.headers.append("Set-Cookie", rewritten);
  }
}

export const GET    = handler;
export const POST   = handler;
export const PATCH  = handler;
export const PUT    = handler;
export const DELETE = handler;
