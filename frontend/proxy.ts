import { NextResponse } from "next/server";
import type { NextRequest } from "next/server";

// Next.js 16 proxy — runs at the Node.js runtime before every matched route.
// File must be named proxy.ts; export must be named `proxy` (Next.js 16+).
//
// Protected routes — redirect to /login if no valid session present.
// This is a lightweight optimistic check only; real auth is enforced
// server-side by the Go backend on every API call.
export function proxy(request: NextRequest) {
  const session     = request.cookies.get("session");
  const oauthBridge = request.cookies.get("oauth_access_token");

  // ── OAuth bridge promotion ─────────────────────────────────────────────────
  // After a real Google OAuth flow the Go backend sets oauth_access_token
  // (non-HttpOnly, MaxAge=30 s, path=/) on localhost:8080 and redirects the
  // browser to localhost:3000/dashboard?provider=google.
  //
  // Browsers do NOT scope cookies by port for localhost, so the cookie is
  // sent in the request to localhost:3000. The proxy sees it here before the
  // page renders and promotes it into a proper session cookie:
  //   1. Set session = access_token  (httpOnly, 15 min)
  //   2. Clear the ephemeral bridge cookie
  //   3. Let the request continue — no redirect needed
  if (!session && oauthBridge) {
    const response = NextResponse.next();
    response.cookies.set("session", oauthBridge.value, {
      httpOnly: true,
      path:     "/",
      maxAge:   15 * 60, // 15 min — matches ACCESS_TOKEN_TTL
      sameSite: "strict",
    });
    response.cookies.set("oauth_access_token", "", {
      path:   "/",
      maxAge: 0,
    });
    return response;
  }

  // ── Standard session guard ─────────────────────────────────────────────────
  if (!session) {
    const loginUrl = new URL("/login", request.url);
    loginUrl.searchParams.set("next", request.nextUrl.pathname);
    return NextResponse.redirect(loginUrl);
  }

  return NextResponse.next();
}

export const config = {
  matcher: ["/dashboard/:path*"],
};
