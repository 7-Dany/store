import { NextResponse } from "next/server";
import { isAxiosError } from "axios";
import { serverApi } from "@/lib/api/http/server";

interface LoginResponse {
  access_token: string;
  refresh_token: string;
  expires_in: number;
}

export async function POST(request: Request) {
  try {
    const body = await request.json();
    const { data } = await serverApi.post<LoginResponse>("/auth/login", body);

    const res = NextResponse.json({ ok: true }, { status: 200 });
    res.cookies.set("session", data.access_token, {
      httpOnly: true,
      path: "/",
      maxAge: data.expires_in ?? 900,
      sameSite: "lax",
    });
    if (data.refresh_token) {
      res.cookies.set("refresh_token", data.refresh_token, {
        httpOnly: true,
        path: "/",
        maxAge: 60 * 60 * 24 * 30, // 30 days
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
