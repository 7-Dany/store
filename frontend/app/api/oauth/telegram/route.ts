import { NextResponse } from "next/server";

// Telegram Login Widget callback bridge.
//
// The Telegram widget delivers signed user data to our client-side JS.
// This route forwards it to the Go backend for HMAC verification, creates or
// finds the user, and promotes the returned access_token into a session cookie
// that proxy.ts recognises.
//
// Flow:
//   1. TelegramLoginButton (client) POSTs widget data here
//   2. We forward to Go → POST /oauth/telegram/callback
//   3. Go validates HMAC + auth_date, upserts user, returns { access_token, expires_in }
//   4. We set session cookie and return { redirectUrl } to the client
const API_BASE = process.env.API_BASE_URL ?? "http://localhost:8080/api/v1";

export async function POST(request: Request) {
  let body: unknown;
  try {
    body = await request.json();
  } catch {
    return NextResponse.json(
      { error: "invalid_body", message: "Invalid JSON body" },
      { status: 400 },
    );
  }

  // Forward to Go backend — HMAC verification and user upsert happen there.
  let goRes: Response;
  try {
    goRes = await fetch(`${API_BASE}/oauth/telegram/callback`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
  } catch {
    return NextResponse.json(
      { error: "upstream_unavailable", message: "Could not reach auth service" },
      { status: 502 },
    );
  }

  if (!goRes.ok) {
    const err = await goRes.json().catch(() => ({
      error: "upstream_error",
      message: "Authentication failed",
    }));
    return NextResponse.json(err, { status: goRes.status });
  }

  const data = (await goRes.json()) as { access_token: string; expires_in: number };

  const response = NextResponse.json(
    { redirectUrl: "/dashboard?provider=telegram&action=login" },
    { status: 200 },
  );

  // Promote access token into a session cookie that proxy.ts recognises.
  // MaxAge matches expires_in returned by the Go backend (ACCESS_TOKEN_TTL).
  response.cookies.set("session", data.access_token, {
    httpOnly: true,
    path: "/",
    maxAge: data.expires_in,
    sameSite: "strict",
  });

  return response;
}
