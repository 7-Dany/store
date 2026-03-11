import { NextResponse } from "next/server";

// Mock logout — clears the session cookie and redirects to /login.
export async function GET(request: Request) {
  const response = NextResponse.redirect(new URL("/login", request.url));
  response.cookies.delete("session");
  return response;
}
