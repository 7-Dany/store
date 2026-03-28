"use client";

import {
  useState,
  useEffect,
  useRef,
  useTransition,
  useEffectEvent,
} from "react";
import { motion, AnimatePresence } from "framer-motion";
import {
  IconActivity,
  IconAlertTriangle,
  IconArrowsLeftRight,
  IconCircleCheck,
  IconClock,
  IconCube,
  IconLoader2,
  IconPlayerPause,
  IconPlugConnected,
  IconRefresh,
  IconRefreshAlert,
  IconWifiOff,
} from "@tabler/icons-react";
import { cn } from "@/lib/utils";
import {
  getBlock,
  getEventsStatus,
  issueSSEToken,
} from "@/lib/api/bitcoin";
import type { BlockDetailsResult } from "@/lib/api/bitcoin";

// ── Constants ─────────────────────────────────────────────────────────────────

const MAX_EVENTS = 60;
const INACTIVITY_MS = 5 * 60 * 1000;
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

const EVENT_CFG: Record<
  BtcEventType,
  {
    label: string;
    Icon: React.ElementType;
    dot: string;
    row: string;
    badge: string;
  }
> = {
  ping: {
    label: "Ping",
    Icon: IconActivity,
    dot: "bg-muted-foreground/40",
    row: "opacity-40",
    badge:
      "border-border/50 bg-muted/40 text-muted-foreground",
  },
  new_block: {
    label: "New block",
    Icon: IconCube,
    dot: "bg-blue-500",
    row: "",
    badge:
      "border-blue-500/30 bg-blue-500/10 text-blue-600 dark:text-blue-400",
  },
  pending_mempool: {
    label: "Mempool",
    Icon: IconClock,
    dot: "bg-amber-500",
    row: "",
    badge:
      "border-amber-500/30 bg-amber-500/10 text-amber-600 dark:text-amber-400",
  },
  confirmed_tx: {
    label: "Confirmed",
    Icon: IconCircleCheck,
    dot: "bg-green-500",
    row: "",
    badge:
      "border-green-500/30 bg-green-500/10 text-green-600 dark:text-green-400",
  },
  mempool_replaced: {
    label: "Replaced",
    Icon: IconArrowsLeftRight,
    dot: "bg-destructive",
    row: "",
    badge: "border-destructive/30 bg-destructive/10 text-destructive",
  },
  stream_requires_reregistration: {
    label: "Re-register",
    Icon: IconRefreshAlert,
    dot: "bg-amber-500 animate-pulse",
    row: "",
    badge:
      "border-amber-500/30 bg-amber-500/10 text-amber-600 dark:text-amber-400",
  },
};

// ── Connection state ──────────────────────────────────────────────────────────

type ConnState =
  | "connecting"
  | "connected"
  | "reconnecting"
  | "inactive"
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
  inactive: {
    label: "Inactive",
    dot: "bg-muted-foreground/40",
    Icon: IconPlayerPause,
    badge: "border-border/60 bg-muted/40 text-muted-foreground",
    hint: "The stream was paused after 5 minutes without activity.",
  },
  error: {
    label: "Error",
    dot: "bg-destructive",
    Icon: IconWifiOff,
    badge: "border-destructive/30 bg-destructive/10 text-destructive",
    hint: "The stream is unavailable right now.",
  },
};

function getBlockHashFromEvent(event: BtcEvent): string | null {
  if (event.type !== "new_block") {
    return null;
  }

  const hash = event.payload.hash;
  if (typeof hash !== "string" || !/^[0-9a-fA-F]{64}$/.test(hash)) {
    return null;
  }

  return hash.toLowerCase();
}

function formatBlockTime(value: number | undefined): string {
  if (!value) {
    return "—";
  }

  const date = new Date(value * 1000);
  if (Number.isNaN(date.getTime())) {
    return "—";
  }

  return date.toLocaleString([], {
    year: "numeric",
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

// ── EventRow ──────────────────────────────────────────────────────────────────

function EventRow({
  event,
  onSelectBlock,
  selected,
  loading,
}: {
  event: BtcEvent;
  onSelectBlock: (hash: string) => void;
  selected: boolean;
  loading: boolean;
}) {
  const cfg = EVENT_CFG[event.type];
  const blockHash = getBlockHashFromEvent(event);
  const clickable = blockHash !== null;
  const time = new Date(event.receivedAt).toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });

  // Pick a meaningful summary from the payload
  const summary: string = (() => {
    const p = event.payload;
    if (event.type === "new_block")
      return `Block #${p.height ? p.height : "?"} · ${String(p.hash ?? "").slice(0, 12)}…`;
    if (event.type === "pending_mempool")
      return `${String(p.txid ?? "").slice(0, 12)}… · ${p.fee_rate ?? 0} sat/vB`;
    if (event.type === "confirmed_tx")
      return `${String(p.txid ?? "").slice(0, 12)}… @ #${p.height ?? "?"}`;
    if (event.type === "mempool_replaced")
      return `Replaced ${String(p.replaced_txid ?? "").slice(0, 12)}…`;
    if (event.type === "stream_requires_reregistration")
      return "Watch addresses expired — re-register now";
    return "–";
  })();

  return (
    <motion.button
      type="button"
      disabled={!clickable}
      onClick={() => {
        if (blockHash) {
          onSelectBlock(blockHash);
        }
      }}
      aria-pressed={clickable ? selected : undefined}
      initial={{ opacity: 0, y: -6 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.18, ease: "easeOut" }}
      className={cn(
        "grid w-full grid-cols-[auto_auto_1fr_auto] items-center gap-3 rounded-lg px-3 py-2 text-left",
        "transition-colors",
        clickable &&
          "cursor-pointer hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        !clickable && "cursor-default",
        selected && "bg-blue-500/8 ring-1 ring-blue-500/20",
        cfg.row,
      )}
    >
      {/* dot */}
      <span className={cn("size-1.5 shrink-0 rounded-full", cfg.dot)} />

      {/* badge */}
      <span
        className={cn(
          "inline-flex shrink-0 items-center gap-1 rounded-full border px-2 py-0.5",
          "text-[10px] font-medium",
          cfg.badge,
        )}
      >
        <cfg.Icon size={9} stroke={2.5} />
        {cfg.label}
      </span>

      {/* summary */}
      <span className="min-w-0 truncate font-mono text-xs text-muted-foreground">
        {summary}
      </span>

      {/* time */}
      <span className="shrink-0 font-mono text-[10px] text-muted-foreground/60">
        {loading ? "Loading…" : time}
      </span>
    </motion.button>
  );
}

// ── EventStreamPanel ──────────────────────────────────────────────────────────

interface EventStreamPanelProps {
  connState: ConnState;
  events: BtcEvent[];
  error: string | null;
  retryCount: number;
  lastHeartbeatAt: number | null;
  working: boolean;
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
  const [working, startTransition] = useTransition();

  const esRef = useRef<EventSource | null>(null);
  const eventIdRef = useRef(0);
  const retryTimerRef = useRef<number | null>(null);
  const idleTimerRef = useRef<number | null>(null);
  const attemptRef = useRef(0);
  const closedForInactivityRef = useRef(false);
  const lifecycleActiveRef = useRef(true);
  const connectingRef = useRef(false);

  const clearRetryTimer = useEffectEvent(() => {
    if (retryTimerRef.current !== null) {
      window.clearTimeout(retryTimerRef.current);
      retryTimerRef.current = null;
    }
  });

  const clearIdleTimer = useEffectEvent(() => {
    if (idleTimerRef.current !== null) {
      window.clearTimeout(idleTimerRef.current);
      idleTimerRef.current = null;
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
    if (!lifecycleActiveRef.current || closedForInactivityRef.current) {
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
    if (
      !lifecycleActiveRef.current ||
      closedForInactivityRef.current ||
      connectingRef.current
    ) {
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

      if (!lifecycleActiveRef.current || closedForInactivityRef.current) {
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
        if (!lifecycleActiveRef.current || closedForInactivityRef.current) {
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

  const markInactive = useEffectEvent(() => {
    closedForInactivityRef.current = true;
    clearRetryTimer();
    closeStream();
    setConnState("inactive");
    setError(null);
    setRetryCount(0);
  });

  const rearmInactivityTimer = useEffectEvent(() => {
    clearIdleTimer();
    idleTimerRef.current = window.setTimeout(() => {
      markInactive();
    }, INACTIVITY_MS);
  });

  const noteActivity = useEffectEvent(() => {
    if (!lifecycleActiveRef.current) {
      return;
    }

    closedForInactivityRef.current = false;
    rearmInactivityTimer();

    if (connState === "inactive") {
      attemptRef.current = 0;
      setRetryCount(0);
      setStreamGeneration((prev) => prev + 1);
    }
  });

  const retryNow = useEffectEvent(() => {
    closedForInactivityRef.current = false;
    attemptRef.current = 0;
    setRetryCount(0);
    setError(null);
    setStreamGeneration((prev) => prev + 1);
  });

  useEffect(() => {
    lifecycleActiveRef.current = true;
    noteActivity();

    const activityEvents: Array<keyof WindowEventMap> = [
      "pointerdown",
      "keydown",
      "scroll",
      "focus",
    ];

    const onVisibility = () => {
      if (document.visibilityState === "visible") {
        noteActivity();
      } else {
        rearmInactivityTimer();
      }
    };

    const onPageHide = () => {
      lifecycleActiveRef.current = false;
      clearIdleTimer();
      clearRetryTimer();
      closeStream();
    };

    for (const eventName of activityEvents) {
      window.addEventListener(eventName, noteActivity, { passive: true });
    }
    document.addEventListener("visibilitychange", onVisibility);
    window.addEventListener("pagehide", onPageHide);

    return () => {
      lifecycleActiveRef.current = false;
      clearIdleTimer();
      clearRetryTimer();
      closeStream();
      for (const eventName of activityEvents) {
        window.removeEventListener(eventName, noteActivity);
      }
      document.removeEventListener("visibilitychange", onVisibility);
      window.removeEventListener("pagehide", onPageHide);
    };
    // useEffectEvent callbacks intentionally stay out of the dependency list.
    // This effect should wire the lifecycle listeners exactly once.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (!lifecycleActiveRef.current || closedForInactivityRef.current) {
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
    working,
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
  working,
  onRetryNow,
  onClearEvents,
  needsReregistration,
}: EventStreamPanelProps) {
  const feedRef = useRef<HTMLDivElement>(null);
  const latestBlockRequestRef = useRef<string | null>(null);
  const stateCfg = STATE_CFG[connState];
  const StateIcon = stateCfg.Icon;
  const [selectedBlockHash, setSelectedBlockHash] = useState<string | null>(null);
  const [blockDetails, setBlockDetails] = useState<BlockDetailsResult | null>(
    null,
  );
  const [blockError, setBlockError] = useState<string | null>(null);
  const [loadingBlockHash, setLoadingBlockHash] = useState<string | null>(null);
  const [loadingBlock, startBlockLookup] = useTransition();
  const isBusy =
    working ||
    connState === "connecting" ||
    connState === "reconnecting";
  const heartbeatLabel =
    lastHeartbeatAt === null
      ? "Waiting for first heartbeat."
      : `Last heartbeat ${new Date(lastHeartbeatAt).toLocaleTimeString([], {
          hour: "2-digit",
          minute: "2-digit",
          second: "2-digit",
        })}`;

  useEffect(() => {
    feedRef.current?.scrollTo({ top: 0, behavior: "smooth" });
  }, [events.length]);

  function handleSelectBlock(hash: string) {
    if (loadingBlockHash === hash) {
      return;
    }

    latestBlockRequestRef.current = hash;
    setSelectedBlockHash(hash);
    setBlockError(null);
    setLoadingBlockHash(hash);

    startBlockLookup(async () => {
      try {
        const details = await getBlock(hash);
        if (latestBlockRequestRef.current !== hash) {
          return;
        }

        setBlockDetails(details);
        setSelectedBlockHash(details.hash.toLowerCase());
      } catch (err: unknown) {
        if (latestBlockRequestRef.current !== hash) {
          return;
        }

        setBlockDetails(null);
        setBlockError(
          err instanceof Error
            ? err.message
            : "Failed to load block details. Try again.",
        );
      } finally {
        if (latestBlockRequestRef.current === hash) {
          setLoadingBlockHash(null);
        }
      }
    });
  }

  const showBlockPanel =
    selectedBlockHash !== null || loadingBlockHash !== null || blockError !== null;

  return (
    <div className="flex flex-col gap-4 pt-4">
      <div className="flex flex-col gap-2 rounded-xl border border-border bg-card px-4 py-3.5">
        <div className="flex items-start justify-between gap-3">
          <div className="flex items-start gap-3">
            <span className={cn("mt-1 size-2 shrink-0 rounded-full", stateCfg.dot)} />
            <div className="flex flex-col gap-1">
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-sm font-medium text-foreground">
                  Event stream
                </span>
                <span
                  className={cn(
                    "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-medium",
                    stateCfg.badge,
                  )}
                >
                  <StateIcon
                    size={11}
                    stroke={2}
                    className={cn(isBusy && "animate-spin")}
                  />
                  {stateCfg.label}
                </span>
                {retryCount > 0 && connState === "reconnecting" && (
                  <span className="rounded-full border border-border/60 bg-muted/40 px-2 py-0.5 font-mono text-[10px] text-muted-foreground">
                    try {retryCount}/{MAX_RETRIES}
                  </span>
                )}
              </div>
              <p className="text-xs text-muted-foreground">{stateCfg.hint}</p>
              {(connState === "connected" || connState === "reconnecting") && (
                <p className="text-[11px] text-muted-foreground/70">
                  {heartbeatLabel}
                </p>
              )}
            </div>
          </div>

          {(connState === "inactive" || connState === "error") && (
            <button
              onClick={onRetryNow}
              className={cn(
                "flex items-center gap-1.5 rounded-lg border border-primary/40 bg-primary/10",
                "px-3 py-1.5 text-xs font-medium text-primary transition-colors hover:bg-primary/20",
                "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
              )}
            >
              <IconRefresh size={13} stroke={2} />
              Resume stream
            </button>
          )}
        </div>

        <AnimatePresence>
          {error && (
            <motion.p
              initial={{ opacity: 0, height: 0 }}
              animate={{ opacity: 1, height: "auto" }}
              exit={{ opacity: 0, height: 0 }}
              className="text-xs text-destructive"
            >
              {error}
            </motion.p>
          )}
        </AnimatePresence>

        <AnimatePresence>
          {needsReregistration && (
            <motion.div
              initial={{ opacity: 0, height: 0 }}
              animate={{ opacity: 1, height: "auto" }}
              exit={{ opacity: 0, height: 0 }}
              className="flex items-center gap-2 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-600 dark:text-amber-400"
            >
              <IconRefreshAlert size={13} stroke={2} className="shrink-0" />
              Watch addresses expired. Re-register them in the Addresses tab to
              resume live updates.
            </motion.div>
          )}
        </AnimatePresence>
      </div>

      <AnimatePresence>
        {showBlockPanel && (
          <motion.div
            initial={{ opacity: 0, y: 6 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, y: 4 }}
            className="flex flex-col gap-3 rounded-xl border border-border bg-card px-4 py-3.5"
          >
            <div className="flex items-start justify-between gap-3">
              <div className="flex items-start gap-3">
                <span className="mt-1 size-2 shrink-0 rounded-full bg-blue-500" />
                <div className="flex flex-col gap-1">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-medium text-foreground">
                      Block details
                    </span>
                    {(loadingBlock || loadingBlockHash !== null) && (
                      <span className="inline-flex items-center gap-1 rounded-full border border-blue-500/30 bg-blue-500/10 px-2 py-0.5 text-[11px] font-medium text-blue-600 dark:text-blue-400">
                        <IconLoader2 size={11} stroke={2} className="animate-spin" />
                        Loading
                      </span>
                    )}
                  </div>
                  <p className="font-mono text-[11px] text-muted-foreground">
                    {(blockDetails?.hash ?? selectedBlockHash ?? "").slice(0, 24)}
                    {(blockDetails?.hash ?? selectedBlockHash ?? "").length > 24
                      ? "…"
                      : ""}
                  </p>
                </div>
              </div>

              <button
                type="button"
                onClick={() => {
                  latestBlockRequestRef.current = null;
                  setSelectedBlockHash(null);
                  setBlockDetails(null);
                  setBlockError(null);
                  setLoadingBlockHash(null);
                }}
                className="text-[10px] text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none"
              >
                Dismiss
              </button>
            </div>

            <AnimatePresence>
              {blockError && (
                <motion.div
                  initial={{ opacity: 0, height: 0 }}
                  animate={{ opacity: 1, height: "auto" }}
                  exit={{ opacity: 0, height: 0 }}
                  className="flex items-center gap-2 rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive"
                >
                  <IconAlertTriangle size={13} stroke={2} className="shrink-0" />
                  {blockError}
                </motion.div>
              )}
            </AnimatePresence>

            {blockDetails && (
              <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
                {[
                  ["Height", blockDetails.height.toLocaleString()],
                  ["Confirmations", blockDetails.confirmations.toLocaleString()],
                  ["Transactions", blockDetails.tx_count.toLocaleString()],
                  ["Mined at", formatBlockTime(blockDetails.time)],
                  ["Median time", formatBlockTime(blockDetails.median_time)],
                  ["Difficulty", blockDetails.difficulty.toLocaleString()],
                  ["Bits", blockDetails.bits],
                  ["Nonce", blockDetails.nonce.toLocaleString()],
                  ["Version", blockDetails.version.toString()],
                ].map(([label, value]) => (
                  <div
                    key={label}
                    className="rounded-lg border border-border/60 bg-muted/20 px-3 py-2"
                  >
                    <p className="text-[10px] uppercase tracking-[0.12em] text-muted-foreground/70">
                      {label}
                    </p>
                    <p className="mt-1 font-mono text-xs text-foreground">{value}</p>
                  </div>
                ))}

                {[
                  ["Merkle root", blockDetails.merkle_root],
                  ["Chainwork", blockDetails.chainwork],
                  ["Previous block", blockDetails.previous_block_hash ?? "—"],
                  ["Next block", blockDetails.next_block_hash ?? "—"],
                ].map(([label, value]) => (
                  <div
                    key={label}
                    className="rounded-lg border border-border/60 bg-muted/20 px-3 py-2 sm:col-span-2 xl:col-span-3"
                  >
                    <p className="text-[10px] uppercase tracking-[0.12em] text-muted-foreground/70">
                      {label}
                    </p>
                    <p className="mt-1 break-all font-mono text-xs text-foreground">
                      {value}
                    </p>
                  </div>
                ))}
              </div>
            )}
          </motion.div>
        )}
      </AnimatePresence>

      <div className="flex flex-col gap-0 overflow-hidden rounded-xl border border-border bg-card">
        <div className="flex items-center justify-between border-b border-border/60 px-4 py-3">
          <div className="flex items-center gap-2">
            <span className="text-xs font-medium text-foreground">Event feed</span>
            {events.length > 0 && (
              <span className="rounded-full bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
                {events.length}
              </span>
            )}
          </div>
          {events.length > 0 && (
            <button
              onClick={onClearEvents}
              className="text-[10px] text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none"
            >
              Clear
            </button>
          )}
        </div>

        <div
          ref={feedRef}
          className="flex max-h-80 flex-col gap-0 overflow-y-auto py-1"
        >
          {events.length === 0 ? (
            <div className="flex flex-col items-center justify-center gap-2 py-10 text-muted-foreground">
              {connState === "connected" ||
              connState === "reconnecting" ||
              connState === "connecting" ? (
                <>
                  <IconActivity
                    size={22}
                    stroke={1.25}
                    className="text-muted-foreground/40"
                  />
                  <p className="text-xs">Waiting for blockchain events…</p>
                </>
              ) : (
                <>
                  <IconAlertTriangle
                    size={22}
                    stroke={1.25}
                    className="text-muted-foreground/30"
                  />
                  <p className="text-xs">The stream is paused until activity resumes.</p>
                </>
              )}
            </div>
          ) : (
            <AnimatePresence initial={false}>
              {events.map((event) => (
                <EventRow
                  key={event.id}
                  event={event}
                  onSelectBlock={handleSelectBlock}
                  selected={getBlockHashFromEvent(event) === selectedBlockHash}
                  loading={getBlockHashFromEvent(event) === loadingBlockHash}
                />
              ))}
            </AnimatePresence>
          )}
        </div>
      </div>
    </div>
  );
}
