"use client";

import { useEffect, useRef, useState } from "react"; // useRef kept for prevConnStateRef
import { motion, AnimatePresence } from "framer-motion";
import { IconRadar } from "@tabler/icons-react";
import { cn } from "@/lib/utils";
import type { BtcEvent, ConnState } from "@/features/bitcoin/types";
import { CONN_CFG, EVENT_TRIGGER_CFG } from "../lib/event-cfg";

export interface FeedButtonProps {
  connState: ConnState;
  events: BtcEvent[];
  feedOpen: boolean;
  /** ID of the most recent non-ping event the user has already seen (set on feed close). */
  lastViewedEventId: string | null;
  onClick: () => void;
}

export function FeedButton({
  connState,
  events,
  feedOpen,
  lastViewedEventId,
  onClick,
}: FeedButtonProps) {
  const latestRelevantEvent = events.find((e) => e.type !== "ping") ?? null;

  const prevConnStateRef = useRef<ConnState>(connState);
  const [showConnectedBurst, setShowConnectedBurst] = useState(false);



  // ── Detect connecting → connected and fire the burst ───────────────────────
  useEffect(() => {
    const prev = prevConnStateRef.current;
    prevConnStateRef.current = connState;

    if (
      connState === "connected" &&
      (prev === "connecting" || prev === "reconnecting")
    ) {
      setShowConnectedBurst(true);
      const t = setTimeout(() => setShowConnectedBurst(false), 900);
      return () => clearTimeout(t);
    }
  }, [connState]);

  const isConnecting = connState === "connecting" || connState === "reconnecting";

  const tone = latestRelevantEvent
    ? EVENT_TRIGGER_CFG[latestRelevantEvent.type]
    : {
        ring: CONN_CFG[connState].ring,
        icon: CONN_CFG[connState].icon,
        ping: CONN_CFG[connState].ping,
      };

  const hasUnviewed =
    latestRelevantEvent !== null &&
    latestRelevantEvent.id !== lastViewedEventId;
  const isPulsing = !feedOpen && hasUnviewed;

  const label = latestRelevantEvent
    ? `Event feed — latest: ${latestRelevantEvent.type}`
    : `Event feed — ${connState}`;

  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      title={label}
      className={cn(
        "relative flex size-8 shrink-0 items-center justify-center rounded-lg ring-1 shadow-sm",
        "transition-colors duration-500",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background",
        tone.ring,
      )}
    >


      {/* ── CONNECTED BURST: two expanding green rings (one-shot) ── */}
      <AnimatePresence>
        {showConnectedBurst && (
          <>
            <motion.span
              key="burst-outer"
              aria-hidden="true"
              className="pointer-events-none absolute inset-0 rounded-lg"
              initial={{ scale: 1,    opacity: 0.7 }}
              animate={{ scale: 3.2,  opacity: 0   }}
              exit={{}}
              transition={{ duration: 0.65, ease: [0.16, 1, 0.3, 1] }}
              style={{ background: "rgb(34 197 94 / 0.40)" }}
            />
            <motion.span
              key="burst-inner"
              aria-hidden="true"
              className="pointer-events-none absolute inset-0 rounded-lg"
              initial={{ scale: 1,    opacity: 0.5 }}
              animate={{ scale: 2.0,  opacity: 0   }}
              exit={{}}
              transition={{ duration: 0.45, ease: [0.16, 1, 0.3, 1], delay: 0.06 }}
              style={{ background: "rgb(34 197 94 / 0.30)" }}
            />
          </>
        )}
      </AnimatePresence>

      {/* ── PULSE: connecting or unviewed event ── */}
      <AnimatePresence mode="wait">
        {(isConnecting || isPulsing) && (
          <motion.span
            key={isConnecting ? "ripple" : (latestRelevantEvent?.id ?? "pulse")}
            aria-hidden="true"
            className="pointer-events-none absolute inset-0"
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.25 }}
          >
            <style>{`@keyframes ping-sm { 75%,100% { transform:scale(1.4); opacity:0 } }`}</style>
            <span
              className={cn(
                "absolute inset-0 rounded-lg",
                isConnecting ? "bg-amber-400/35" : cn(tone.ping, "opacity-40"),
              )}
              style={{ animation: "ping-sm 2s cubic-bezier(0,0,0.2,1) infinite" }}
            />
          </motion.span>
        )}
      </AnimatePresence>

      {/* ── ICON ── */}
      <div className="flex items-center justify-center">
        <IconRadar
          size={15}
          stroke={1.75}
          className={cn(tone.icon, "transition-colors duration-500")}
          aria-hidden="true"
        />
      </div>
    </button>
  );
}
