import { NextResponse } from "next/server";

// Mock login — sets a session cookie so proxy.ts lets the request through.
// This is dev-only scaffolding; remove before production.
export async function GET(request: Request) {
  const { searchParams } = new URL(request.url);
  const next = searchParams.get("next") ?? "/dashboard";

  const response = NextResponse.redirect(new URL(next, request.url));

  response.cookies.set("session", "mock-session-token", {
    httpOnly: true,
    path: "/",
    // No maxAge → session cookie; cleared when the browser closes.
  });

  return response;
}
