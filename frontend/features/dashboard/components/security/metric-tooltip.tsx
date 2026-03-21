import { IconInfoCircle } from "@tabler/icons-react"
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"
import { cn } from "@/lib/utils"

// ── Types ────────────────────────────────────────────────────────────────────

export interface MetricThreshold {
  color: "green" | "amber" | "red"
  label: string
}

export interface MetricTooltipProps {
  /** Prometheus metric name, e.g. "process_goroutines" */
  metricKey: string
  /** Plain-English explanation of what this metric measures */
  description: string
  /** Optional colour-coded thresholds so users know good vs bad */
  thresholds?: MetricThreshold[]
  /** Popover alignment relative to trigger (default: "start") */
  align?: "start" | "center" | "end"
  /** Popover side (default: "bottom") */
  side?: "top" | "bottom" | "left" | "right" | "inline-start" | "inline-end"
}

// ── Helpers ───────────────────────────────────────────────────────────────────

const DOT_COLOR: Record<MetricThreshold["color"], string> = {
  green: "bg-green-500",
  amber: "bg-amber-500",
  red:   "bg-destructive",
}

// ── Component ─────────────────────────────────────────────────────────────────

/**
 * MetricTooltip — a small ⓘ button that opens a Popover explaining a metric.
 *
 * Designed to sit inline next to a card title. Uses Popover (not Tooltip)
 * because the content is multi-line with threshold rows, which needs click-to-
 * open behaviour on mobile and proper keyboard accessibility.
 *
 * Usage:
 *   <MetricTooltip
 *     metricKey="process_goroutines"
 *     description="Current goroutine count. Sustained growth indicates a leak."
 *     thresholds={[
 *       { color: "green", label: "Good: < 200 at idle" },
 *       { color: "amber", label: "Warn: > 500 — monitor trend" },
 *       { color: "red",   label: "Critical: continuously growing" },
 *     ]}
 *   />
 */
export function MetricTooltip({
  metricKey,
  description,
  thresholds,
  align = "start",
  side  = "bottom",
}: MetricTooltipProps) {
  return (
    <Popover>
      <PopoverTrigger
        aria-label={`Info about ${metricKey}`}
        className={cn(
          "inline-flex size-3.5 shrink-0 cursor-pointer items-center justify-center",
          "rounded-full border border-border/60 bg-muted/50",
          "text-[9px] font-semibold text-muted-foreground",
          "transition-colors hover:border-border hover:bg-muted hover:text-foreground",
          "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        )}
      >
        i
      </PopoverTrigger>

      <PopoverContent
        side={side}
        align={align}
        sideOffset={6}
        className="w-60 gap-0 p-3"
      >
        <div className="flex flex-col gap-2">
          {/* Prometheus metric name */}
          <code className="font-mono text-[10px] text-muted-foreground">
            {metricKey}
          </code>

          {/* Human-readable explanation */}
          <p className="text-xs leading-relaxed text-foreground">
            {description}
          </p>

          {/* Threshold rows */}
          {thresholds && thresholds.length > 0 && (
            <div className="flex flex-col gap-1 border-t border-border/50 pt-2">
              {thresholds.map((t, i) => (
                <div key={i} className="flex items-start gap-2">
                  <span
                    className={cn(
                      "mt-1 size-1.5 shrink-0 rounded-full",
                      DOT_COLOR[t.color],
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
  )
}
