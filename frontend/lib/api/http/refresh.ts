/**
 * Shared token refresh mutex.
 *
 * Both the Axios 401 interceptor (client.ts) and the proactive TokenRefresher
 * component import this module. Because both run in the same browser JS context
 * they share the same module-level state, so only one refresh request is ever
 * in-flight at a time.
 *
 * This is critical: the backend issues single-use refresh tokens. If two
 * callers fired POST /api/auth/refresh simultaneously, the second would
 * present an already-rotated token, trigger reuse detection, and revoke the
 * entire token family — logging the user out immediately.
 */

let isRefreshing = false;
let queue: Array<(ok: boolean) => void> = [];

function drain(ok: boolean) {
  queue.forEach((resolve) => resolve(ok));
  queue = [];
}

/**
 * doRefresh calls POST /api/auth/refresh exactly once at a time.
 *
 * If a refresh is already in-flight, subsequent callers are queued and resolve
 * when the in-flight call settles — they do NOT fire a second request.
 *
 * Returns true if the refresh succeeded (new cookies are set), false otherwise.
 */
export async function doRefresh(): Promise<boolean> {
  if (isRefreshing) {
    return new Promise<boolean>((resolve) => queue.push(resolve));
  }

  isRefreshing = true;
  try {
    const res = await fetch("/api/auth/refresh", { method: "POST" });
    const ok = res.ok;
    drain(ok);
    return ok;
  } catch {
    drain(false);
    return false;
  } finally {
    isRefreshing = false;
  }
}
