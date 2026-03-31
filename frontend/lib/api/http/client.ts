import axios from "axios";
import { doRefresh, getLastRefreshError } from "./refresh"; // shared mutex — see refresh.ts
import { loginUrlForCurrentPage } from "@/lib/auth/redirect";

/**
 * Browser-side Axios client — proxied.
 * Points at the Next.js API layer (`/api/*`), never at the Go backend directly.
 * Use for calls that need the httpOnly session cookie (login, OAuth, etc.).
 */
export const apiClient = axios.create({
  baseURL: "/api",
  headers: { "Content-Type": "application/json" },
  timeout: 12_000,
});

/**
 * Browser-side Axios client — direct.
 * Calls the Go backend directly for unauthenticated, stateless endpoints
 * that don't need cookie handling (verify-email, resend-verification, etc.).
 */
export const publicClient = axios.create({
  baseURL: process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080/api/v1",
  headers: { "Content-Type": "application/json" },
  timeout: 12_000,
});

// ─── Silent token refresh interceptor ────────────────────────────────────────
//
// On any 401 from apiClient:
//   1. Call doRefresh() — shared mutex ensures only one refresh runs at a time,
//      even if TokenRefresher fires concurrently (single-use token safety).
//   2. If refresh succeeds → retry the original request once.
//   3. If refresh fails (expired, revoked, reuse detected, locked) → hard-
//      redirect to /login so the user re-authenticates.

apiClient.interceptors.response.use(
  (response) => response,
  async (error) => {
    const original = error.config;

    // Only handle 401 errors that haven't already been retried,
    // and never intercept auth endpoints — their 401s are intentional
    // (wrong password, missing token, etc.) and must reach the caller.
    const url: string = original?.url ?? "";
    const isAuthEndpoint =
      url.includes("/auth/login") ||
      url.includes("/auth/register") ||
      url.includes("/auth/refresh") ||
      url.includes("/auth/logout");

    if (
      !axios.isAxiosError(error) ||
      error.response?.status !== 401 ||
      original?._retried ||
      isAuthEndpoint
    ) {
      return Promise.reject(error);
    }

    // Mark this request so we don't loop.
    original._retried = true;

    const ok = await doRefresh();
    if (ok) return apiClient(original);

    // Only force-logout on a genuine auth rejection (expired/revoked token).
    // A transient network error should not clear the session — the request
    // will fail naturally and the caller can surface an error state instead.
    if (getLastRefreshError() !== "network" && typeof window !== "undefined") {
      window.location.href = loginUrlForCurrentPage();
    }
    return Promise.reject(error);
  },
);
