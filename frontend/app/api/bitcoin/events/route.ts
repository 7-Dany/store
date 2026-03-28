/**
 * SSE streaming proxy for GET /api/bitcoin/events → backend GET /api/v1/bitcoin/events
 *
 * Why a dedicated route instead of the catch-all [...path]/route.ts:
 *   The catch-all reads the entire response body as text before returning,
 *   which buffers the SSE stream forever and never delivers events to the
 *   browser.  This route streams the backend response body directly.
 *
 * Cookie handling:
 *   The browser cannot send the HttpOnly btc_sse_jti cookie to the backend
 *   directly (different origin).  This proxy reads it from the Next.js cookie
 *   store and forwards it in the upstream request header.
 *
 * Origin forwarding:
 *   The backend's Events handler validates the Origin header against
 *   BTC_ALLOWED_ORIGINS before consuming the JTI token.  We forward the
 *   browser's original Origin so the guard passes.
 *   EventSource is always same-origin, so browsers omit the Origin header;
 *   in that case we derive it from request.url so the backend guard is satisfied.
 */
import { NextRequest, NextResponse } from "next/server";
import { cookies } from "next/headers";

const API_BASE = process.env.API_BASE_URL ?? "http://localhost:8080/api/v1";

export async function GET(request: NextRequest) {
  const cookieStore = await cookies();

  // Session guard — same as the catch-all proxy.
  const token = cookieStore.get("session")?.value;
  if (!token) {
    return NextResponse.json(
      { code: "unauthorized", message: "Not authenticated." },
      { status: 401 },
    );
  }

  // The one-time SSE JTI cookie issued by POST /events/token.
  const sseJti = cookieStore.get("btc_sse_jti")?.value;
  if (!sseJti) {
    return NextResponse.json(
      { code: "sse_token_missing", message: "SSE token cookie missing — call POST /api/bitcoin/events/token first." },
      { status: 401 },
    );
  }

  // Forward the browser's Origin so the backend's origin-allow-list check passes.
  // EventSource is always same-origin — browsers omit the Origin header on
  // same-origin requests, so we derive it from request.url as a fallback.
  // Without this the backend's missing-origin guard returns 403.
  const origin =
    request.headers.get("origin") ?? new URL(request.url).origin;
  const upstreamAbort = new AbortController();
  const abortUpstream = () => {
    if (!upstreamAbort.signal.aborted) {
      upstreamAbort.abort();
    }
  };

  request.signal.addEventListener("abort", abortUpstream, { once: true });

  let backendRes: Response;
  try {
    console.log("[bitcoin/events] → upstream fetch", { origin, hasSseJti: !!sseJti });
    backendRes = await fetch(`${API_BASE}/bitcoin/events`, {
      method: "GET",
      headers: {
        Authorization: `Bearer ${token}`,
        Accept: "text/event-stream",
        "Cache-Control": "no-cache",
        Cookie: `btc_sse_jti=${sseJti}`,
        Origin: origin,
      },
      signal: upstreamAbort.signal,
      // cache: "no-store" prevents Next.js from buffering the response.
      cache: "no-store",
    });
  } catch (err) {
    request.signal.removeEventListener("abort", abortUpstream);
    console.error("[bitcoin/events] upstream fetch threw — backend unreachable:", err);
    return NextResponse.json(
      { code: "upstream_unavailable", message: "Service temporarily unavailable." },
      { status: 502 },
    );
  }

  if (!backendRes.ok) {
    request.signal.removeEventListener("abort", abortUpstream);
    // Surface the backend error status so the frontend can handle 401 / 503 etc.
    const body = await backendRes.text().catch(() => "");
    const json = (() => { try { return JSON.parse(body); } catch { return { message: body }; } })();
    console.error(
      `[bitcoin/events] backend returned ${backendRes.status}:`,
      JSON.stringify(json),
    );
    return NextResponse.json(json, { status: backendRes.status });
  }

  if (!backendRes.body) {
    request.signal.removeEventListener("abort", abortUpstream);
    return new NextResponse(null, { status: 502 });
  }

  const reader = backendRes.body.getReader();

  const stream = new ReadableStream<Uint8Array>({
    async pull(controller) {
      try {
        const { done, value } = await reader.read();
        if (done) {
          controller.close();
          return;
        }
        controller.enqueue(value);
      } catch (err) {
        if (!upstreamAbort.signal.aborted) {
          controller.error(err);
        }
      }
    },
    cancel() {
      abortUpstream();
      return reader.cancel().catch(() => undefined);
    },
  });

  // Stream the SSE body directly to the browser.
  // Tie upstream cancellation to the browser connection so stale SSE slots are released promptly.
  return new NextResponse(stream, {
    status: 200,
    headers: {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-cache, no-transform",
      "X-Accel-Buffering": "no",   // disable nginx/proxy buffering
      Connection: "keep-alive",
    },
  });
}
