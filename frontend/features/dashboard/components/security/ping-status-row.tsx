"use client";

/**
 * PingStatusRow — compact horizontal strip showing live service ping results.
 *
 * Renders below the HealthBanner. Each ping shows: name, animated status dot,
 * status badge, and latency (ms) for active HTTP probes.
 *
 * Uses "use client" because it accesses data passed from a client component.
 */

import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import type { ServicePingResult } from "@/lib/api/telemetry/prometheus";

const STATUS_DOT: Record<ServicePingResult["status"], string> = {
  up:      "bg-green-500",
  down:    "bg-destructive animate-pulse",
  unknown: "bg-amber-500",
};

const BADGE_VARIANT: Record<
  ServicePingResult["status"],
  "secondary" | "destructive" | "outline"
> = {
  up:      "secondary",
  down:    "destructive",
  unknown: "outline",
};

interface PingStatusRowProps {
  pingResults: ServicePingResult[];
}

export function PingStatusRow({ pingResults }: PingStatusRowProps) {
  if (pingResults.length === 0) return null;

  return (
    <div
      className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-xl border border-border/50 bg-muted/30 px-4 py-2.5"
      role="list"
      aria-label="Live service health pings"
    >
      {pingResults.map((p) => (
        <div
          key={p.name}
          role="listitem"
          className="flex items-center gap-2"
          title={p.detail}
        >
          {/* Animated status dot */}
          <span className={cn("size-2 shrink-0 rounded-full", STATUS_DOT[p.status])} />

          {/* Service name */}
          <span className="text-xs font-medium text-foreground">{p.name}</span>

          {/* Latency — only for active HTTP probes that have a latency */}
          {p.latencyMs !== null && (
            <span className="text-xs tabular-nums text-muted-foreground">
              {p.latencyMs}ms
            </span>
          )}

          {/* Status badge */}
          <Badge
            variant={BADGE_VARIANT[p.status]}
            className="h-4 px-1.5 text-[10px] font-medium leading-none"
          >
            {p.status}
          </Badge>
        </div>
      ))}
    </div>
  );
}
