import { NextResponse } from "next/server";
import { isAxiosError } from "axios";
import { serverApi } from "@/lib/api/http/server";

export async function POST(request: Request) {
  try {
    const body = await request.json();
    const { data, status } = await serverApi.post("/auth/register", body);
    return NextResponse.json(data, { status });
  } catch (e) {
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
}
