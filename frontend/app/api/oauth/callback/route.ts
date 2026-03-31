import { NextResponse } from "next/server";
import { cookies } from "next/headers";
import { extractCookieValue, readSetCookieHeaders } from "@/lib/api/http/cookies";

// OAuth callback bridge — called by the Go backend after a successful Google
// OAuth flow via OAUTH_SUCCESS_URL=http://localhost:3000/api/oauth/callback.
//
// Query params forwarded by the Go handler:
//   ?provider=google               — login / register mode
//   ?provider=google&action=linked — link mode (no new session is issued)
//
// Login/register mode:
//   The Go backend redirects here after setting:
//     - oauth_access_token  (non-HttpOnly, SameSite=Lax, 30s)
//     - refresh_token       (HttpOnly, SameSite=Strict, Path=/api/v1/auth, 30d)
//   During the cross-site Google redirect chain, the Strict refresh cookie is
//   not reliable for an immediate frontend bridge call. So we bootstrap the
//   frontend session from oauth_access_token here, then land on a same-origin
//   bridge page that performs the first /api/v1/auth/refresh call immediately.
//   That promotes the long-lived refresh cookie onto the frontend origin before
//   the user settles on /dashboard, so later reloads and middleware recovery do
//   not depend on the original backend-scoped cookie surviving untouched.
//
// Link mode:
//   The Go backend does NOT set any new cookies — the existing session stays.
//   We skip cookie handling entirely and redirect straight to /dashboard/profile.
export async function GET(request: Request) {
  const { searchParams } = new URL(request.url);
  const provider = searchParams.get("provider") ?? "unknown";
  const action   = searchParams.get("action")   ?? "login";

  // ── Link mode — no new tokens, just return to profile ─────────────────────
  if (action === "linked") {
    const dest = new URL("/dashboard/profile", request.url);
    dest.searchParams.set("linked", provider);
    return NextResponse.redirect(dest);
  }

  // Skip the bridge page — redirect straight to /dashboard.
  // TokenRefresher detects ?provider=... on mount and immediately calls
  // doRefresh(), which promotes the refresh_token from the backend-scoped
  // Path=/api/v1/auth onto Path=/ before any reload could need it.
  const dashboardUrl = new URL("/dashboard", request.url);
  dashboardUrl.searchParams.set("provider", provider);

  const cookieStore = await cookies();
  const oauthToken = cookieStore.get("oauth_access_token")?.value;

  if (!oauthToken) {
    const loginUrl = new URL("/login", request.url);
    loginUrl.searchParams.set("error", "oauth_session_expired");
    return NextResponse.redirect(loginUrl);
  }

  const response = NextResponse.redirect(dashboardUrl);

  response.cookies.set("session", oauthToken, {
    httpOnly: true,
    path: "/",
    maxAge: 15 * 60,
    sameSite: "lax",
  });

  response.cookies.set("oauth_access_token", "", {
    path: "/",
    maxAge: 0,
  });

  // Immediately promote the refresh_token from Path=/api/v1/auth to Path=/
  // so TokenRefresher doesn't need to call refresh on mount.
  const refreshToken = cookieStore.get("refresh_token")?.value;
  if (refreshToken) {
    try {
      const upstream = await fetch(`${process.env.API_BASE_URL ?? "http://localhost:8080/api/v1"}/auth/refresh`, {
        method: "POST",
        headers: { Cookie: `refresh_token=${refreshToken}` },
        cache: "no-store",
      });

      if (upstream.ok) {
        const data = (await upstream.json()) as { access_token: string; refresh_expiry: string; expires_in: number };
        const nextRefreshToken = extractCookieValue(readSetCookieHeaders(upstream.headers), "refresh_token");

        if (nextRefreshToken) {
          // Update session with fresh access token
          response.cookies.set("session", data.access_token, {
            httpOnly: true,
            path: "/",
            maxAge: data.expires_in ?? 900,
            sameSite: "lax",
          });

          // Set refresh cookie with Path=/
          const maxAge = Math.floor((new Date(data.refresh_expiry).getTime() - Date.now()) / 1000);
          response.cookies.set("refresh_token", nextRefreshToken, {
            httpOnly: true,
            path: "/",
            maxAge: maxAge > 0 ? maxAge : 60 * 60 * 24 * 30,
            sameSite: "lax",
          });

          // Clear the old backend-scoped cookie
          response.cookies.set("refresh_token", "", {
            path: "/api/v1/auth",
            maxAge: 0,
            httpOnly: true,
            sameSite: "strict",
          });
        }
      }
    } catch {
      // Ignore errors; the session is still valid for 15 minutes
    }
  }

  return response;
}
