"use client";

import {
  useState,
  useEffect,
  useRef,
  useTransition,
  useEffectEvent,
} from "react";
import {
  IconActivity,
  IconLoader2,
  IconPlugConnected,
  IconRefresh,
  IconWifiOff,
} from "@tabler/icons-react";
import { cn } from "@/lib/utils";
import {
  getEventsStatus,
  issueSSEToken,
} from "@/lib/api/bitcoin";
import {
  EventFeedSheet,
  formatFeedEventTime,
  getFeedEventLabel,
} from "./event-feed-sheet";

// ── Constants ─────────────────────────────────────────────────────────────────

const MAX_EVENTS = 60;
const MAX_RETRIES = 5;
const RETRY_BASE_MS = 1_000;
const SSE_PROXY_URL = "/api/bitcoin/events";

// ── Event types ───────────────────────────────────────────────────────────────

type BtcEventType =
  | "ping"
  | "new_block"
  | "pending_mempool"
  | "confirmed_tx"
  | "mempool_replaced"
  | "stream_requires_reregistration";

interface BtcEvent {
  id: string;
  type: BtcEventType;
  payload: Record<string, unknown>;
  receivedAt: number;
}

// ── Connection state ──────────────────────────────────────────────────────────

type ConnState =
  | "connecting"
  | "connected"
  | "reconnecting"
  | "error";

const STATE_CFG: Record<
  ConnState,
  {
    label: string;
    dot: string;
    Icon: React.ElementType;
    badge: string;
    hint: string;
  }
> = {
  connecting: {
    label: "Connecting",
    dot: "bg-amber-500 animate-pulse",
    Icon: IconLoader2,
    badge:
      "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-400",
    hint: "Issuing a fresh SSE token and opening the stream.",
  },
  connected: {
    label: "Live",
    dot: "bg-green-500",
    Icon: IconPlugConnected,
    badge:
      "border-green-500/30 bg-green-500/10 text-green-700 dark:text-green-400",
    hint: "Listening for blockchain events for this workspace.",
  },
  reconnecting: {
    label: "Reconnecting",
    dot: "bg-amber-500 animate-pulse",
    Icon: IconRefresh,
    badge:
      "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-400",
    hint: "The stream dropped. Reconnecting automatically.",
  },
  error: {
    label: "Error",
    dot: "bg-destructive",
    Icon: IconWifiOff,
    badge: "border-destructive/30 bg-destructive/10 text-destructive",
    hint: "The stream is unavailable right now.",
  },
};

const EVENT_TRIGGER_CFG: Record<
  BtcEventType,
  { ring: string; icon: string; ping: string }
> = {
  ping: {
    ring: "bg-background/80 ring-border/60 hover:bg-muted/40",
    icon: "text-muted-foreground",
    ping: "bg-muted-foreground/50",
  },
  new_block: {
    ring: "bg-blue-500/10 ring-blue-500/30 hover:bg-blue-500/15",
    icon: "text-blue-700 dark:text-blue-300",
    ping: "bg-blue-500",
  },
  pending_mempool: {
    ring: "bg-amber-500/10 ring-amber-500/30 hover:bg-amber-500/15",
    icon: "text-amber-700 dark:text-amber-300",
    ping: "bg-amber-500",
  },
  confirmed_tx: {
    ring: "bg-green-500/10 ring-green-500/30 hover:bg-green-500/15",
    icon: "text-green-700 dark:text-green-300",
    ping: "bg-green-500",
  },
  mempool_replaced: {
    ring: "bg-destructive/10 ring-destructive/30 hover:bg-destructive/15",
    icon: "text-destructive",
    ping: "bg-destructive/70",
  },
  stream_requires_reregistration: {
    ring: "bg-amber-500/10 ring-amber-500/30 hover:bg-amber-500/15",
    icon: "text-amber-700 dark:text-amber-300",
    ping: "bg-amber-500",
  },
};

const STATE_TRIGGER_CFG: Record<
  ConnState,
  { ring: string; icon: string; ping: string }
> = {
  connecting: {
    ring: "bg-amber-500/10 ring-amber-500/30 hover:bg-amber-500/15",
    icon: "text-amber-700 dark:text-amber-300",
    ping: "bg-amber-500",
  },
  connected: {
    ring: "bg-green-500/10 ring-green-500/30 hover:bg-green-500/15",
    icon: "text-green-700 dark:text-green-300",
    ping: "bg-green-500",
  },
  reconnecting: {
    ring: "bg-amber-500/10 ring-amber-500/30 hover:bg-amber-500/15",
    icon: "text-amber-700 dark:text-amber-300",
    ping: "bg-amber-500",
  },
  error: {
    ring: "bg-destructive/10 ring-destructive/30 hover:bg-destructive/15",
    icon: "text-destructive",
    ping: "bg-destructive/70",
  },
};

// ── EventStreamPanel ──────────────────────────────────────────────────────────

interface EventStreamPanelProps {
  connState: ConnState;
  events: BtcEvent[];
  error: string | null;
  retryCount: number;
  lastHeartbeatAt: number | null;
  onRetryNow: () => void;
  onClearEvents: () => void;
  needsReregistration: boolean;
}

interface UseEventStreamControllerOptions {
  onReregistrationNeeded: () => void;
}

export function useEventStreamController({
  onReregistrationNeeded,
}: UseEventStreamControllerOptions) {
  const [connState, setConnState] = useState<ConnState>("connecting");
  const [events, setEvents] = useState<BtcEvent[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [retryCount, setRetryCount] = useState(0);
  const [lastHeartbeatAt, setLastHeartbeatAt] = useState<number | null>(null);
  const [streamGeneration, setStreamGeneration] = useState(0);
  const [, startTransition] = useTransition();

  const esRef = useRef<EventSource | null>(null);
  const eventIdRef = useRef(0);
  const retryTimerRef = useRef<number | null>(null);
  const attemptRef = useRef(0);
  const lifecycleActiveRef = useRef(true);
  const connectingRef = useRef(false);

  const clearRetryTimer = useEffectEvent(() => {
    if (retryTimerRef.current !== null) {
      window.clearTimeout(retryTimerRef.current);
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
      // Keep heartbeat and non-JSON frames lightweight.
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
    if (!lifecycleActiveRef.current) {
      return;
    }

    if (attemptRef.current >= MAX_RETRIES) {
      setConnState("error");
      setError(
        "Unable to restore the event stream automatically. Retry when you are ready.",
      );
      return;
    }

    attemptRef.current += 1;
    const delay = Math.min(RETRY_BASE_MS * 2 ** (attemptRef.current - 1), 15_000);
    setRetryCount(attemptRef.current);
    setConnState("reconnecting");
    setError(`Connection dropped (${reason}). Retrying in ${Math.ceil(delay / 1000)}s.`);

    clearRetryTimer();
    retryTimerRef.current = window.setTimeout(() => {
      retryTimerRef.current = null;
      setStreamGeneration((prev) => prev + 1);
    }, delay);
  });

  const resolveBlockerState = useEffectEvent(async () => {
    try {
      const status = await getEventsStatus();
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
      // If status lookup fails, fall back to the normal reconnect path.
    }
    return false;
  });

  const beginStream = useEffectEvent(() => {
    if (!lifecycleActiveRef.current || connectingRef.current) {
      return;
    }

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

      const es = new EventSource(SSE_PROXY_URL, {
        withCredentials: true,
      });
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
        if (!lifecycleActiveRef.current) {
          return;
        }
        void (async () => {
          const blocked = await resolveBlockerState();
          if (!blocked) {
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
        es.addEventListener(t, (e: MessageEvent) => {
          pushEvent(t, e.data);
        });
      }
    });
  });

  const retryNow = useEffectEvent(() => {
    attemptRef.current = 0;
    setRetryCount(0);
    setError(null);
    setStreamGeneration((prev) => prev + 1);
  });

  useEffect(() => {
    lifecycleActiveRef.current = true;

    const onPageHide = () => {
      lifecycleActiveRef.current = false;
      clearRetryTimer();
      closeStream();
    };

    window.addEventListener("pagehide", onPageHide);

    return () => {
      lifecycleActiveRef.current = false;
      clearRetryTimer();
      closeStream();
      window.removeEventListener("pagehide", onPageHide);
    };
    // useEffectEvent callbacks intentionally stay out of the dependency list.
    // This effect should wire the lifecycle listeners exactly once.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (!lifecycleActiveRef.current) {
      return;
    }
    beginStream();
    // The stream should only (re)start on generation bumps.
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

export function EventStreamPanel({
  connState,
  events,
  error,
  retryCount,
  lastHeartbeatAt,
  onRetryNow,
  onClearEvents,
  needsReregistration,
}: EventStreamPanelProps) {
  const [feedOpen, setFeedOpen] = useState(false);
  const latestEvent = events[0] ?? null;
  const latestEventId = latestEvent?.id ?? null;
  const [pulseKey, setPulseKey] = useState(0);
  const previousEventIdRef = useRef<string | null>(null);
  const triggerTone = latestEvent
    ? EVENT_TRIGGER_CFG[latestEvent.type]
    : STATE_TRIGGER_CFG[connState];
  const triggerLabel = latestEvent
    ? `${getFeedEventLabel(latestEvent.type)} received at ${formatFeedEventTime(latestEvent.receivedAt)}`
    : `Event feed ${STATE_CFG[connState].label.toLowerCase()}`;
  
  // Keep pulsing while feed is closed and there are unviewed events
  const isPulsing = !feedOpen && events.length > 0;

  useEffect(() => {
    if (!latestEventId) {
      previousEventIdRef.current = null;
      return;
    }

    if (previousEventIdRef.current !== latestEventId) {
      setPulseKey((value) => value + 1);
    }

    previousEventIdRef.current = latestEventId;
  }, [latestEventId]);

  return (
    <>
      <button
        type="button"
        onClick={() => setFeedOpen(true)}
        aria-label={triggerLabel}
        title={triggerLabel}
        className={cn(
          "relative flex size-10 shrink-0 items-center justify-center rounded-xl ring-1 shadow-sm transition-colors",
          "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background",
          triggerTone.ring,
        )}
      >
        {isPulsing && (
          <span
            key={pulseKey}
            className={cn(
              "pointer-events-none absolute inset-0 animate-ping rounded-xl opacity-40",
              triggerTone.ping,
            )}
          />
        )}
        <IconActivity size={19} stroke={1.75} className={triggerTone.icon} />
      </button>

      <EventFeedSheet
        open={feedOpen}
        onOpenChange={setFeedOpen}
        connState={connState}
        events={events}
        error={error}
        retryCount={retryCount}
        lastHeartbeatAt={lastHeartbeatAt}
        needsReregistration={needsReregistration}
        onClearEvents={onClearEvents}
        onRetryNow={onRetryNow}
      />
    </>
  );
}
