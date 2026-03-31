import { NextResponse } from "next/server";
import { cookies } from "next/headers";
import { isAxiosError } from "axios";
import { serverApi } from "@/lib/api/http/server";
import { extractCookieValue, readSetCookieHeaders } from "@/lib/api/http/cookies";

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

  const data = (await goRes.json()) as {
    access_token: string;
    expires_in: number;
  };

  const response = NextResponse.json(
    { redirectUrl: "/dashboard?provider=telegram&action=login" },
    { status: 200 },
  );

  response.cookies.set("session", data.access_token, {
    httpOnly: true,
    path: "/",
    maxAge: data.expires_in,
    sameSite: "lax",
  });

  const refreshToken = extractCookieValue(
    readSetCookieHeaders(goRes.headers),
    "refresh_token",
  );
  if (refreshToken) {
    response.cookies.set("refresh_token", refreshToken, {
      httpOnly: true,
      path: "/",
      maxAge: 60 * 60 * 24 * 30,
      sameSite: "lax",
    });
  }

  return response;
}

export async function PUT(request: Request) {
  const cookieStore = await cookies();
  const token = cookieStore.get("session")?.value;

  if (!token) {
    return NextResponse.json(
      { code: "unauthorized", message: "Not authenticated." },
      { status: 401 },
    );
  }

  let body: unknown;
  try {
    body = await request.json();
  } catch {
    return NextResponse.json(
      { code: "bad_request", message: "Invalid JSON body" },
      { status: 400 },
    );
  }

  try {
    await serverApi.put("/oauth/telegram", body, {
      headers: { Authorization: `Bearer ${token}` },
    });
    return new NextResponse(null, { status: 204 });
  } catch (e) {
    return proxyError(e);
  }
}

export async function DELETE() {
  const cookieStore = await cookies();
  const token = cookieStore.get("session")?.value;

  if (!token) {
    return NextResponse.json(
      { code: "unauthorized", message: "Not authenticated." },
      { status: 401 },
    );
  }

  try {
    await serverApi.delete("/oauth/telegram", {
      headers: { Authorization: `Bearer ${token}` },
    });
    return new NextResponse(null, { status: 204 });
  } catch (e) {
    return proxyError(e);
  }
}

function proxyError(e: unknown): NextResponse {
  if (isAxiosError(e) && e.response) {
    return NextResponse.json(e.response.data, { status: e.response.status });
  }
  return NextResponse.json(
    { code: "upstream_unavailable", message: "Service temporarily unavailable." },
    { status: 502 },
  );
}
