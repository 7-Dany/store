import { NextResponse } from "next/server";
import { isAxiosError } from "axios";
import { serverApi } from "@/lib/api/http/server";

interface LoginResponse {
  access_token: string;
  // refresh_token is no longer in the JSON body (F-02 fix).
  // The Go backend delivers it exclusively via Set-Cookie.
  refresh_expiry: string;
  expires_in: number;
  scheduled_deletion_at?: string;
}

/**
 * Extracts the refresh_token value from a Set-Cookie header array.
 * The Go backend sets: refresh_token=<value>; Path=/api/v1/auth; HttpOnly; ...
 * We pull out just the value so we can re-set it on the Next.js domain.
 */
function extractRefreshToken(
  setCookieHeaders: string | string[] | undefined,
): string | null {
  const headers = Array.isArray(setCookieHeaders)
    ? setCookieHeaders
    : setCookieHeaders
      ? [setCookieHeaders]
      : [];
  for (const header of headers) {
    const match = header.match(/^refresh_token=([^;]+)/);
    if (match) return match[1];
  }
  return null;
}

export async function POST(request: Request) {
  try {
    const body = await request.json();
    const { data, headers } = await serverApi.post<LoginResponse>("/auth/login", body);

    const res = NextResponse.json({ ok: true }, { status: 200 });

    // Promote access token to session cookie (HttpOnly, browser-scoped).
    res.cookies.set("session", data.access_token, {
      httpOnly: true,
      path: "/",
      maxAge: data.expires_in ?? 900,
      sameSite: "lax",
    });

    // The refresh token is no longer in the JSON body — read it from the
    // Set-Cookie header that the Go backend placed on the Axios response.
    // In Node.js, Axios exposes Set-Cookie as string[] on response.headers.
    const refreshToken = extractRefreshToken(
      headers["set-cookie"] as string | string[] | undefined,
    );
    if (refreshToken) {
      // Compute MaxAge from refresh_expiry so the cookie lifetime matches the
      // server-side TTL exactly, rather than using a hardcoded 30-day constant.
      const expiryMs = new Date(data.refresh_expiry).getTime() - Date.now();
      const maxAge = Math.max(Math.floor(expiryMs / 1000), 0);
      res.cookies.set("refresh_token", refreshToken, {
        httpOnly: true,
        path: "/",
        maxAge: maxAge || 60 * 60 * 24 * 30, // fallback to 30d if expiry parse fails
        sameSite: "lax",
      });
    }

    return res;
  } catch (e) {
    return proxyError(e);
  }
}

function proxyError(e: unknown): NextResponse {
  if (isAxiosError(e) && e.response) {
    const res = NextResponse.json(e.response.data, { status: e.response.status });
    const retryAfter = e.response.headers["retry-after"] as string | undefined;
    if (retryAfter) res.headers.set("Retry-After", retryAfter);
    return res;
  }
  return NextResponse.json(
    { code: "upstream_unavailable", message: "Service temporarily unavailable." },
    { status: 502 },
  );
}
