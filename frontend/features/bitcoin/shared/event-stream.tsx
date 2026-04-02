"use client";

import { useState } from "react";
import type { BtcEvent, ConnState } from "@/features/bitcoin/types";
import { FeedButton } from "../events/components/feed-button";
import { EventFeedSheet } from "../events/components/feed-sheet";
import { CONN_CFG } from "../events/lib/event-cfg";

// Re-export hook so dashboard only needs one import point.
export { useEventStreamController } from "@/features/bitcoin/events/hooks/use-event-stream";

// Re-export STATE_CFG (aliased from CONN_CFG) for any consumers that still use it.
export const STATE_CFG = CONN_CFG;

// ── EventStreamPanel ───────────────────────────────────────────────────────────

export interface EventStreamPanelProps {
  connState: ConnState;
  events: BtcEvent[];
  error: string | null;
  retryCount: number;
  lastHeartbeatAt: number | null;
  onRetryNow: () => void;
  onClearEvents: () => void;
  needsReregistration: boolean;
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

  // Track the latest relevant event ID the user has already seen.
  // Set to the current latest when the sheet closes so the button stops pulsing.
  const [lastViewedEventId, setLastViewedEventId] = useState<string | null>(null);

  // Latest non-ping event — used when snapshotting "viewed" on close.
  const latestRelevantEventId =
    events.find((e) => e.type !== "ping")?.id ?? null;

  function handleOpenChange(open: boolean) {
    setFeedOpen(open);
    // When closing the feed, mark the latest event as seen → stops pulse.
    if (!open) {
      setLastViewedEventId(latestRelevantEventId);
    }
  }

  function handleOpen() {
    setFeedOpen(true);
  }

  return (
    <>
      <FeedButton
        connState={connState}
        events={events}
        feedOpen={feedOpen}
        lastViewedEventId={lastViewedEventId}
        onClick={handleOpen}
      />

      <EventFeedSheet
        open={feedOpen}
        onOpenChange={handleOpenChange}
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
