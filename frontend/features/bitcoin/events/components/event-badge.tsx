"use client";

import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { EVENT_CFG } from "../lib/event-cfg";
import type { BtcEvent } from "@/features/bitcoin/types";

interface EventTypeBadgeProps {
  event: BtcEvent;
  className?: string;
}

export function EventTypeBadge({ event, className }: EventTypeBadgeProps) {
  const cfg = EVENT_CFG[event.type];
  return (
    <Badge
      className={cn(
        "inline-flex shrink-0 items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] font-medium",
        cfg.badge,
        className,
      )}
      variant="outline"
    >
      <span
        className={cn("size-1.5 shrink-0 rounded-full", cfg.dot)}
        aria-hidden="true"
      />
      {cfg.label}
    </Badge>
  );
}
