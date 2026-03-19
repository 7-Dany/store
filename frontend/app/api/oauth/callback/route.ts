import { NextResponse } from "next/server";
import { cookies } from "next/headers";

// OAuth callback bridge — called by the Go backend after a successful Google
// OAuth flow via OAUTH_SUCCESS_URL=http://localhost:3000/api/oauth/callback.
//
// Query params forwarded by the Go handler:
//   ?provider=google               — login / register mode
//   ?provider=google&action=linked — link mode (no new session is issued)
//
// Login/register mode:
//   The Go backend sets two cookies before this redirect:
//     - oauth_access_token  (non-HttpOnly, SameSite=Lax, 30s) — bridge token
//     - refresh_token       (HttpOnly, SameSite=Strict, Path=/api/v1/auth, 30d)
//   We read both here:
//     - Promote oauth_access_token → session (httpOnly, SameSite=lax)
//     - Re-set refresh_token with Path=/ so the frontend refresh proxy can use it
//     - Clear the bridge cookie
//   Then redirect to /dashboard.
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

  // ── Login / register mode — promote the bridge cookies ────────────────────
  const cookieStore = await cookies();
  const oauthToken  = cookieStore.get("oauth_access_token")?.value;
  const refreshToken = cookieStore.get("refresh_token")?.value;

  if (!oauthToken) {
    // Token missing (expired 30s window) or bridge cookie was never set.
    const loginUrl = new URL("/login", request.url);
    loginUrl.searchParams.set("error", "oauth_session_expired");
    return NextResponse.redirect(loginUrl);
  }

  const dashboardUrl = new URL("/dashboard", request.url);
  dashboardUrl.searchParams.set("provider", provider);

  const response = NextResponse.redirect(dashboardUrl);

  // Promote the bridge token to a proper httpOnly session cookie.
  response.cookies.set("session", oauthToken, {
    httpOnly: true,
    path:     "/",
    maxAge:   15 * 60,
    sameSite: "lax",
  });

  // Re-set the refresh token with Path=/ so the frontend refresh proxy at
  // /api/auth/refresh can read it. The backend set it with Path=/api/v1/auth
  // (scoped to port 8080), so we capture it here and re-issue it for port 3000.
  if (refreshToken) {
    response.cookies.set("refresh_token", refreshToken, {
      httpOnly: true,
      path:     "/",
      maxAge:   60 * 60 * 24 * 30, // 30 days
      sameSite: "lax",
    });
  }

  // Clear the short-lived bridge cookie.
  response.cookies.set("oauth_access_token", "", {
    path:   "/",
    maxAge: 0,
  });

  return response;
}
