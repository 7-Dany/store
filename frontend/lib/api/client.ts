import axios from "axios";

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
