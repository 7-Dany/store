/**
 * PATCH /api/auth/password → PATCH /api/v1/auth/password
 * Change password for the currently signed-in account.
 * Requires session cookie (Bearer token forwarded to backend).
 */
import { NextResponse } from "next/server";
import { cookies } from "next/headers";

const API_BASE = process.env.API_BASE_URL ?? "http://localhost:8080/api/v1";

export async function PATCH(request: Request) {
  const cookieStore = await cookies();
  const token = cookieStore.get("session")?.value;

  if (!token) {
    return NextResponse.json(
      { code: "unauthorized", message: "Not authenticated." },
      { status: 401 },
    );
  }

  let body: string;
  try {
    body = await request.text();
  } catch {
    return NextResponse.json(
      { code: "bad_request", message: "Invalid request body." },
      { status: 400 },
    );
  }

  let res: Response;
  try {
    res = await fetch(`${API_BASE}/auth/password`, {
      method: "PATCH",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${token}`,
      },
      body,
    });
  } catch {
    return NextResponse.json(
      { code: "upstream_unavailable", message: "Service temporarily unavailable." },
      { status: 502 },
    );
  }

  const text = await res.text();
  const json = text
    ? (() => { try { return JSON.parse(text); } catch { return { message: text }; } })()
    : null;

  const out = NextResponse.json(json, { status: res.status });
  const retryAfter = res.headers.get("retry-after");
  if (retryAfter) out.headers.set("Retry-After", retryAfter);

  return out;
}
