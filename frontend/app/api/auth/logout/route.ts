import { NextResponse } from "next/server";
import { cookies } from "next/headers";
import { extractRequestCookie } from "@/lib/api/http/cookies";

const API_BASE = process.env.API_BASE_URL ?? "http://localhost:8080/api/v1";

// GET /api/auth/logout
// Used by server components (layout redirect) to clear a stale/invalid
// session cookie without needing a POST. Skips the backend call since the
// token is likely already invalid — just clears cookies and redirects.
export async function GET(request: Request) {
  const origin = new URL(request.url).origin;
  const res = NextResponse.redirect(`${origin}/login`);
  res.cookies.set("session", "", { path: "/", maxAge: 0, httpOnly: true, sameSite: "strict" });
  res.cookies.set("refresh_token", "", { path: "/", maxAge: 0, httpOnly: true, sameSite: "strict" });
  res.cookies.set("refresh_token", "", { path: "/api/v1/auth", maxAge: 0, httpOnly: true, sameSite: "strict" });
  res.cookies.set("oauth_access_token", "", { path: "/", maxAge: 0 });
  return res;
}

// POST /api/auth/logout
// Forwards to Go backend with Bearer + refresh_token cookie, clears both
// cookies, then redirects to /login. Always treats as success — the backend
// returns 204 regardless (idempotent, best-effort).
export async function POST(request: Request) {
  const cookieStore = await cookies();
  const session = cookieStore.get("session")?.value;
  const refreshToken = extractRequestCookie(
    request.headers.get("cookie"),
    "refresh_token",
  );

  try {
    await fetch(`${API_BASE}/auth/logout`, {
      method: "POST",
      headers: {
        ...(session ? { Authorization: `Bearer ${session}` } : {}),
        ...(refreshToken ? { Cookie: `refresh_token=${refreshToken}` } : {}),
      },
    });
  } catch {
    // Backend unreachable — still clear cookies and redirect.
  }

  const origin = new URL(request.url).origin;
  const res = NextResponse.redirect(`${origin}/login`);

  // Clear session cookie (access token)
  res.cookies.set("session", "", {
    path: "/",
    maxAge: 0,
    httpOnly: true,
    sameSite: "strict",
  });

  // Clear refresh token cookie — scoped to /api/v1/auth on the Go backend
  res.cookies.set("refresh_token", "", {
    path: "/",
    maxAge: 0,
    httpOnly: true,
    sameSite: "strict",
  });
  res.cookies.set("refresh_token", "", {
    path: "/api/v1/auth",
    maxAge: 0,
    httpOnly: true,
    sameSite: "strict",
  });
  res.cookies.set("oauth_access_token", "", {
    path: "/",
    maxAge: 0,
  });

  return res;
}
