/**
 * event-cfg.ts
 *
 * Single source of truth for per-event-type visual tokens.
 * Pure TypeScript — no React imports, fully tree-shakeable.
 *
 * Used by:
 *   event-badge.tsx   (badge chip)
 *   event-row.tsx     (dot indicator + row highlight)
 *   event-detail.tsx  (header tint)
 *   feed-trigger.tsx  (button ring colour)
 */

import type { BtcEventType, ConnState } from "@/features/bitcoin/types";

// ── Per-event-type tokens ──────────────────────────────────────────────────────

export const EVENT_CFG: Record<
  BtcEventType,
  { label: string; dot: string; badge: string }
> = {
  ping: {
    label: "Ping",
    dot: "bg-muted-foreground/40",
    badge: "border-border/60 bg-muted/40 text-muted-foreground",
  },
  new_block: {
    label: "New block",
    dot: "bg-blue-500",
    badge:
      "border-blue-500/30 bg-blue-500/10 text-blue-700 dark:text-blue-300",
  },
  pending_mempool: {
    label: "Mempool",
    dot: "bg-amber-500",
    badge:
      "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300",
  },
  confirmed_tx: {
    label: "Confirmed",
    dot: "bg-green-500",
    badge:
      "border-green-500/30 bg-green-500/10 text-green-700 dark:text-green-300",
  },
  mempool_replaced: {
    label: "Replaced",
    dot: "bg-destructive",
    badge: "border-destructive/30 bg-destructive/10 text-destructive",
  },
  stream_requires_reregistration: {
    label: "Re-register",
    dot: "bg-amber-500 animate-pulse",
    badge:
      "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300",
  },
};

// ── Per-connection-state tokens ────────────────────────────────────────────────
// Mirrors STATE_CFG in event-stream.tsx so feed-trigger can be self-contained.

export const CONN_CFG: Record<
  ConnState,
  { label: string; dot: string; badge: string; ring: string; icon: string; ping: string }
> = {
  connecting: {
    label: "Connecting",
    dot: "bg-amber-500 animate-pulse",
    badge:
      "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-400",
    ring: "bg-amber-500/10 ring-amber-500/30 hover:bg-amber-500/15",
    icon: "text-amber-700 dark:text-amber-300",
    ping: "bg-amber-500",
  },
  connected: {
    label: "Live",
    dot: "bg-green-500",
    badge:
      "border-green-500/30 bg-green-500/10 text-green-700 dark:text-green-400",
    ring: "bg-green-500/10 ring-green-500/30 hover:bg-green-500/15",
    icon: "text-green-700 dark:text-green-300",
    ping: "bg-green-500",
  },
  reconnecting: {
    label: "Reconnecting",
    dot: "bg-amber-500 animate-pulse",
    badge:
      "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-400",
    ring: "bg-amber-500/10 ring-amber-500/30 hover:bg-amber-500/15",
    icon: "text-amber-700 dark:text-amber-300",
    ping: "bg-amber-500",
  },
  error: {
    label: "Error",
    dot: "bg-destructive",
    badge: "border-destructive/30 bg-destructive/10 text-destructive",
    ring: "bg-destructive/10 ring-destructive/30 hover:bg-destructive/15",
    icon: "text-destructive",
    ping: "bg-destructive/70",
  },
};

// ── Per-event-type trigger tokens ──────────────────────────────────────────────

export const EVENT_TRIGGER_CFG: Record<
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
