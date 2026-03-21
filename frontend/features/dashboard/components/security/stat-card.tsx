import { cn } from "@/lib/utils";
import type React from "react";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import type { MetricTooltipProps } from "./metric-tooltip";

interface StatCardProps {
  title: string;
  value: string | number;
  sub?: React.ReactNode;
  icon: React.ElementType;
  status?: string;
  statusVariant?: "default" | "secondary" | "destructive" | "outline";
  iconColor?: string;
  fillPct?: number;
  tooltip?: Omit<MetricTooltipProps, "side"> & {
    side?: MetricTooltipProps["side"];
  };
}

function deriveState(
  iconColor: string,
): "critical" | "warning" | "ok" | "neutral" {
  if (iconColor.includes("destructive")) return "critical";
  if (iconColor.includes("amber")) return "warning";
  if (iconColor.includes("green")) return "ok";
  return "neutral";
}

const STATE = {
  critical: {
    border: "border-l-2 border-l-destructive/60",
    glow: "bg-destructive",
    iconBg: "bg-destructive/15",
    iconText: "text-destructive",
    iconRing: true,
    valueText: "text-destructive",
    dot: "bg-destructive",
    statusText: "text-destructive",
  },
  warning: {
    border: "border-l-2 border-l-amber-500/50",
    glow: "bg-amber-400",
    iconBg: "bg-amber-500/15",
    iconText: "text-amber-500",
    iconRing: false,
    valueText: "text-amber-500 dark:text-amber-400",
    dot: "bg-amber-500",
    statusText: "text-amber-600 dark:text-amber-400",
  },
  // ok and neutral both resolve to the same primary teal — one "not alerting" state
  ok: {
    border: "",
    glow: "",
    iconBg: "bg-primary/10",
    iconText: "text-primary",
    iconRing: false,
    valueText: "text-foreground",
    dot: "bg-primary",
    statusText: "text-primary",
  },
  neutral: {
    border: "",
    glow: "",
    iconBg: "bg-primary/10",
    iconText: "text-primary",
    iconRing: false,
    valueText: "text-foreground",
    dot: "bg-primary",
    statusText: "text-primary",
  },
} as const;

const THRESHOLD_DOT: Record<"green" | "amber" | "red", string> = {
  green: "bg-green-500",
  amber: "bg-amber-500",
  red: "bg-destructive",
};

export function StatCard({
  title,
  value,
  sub,
  icon: Icon,
  status,
  iconColor = "bg-primary/10 text-primary",
  fillPct,
  tooltip,
}: StatCardProps) {
  const state = deriveState(iconColor);
  const s = STATE[state];
  const isAlert = state === "critical" || state === "warning";

  const iconContainer = (
    <div
      className={cn(
        "relative flex size-8 shrink-0 items-center justify-center rounded-lg transition-colors",
        s.iconBg,
        tooltip && "cursor-pointer",
      )}
    >
      {s.iconRing && (
        <span className="absolute inset-0 animate-ping rounded-lg bg-destructive/40 opacity-40" />
      )}
      <Icon
        size={15}
        stroke={1.75}
        className={cn("relative z-10", s.iconText)}
      />
    </div>
  );

  return (
    <div
      className={cn(
        "group relative flex flex-col overflow-hidden rounded-2xl bg-card",
        "ring-1 ring-foreground/10 transition-all duration-200",
        // pt-3: label sits snug at top, not floating in space
        // px-4 pb-4: generous sides and bottom for the value to breathe
        "min-w-65 px-4 pb-4 pt-3",
        s.border,
        isAlert && "pl-3.5",
      )}
    >
      {/* Ambient glow — critical + warning only */}
      {isAlert && (
        <span
          className={cn(
            "pointer-events-none absolute -left-5 -top-5 size-24 rounded-full opacity-[0.12] blur-2xl",
            s.glow,
          )}
        />
      )}

      {/* Row 1: label · dot · icon — all on the same line */}
      <div className="mb-3 flex items-center gap-2">
        <span className="flex-1 truncate text-xs font-medium leading-none text-muted-foreground">
          {title}
        </span>

        {/* Status dot — sits right next to the icon, vertically aligned */}

        {tooltip ? (
          <Popover>
            <PopoverTrigger>{iconContainer}</PopoverTrigger>
            <PopoverContent
              side={tooltip.side ?? "bottom"}
              align={tooltip.align ?? "end"}
              sideOffset={6}
              className="w-60 gap-0 p-3"
            >
              <div className="flex flex-col gap-2">
                <code className="font-mono text-[10px] text-muted-foreground">
                  {tooltip.metricKey}
                </code>
                <p className="text-xs leading-relaxed text-foreground">
                  {tooltip.description}
                </p>
                {tooltip.thresholds && tooltip.thresholds.length > 0 && (
                  <div className="flex flex-col gap-1 border-t border-border/50 pt-2">
                    {tooltip.thresholds.map((t, i) => (
                      <div key={i} className="flex items-start gap-2">
                        <span
                          className={cn(
                            "mt-1 size-1.5 shrink-0 rounded-full",
                            THRESHOLD_DOT[t.color],
                          )}
                        />
                        <span className="text-[10px] leading-relaxed text-muted-foreground">
                          {t.label}
                        </span>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </PopoverContent>
          </Popover>
        ) : (
          iconContainer
        )}
      </div>

      {/* Row 2: value — mb-1.5 keeps sub-label close, reading as one unit */}
      <p
        className={cn(
          "mb-1.5 text-3xl font-bold leading-none tracking-tight",
          s.valueText,
        )}
      >
        {value}
      </p>

      {/* Row 3: sub-label left · status dot+text right */}
      <div className="flex items-center w-full gap-1.5">
        {sub &&
          (typeof sub === "string" ? (
            <span className="text-xs text-muted-foreground">{sub}</span>
          ) : (
            <div className="text-xs text-muted-foreground">{sub}</div>
          ))}
        {status && (
          <span
            className={cn(
              "size-2 shrink-0 rounded-full mr-3 ml-auto flex-end",
              s.dot,
            )}
            title={status}
          />
        )}
      </div>

      {/* Utilisation bar */}
      {fillPct !== undefined && (
        <div className="mt-2 h-0.5 w-full overflow-hidden rounded-full bg-muted">
          <div
            className={cn(
              "h-full rounded-full transition-all duration-700",
              fillPct >= 100
                ? "bg-destructive"
                : fillPct > 85
                  ? "bg-amber-500"
                  : "bg-green-500",
            )}
            style={{ width: `${Math.min(fillPct, 100)}%` }}
          />
        </div>
      )}
    </div>
  );
}
