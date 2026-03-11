import { NextResponse } from "next/server";
import { cookies } from "next/headers";

// OAuth callback bridge — called by the Go backend after a successful Google
// OAuth flow via OAUTH_SUCCESS_URL=http://localhost:3000/api/oauth/callback.
//
// The Go handler sets two cookies on localhost:8080 before the redirect:
//   oauth_access_token — short-lived (30 s), non-HttpOnly, path=/
//   refresh_token      — long-lived, HttpOnly, path=/api/v1/auth (not readable here)
//
// This route reads oauth_access_token (same eTLD "localhost", so the browser
// includes it in the request), promotes it into a proper session cookie, then
// clears the ephemeral bridge cookie.
//
// Query params forwarded by the Go handler:
//   ?provider=google              — login or register mode
//   ?provider=google&action=linked — link mode (no new session minted)
export async function GET(request: Request) {
  const { searchParams } = new URL(request.url);
  const provider = searchParams.get("provider") ?? "unknown";
  const action   = searchParams.get("action")   ?? "login";

  const cookieStore = await cookies();
  const oauthToken  = cookieStore.get("oauth_access_token")?.value;

  if (!oauthToken) {
    // Token missing (expired 30 s window) or bridge cookie was never set.
    const loginUrl = new URL("/login", request.url);
    loginUrl.searchParams.set("error", "oauth_session_expired");
    return NextResponse.redirect(loginUrl);
  }

  const dashboardUrl = new URL("/dashboard", request.url);
  dashboardUrl.searchParams.set("provider", provider);
  dashboardUrl.searchParams.set("action", action);

  const response = NextResponse.redirect(dashboardUrl);

  // Promote the access token into a session cookie that proxy.ts recognises.
  // MaxAge matches ACCESS_TOKEN_TTL (15 min) so the session expires when the
  // token does. Silent-refresh wiring is a later task.
  response.cookies.set("session", oauthToken, {
    httpOnly: true,
    path:     "/",
    maxAge:   15 * 60, // 15 minutes — matches ACCESS_TOKEN_TTL
    sameSite: "strict",
  });

  // Clear the ephemeral bridge cookie set by the Go backend.
  response.cookies.set("oauth_access_token", "", {
    path:   "/",
    maxAge: 0,
  });

  return response;
}
