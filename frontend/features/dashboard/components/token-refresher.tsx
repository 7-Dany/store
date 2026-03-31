"use client";

import { useEffect, useEffectEvent, useRef } from "react";
import { doRefresh, getLastRefreshAt, getLastRefreshError, enableRefreshDebug } from "@/lib/api/http/refresh";
import { loginUrlForCurrentPage } from "@/lib/auth/redirect";

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

// Minimum delay before first refresh on initial page load.
// This gives the session cookie time to be set from the server response.
const FIRST_REFRESH_DELAY_MS = 1000;

// Maximum delay for the first refresh to prevent token expiry during page load.
// Access token is 15 min, so we must refresh within ~12 min of session start.
const MAX_FIRST_REFRESH_DELAY_MS = 12 * 60 * 1000;

// Retry configuration for first refresh in case of transient failures
const FIRST_REFRESH_RETRY_DELAY_MS = 2000;
const FIRST_REFRESH_MAX_RETRIES = 2;

export function TokenRefresher() {
  // lastRefresh tracks the timestamp of the last successful refresh so the
  // visibility handler can decide whether enough time has passed. Using a ref
  // means reads/writes never trigger a re-render (rerender-use-ref-transient-values).
  const lastRefresh = useRef<number>(getLastRefreshAt());
  const debugMode = useRef(false);

  // Detect debug mode from localStorage or query param
  useEffect(() => {
    if (typeof window !== "undefined") {
      const debugParam = new URLSearchParams(window.location.search).get("debug");
      debugMode.current = debugParam === "refresh" || (typeof localStorage !== "undefined" && localStorage.getItem("__DEBUG_REFRESH") === "1");
      if (debugMode.current) {
        console.log("[TokenRefresher] Debug mode enabled");
      }
    }
  }, []);

  const refreshNow = useEffectEvent(async () => {
    const ok = await doRefresh();
    if (ok) {
      lastRefresh.current = Date.now();
      if (debugMode.current) {
        console.log("[TokenRefresher] Refresh succeeded at", new Date().toLocaleTimeString());
      }
      return;
    }

    // Only redirect on a genuine auth rejection (expired/revoked token).
    // Transient network errors should not log the user out — the next interval
    // or visibility event will retry automatically.
    const error = getLastRefreshError();
    if (error !== "network") {
      if (debugMode.current) {
        console.log("[TokenRefresher] Auth error, redirecting to login. Error:", error);
      }
      window.location.href = loginUrlForCurrentPage();
    } else if (debugMode.current) {
      console.log("[TokenRefresher] Network error, will retry on next interval");
    }
  });

  const refreshNowWithRetry = useEffectEvent(async () => {
    let lastError = null;
    const maxRetries = FIRST_REFRESH_MAX_RETRIES;

    for (let attempt = 0; attempt <= maxRetries; attempt++) {
      if (attempt > 0) {
        if (debugMode.current) {
          console.log(`[TokenRefresher] First refresh attempt ${attempt} failed, retrying in ${FIRST_REFRESH_RETRY_DELAY_MS}ms...`);
        }
        await new Promise((resolve) => setTimeout(resolve, FIRST_REFRESH_RETRY_DELAY_MS));
      }

      const ok = await doRefresh();
      if (ok) {
        lastRefresh.current = Date.now();
        if (debugMode.current) {
          console.log(`[TokenRefresher] First refresh succeeded on attempt ${attempt + 1}`);
        }
        return;
      }

      lastError = getLastRefreshError();
      if (debugMode.current) {
        console.log(`[TokenRefresher] First refresh attempt ${attempt + 1} failed:`, lastError);
      }

      // Stop retrying on auth errors (token revoked, etc)
      if (lastError !== "network") {
        break;
      }
    }

    // After all retries exhausted:
    // - On network errors: log warning but DON'T redirect (connectivity might be restored)
    // - On auth errors: the token is definitely invalid, redirect to re-login
    if (lastError === "network") {
      if (debugMode.current) {
        console.log("[TokenRefresher] First refresh failed due to network, will retry on next interval");
      }
      // Don't redirect - let the regular interval retry
      return;
    }

    // Auth error on first refresh = token is invalid
    // This could happen if:
    // - Cookie wasn't set properly after login
    // - Backend rejected the token
    // - Token was already rotated/revoked
    if (debugMode.current) {
      console.log("[TokenRefresher] First refresh failed with auth error, redirecting to login");
    }
    window.location.href = loginUrlForCurrentPage();
  });

  const refreshOnVisibility = useEffectEvent(async () => {
    if (document.visibilityState !== "visible") return;

    const elapsed = Date.now() - lastRefresh.current;
    if (elapsed < REFRESH_INTERVAL_MS) {
      if (debugMode.current) {
        console.log(`[TokenRefresher] Tab visible but only ${Math.round(elapsed / 1000)}s since last refresh`);
      }
      return;
    }

    if (debugMode.current) {
      console.log(`[TokenRefresher] Tab visible after ${Math.round(elapsed / 1000)}s, refreshing now`);
    }
    await refreshNow();
  });

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const oauthProvider = params.get("provider");

    if (debugMode.current) {
      console.log("[TokenRefresher] Mounted, lastRefreshAt =", lastRefresh.current ? new Date(lastRefresh.current).toLocaleTimeString() : "never");
    }

    let intervalId: number | ReturnType<typeof window.setInterval>;
    let timeoutId: number | ReturnType<typeof window.setTimeout>;

    if (oauthProvider) {
      // Fresh OAuth login: the callback has already refreshed the token,
      // so mark it as fresh and start the keep-alive interval.
      lastRefresh.current = Date.now();
      if (debugMode.current) {
        console.log("[TokenRefresher] OAuth provider detected, starting 13-min intervals");
      }
      intervalId = window.setInterval(() => void refreshNow(), REFRESH_INTERVAL_MS);
    } else {
      // Non-OAuth mount: schedule the first fire relative to the last known refresh.
      // If there's no prior refresh timestamp (zero), this is a fresh page load from an
      // authenticated session created via server-side redirect. We give the DOM brief
      // time to stabilize before attempting refresh to ensure cookies are properly set.
      const lastRefreshTime = lastRefresh.current;
      const isFirstEverRefresh = lastRefreshTime === 0;

      if (isFirstEverRefresh) {
        // Fresh page load: delay first refresh to allow cookies to settle.
        // Use FIRST_REFRESH_DELAY_MS as default, capped at MAX_FIRST_REFRESH_DELAY_MS
        // to prevent token window expiry during load.
        const firstFireIn = Math.min(FIRST_REFRESH_DELAY_MS, MAX_FIRST_REFRESH_DELAY_MS);

        if (debugMode.current) {
          console.log(`[TokenRefresher] First ever refresh, scheduling in ${firstFireIn}ms with ${FIRST_REFRESH_MAX_RETRIES} retries`);
        }

        timeoutId = window.setTimeout(() => {
          void refreshNowWithRetry();
          // After the first fire switch to a regular interval anchored from now.
          intervalId = window.setInterval(() => void refreshNow(), REFRESH_INTERVAL_MS);
        }, firstFireIn);
      } else {
        // Prior refresh exists: schedule relative to when that refresh happened.
        // Example: last refresh was 12 min ago → fire in 1 min, not 13 min from now.
        const elapsed = Date.now() - lastRefreshTime;
        const firstFireIn = Math.max(0, REFRESH_INTERVAL_MS - elapsed);

        if (debugMode.current) {
          console.log(`[TokenRefresher] Prior refresh was ${Math.round(elapsed / 1000)}s ago, scheduling next in ${Math.round(firstFireIn / 1000)}s`);
        }

        timeoutId = window.setTimeout(() => {
          void refreshNow();
          // After the first fire switch to a regular interval anchored from there.
          intervalId = window.setInterval(() => void refreshNow(), REFRESH_INTERVAL_MS);
        }, firstFireIn);
      }
    }

    // ── Visibility-based refresh on tab focus ──────────────────────────────
    // Following client-event-listeners rule: one handler reference, removed on cleanup.
    const handleVisibility = () => void refreshOnVisibility();
    document.addEventListener("visibilitychange", handleVisibility);

    return () => {
      window.clearTimeout(timeoutId);
      clearInterval(intervalId);
      document.removeEventListener("visibilitychange", handleVisibility);
    };
  }, []); // useEffectEvent callbacks intentionally stay out of the dependency list.

  return null;
}
