/**
 * Catch-all proxy for /api/profile/* → backend /api/v1/profile/*
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
  const search = request.nextUrl.search; // preserve query string (e.g. ?username=...)
  const backendUrl = `${API_BASE}/profile/${path.join("/")}${search}`;

  // Read body for mutating methods
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

  // Parse response — some endpoints return empty body (204, 205, etc.)
  const text = await res.text();

  // Status codes that must not carry a body (Response constructor throws otherwise)
  if (!text || res.status === 204 || res.status === 205) {
    const out = new NextResponse(null, { status: res.status });
    const retryAfter = res.headers.get("retry-after");
    if (retryAfter) out.headers.set("Retry-After", retryAfter);
    return out;
  }

  const json = (() => { try { return JSON.parse(text); } catch { return { message: text }; } })();
  const out = NextResponse.json(json, { status: res.status });

  const retryAfter = res.headers.get("retry-after");
  if (retryAfter) out.headers.set("Retry-After", retryAfter);

  return out;
}

export const GET     = handler;
export const POST    = handler;
export const PATCH   = handler;
export const PUT     = handler;
export const DELETE  = handler;
