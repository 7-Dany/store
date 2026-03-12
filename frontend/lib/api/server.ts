import axios from "axios";

const API_BASE = process.env.API_BASE_URL ?? "http://localhost:8080/api/v1";

/**
 * Server-side Axios client.
 * Used exclusively inside Next.js API route handlers to reach the Go backend.
 * Never imported in client components.
 */
export const serverApi = axios.create({
  baseURL: API_BASE,
  headers: { "Content-Type": "application/json" },
  timeout: 12_000,
});
