"use client";

/**
 * event-row.tsx
 *
 * Single row in the event feed list.
 * Extracted from event-feed-sheet.tsx for composability.
 *
 * Layout: dot · badge · summary (truncated) · timestamp
 * A left-edge accent bar appears when the row is selected.
 */

import { cn } from "@/lib/utils";
import { EVENT_CFG } from "../lib/event-cfg";
import { EventTypeBadge } from "./event-badge";
import {
  formatFeedEventTime,
  getFeedEventSummary,
  getBlockHashFromEvent,
} from "@/features/bitcoin/events/lib/feed-formatters";
import type { BtcEvent } from "@/features/bitcoin/types";

interface EventRowProps {
  event: BtcEvent;
  selected: boolean;
  /** Hash currently being resolved for a block-detail fetch. */
  loadingBlockHash: string | null;
  onSelect: (event: BtcEvent) => void;
}

export function EventRow({
  event,
  selected,
  loadingBlockHash,
  onSelect,
}: EventRowProps) {
  const eventBlockHash = getBlockHashFromEvent(event);
  const isThisLoading =
    loadingBlockHash !== null && loadingBlockHash === eventBlockHash;

  return (
    <button
      type="button"
      onClick={() => onSelect(event)}
      aria-pressed={selected}
      className={cn(
        "relative grid w-full grid-cols-[auto_auto_minmax(0,1fr)_auto] items-center gap-3",
        "border-b border-border/60 px-4 py-3 text-left transition-colors",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset",
        selected ? "bg-muted/70" : "hover:bg-muted/35",
      )}
    >
      {selected && (
        <span
          aria-hidden="true"
          className="absolute inset-y-0 left-0 w-0.5 rounded-r-full bg-foreground"
        />
      )}

      {/* Type dot */}
      <span
        aria-hidden="true"
        className={cn(
          "size-1.5 shrink-0 rounded-full",
          EVENT_CFG[event.type].dot,
        )}
      />

      {/* Type badge chip */}
      <EventTypeBadge event={event} />

      {/* Summary — truncated to single line */}
      <p className="truncate font-mono text-[12px] text-foreground">
        {getFeedEventSummary(event)}
      </p>

      {/* Timestamp / loading indicator */}
      <span className="shrink-0 font-mono text-[10px] text-muted-foreground/70">
        {isThisLoading ? "Loading…" : formatFeedEventTime(event.receivedAt)}
      </span>
    </button>
  );
}
