import { NextResponse } from "next/server";
import type { NextRequest } from "next/server";

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

export function proxy(request: NextRequest) {
  const { pathname } = request.nextUrl;
  const hasSession = !!request.cookies.get("session")?.value;

  // Protected route — no cookie → send to login, preserving intended destination.
  if (PROTECTED.test(pathname) && !hasSession) {
    const loginUrl = new URL("/login", request.url);
    loginUrl.searchParams.set("from", pathname);
    return NextResponse.redirect(loginUrl);
  }

  // Public auth route — already has a cookie → send straight to dashboard.
  if (PUBLIC.test(pathname) && hasSession) {
    return NextResponse.redirect(new URL("/dashboard", request.url));
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
