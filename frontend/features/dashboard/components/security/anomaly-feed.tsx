import {
  IconAlertOctagon,
  IconAlertTriangle,
  IconInfoCircle,
  IconShieldCheck,
} from "@tabler/icons-react";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  CardDescription,
  CardAction,
} from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import {
  Empty,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
  EmptyDescription,
} from "@/components/ui/empty";
import { cn } from "@/lib/utils";
import type { Anomaly } from "@/lib/api/telemetry/prometheus";

const SEV_CFG = {
  critical: {
    label: "Critical",
    rowClass: "border-destructive/20 bg-destructive/5",
    leftBar: "bg-destructive",
    badgeClass: "bg-destructive/10 text-destructive border-transparent",
    iconClass: "text-destructive",
    Icon: IconAlertOctagon,
  },
  warning: {
    label: "Warning",
    rowClass: "border-amber-500/20 bg-amber-500/5",
    leftBar: "bg-amber-500",
    badgeClass: "bg-amber-500/10 text-amber-700 dark:text-amber-400 border-transparent",
    iconClass: "text-amber-600 dark:text-amber-400",
    Icon: IconAlertTriangle,
  },
  info: {
    label: "Info",
    rowClass: "border-border bg-muted/30",
    leftBar: "bg-primary/60",
    badgeClass: "bg-secondary text-secondary-foreground border-transparent",
    iconClass: "text-muted-foreground",
    Icon: IconInfoCircle,
  },
} as const;

interface AnomalyFeedProps {
  anomalies: Anomaly[];
}

export function AnomalyFeed({ anomalies }: AnomalyFeedProps) {
  const criticalCount = anomalies.filter((a) => a.severity === "critical").length;
  const warningCount = anomalies.filter((a) => a.severity === "warning").length;

  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>Security events</CardTitle>
          <CardDescription>
            Computed from live Prometheus metrics · refreshes every 15 s
          </CardDescription>
        </div>
        <CardAction>
          {anomalies.length > 0 && (
            <div className="flex items-center gap-1.5">
              {criticalCount > 0 && (
                <Badge className="bg-destructive/10 text-destructive border-transparent">
                  {criticalCount} critical
                </Badge>
              )}
              {warningCount > 0 && (
                <Badge className="bg-amber-500/10 text-amber-600 border-transparent dark:text-amber-400">
                  {warningCount} warning
                </Badge>
              )}
            </div>
          )}
        </CardAction>
      </CardHeader>

      <CardContent>
        {anomalies.length === 0 ? (
          <Empty className="border-dashed py-8">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <IconShieldCheck />
              </EmptyMedia>
              <EmptyTitle className="text-base">No anomalies detected</EmptyTitle>
              <EmptyDescription>
                All metrics are within normal thresholds.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <div className="flex flex-col gap-2">
            {anomalies.map((a) => {
              const cfg = SEV_CFG[a.severity];
              const { Icon } = cfg;
              return (
                <div
                  key={a.id}
                  className={cn(
                    // overflow-hidden on the parent clips the bar to the card's
                    // border-radius automatically — no need for rounded-l-xl on the bar
                    "relative flex items-start gap-3 overflow-hidden rounded-xl border px-4 py-3 pl-5",
                    cfg.rowClass,
                  )}
                >
                  {/* Severity accent bar — no border-radius needed, clipped by parent */}
                  <span
                    className={cn(
                      "absolute inset-y-0 left-0 w-1",
                      cfg.leftBar,
                    )}
                  />

                  <Icon
                    size={16}
                    stroke={2}
                    className={cn("mt-0.5 shrink-0", cfg.iconClass)}
                  />

                  <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="text-sm font-medium text-foreground">
                        {a.title}
                      </span>
                      <Badge className={cn("text-xs", cfg.badgeClass)}>
                        {cfg.label}
                      </Badge>
                    </div>
                    <p className="text-xs leading-relaxed text-muted-foreground">
                      {a.detail}
                    </p>
                  </div>

                  <time
                    dateTime={new Date(a.detectedAt).toISOString()}
                    className="shrink-0 whitespace-nowrap rounded-md bg-background/60 px-1.5 py-0.5 text-xs text-muted-foreground ring-1 ring-border/50"
                    suppressHydrationWarning
                  >
                    {new Date(a.detectedAt).toLocaleTimeString([], {
                      hour: "2-digit",
                      minute: "2-digit",
                    })}
                  </time>
                </div>
              );
            })}
          </div>
        )}
      </CardContent>
    </Card>
  );
}
