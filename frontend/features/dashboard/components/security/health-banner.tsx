"use client";

import { useEffect, useState } from "react";
import {
  IconCircleCheck,
  IconAlertTriangle,
  IconXboxX,
  IconRefresh,
  IconWifiOff,
  IconTestPipe,
} from "@tabler/icons-react";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import type { OverallStatus } from "@/lib/api/telemetry/prometheus";

const STATUS = {
  healthy: {
    label: "All systems operational",
    wrapperClass: "border-green-500/25 bg-gradient-to-r from-green-500/8 via-green-500/5 to-transparent",
    textClass: "text-green-700 dark:text-green-400",
    pingClass: "bg-green-400",
    iconBg: "bg-green-500/15",
    Icon: IconCircleCheck,
  },
  degraded: {
    label: "Degraded performance",
    wrapperClass: "border-amber-500/25 bg-gradient-to-r from-amber-500/8 via-amber-500/5 to-transparent",
    textClass: "text-amber-700 dark:text-amber-400",
    pingClass: "bg-amber-400",
    iconBg: "bg-amber-500/15",
    Icon: IconAlertTriangle,
  },
  critical: {
    label: "Critical — service impacted",
    wrapperClass: "border-destructive/25 bg-gradient-to-r from-destructive/8 via-destructive/5 to-transparent",
    textClass: "text-destructive",
    pingClass: "bg-destructive/70",
    iconBg: "bg-destructive/15",
    Icon: IconXboxX,
  },
} as const;

const POLL_INTERVAL_S = 15;

interface HealthBannerProps {
  overall: OverallStatus;
  fetchedAt: number;
  prometheusReachable: boolean;
  refreshing?: boolean;
  /** When "mock", shows a MOCK badge indicating Prometheus is bypassed. */
  dataSource?: "prometheus" | "mock";
}

export function HealthBanner({
  overall,
  fetchedAt,
  prometheusReachable,
  refreshing = false,
  dataSource = "prometheus",
}: HealthBannerProps) {
  const cfg = STATUS[overall];
  const { Icon } = cfg;

  const [secondsLeft, setSecondsLeft] = useState<number>(POLL_INTERVAL_S);

  useEffect(() => {
    const calc = () => {
      const elapsed = Math.round((Date.now() - fetchedAt) / 1000);
      return Math.max(0, POLL_INTERVAL_S - (elapsed % POLL_INTERVAL_S));
    };

    setSecondsLeft(calc());
    const id = setInterval(() => setSecondsLeft(calc()), 1000);
    return () => clearInterval(id);
  }, [fetchedAt]);

  const progress = (POLL_INTERVAL_S - secondsLeft) / POLL_INTERVAL_S;

  return (
    <div
      className={cn(
        "flex flex-wrap items-center justify-between gap-4 rounded-2xl border px-5 py-4 transition-colors duration-500",
        cfg.wrapperClass,
      )}
      role="status"
      aria-label={`System status: ${overall}`}
    >
      {/* Left: status */}
      <div className="flex items-center gap-3.5">
        <div className={cn("relative flex size-8 shrink-0 items-center justify-center rounded-lg", cfg.iconBg)}>
          {overall !== "healthy" && (
            <span className={cn("absolute inset-0 animate-ping rounded-lg opacity-40", cfg.pingClass)} />
          )}
          <Icon size={16} stroke={2} className={cfg.textClass} />
        </div>

        <div className="flex flex-col gap-0.5">
          <span className={cn("text-sm font-semibold leading-none", cfg.textClass)}>
            {cfg.label}
          </span>
          {!prometheusReachable && (
            <span className="flex items-center gap-1 text-xs text-muted-foreground">
              <IconWifiOff size={11} stroke={2} />
              Prometheus unreachable — check{" "}
              <code className="font-mono text-[11px]">PROMETHEUS_URL</code>
            </span>
          )}
          {dataSource === "mock" && (
            <span className="flex items-center gap-1.5 text-xs text-amber-600 dark:text-amber-400">
              <IconTestPipe size={11} stroke={2} />
              <Badge
                variant="outline"
                className="h-4 border-amber-500/50 bg-amber-500/10 px-1.5 text-[10px] font-semibold text-amber-600 dark:text-amber-400"
              >
                MOCK
              </Badge>
              Mock data — Prometheus bypassed
            </span>
          )}
        </div>
      </div>

      {/* Right: countdown pill */}
      <span className="flex items-center gap-2 rounded-full border border-border/60 bg-background/60 px-2.5 py-1 text-xs text-muted-foreground shadow-sm backdrop-blur-sm">
        <IconRefresh
          size={11}
          stroke={2}
          className={cn("shrink-0", refreshing && "animate-spin")}
        />

        {refreshing ? (
          <span>Refreshing…</span>
        ) : (
          <>
            <svg width="14" height="14" viewBox="0 0 14 14" className="-mx-0.5 shrink-0">
              <circle cx="7" cy="7" r="5" fill="none" strokeWidth="1.5" className="stroke-border" />
              <circle
                cx="7" cy="7" r="5"
                fill="none"
                strokeWidth="1.5"
                strokeLinecap="round"
                className="stroke-muted-foreground/60"
                strokeDasharray={`${2 * Math.PI * 5}`}
                strokeDashoffset={`${2 * Math.PI * 5 * (1 - progress)}`}
                transform="rotate(-90 7 7)"
                style={{ transition: "stroke-dashoffset 0.9s linear" }}
              />
            </svg>
            <span suppressHydrationWarning>{secondsLeft}s</span>
          </>
        )}
      </span>
    </div>
  );
}
