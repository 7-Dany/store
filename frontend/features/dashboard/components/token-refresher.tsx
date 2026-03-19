"use client";

import { useEffect, useRef } from "react";
import { doRefresh } from "@/lib/api/http/refresh";

/**
 * TokenRefresher — proactive client-side session keep-alive.
 *
 * Renders nothing. Mounts once in DashboardLayout and keeps the session alive
 * through two mechanisms:
 *
 *   1. Interval (every 13 min) — refreshes before the 15-min access token TTL
 *      so server components never encounter an expired session cookie during
 *      normal use.
 *
 *   2. visibilitychange — refreshes when the tab becomes visible again after
 *      being hidden, but only if ≥ 13 minutes have passed since the last
 *      successful refresh. This covers the "away for a while then switch back"
 *      case without burning rate-limit slots on rapid tab switches.
 *
 * Both paths go through doRefresh() (lib/api/http/refresh.ts) which holds a
 * shared module-level mutex. The Axios 401 interceptor also uses doRefresh(),
 * so a concurrent 401 retry and a timer refresh will never race into two
 * simultaneous POST /api/auth/refresh calls — which would trigger the backend's
 * single-use token reuse detection and revoke the entire token family.
 *
 * Rate limit awareness: the backend allows 5 refreshes per 15 min per IP.
 * With a 13-min interval and visibility-gating at 13 min we stay well within
 * this budget even across multiple open tabs (each tab shares the same cookie,
 * so a successful refresh in one tab silently satisfies the others).
 */

// 13 minutes in ms — fires before the 15-min access token TTL.
const REFRESH_INTERVAL_MS = 13 * 60 * 1000;

export function TokenRefresher() {
  // lastRefresh tracks the timestamp of the last successful refresh so the
  // visibility handler can decide whether enough time has passed. Using a ref
  // means reads/writes never trigger a re-render (rerender-use-ref-transient-values).
  const lastRefresh = useRef<number>(Date.now());

  useEffect(() => {
    // ── 1. Interval-based proactive refresh ──────────────────────────────────
    const intervalId = setInterval(async () => {
      const ok = await doRefresh();
      if (ok) {
        lastRefresh.current = Date.now();
      } else {
        // Refresh failed (token expired/revoked) — redirect to login.
        window.location.href = "/login";
      }
    }, REFRESH_INTERVAL_MS);

    // ── 2. Visibility-based refresh on tab focus ──────────────────────────────
    // Following client-event-listeners rule: one handler reference, removed on cleanup.
    const handleVisibility = async () => {
      if (document.visibilityState !== "visible") return;
      const elapsed = Date.now() - lastRefresh.current;
      if (elapsed < REFRESH_INTERVAL_MS) return; // recent refresh — skip

      const ok = await doRefresh();
      if (ok) {
        lastRefresh.current = Date.now();
      } else {
        window.location.href = "/login";
      }
    };

    document.addEventListener("visibilitychange", handleVisibility);

    return () => {
      clearInterval(intervalId);
      document.removeEventListener("visibilitychange", handleVisibility);
    };
  }, []); // empty deps — register once for the lifetime of the dashboard

  return null;
}
