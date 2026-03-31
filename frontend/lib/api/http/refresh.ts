/**
 * Shared token refresh coordinator.
 *
 * The backend rotates refresh tokens as single-use credentials. A plain
 * module-level mutex only protects one browser tab; a second tab would still
 * send the old refresh token concurrently and trigger reuse detection. This
 * module coordinates refreshes both within the current tab and across tabs.
 */

const LOCK_KEY = "__store_refresh_lock__";
const RESULT_KEY = "__store_refresh_result__";
const LAST_REFRESH_KEY = "__store_last_refresh_at__";
const CHANNEL_NAME = "store-auth-refresh";
const LOCK_TTL_MS = 10_000;
const WAIT_TIMEOUT_MS = 12_000;

const tabId =
  typeof crypto !== "undefined" && "randomUUID" in crypto
    ? crypto.randomUUID()
    : `tab-${Math.random().toString(36).slice(2)}`;

const channel =
  typeof window !== "undefined" && "BroadcastChannel" in window
    ? new BroadcastChannel(CHANNEL_NAME)
    : null;

let isRefreshing = false;
let queue: Array<(ok: boolean) => void> = [];
let lastRefreshError: "network" | "auth" | null = null;

/**
 * Enable debug logging for token refresh operations.
 * Call this in browser console: enableRefreshDebug(true)
 */
export function enableRefreshDebug(enable: boolean) {
  if (typeof window !== "undefined") {
    (window as any).__DEBUG_REFRESH = enable;
    if (enable) {
      console.log("[TokenRefresh] Debug mode enabled. Refresh operations will be logged.");
    }
  }
}

/**
 * Returns the error type from the most recent failed refresh.
 * "network" = transient connectivity failure, safe to retry silently.
 * "auth"    = server explicitly rejected the token (401/403/423).
 */
export function getLastRefreshError(): "network" | "auth" | null {
  return lastRefreshError;
}

function drain(ok: boolean) {
  queue.forEach((resolve) => resolve(ok));
  queue = [];
}

function readLastRefreshAt(): number {
  if (typeof window === "undefined") return 0;
  const raw = window.localStorage.getItem(LAST_REFRESH_KEY);
  const parsed = raw ? Number(raw) : 0;
  return Number.isFinite(parsed) ? parsed : 0;
}

function writeLastRefreshAt(at: number) {
  if (typeof window === "undefined") return;
  window.localStorage.setItem(LAST_REFRESH_KEY, String(at));
}

function publishResult(ok: boolean) {
  if (typeof window === "undefined") return;

  const at = Date.now();
  if (ok) {
    writeLastRefreshAt(at);
  }

  const payload = JSON.stringify({ ok, at, owner: tabId });
  window.localStorage.setItem(RESULT_KEY, payload);
  channel?.postMessage({ ok, at, owner: tabId });
}

function readLock(): { owner: string; expiresAt: number } | null {
  if (typeof window === "undefined") return null;

  const raw = window.localStorage.getItem(LOCK_KEY);
  if (!raw) return null;

  try {
    const parsed = JSON.parse(raw) as { owner?: string; expiresAt?: number };
    if (
      typeof parsed.owner === "string" &&
      typeof parsed.expiresAt === "number" &&
      Number.isFinite(parsed.expiresAt)
    ) {
      return { owner: parsed.owner, expiresAt: parsed.expiresAt };
    }
  } catch {
    // Ignore malformed lock values and treat as unlocked.
  }

  return null;
}

function releaseLock() {
  if (typeof window === "undefined") return;

  const current = readLock();
  if (current?.owner === tabId) {
    window.localStorage.removeItem(LOCK_KEY);
  }
}

function tryAcquireLock(): boolean {
  if (typeof window === "undefined") return false;

  const now = Date.now();
  const current = readLock();
  if (current && current.expiresAt > now && current.owner !== tabId) {
    return false;
  }

  const next = JSON.stringify({ owner: tabId, expiresAt: now + LOCK_TTL_MS });
  window.localStorage.setItem(LOCK_KEY, next);

  const confirmed = readLock();
  return confirmed?.owner === tabId;
}

async function waitForRefreshResult(): Promise<boolean> {
  if (typeof window === "undefined") return false;

  const startedAt = Date.now();

  const fromStorage = () => {
    const raw = window.localStorage.getItem(RESULT_KEY);
    if (!raw) return null;

    try {
      const parsed = JSON.parse(raw) as { ok?: boolean; at?: number };
      if (
        typeof parsed.ok === "boolean" &&
        typeof parsed.at === "number" &&
        parsed.at >= startedAt
      ) {
        return parsed.ok;
      }
    } catch {
      // Ignore malformed result payloads.
    }

    return null;
  };

  const existing = fromStorage();
  if (existing !== null) {
    return existing;
  }

  return new Promise<boolean>((resolve) => {
    let timeoutId: number | undefined;

    const cleanup = () => {
      if (timeoutId) window.clearTimeout(timeoutId);
      channel?.removeEventListener("message", onMessage);
      window.removeEventListener("storage", onStorage);
    };

    const finish = (ok: boolean) => {
      cleanup();
      resolve(ok);
    };

    const onMessage = (event: MessageEvent) => {
      const data = event.data as { ok?: boolean; at?: number } | undefined;
      if (
        data &&
        typeof data.ok === "boolean" &&
        typeof data.at === "number" &&
        data.at >= startedAt
      ) {
        finish(data.ok);
      }
    };

    const onStorage = (event: StorageEvent) => {
      if (event.key !== RESULT_KEY || !event.newValue) return;

      try {
        const parsed = JSON.parse(event.newValue) as { ok?: boolean; at?: number };
        if (
          typeof parsed.ok === "boolean" &&
          typeof parsed.at === "number" &&
          parsed.at >= startedAt
        ) {
          finish(parsed.ok);
        }
      } catch {
        // Ignore malformed storage events.
      }
    };

    channel?.addEventListener("message", onMessage);
    window.addEventListener("storage", onStorage);

    timeoutId = window.setTimeout(() => {
      const currentLock = readLock();
      if (!currentLock || currentLock.expiresAt <= Date.now()) {
        finish(false);
        return;
      }
      finish(false);
    }, WAIT_TIMEOUT_MS);
  });
}

async function performRefresh(): Promise<boolean> {
  try {
    const res = await fetch("/api/auth/refresh", { method: "POST" });
    const ok = res.ok;
    lastRefreshError = ok ? null : "auth";
    
    // Log refresh attempts to help diagnose session issues
    if (typeof window !== "undefined" && (window as any).__DEBUG_REFRESH) {
      console.log(
        `[TokenRefresh] ${ok ? "✓" : "✗"} Status: ${res.status}`,
        new Date().toISOString(),
      );
    }
    
    publishResult(ok);
    return ok;
  } catch (error) {
    // Network / DNS / TCP failure — do NOT publish false to other tabs because
    // that would force-logout everyone. Just release the lock and let each
    // caller decide whether to redirect based on getLastRefreshError().
    lastRefreshError = "network";
    releaseLock();
    
    // Log network errors for debugging
    if (typeof window !== "undefined" && (window as any).__DEBUG_REFRESH) {
      console.warn(
        `[TokenRefresh] Network error`,
        error instanceof Error ? error.message : String(error),
        new Date().toISOString(),
      );
    }
    
    return false;
  }
}

/**
 * Returns the last known successful refresh timestamp.
 * Used by TokenRefresher to decide whether a proactive refresh is needed now.
 */
export function getLastRefreshAt(): number {
  return readLastRefreshAt();
}

/**
 * Marks the session as freshly issued. Login uses this so the dashboard does
 * not immediately rotate a brand new refresh token on first mount.
 */
export function markSessionFresh(at = Date.now()) {
  writeLastRefreshAt(at);
}

/**
 * doRefresh calls POST /api/auth/refresh with in-tab and cross-tab coordination.
 */
export async function doRefresh(): Promise<boolean> {
  if (isRefreshing) {
    return new Promise<boolean>((resolve) => queue.push(resolve));
  }

  isRefreshing = true;
  try {
    let ok: boolean;
    if (tryAcquireLock()) {
      ok = await performRefresh();
    } else {
      ok = await waitForRefreshResult();
    }

    drain(ok);
    return ok;
  } finally {
    isRefreshing = false;
  }
}
