import {
  IconCircleCheck,
  IconAlertTriangle,
  IconXboxX,
} from "@tabler/icons-react";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  CardDescription,
} from "@/components/ui/card";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import type {
  ServiceHealth,
  ServiceStatus,
} from "@/lib/api/telemetry/prometheus";

// ── Status config ─────────────────────────────────────────────────────────────

const STATUS_CFG: Record<
  ServiceStatus,
  {
    iconClass: string;
    pingClass?: string;
    rowClass: string;
    Icon: React.ElementType;
  }
> = {
  healthy: {
    iconClass: "text-green-600 dark:text-green-400",
    rowClass: "",
    Icon: IconCircleCheck,
  },
  degraded: {
    iconClass: "text-amber-600 dark:text-amber-400",
    pingClass: "bg-amber-400",
    rowClass: "border-l-2 border-l-amber-500/60 bg-amber-500/5 pl-3.5",
    Icon: IconAlertTriangle,
  },
  down: {
    iconClass: "text-destructive",
    pingClass: "bg-destructive/70",
    rowClass: "border-l-2 border-l-destructive/60 bg-destructive/5 pl-3.5",
    Icon: IconXboxX,
  },
};

// ── Component ─────────────────────────────────────────────────────────────────

interface ServiceGridProps {
  services: ServiceHealth[];
}

export function ServiceGrid({ services }: ServiceGridProps) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle>Service health</CardTitle>
        <CardDescription>Live status from Prometheus metrics</CardDescription>
      </CardHeader>

      <CardContent className="p-0 pb-1">
        <ul className="flex flex-col">
          {services.map((svc) => {
            const cfg = STATUS_CFG[svc.status];
            const { Icon } = cfg;

            return (
              <Tooltip key={svc.name}>
                <TooltipTrigger>
                  <li
                    className={cn(
                      // base padding — affected rows override pl via rowClass (border-l-2 + pl-[12px])
                      "flex cursor-default items-center gap-2.5 border-t border-border/50 px-4 py-2.5 first:border-t-0",
                      cfg.rowClass,
                    )}
                  >
                    {/* Status icon with optional pulse ring */}
                    <div className="relative flex size-4 shrink-0 items-center justify-center">
                      {cfg.pingClass && (
                        <span
                          className={cn(
                            "absolute inline-flex size-full animate-ping rounded-full opacity-40",
                            cfg.pingClass,
                          )}
                        />
                      )}
                      <Icon size={14} stroke={2} className={cfg.iconClass} />
                    </div>

                    {/* Service name — muted on healthy, prominent on down/degraded */}
                    <span
                      className={cn(
                        "truncate text-sm",
                        svc.status === "healthy"
                          ? "font-normal text-muted-foreground"
                          : "font-medium text-foreground",
                      )}
                    >
                      {svc.name}
                    </span>
                  </li>
                </TooltipTrigger>
                <TooltipContent side="right" align="center" sideOffset={8}>
                  {svc.detail}
                </TooltipContent>
              </Tooltip>
            );
          })}
        </ul>
      </CardContent>
    </Card>
  );
}
