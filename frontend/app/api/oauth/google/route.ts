import { NextResponse } from "next/server";
import { cookies } from "next/headers";
import { isAxiosError } from "axios";
import { serverApi } from "@/lib/api/http/server";

export async function DELETE(request: Request) {
  const cookieStore = await cookies();
  const token = cookieStore.get("session")?.value;

  if (!token) {
    return NextResponse.json(
      { code: "unauthorized", message: "Not authenticated." },
      { status: 401 },
    );
  }

  try {
    await serverApi.delete("/oauth/google", {
      headers: { Authorization: `Bearer ${token}` },
    });
    return new NextResponse(null, { status: 200 });
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
