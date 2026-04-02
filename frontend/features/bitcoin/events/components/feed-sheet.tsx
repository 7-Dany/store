"use client";

import { useRef, useState, useTransition } from "react";
import { motion, AnimatePresence } from "framer-motion";
import {
  IconActivity,
  IconAlertTriangle,
  IconRefresh,
  IconRefreshAlert,
  IconX,
} from "@tabler/icons-react";
import { Button } from "@/components/ui/button";
import { Alert, AlertDescription } from "@/components/ui/alert";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { cn } from "@/lib/utils";
import { getBlockDetailsCached } from "@/features/bitcoin/events/lib/block-cache";
import {
  getBlockHashFromEvent,
  getFeedHeightStat,
  getMempoolStat,
} from "@/features/bitcoin/events/lib/feed-formatters";
import { getLiveStat } from "@/features/bitcoin/events/lib/feed-helpers";
import type { BtcEvent, ConnState } from "@/features/bitcoin/types";
import type { BlockDetailsResult } from "@/lib/api/bitcoin";
import { CONN_CFG } from "../lib/event-cfg";
import { StatTile } from "./stat-tile";
import { EventDetail } from "./event-detail";
import { EventRow } from "./event-row";

// ── Layout constants ───────────────────────────────────────────────────────────

/** Width of the always-visible feed list column. */
const FEED_W = 420; // px  (26.25rem)
/** Width of the event detail column that slides in. */
const DETAIL_W = 340; // px  (21.25rem)

/** Easing curve — matches the Base UI sheet open/close spring. */
const SLIDE_EASE: [number, number, number, number] = [0.32, 0.72, 0, 1];
const SLIDE_DURATION = 0.28;

// ── EventFeedSheet ─────────────────────────────────────────────────────────────

export interface EventFeedSheetProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  connState: ConnState;
  events: BtcEvent[];
  error: string | null;
  retryCount: number;
  lastHeartbeatAt: number | null;
  needsReregistration: boolean;
  onClearEvents: () => void;
  onRetryNow: () => void;
}

export function EventFeedSheet({
  open,
  onOpenChange,
  connState,
  events,
  error,
  retryCount,
  lastHeartbeatAt,
  needsReregistration,
  onClearEvents,
  onRetryNow,
}: EventFeedSheetProps) {
  const [selectedEventId, setSelectedEventId] = useState<string | null>(null);
  const [blockDetails, setBlockDetails] = useState<BlockDetailsResult | null>(null);
  const [blockError, setBlockError] = useState<string | null>(null);
  const [loadingBlockHash, setLoadingBlockHash] = useState<string | null>(null);
  const [loadingBlock, startBlockLookup] = useTransition();
  const latestBlockRequestRef = useRef<string | null>(null);

  const stateCfg = CONN_CFG[connState];
  const selectedEvent = events.find((e) => e.id === selectedEventId) ?? null;
  const hasDetail = selectedEvent !== null;

  const heightStat = getFeedHeightStat(events);
  const mempoolStat = getMempoolStat(events);
  const liveStat = getLiveStat(connState, lastHeartbeatAt, retryCount);
  const retryMeta =
    connState === "reconnecting" && retryCount > 0 ? ` · try ${retryCount}` : "";

  function clearSelection() {
    latestBlockRequestRef.current = null;
    setSelectedEventId(null);
    setBlockDetails(null);
    setBlockError(null);
    setLoadingBlockHash(null);
  }

  function handleSelectEvent(event: BtcEvent) {
    if (selectedEventId === event.id) {
      clearSelection();
      return;
    }
    setSelectedEventId(event.id);
    setBlockError(null);

    const blockHash = getBlockHashFromEvent(event);
    if (!blockHash) {
      latestBlockRequestRef.current = null;
      setBlockDetails(null);
      setLoadingBlockHash(null);
      return;
    }

    latestBlockRequestRef.current = blockHash;
    setLoadingBlockHash(blockHash);

    startBlockLookup(async () => {
      try {
        const details = await getBlockDetailsCached(blockHash);
        if (latestBlockRequestRef.current !== blockHash) return;
        setBlockDetails(details);
      } catch (err: unknown) {
        if (latestBlockRequestRef.current !== blockHash) return;
        setBlockDetails(null);
        setBlockError(err instanceof Error ? err.message : "Failed to load block details.");
      } finally {
        if (latestBlockRequestRef.current === blockHash) setLoadingBlockHash(null);
      }
    });
  }

  function handleOpenChange(nextOpen: boolean) {
    onOpenChange(nextOpen);
    if (!nextOpen) clearSelection();
  }

  return (
    <Sheet open={open} onOpenChange={handleOpenChange}>
      {/*
       * Key points:
       *  - `!w-auto !max-w-none` — overrides Base UI's default `w-3/4 sm:max-w-sm`
       *    (sm:max-w-sm = 384 px which clips our 420 px feed panel).
       *  - NO `transition-[width]` here — Base UI animates the sheet via
       *    translate-x + opacity. Adding a width CSS transition on the same
       *    element causes jank. Width expansion is handled entirely by framer-motion
       *    on the inner detail panel instead.
       *  - `overflow-hidden` clips the detail panel while it animates in.
       *  - `flex-row` + fixed-width section ensures the feed column never shifts.
       */}
      <SheetContent
        side="right"
        showCloseButton={false}
        className="!w-auto !max-w-none flex flex-row overflow-hidden p-0 h-full"
      >
        {/* ── LEFT: Detail panel — slides in from right to left ── */}
        <AnimatePresence initial={false}>
          {hasDetail && (
            <motion.div
              key="detail"
              initial={{ width: 0, opacity: 0 }}
              animate={{ width: DETAIL_W, opacity: 1 }}
              exit={{ width: 0, opacity: 0 }}
              transition={{ duration: SLIDE_DURATION, ease: SLIDE_EASE }}
              className="shrink-0 overflow-hidden border-r border-border/60 h-full"
              style={{ minWidth: 0 }}
            >
              {/* Inner wrapper holds the full column width so content never word-wraps during animation */}
              <div style={{ width: DETAIL_W, minWidth: DETAIL_W }} className="h-full">
                <EventDetail
                  event={selectedEvent!}
                  blockDetails={blockDetails}
                  blockError={blockError}
                  loadingBlock={
                    loadingBlock ||
                    loadingBlockHash === getBlockHashFromEvent(selectedEvent!)
                  }
                  onClose={clearSelection}
                />
              </div>
            </motion.div>
          )}
        </AnimatePresence>

        {/* ── RIGHT: Feed panel — always present, never resizes ── */}
        <section
          className="flex shrink-0 flex-col bg-background h-full border-l border-border/60"
          style={{ width: FEED_W, minWidth: FEED_W }}
        >
          {/* ── Header ── */}
          <SheetHeader className="shrink-0 gap-0 border-b border-border/60 px-4 py-4">
            <div className="flex items-start justify-between gap-3">
              <div className="flex min-w-0 items-center gap-2">
                <SheetTitle className="text-sm font-semibold">Event Feed</SheetTitle>
                <span className="rounded-full bg-muted px-1.5 py-0.5 font-mono text-[10px] text-foreground">
                  {events.length}
                </span>
              </div>
              <button
                type="button"
                onClick={() => handleOpenChange(false)}
                aria-label="Close event feed"
                className="rounded-md p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                <IconX size={14} stroke={1.8} />
              </button>
            </div>

            <SheetDescription className="sr-only">
              Live Bitcoin event feed. Select a row to inspect event details.
            </SheetDescription>

            {/* Connection status row */}
            <div className="mt-4 flex items-center justify-between gap-3">
              <div className="flex min-w-0 items-center gap-2">
                <span className={cn("size-1.5 rounded-full", stateCfg.dot)} aria-hidden="true" />
                <p className="truncate text-sm font-medium text-foreground">Bitcoin mainnet</p>
              </div>
              <span
                className={cn(
                  "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-medium",
                  stateCfg.badge,
                )}
              >
                <span className={cn("size-1.5 rounded-full", stateCfg.dot)} aria-hidden="true" />
                {stateCfg.dot.includes("animate-pulse") ? "Connecting" : "Live"}
                {retryMeta}
              </span>
            </div>

            {/* Stats grid */}
            <div className="mt-4 grid grid-cols-3 gap-2">
              <StatTile label="Height" value={heightStat} />
              <StatTile label="Mempool" value={mempoolStat} />
              <StatTile label="Heartbeat" value={liveStat} />
            </div>
          </SheetHeader>

          {/* ── Error / re-registration alerts ── */}
          {(error || needsReregistration) && (
            <div className="flex shrink-0 flex-col gap-2 border-b border-border/60 px-4 py-3">
              {error && (
                <Alert variant="destructive">
                  <IconAlertTriangle size={13} stroke={2} />
                  <AlertDescription className="flex items-center justify-between gap-3">
                    <span>{error}</span>
                    {(connState === "error" || connState === "reconnecting") && (
                      <Button
                        variant="outline"
                        size="xs"
                        onClick={onRetryNow}
                        className="shrink-0 border-destructive/30 bg-background/60 text-destructive hover:bg-background"
                      >
                        <IconRefresh size={12} stroke={2} data-icon="inline-start" />
                        Retry
                      </Button>
                    )}
                  </AlertDescription>
                </Alert>
              )}
              {needsReregistration && (
                <Alert>
                  <IconRefreshAlert size={13} stroke={2} />
                  <AlertDescription>
                    Watch addresses expired. Re-register in the Addresses tab to resume live updates.
                  </AlertDescription>
                </Alert>
              )}
            </div>
          )}

          {/* ── Events sub-header ── */}
          <div className="flex shrink-0 items-center justify-between border-b border-border/60 px-4 py-3">
            <p className="text-[11px] font-medium uppercase tracking-[0.14em] text-muted-foreground/60">
              Events
            </p>
            {events.length > 0 && (
              <button
                type="button"
                onClick={() => { clearSelection(); onClearEvents(); }}
                className="text-[11px] text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none"
              >
                Clear
              </button>
            )}
          </div>

          {/* ── Event list ── */}
          <div className="min-h-0 flex-1 overflow-y-auto">
            {events.length === 0 ? (
              <div className="flex h-full flex-col items-center justify-center gap-2 px-6 text-center text-muted-foreground">
                <IconActivity
                  size={22}
                  stroke={1.25}
                  className="text-muted-foreground/40"
                  aria-hidden="true"
                />
                <p className="text-xs">
                  {connState === "error"
                    ? "The stream is unavailable right now."
                    : "Waiting for blockchain events…"}
                </p>
              </div>
            ) : (
              events.map((event) => (
                <EventRow
                  key={event.id}
                  event={event}
                  selected={event.id === selectedEventId}
                  loadingBlockHash={loadingBlockHash}
                  onSelect={handleSelectEvent}
                />
              ))
            )}
          </div>

          {/* ── Footer status bar ── */}
          <div className="flex shrink-0 items-center gap-2 border-t border-border/60 px-4 py-2.5">
            <span
              className={cn(
                "size-1.5 rounded-full",
                connState === "connected" ? "bg-green-500" : "bg-muted-foreground/40",
              )}
              aria-hidden="true"
            />
            <span className="text-[10.5px] text-muted-foreground">
              {connState === "connected"
                ? "Live feed active"
                : connState === "reconnecting"
                  ? "Reconnecting…"
                  : connState === "connecting"
                    ? "Connecting…"
                    : "Stream unavailable"}
            </span>
          </div>
        </section>
      </SheetContent>
    </Sheet>
  );
}
