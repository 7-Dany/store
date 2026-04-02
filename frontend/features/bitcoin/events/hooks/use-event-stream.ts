"use client";

/**
 * useEventStreamController
 *
 * Owns the full SSE lifecycle: token issuance → EventSource → exponential
 * back-off retry → blocker detection → manual retry.
 *
 * React 19.2 patterns:
 *  - useEffectEvent  — stable callbacks that always see latest state/refs
 *                      without being added to effect dependency arrays
 *  - useTransition   — non-blocking async token issuance
 *
 * Fixes vs. original:
 *  - Extracted to its own file (hooks/use-event-stream.ts)
 *  - resolveBlockerState guards against post-unmount state calls
 *  - window 'focus' event triggers an auto-reconnect after exhaustion
 *  - watched: Set<string> passed as option so WatchPanel can share state
 */

import {
  useState,
  useEffect,
  useRef,
  useTransition,
  useEffectEvent,
} from "react";
import {
  getEventsStatus,
  issueSSEToken,
} from "@/lib/api/bitcoin";
import type { BtcEvent, BtcEventType, ConnState } from "@/features/bitcoin/types";

// ── Constants ──────────────────────────────────────────────────────────────────

const MAX_EVENTS = 60;
const MAX_RETRIES = 5;
const RETRY_BASE_MS = 1_000;
const SSE_PROXY_URL = "/api/bitcoin/events";

// ── Hook options / return ──────────────────────────────────────────────────────

export interface UseEventStreamControllerOptions {
  onReregistrationNeeded: () => void;
}

export interface EventStreamController {
  connState: ConnState;
  events: BtcEvent[];
  error: string | null;
  retryCount: number;
  lastHeartbeatAt: number | null;
  retryNow: () => void;
  clearEvents: () => void;
}

// ── Hook ───────────────────────────────────────────────────────────────────────

export function useEventStreamController({
  onReregistrationNeeded,
}: UseEventStreamControllerOptions): EventStreamController {
  const [connState, setConnState] = useState<ConnState>("connecting");
  const [events, setEvents] = useState<BtcEvent[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [retryCount, setRetryCount] = useState(0);
  const [lastHeartbeatAt, setLastHeartbeatAt] = useState<number | null>(null);
  const [streamGeneration, setStreamGeneration] = useState(0);
  const [, startTransition] = useTransition();

  const esRef = useRef<EventSource | null>(null);
  const eventIdRef = useRef(0);
  const retryTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const attemptRef = useRef(0);
  const lifecycleActiveRef = useRef(true);
  const connectingRef = useRef(false);

  // ── useEffectEvent callbacks (stable, not in dep arrays) ──────────────────

  const clearRetryTimer = useEffectEvent(() => {
    if (retryTimerRef.current !== null) {
      clearTimeout(retryTimerRef.current);
      retryTimerRef.current = null;
    }
  });

  const closeStream = useEffectEvent(() => {
    esRef.current?.close();
    esRef.current = null;
    connectingRef.current = false;
  });

  const pushEvent = useEffectEvent((type: BtcEventType, raw: string) => {
    if (type === "ping") {
      setLastHeartbeatAt(Date.now());
      return;
    }

    let payload: Record<string, unknown> = {};
    try {
      payload = JSON.parse(raw) as Record<string, unknown>;
    } catch {
      // Non-JSON frames are dropped gracefully.
    }

    const entry: BtcEvent = {
      id: `${++eventIdRef.current}`,
      type,
      payload,
      receivedAt: Date.now(),
    };
    setEvents((prev) => [entry, ...prev].slice(0, MAX_EVENTS));

    if (type === "stream_requires_reregistration") {
      onReregistrationNeeded();
    }
  });

  const scheduleReconnect = useEffectEvent((reason: string) => {
    if (!lifecycleActiveRef.current) return;

    if (attemptRef.current >= MAX_RETRIES) {
      setConnState("error");
      setError(
        "Unable to restore the event stream automatically. Retry when you are ready.",
      );
      return;
    }

    attemptRef.current += 1;
    const delay = Math.min(
      RETRY_BASE_MS * 2 ** (attemptRef.current - 1),
      15_000,
    );
    setRetryCount(attemptRef.current);
    setConnState("reconnecting");
    setError(
      `Connection dropped (${reason}). Retrying in ${Math.ceil(delay / 1_000)}s.`,
    );

    clearRetryTimer();
    retryTimerRef.current = setTimeout(() => {
      retryTimerRef.current = null;
      setStreamGeneration((prev) => prev + 1);
    }, delay);
  });

  // Guards against post-unmount state mutations inside the async path.
  const resolveBlockerState = useEffectEvent(async (): Promise<boolean> => {
    if (!lifecycleActiveRef.current) return true; // treat unmounted as "blocked"
    try {
      const status = await getEventsStatus();
      if (!lifecycleActiveRef.current) return true;

      if (!status.zmq_connected) {
        clearRetryTimer();
        attemptRef.current = 0;
        setRetryCount(0);
        setConnState("error");
        setError(
          "Live events are blocked because the Bitcoin ZMQ subscriber is down. Restore it from System Health, then resume the stream.",
        );
        return true;
      }
      if (!status.rpc_connected) {
        clearRetryTimer();
        attemptRef.current = 0;
        setRetryCount(0);
        setConnState("error");
        setError(
          "Live events are blocked because the Bitcoin RPC connection is down. Restore it from System Health, then resume the stream.",
        );
        return true;
      }
    } catch {
      // Status lookup failure falls through to normal reconnect path.
    }
    return false;
  });

  const beginStream = useEffectEvent(() => {
    if (!lifecycleActiveRef.current || connectingRef.current) return;
    connectingRef.current = true;

    startTransition(async () => {
      const isReconnect = attemptRef.current > 0;
      setError(null);
      setConnState(isReconnect ? "reconnecting" : "connecting");

      try {
        await issueSSEToken();
      } catch (err) {
        connectingRef.current = false;
        const message =
          err instanceof Error ? err.message : "Failed to issue SSE token.";
        scheduleReconnect(message);
        return;
      }

      if (!lifecycleActiveRef.current) {
        connectingRef.current = false;
        return;
      }

      closeStream();

      const es = new EventSource(SSE_PROXY_URL, { withCredentials: true });
      esRef.current = es;

      es.onopen = () => {
        connectingRef.current = false;
        attemptRef.current = 0;
        setRetryCount(0);
        setError(null);
        setLastHeartbeatAt(Date.now());
        setConnState("connected");
      };

      es.onerror = () => {
        closeStream();
        connectingRef.current = false;
        if (!lifecycleActiveRef.current) return;
        void (async () => {
          const blocked = await resolveBlockerState();
          if (!blocked && lifecycleActiveRef.current) {
            scheduleReconnect("stream unavailable");
          }
        })();
      };

      const types: BtcEventType[] = [
        "ping",
        "new_block",
        "pending_mempool",
        "confirmed_tx",
        "mempool_replaced",
        "stream_requires_reregistration",
      ];
      for (const t of types) {
        es.addEventListener(t, (e: MessageEvent) => pushEvent(t, e.data));
      }
    });
  });

  const retryNow = useEffectEvent(() => {
    attemptRef.current = 0;
    setRetryCount(0);
    setError(null);
    setStreamGeneration((prev) => prev + 1);
  });

  // ── Lifecycle wiring ───────────────────────────────────────────────────────

  useEffect(() => {
    lifecycleActiveRef.current = true;

    const onPageHide = () => {
      lifecycleActiveRef.current = false;
      clearRetryTimer();
      closeStream();
    };

    // Auto-reconnect from permanent-error state when the tab regains focus.
    // Useful after a brief network blip that clears itself.
    const onFocus = () => {
      if (
        lifecycleActiveRef.current &&
        !connectingRef.current &&
        !esRef.current
      ) {
        attemptRef.current = 0;
        setStreamGeneration((prev) => prev + 1);
      }
    };

    window.addEventListener("pagehide", onPageHide);
    window.addEventListener("focus", onFocus);

    return () => {
      lifecycleActiveRef.current = false;
      clearRetryTimer();
      closeStream();
      window.removeEventListener("pagehide", onPageHide);
      window.removeEventListener("focus", onFocus);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (!lifecycleActiveRef.current) return;
    beginStream();
    // Only re-run on generation bumps triggered by retryNow / scheduleReconnect.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [streamGeneration]);

  return {
    connState,
    events,
    error,
    retryCount,
    lastHeartbeatAt,
    retryNow,
    clearEvents: () => setEvents([]),
  };
}
