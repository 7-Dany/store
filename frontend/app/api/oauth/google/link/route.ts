import { NextResponse } from "next/server";
import { cookies } from "next/headers";

const API_BASE = process.env.API_BASE_URL ?? "http://localhost:8080/api/v1";

// Google OAuth link bridge.
//
// Browser navigations can't carry an Authorization header, so we proxy the
// initiate request server-side with the session JWT, then redirect the browser
// to the Google URL returned by the backend.
//
// Flow:
//   1. User clicks "Link Google" on the profile page
//   2. Browser navigates to GET /api/oauth/google/link
//   3. This route reads the session cookie and calls the backend
//      GET /oauth/google with Authorization: Bearer <token>
//   4. Backend returns 302 → accounts.google.com with link-mode state baked in
//   5. We forward that redirect to the browser
//   6. On return the callback route (/api/oauth/callback) handles ?action=linked
export async function GET(request: Request) {
  const cookieStore = await cookies();
  const token = cookieStore.get("session")?.value;

  if (!token) {
    return NextResponse.redirect(new URL("/login", request.url));
  }

  let backendRes: Response;
  try {
    backendRes = await fetch(`${API_BASE}/oauth/google`, {
      headers: { Authorization: `Bearer ${token}` },
      redirect: "manual", // capture the 302, don't follow it
    });
  } catch {
    const url = new URL("/dashboard/profile", request.url);
    url.searchParams.set("error", "google_link_failed");
    return NextResponse.redirect(url);
  }

  const location = backendRes.headers.get("location");
  if (!location) {
    const url = new URL("/dashboard/profile", request.url);
    url.searchParams.set("error", "google_link_failed");
    return NextResponse.redirect(url);
  }

  return NextResponse.redirect(location);
}
