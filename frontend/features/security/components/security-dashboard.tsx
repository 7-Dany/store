"use client";

/**
 * SecurityDashboard — live system health dashboard.
 *
 * React / Next.js patterns used:
 *  - useEffectEvent (React 19.2) — stable poll callback that always reads the
 *    latest mockScenario without needing it as a dependency. Replaces the old
 *    useCallback + [mockScenario] dep pattern, so the interval is never torn
 *    down and recreated when the prop changes.
 *  - useTransition — marks the async poll as a non-urgent transition so the UI
 *    stays interactive during the fetch.
 *  - React Compiler (stable in Next 16) — memoisation is automatic; no manual
 *    useMemo / useCallback anywhere in this file.
 *  - Tabs with keepMounted — base-ui TabPanel renders hidden panels with
 *    display:none instead of unmounting, so Recharts state is preserved when
 *    switching tabs. Same semantics as React 19.2's <Activity mode="hidden">.
 */

import {
  useCallback,
  useEffect,
  useState,
  useTransition,
  useEffectEvent,
  useDeferredValue,
  Activity,
} from "react";
import dynamic from "next/dynamic";
import { motion } from "framer-motion";
import {
  IconShieldBolt,
  IconActivity,
  IconLogin,
  IconLock,
  IconDatabase,
  IconServer,
  IconBug,
  IconTimeDuration15,
  IconChartLine,
  IconAlertTriangle,
  IconCircuitBattery,
  IconPlugConnected,
  IconAnchor,
  IconArrowsShuffle,
  IconCoins,
  IconClock,
  IconBolt,
  IconReceipt,
  IconGauge,
  IconCloudDownload,
  IconSend,
  IconStack2,
  IconReload,
  IconBraces,
  IconRefreshAlert,
  IconWifi,
  IconCpu,
  IconChevronDown,
  IconRefresh,
  IconCircleCheck,
  IconWifiOff,
  IconTestPipe,
  IconXboxX,
  IconBell,
  IconChartBar,
} from "@tabler/icons-react";
import { Tabs, TabsContent } from "@/components/ui/tabs";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { ServiceGrid } from "./service-grid";
import { AnomalyFeed } from "./anomaly-feed";
import { StatCard } from "./stat-card";
import { cn } from "@/lib/utils";
import type {
  OverallStatus,
  SecuritySnapshot,
} from "@/lib/api/telemetry/prometheus";

// ── DashboardHeader ───────────────────────────────────────────────────────────

const POLL_INTERVAL_S = 15;

const STATUS_CFG = {
  healthy: {
    label: "All systems operational",
    iconClass: "text-green-600 dark:text-green-400",
    ringClass: "ring-green-500/30 bg-green-500/10",
    badgeClass:
      "border-green-500/30 bg-green-500/10 text-green-700 dark:text-green-400",
    ping: null,
    Icon: IconCircleCheck,
  },
  degraded: {
    label: "Degraded performance",
    iconClass: "text-amber-600 dark:text-amber-400",
    ringClass: "ring-amber-500/30 bg-amber-500/10",
    badgeClass:
      "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-400",
    ping: "bg-amber-400/50",
    Icon: IconAlertTriangle,
  },
  critical: {
    label: "Critical — service impacted",
    iconClass: "text-destructive",
    ringClass: "ring-destructive/30 bg-destructive/10",
    badgeClass: "border-destructive/30 bg-destructive/10 text-destructive",
    ping: "bg-destructive/50",
    Icon: IconXboxX,
  },
} as const;

interface DashboardHeaderProps {
  overall: OverallStatus;
  services: SecuritySnapshot["services"];
  fetchedAt: number;
  prometheusReachable: boolean;
  refreshing: boolean;
  dataSource: "prometheus" | "mock";
}

function DashboardHeader({
  overall,
  services,
  fetchedAt,
  prometheusReachable,
  refreshing,
  dataSource,
}: DashboardHeaderProps) {
  const cfg = STATUS_CFG[overall];

  const [secondsLeft, setSecondsLeft] = useState(POLL_INTERVAL_S);
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
  const affected = services.filter((s) => s.status !== "healthy");
  const downSvcs = affected.filter((s) => s.status === "down");
  const degraded = affected.filter((s) => s.status === "degraded");

  return (
    <div className="flex flex-col gap-2 sm:flex-row sm:flex-wrap sm:items-center sm:gap-x-3 sm:gap-y-2">
      {/* Row 1 — icon · title · i button */}
      <div className="flex min-w-0 items-center gap-3">
        <div
          className={cn(
            "relative flex size-10 shrink-0 items-center justify-center rounded-xl ring-1",
            cfg.ringClass,
          )}
          aria-label={`System status: ${overall}`}
        >
          {cfg.ping && (
            <span
              className={cn(
                "absolute inset-0 animate-ping rounded-xl opacity-40",
                cfg.ping,
              )}
            />
          )}
          <IconShieldBolt size={19} stroke={1.75} className={cfg.iconClass} />
        </div>

        <div className="min-w-0 flex-1">
          <h1 className="text-xl font-semibold tracking-tight text-foreground leading-none">
            System Health
          </h1>
          <p className="mt-0.5 text-xs text-muted-foreground">
            Live metrics from Prometheus
          </p>
        </div>

        <Popover>
          <PopoverTrigger
            aria-label="Show affected services"
            className={cn(
              "inline-flex size-4 shrink-0 cursor-pointer items-center justify-center",
              "rounded-full border border-border/60 bg-muted/50",
              "text-[9px] font-semibold text-muted-foreground",
              "transition-colors hover:border-border hover:bg-muted hover:text-foreground",
              "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
            )}
          >
            i
          </PopoverTrigger>
          <PopoverContent
            side="bottom"
            align="start"
            sideOffset={8}
            className="w-64 gap-0 p-3"
          >
            <div className="flex flex-col gap-3">
              <div className="flex items-center gap-2">
                <cfg.Icon size={13} stroke={2} className={cfg.iconClass} />
                <span className={cn("text-xs font-medium", cfg.iconClass)}>
                  {cfg.label}
                </span>
              </div>
              {affected.length > 0 ? (
                <div className="flex flex-col gap-1 border-t border-border/50 pt-2">
                  <p className="mb-1 text-[10px] font-semibold uppercase tracking-widest text-muted-foreground">
                    Affected services
                  </p>
                  {downSvcs.map((s) => (
                    <div key={s.name} className="flex items-start gap-2">
                      <span className="mt-1 size-1.5 shrink-0 rounded-full bg-destructive" />
                      <div className="min-w-0">
                        <span className="text-xs font-medium text-foreground">
                          {s.name}
                        </span>
                        <p className="text-[10px] leading-snug text-muted-foreground">
                          {s.detail}
                        </p>
                      </div>
                    </div>
                  ))}
                  {degraded.map((s) => (
                    <div key={s.name} className="flex items-start gap-2">
                      <span className="mt-1 size-1.5 shrink-0 rounded-full bg-amber-500" />
                      <div className="min-w-0">
                        <span className="text-xs font-medium text-foreground">
                          {s.name}
                        </span>
                        <p className="text-[10px] leading-snug text-muted-foreground">
                          {s.detail}
                        </p>
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <p className="border-t border-border/50 pt-2 text-xs text-muted-foreground">
                  All services are healthy.
                </p>
              )}
              {(!prometheusReachable || dataSource === "mock") && (
                <div className="flex flex-col gap-1 border-t border-border/50 pt-2">
                  {!prometheusReachable && (
                    <div className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
                      <IconWifiOff size={11} stroke={2} className="shrink-0" />
                      Prometheus unreachable —{" "}
                      <code className="font-mono">PROMETHEUS_URL</code>
                    </div>
                  )}
                  {dataSource === "mock" && (
                    <div className="flex items-center gap-1.5 text-[10px] text-amber-600 dark:text-amber-400">
                      <IconTestPipe size={11} stroke={2} className="shrink-0" />
                      Mock data — Prometheus bypassed
                    </div>
                  )}
                </div>
              )}
            </div>
          </PopoverContent>
        </Popover>
      </div>
      {/* end row 1 */}

      <div className="hidden sm:block sm:flex-1" />

      {/* Row 2 — status badge, right-aligned on all screens */}
      <div className="flex justify-end sm:contents">
        {refreshing ? (
          <span className="flex shrink-0 items-center gap-1.5 rounded-full border border-border/60 bg-muted/40 px-3 py-1 text-xs text-muted-foreground">
            <IconRefresh
              size={11}
              stroke={2}
              className="shrink-0 animate-spin"
            />
            Refreshing…
          </span>
        ) : (
          <span
            className={cn(
              "flex shrink-0 items-center gap-2 rounded-full border px-3 py-1 text-xs font-medium",
              cfg.badgeClass,
            )}
          >
            <span
              className={cn(
                "size-1.5 shrink-0 rounded-full",
                overall === "healthy" && "bg-green-500",
                overall === "degraded" && "bg-amber-500 animate-pulse",
                overall === "critical" && "bg-destructive animate-pulse",
              )}
            />
            {cfg.label}
            <span className="h-3 w-px shrink-0 bg-current opacity-25" />
            <svg
              width="12"
              height="12"
              viewBox="0 0 14 14"
              className="-mx-0.5 shrink-0"
              aria-hidden
            >
              <circle
                cx="7"
                cy="7"
                r="5"
                fill="none"
                strokeWidth="1.5"
                className="stroke-current opacity-20"
              />
              <circle
                cx="7"
                cy="7"
                r="5"
                fill="none"
                strokeWidth="1.5"
                strokeLinecap="round"
                className="stroke-current opacity-70"
                strokeDasharray={`${2 * Math.PI * 5}`}
                strokeDashoffset={`${2 * Math.PI * 5 * (1 - progress)}`}
                transform="rotate(-90 7 7)"
                style={{ transition: "stroke-dashoffset 0.9s linear" }}
              />
            </svg>
            <span suppressHydrationWarning>{secondsLeft}s</span>
          </span>
        )}
      </div>
      {/* end row 2 */}
    </div>
  );
}

// ── Dynamic chart imports ─────────────────────────────────────────────────────

const RequestErrorChart = dynamic(
  () => import("./request-error-chart").then((m) => m.RequestErrorChart),
  { ssr: false, loading: () => <ChartSkeleton h={200} /> },
);
const MetricAreaChart = dynamic(
  () => import("./metric-area-chart").then((m) => m.MetricAreaChart),
  { ssr: false, loading: () => <ChartSkeleton h={200} /> },
);
const ErrorBarChart = dynamic(
  () => import("./error-bar-chart").then((m) => m.ErrorBarChart),
  { ssr: false, loading: () => <ChartSkeleton h={200} /> },
);
const InvoiceStateChart = dynamic(
  () => import("./invoice-state-chart").then((m) => m.InvoiceStateChart),
  { ssr: false, loading: () => <ChartSkeleton h={160} /> },
);

function ChartSkeleton({ h }: { h: number }) {
  return (
    <div className="animate-pulse rounded-xl bg-muted" style={{ height: h }} />
  );
}

// ── Polling ───────────────────────────────────────────────────────────────────

const POLL_MS = 15_000;

// ── Section visibility ────────────────────────────────────────────────────────

const TAB_SECTIONS = {
  infra: [
    { id: "keyMetrics", label: "Key metrics", icon: IconActivity },
    { id: "serverInternals", label: "Server internals", icon: IconServer },
    { id: "httpTraffic", label: "HTTP traffic", icon: IconChartBar },
  ],
  auth: [
    { id: "threatSignals", label: "Threat signals", icon: IconLock },
    { id: "userActivity", label: "User activity", icon: IconBraces },
    { id: "loginTrend", label: "Login failure trend", icon: IconChartBar },
  ],
  bitcoin: [
    { id: "connections", label: "Connections", icon: IconPlugConnected },
    { id: "financialIntegrity", label: "Financial integrity", icon: IconCoins },
    { id: "invoices", label: "Invoices", icon: IconReceipt },
    { id: "operations", label: "Operations", icon: IconGauge },
    {
      id: "processorInternals",
      label: "Processor internals",
      icon: IconServer,
    },
  ],
  jobs: [
    { id: "health", label: "Health", icon: IconBug },
    { id: "throughput", label: "Throughput", icon: IconGauge },
  ],
} as const;

type TabId = keyof typeof TAB_SECTIONS;
type HiddenMap = Record<TabId, Set<string>>;

const SECTION_STORAGE_KEY = "security-dashboard-hidden-sections";
const DEFAULT_HIDDEN: Record<TabId, string[]> = {
  infra: ["serverInternals"],
  auth: ["userActivity"],
  bitcoin: ["operations", "processorInternals"],
  jobs: ["throughput"],
};

function loadHiddenSections(): HiddenMap {
  try {
    const raw = localStorage.getItem(SECTION_STORAGE_KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as Record<string, string[]>;
      return Object.fromEntries(
        (Object.keys(TAB_SECTIONS) as TabId[]).map((k) => [
          k,
          new Set<string>(parsed[k] ?? DEFAULT_HIDDEN[k] ?? []),
        ]),
      ) as HiddenMap;
    }
  } catch {}
  return Object.fromEntries(
    (Object.keys(TAB_SECTIONS) as TabId[]).map((k) => [
      k,
      new Set<string>(DEFAULT_HIDDEN[k]),
    ]),
  ) as HiddenMap;
}

function saveHiddenSections(hidden: HiddenMap) {
  try {
    const serializable = Object.fromEntries(
      Object.entries(hidden).map(([k, v]) => [k, [...v]]),
    );
    localStorage.setItem(SECTION_STORAGE_KEY, JSON.stringify(serializable));
  } catch {}
}

async function fetchSnapshot(
  mock?: string,
): Promise<{ data: SecuritySnapshot; source: "prometheus" | "mock" } | null> {
  try {
    const url = mock
      ? `/api/telemetry?mock=${encodeURIComponent(mock)}`
      : "/api/telemetry";
    const res = await fetch(url, { cache: "no-store" });
    if (!res.ok) return null;
    const source =
      res.headers.get("X-Data-Source") === "mock" ? "mock" : "prometheus";
    return { data: (await res.json()) as SecuritySnapshot, source };
  } catch {
    return null;
  }
}

// ── Fee / latency sub-displays ────────────────────────────────────────────────

function FeeRows({ f1, f6 }: { f1: number | null; f6: number | null }) {
  const fmt = (v: number | null) =>
    v === null ? "—" : `${v.toFixed(1)} sat/vB`;
  return (
    <div className="flex flex-col gap-px text-xs text-muted-foreground">
      <span>
        <span className="text-foreground/60">1-blk</span> {fmt(f1)}
      </span>
      <span>
        <span className="text-foreground/60">6-blk</span> {fmt(f6)}
      </span>
    </div>
  );
}

function LatencyRows({ p50, p95 }: { p50: number | null; p95: number | null }) {
  const fmt = (v: number | null) => (v === null ? "—" : `${v.toFixed(1)}s`);
  return (
    <div className="flex flex-col gap-px text-xs text-muted-foreground">
      <span>
        <span className="text-foreground/60">P50</span> {fmt(p50)}
      </span>
      <span>
        <span className="text-foreground/60">P95</span> {fmt(p95)}
      </span>
    </div>
  );
}

// ── Tab status derivation ─────────────────────────────────────────────────────

type TabStatus = "healthy" | "warning" | "critical";

function getTabStatuses(
  data: SecuritySnapshot,
): Record<"infra" | "auth" | "bitcoin" | "jobs", TabStatus> {
  let infra: TabStatus = "healthy";
  if (
    data.errorRatePct > 1 ||
    data.infraPollerAgeSeconds > 60 ||
    data.dbUp === 0
  ) {
    infra = "critical";
  } else if (
    data.errorRatePct > 0.1 ||
    data.dbPoolIdlePct < 15 ||
    data.goroutines > 500 ||
    data.infraPollerAgeSeconds > 20
  ) {
    infra = "warning";
  }

  let auth: TabStatus = "healthy";
  if (
    data.loginFailuresPerMin > 10 ||
    data.accountLocksLastHour > 20 ||
    data.dbPoolUtilPct >= 100
  ) {
    auth = "critical";
  } else if (
    data.loginFailuresPerMin > 3 ||
    data.accountLocksLastHour > 5 ||
    data.tokenValidationFailuresPerMin > 0.5 ||
    data.dbPoolUtilPct > 85 ||
    data.sessionRevocationsLastHour > 10
  ) {
    auth = "warning";
  }

  let bitcoin: TabStatus = "healthy";
  const driftCrit =
    data.balanceDriftSatoshis !== null && data.balanceDriftSatoshis !== 0;
  const holdCrit = data.reconciliationHoldActive;
  const reorgCrit = data.reorgDetectedLastDay > 0;
  const payoutCrit = data.payoutFailuresLastHour > 0;
  if (
    data.rpcConnected === 0 ||
    data.zmqConnected === 0 ||
    driftCrit ||
    holdCrit ||
    reorgCrit ||
    payoutCrit
  ) {
    bitcoin = "critical";
  } else if (
    (data.reconciliationLagBlocks ?? 0) > 6 ||
    (data.rateFeedStalenessSec ?? 0) > 300 ||
    (data.walletBackupAgeSec ?? 0) > 86_400 ||
    data.zmqHandlerPanicsLastHour > 0 ||
    data.zmqHandlerTimeoutsLastHour > 0 ||
    data.zmqDroppedLastHour > 0
  ) {
    bitcoin = "warning";
  }

  let jobs: TabStatus = "healthy";
  if (data.deadJobsTotal > 0) {
    jobs = "critical";
  } else if (
    data.jobsFailedLastHour > 0 ||
    data.jobsRequeuedLastHour > 0 ||
    (data.jobDurationP95Sec ?? 0) > 120
  ) {
    jobs = "warning";
  }

  return { infra, auth, bitcoin, jobs };
}

// ── Tab status dot ────────────────────────────────────────────────────────────

const STATUS_DOT: Record<TabStatus, string> = {
  healthy: "bg-green-500",
  warning: "bg-amber-500",
  critical: "bg-destructive",
};

const STATUS_DOT_GLOW: Record<TabStatus, string> = {
  healthy: "",
  warning: "shadow-[0_0_4px_1px_theme(colors.amber.500/0.6)]",
  critical: "shadow-[0_0_4px_1px_theme(colors.red.500/0.7)] animate-pulse",
};

function TabDot({ status }: { status: TabStatus }) {
  return (
    <span
      className={cn(
        "size-1.5 shrink-0 rounded-full",
        STATUS_DOT[status],
        STATUS_DOT_GLOW[status],
      )}
    />
  );
}

// ── Zero-tolerance pulse wrapper ──────────────────────────────────────────────

function ZTPulse({
  active,
  children,
}: {
  active: boolean;
  children: React.ReactNode;
}) {
  return (
    <motion.div
      className="rounded-xl"
      animate={
        active
          ? {
              boxShadow: [
                "0 0 0 0px hsl(var(--destructive)/0)",
                "0 0 0 3px hsl(var(--destructive)/.4)",
                "0 0 0 0px hsl(var(--destructive)/0)",
              ],
            }
          : { boxShadow: "0 0 0 0px transparent" }
      }
      transition={
        active
          ? { duration: 1.8, repeat: Infinity, ease: "easeInOut" }
          : { duration: 0.2 }
      }
    >
      {children}
    </motion.div>
  );
}

// ── PageSection ──────────────────────────────────────────────────────────────
// Flat always-visible section used for page-level content outside tabs
// (Security events, Errors by component).

function PageSection({
  label,
  icon: Icon,
  alertColor,
  children,
}: {
  label: string;
  icon: React.ElementType;
  alertColor?: "amber" | "red";
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2 px-1 text-xs font-medium text-muted-foreground">
        <Icon size={12} stroke={1.75} />
        <span>{label}</span>
        {alertColor === "red" && (
          <span className="size-1.5 shrink-0 rounded-full bg-destructive" />
        )}
        {alertColor === "amber" && (
          <span className="size-1.5 shrink-0 rounded-full bg-amber-500" />
        )}
      </div>
      {children}
    </div>
  );
}

// ── SectionBlock ──────────────────────────────────────────────────────────────
// Flat (non-collapsible) section used inside tabs — visibility controlled by
// the per-tab popover instead of an inline toggle.

function SectionBlock({
  id,
  hidden,
  label,
  icon: Icon,
  alertColor,
  children,
}: {
  id: string;
  hidden: Set<string>;
  label: string;
  icon: React.ElementType;
  alertColor?: "amber" | "red";
  children: React.ReactNode;
}) {
  if (hidden.has(id)) return null;
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2 px-1 text-xs font-medium text-muted-foreground">
        <Icon size={12} stroke={1.75} />
        <span>{label}</span>
        {alertColor === "red" && (
          <span className="size-1.5 shrink-0 rounded-full bg-destructive" />
        )}
        {alertColor === "amber" && (
          <span className="size-1.5 shrink-0 rounded-full bg-amber-500" />
        )}
      </div>
      {children}
    </div>
  );
}

// ── SectionTogglePopover ──────────────────────────────────────────────────────
// Small caret button rendered inside each TabsTrigger. Opens a popover that
// lets the user toggle individual sections on/off.

function SectionTogglePopover({
  tabId,
  hidden,
  onToggle,
}: {
  tabId: TabId;
  hidden: Set<string>;
  onToggle: (tabId: TabId, sectionId: string) => void;
}) {
  const sections = TAB_SECTIONS[tabId];
  return (
    <Popover>
      <PopoverTrigger
        onClick={(e) => e.stopPropagation()}
        aria-label="Toggle sections"
        className={cn(
          "ml-1 inline-flex size-4.5 shrink-0 items-center justify-center",
          "rounded border border-border/40 bg-transparent",
          "text-muted-foreground/60 transition-all",
          "hover:border-border hover:bg-muted hover:text-foreground",
          "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
          "data-[state=open]:border-border data-[state=open]:bg-muted data-[state=open]:text-foreground",
        )}
      >
        <IconChevronDown size={10} stroke={2.5} />
      </PopoverTrigger>
      <PopoverContent
        side="bottom"
        align="start"
        sideOffset={6}
        className="w-52 p-1.5"
        onClick={(e) => e.stopPropagation()}
      >
        <p className="mb-1 px-2 pt-1 text-[10px] font-semibold uppercase tracking-widest text-muted-foreground">
          Sections
        </p>
        <div className="flex flex-col gap-0.5">
          {sections.map((s) => {
            const visible = !hidden.has(s.id);
            return (
              <button
                key={s.id}
                onClick={() => onToggle(tabId, s.id)}
                className={cn(
                  "flex w-full items-center justify-between rounded-md px-2 py-1.5 text-left",
                  "transition-colors hover:bg-muted",
                  "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                )}
              >
                <div className="flex items-center gap-2">
                  <s.icon
                    size={12}
                    stroke={1.75}
                    className="shrink-0 text-muted-foreground"
                  />
                  <span
                    className={cn(
                      "text-xs",
                      visible ? "text-foreground" : "text-muted-foreground",
                    )}
                  >
                    {s.label}
                  </span>
                </div>
                <span
                  className={cn(
                    "relative ml-3 inline-flex h-4 w-7 shrink-0 items-center rounded-full border transition-all duration-200",
                    visible
                      ? "border-primary/40 bg-primary/15"
                      : "border-border bg-muted",
                  )}
                >
                  <span
                    className={cn(
                      "absolute size-2.5 rounded-full transition-all duration-200",
                      visible
                        ? "left-3.25 bg-primary"
                        : "left-05 bg-muted-foreground/40",
                    )}
                  />
                </span>
              </button>
            );
          })}
        </div>
      </PopoverContent>
    </Popover>
  );
}

// ══════════════════════════════════════════════════════════════════════════════
// § TAB SECTIONS
// ══════════════════════════════════════════════════════════════════════════════

// ── Infrastructure ────────────────────────────────────────────────────────────

function InfraSection({
  data,
  hidden,
}: {
  data: SecuritySnapshot;
  hidden: Set<string>;
}) {
  const keyAlert =
    data.errorRatePct > 1 || data.infraPollerAgeSeconds > 60 || data.dbUp === 0
      ? ("red" as const)
      : data.errorRatePct > 0.1 || data.infraPollerAgeSeconds > 20
        ? ("amber" as const)
        : undefined;

  const internalsAlert =
    data.goroutines > 500 || data.dbPoolIdlePct < 15
      ? ("amber" as const)
      : undefined;

  return (
    <div className="flex flex-col gap-6 pt-4">
      <SectionBlock
        id="keyMetrics"
        hidden={hidden}
        label="Key metrics"
        icon={IconActivity}
        alertColor={keyAlert}
      >
        <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-3">
          <StatCard
            title="Requests per minute"
            value={data.requestsPerMin.toLocaleString()}
            sub="Average over last 5 min"
            icon={IconActivity}
            iconColor="bg-primary/10 text-primary"
            tooltip={{
              metricKey: "Traffic volume",
              description:
                "How many requests your server is handling per minute, averaged over the last 5 minutes.",
              thresholds: [
                {
                  color: "green",
                  label: "Good: steady, matches your usual traffic",
                },
                {
                  color: "amber",
                  label: "Unusual: sudden spike or unexpected drop",
                },
                {
                  color: "red",
                  label: "Critical: 0 — nothing is reaching the server",
                },
              ],
            }}
          />
          <StatCard
            title="Server error rate"
            value={`${data.errorRatePct}%`}
            sub="Last 5 minutes"
            icon={IconBug}
            iconColor={
              data.errorRatePct > 1
                ? "bg-destructive/10 text-destructive"
                : data.errorRatePct > 0.1
                  ? "bg-amber-500/10 text-amber-600"
                  : "bg-green-500/10 text-green-600"
            }
            status={
              data.errorRatePct > 1
                ? "Critical"
                : data.errorRatePct > 0.1
                  ? "Elevated"
                  : undefined
            }
            statusVariant={data.errorRatePct > 1 ? "destructive" : "outline"}
            tooltip={{
              metricKey: "Server error rate",
              description:
                "How often the server returns an error instead of a valid response. These are problems on the server side, not mistakes by users.",
              thresholds: [
                { color: "green", label: "Good: 0% — no errors" },
                {
                  color: "amber",
                  label: "Elevated: > 0.1% — worth investigating",
                },
                {
                  color: "red",
                  label: "Critical: > 1% — users are being affected",
                },
              ],
            }}
          />
          <StatCard
            title="Health checker"
            value={`${Math.round(data.infraPollerAgeSeconds)}s ago`}
            sub="Last ran"
            icon={IconRefreshAlert}
            iconColor={
              data.infraPollerAgeSeconds > 60
                ? "bg-destructive/10 text-destructive"
                : "bg-green-500/10 text-green-600"
            }
            status={data.infraPollerAgeSeconds > 60 ? "Stale" : undefined}
            statusVariant="destructive"
            tooltip={{
              metricKey: "Background health checker",
              description:
                "A background job that checks your database, cache, and Bitcoin connection every 15 seconds. If it stops running, the status shown here may be out of date.",
              thresholds: [
                { color: "green", label: "Good: ran less than 20s ago" },
                { color: "amber", label: "Slow: 20–60s — checker is lagging" },
                {
                  color: "red",
                  label: "Critical: over 60s — checker has stopped",
                },
              ],
            }}
          />
        </div>
      </SectionBlock>

      <SectionBlock
        id="serverInternals"
        hidden={hidden}
        label="Server internals"
        icon={IconServer}
        alertColor={internalsAlert}
      >
        <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-3">
          <StatCard
            title="Background tasks"
            value={data.goroutines.toLocaleString()}
            sub="Running now"
            icon={IconServer}
            iconColor={
              data.goroutines > 500
                ? "bg-amber-500/10 text-amber-600"
                : "bg-primary/10 text-primary"
            }
            status={data.goroutines > 500 ? "Leak?" : undefined}
            statusVariant="outline"
            tooltip={{
              metricKey: "Concurrent background tasks",
              description:
                "The number of simultaneous tasks the server is running internally. If this keeps growing without dropping back down, something may not be cleaning up after itself.",
              thresholds: [
                {
                  color: "green",
                  label: "Good: under 200 at rest, under 500 under load",
                },
                {
                  color: "amber",
                  label: "High: over 500 — keep an eye on the trend",
                },
                {
                  color: "red",
                  label: "Critical: keeps growing — possible resource leak",
                },
              ],
            }}
          />
          <StatCard
            title="Memory in use"
            value={`${data.processMemAllocMB} MB`}
            sub="Server process"
            icon={IconCpu}
            iconColor="bg-primary/10 text-primary"
            tooltip={{
              metricKey: "Memory in use",
              description:
                "How much memory the server is currently using. It's normal for this number to go up and down — what matters is whether it keeps climbing without ever coming back down.",
              thresholds: [
                {
                  color: "green",
                  label: "Good: stable or fluctuates normally",
                },
                {
                  color: "amber",
                  label:
                    "Warning: steadily rising — may indicate a memory leak",
                },
              ],
            }}
          />
          <StatCard
            title="Database capacity free"
            value={`${data.dbPoolIdlePct}%`}
            sub="Available connections"
            icon={IconDatabase}
            iconColor={
              data.dbPoolIdlePct < 15
                ? "bg-amber-500/10 text-amber-600"
                : "bg-green-500/10 text-green-600"
            }
            status={data.dbPoolIdlePct < 15 ? "Low" : undefined}
            statusVariant="outline"
            tooltip={{
              metricKey: "Database spare capacity",
              description:
                "How much free capacity your database has right now. When this gets low, new requests start waiting in line, which slows the whole app down.",
              thresholds: [
                { color: "green", label: "Good: over 30% free" },
                {
                  color: "amber",
                  label: "Tight: under 15% — slowdowns may start",
                },
                {
                  color: "red",
                  label: "Critical: 0% — database is full, requests will fail",
                },
              ],
            }}
          />
        </div>
      </SectionBlock>

      <SectionBlock
        id="httpTraffic"
        hidden={hidden}
        label="HTTP traffic"
        icon={IconChartBar}
      >
        <RequestErrorChart
          requestRateSeries={data.requestRateSeries}
          errorRateSeries={data.errorRateSeries}
        />
      </SectionBlock>
    </div>
  );
}

// ── Auth security ─────────────────────────────────────────────────────────────

function AuthSection({
  data,
  hidden,
}: {
  data: SecuritySnapshot;
  hidden: Set<string>;
}) {
  const threatAlert =
    data.loginFailuresPerMin > 10 ||
    data.accountLocksLastHour > 20 ||
    data.dbPoolUtilPct >= 100
      ? ("red" as const)
      : data.loginFailuresPerMin > 3 ||
          data.accountLocksLastHour > 5 ||
          data.tokenValidationFailuresPerMin > 0.5 ||
          data.dbPoolUtilPct > 85
        ? ("amber" as const)
        : undefined;

  const activityAlert =
    data.sessionRevocationsLastHour > 10 ? ("amber" as const) : undefined;

  return (
    <div className="flex flex-col gap-6 pt-4">
      <SectionBlock
        id="threatSignals"
        hidden={hidden}
        label="Threat signals"
        icon={IconLock}
        alertColor={threatAlert}
      >
        <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-3">
          <StatCard
            title="Failed logins"
            value={data.loginFailuresPerMin.toFixed(1)}
            sub="Per minute, last 5 min"
            icon={IconLogin}
            iconColor={
              data.loginFailuresPerMin > 10
                ? "bg-destructive/10 text-destructive"
                : data.loginFailuresPerMin > 3
                  ? "bg-amber-500/10 text-amber-600"
                  : "bg-green-500/10 text-green-600"
            }
            status={
              data.loginFailuresPerMin > 10
                ? "Spike"
                : data.loginFailuresPerMin > 3
                  ? "Elevated"
                  : undefined
            }
            statusVariant={
              data.loginFailuresPerMin > 10 ? "destructive" : "outline"
            }
            tooltip={{
              metricKey: "Failed login attempts",
              description:
                "How many times per minute someone enters the wrong password. A sudden spike usually means someone is trying to break into accounts.",
              thresholds: [
                { color: "green", label: "Good: under 3 per minute" },
                {
                  color: "amber",
                  label: "Elevated: 3–10 per minute — worth watching",
                },
                {
                  color: "red",
                  label: "Critical: over 10 per minute — likely an attack",
                },
              ],
            }}
          />
          <StatCard
            title="Locked accounts"
            value={data.accountLocksLastHour}
            sub="Auto-locked, last hour"
            icon={IconLock}
            iconColor={
              data.accountLocksLastHour > 20
                ? "bg-destructive/10 text-destructive"
                : data.accountLocksLastHour > 5
                  ? "bg-amber-500/10 text-amber-600"
                  : "bg-green-500/10 text-green-600"
            }
            status={
              data.accountLocksLastHour > 20
                ? "Attack?"
                : data.accountLocksLastHour > 5
                  ? "Elevated"
                  : undefined
            }
            statusVariant={
              data.accountLocksLastHour > 20 ? "destructive" : "outline"
            }
            tooltip={{
              metricKey: "Locked accounts",
              description:
                "Accounts that got automatically locked after too many failed login attempts. A high number may mean real users are being locked out by an attack.",
              thresholds: [
                { color: "green", label: "Good: 0–5 per hour" },
                {
                  color: "amber",
                  label: "Elevated: 5–20 per hour — investigate",
                },
                {
                  color: "red",
                  label: "Critical: over 20 per hour — likely targeted",
                },
              ],
            }}
          />
          <StatCard
            title="Invalid sessions"
            value={data.tokenValidationFailuresPerMin.toFixed(2)}
            sub="Rejected per minute"
            icon={IconTimeDuration15}
            iconColor={
              data.tokenValidationFailuresPerMin > 0.5
                ? "bg-amber-500/10 text-amber-600"
                : "bg-green-500/10 text-green-600"
            }
            status={
              data.tokenValidationFailuresPerMin > 0.5
                ? "Investigate"
                : undefined
            }
            statusVariant="outline"
            tooltip={{
              metricKey: "Invalid session tokens",
              description:
                "How often login sessions are being rejected as invalid — expired, tampered, or unrecognised. A spike could point to a security issue with how sessions are managed.",
              thresholds: [
                { color: "green", label: "Good: under 0.5 per minute" },
                {
                  color: "amber",
                  label: "Elevated: over 0.5 per minute — check why",
                },
              ],
            }}
          />
          <StatCard
            title="Login DB usage"
            value={`${data.dbPoolUtilPct}%`}
            sub="Capacity in use"
            icon={IconDatabase}
            iconColor={
              data.dbPoolUtilPct >= 100
                ? "bg-destructive/10 text-destructive"
                : data.dbPoolUtilPct > 85
                  ? "bg-amber-500/10 text-amber-600"
                  : "bg-green-500/10 text-green-600"
            }
            status={
              data.dbPoolUtilPct >= 100
                ? "Exhausted"
                : data.dbPoolUtilPct > 85
                  ? "Near limit"
                  : undefined
            }
            statusVariant={
              data.dbPoolUtilPct >= 100 ? "destructive" : "outline"
            }
            fillPct={data.dbPoolUtilPct}
            tooltip={{
              metricKey: "Login database usage",
              description:
                "How much of the login system's database capacity is currently in use. At 100%, new login or signup attempts will fail or be stuck waiting.",
              thresholds: [
                { color: "green", label: "Good: under 85%" },
                { color: "amber", label: "High: 85–99% — approaching limit" },
                { color: "red", label: "Critical: 100% — logins will fail" },
              ],
            }}
          />
        </div>
      </SectionBlock>

      <SectionBlock
        id="userActivity"
        hidden={hidden}
        label="User activity"
        icon={IconBraces}
        alertColor={activityAlert}
      >
        <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-3">
          <StatCard
            title="New signups"
            value={data.registrationsLastHour}
            sub="Last hour"
            icon={IconBraces}
            iconColor="bg-primary/10 text-primary"
            tooltip={{
              metricKey: "New signups",
              description:
                "The number of users who successfully created an account in the last hour.",
            }}
          />
          <StatCard
            title="Session renewals"
            value={data.tokenRefreshesPerMin.toFixed(1)}
            sub="Per minute"
            icon={IconReload}
            iconColor="bg-primary/10 text-primary"
            tooltip={{
              metricKey: "Session renewals",
              description:
                "How often user sessions are being automatically kept alive. A sudden drop means sessions are expiring and users may start getting unexpectedly logged out.",
            }}
          />
          <StatCard
            title="Force logouts"
            value={data.sessionRevocationsLastHour}
            sub="Last hour"
            icon={IconArrowsShuffle}
            iconColor={
              data.sessionRevocationsLastHour > 10
                ? "bg-amber-500/10 text-amber-600"
                : "bg-primary/10 text-primary"
            }
            status={
              data.sessionRevocationsLastHour > 10 ? "Elevated" : undefined
            }
            statusVariant="outline"
            tooltip={{
              metricKey: "Force logouts",
              description:
                "Times a user was forcibly signed out of all their devices. A spike may mean someone is responding to a compromised account.",
              thresholds: [
                { color: "green", label: "Good: low and infrequent" },
                {
                  color: "amber",
                  label: "Elevated: over 10 per hour — investigate",
                },
              ],
            }}
          />
        </div>
      </SectionBlock>

      <SectionBlock
        id="loginTrend"
        hidden={hidden}
        label="Login failure trend"
        icon={IconChartBar}
      >
        <MetricAreaChart
          title="Login failures"
          description="Failures per minute · last 30 min"
          data={data.loginFailureSeries}
          seriesLabel="failures/min"
          colorKey="loginFails"
          color="var(--chart-3)"
          unit="/min"
          yDomain={[0, "auto"]}
        />
      </SectionBlock>
    </div>
  );
}

// ── Bitcoin ───────────────────────────────────────────────────────────────────

function BitcoinSection({
  data,
  hidden,
}: {
  data: SecuritySnapshot;
  hidden: Set<string>;
}) {
  const driftCrit =
    data.balanceDriftSatoshis !== null && data.balanceDriftSatoshis !== 0;
  const holdCrit = data.reconciliationHoldActive;
  const reorgCrit = data.reorgDetectedLastDay > 0;
  const payoutCrit = data.payoutFailuresLastHour > 0;

  const connAlert =
    data.zmqConnected === 0 || data.rpcConnected === 0
      ? ("red" as const)
      : undefined;
  const integrityAlert =
    driftCrit || holdCrit || reorgCrit || payoutCrit
      ? ("red" as const)
      : undefined;
  const invoiceAlert =
    (data.invoiceDetectionP95Sec ?? 0) > 60 ||
    (data.rateFeedStalenessSec ?? 0) > 300
      ? ("amber" as const)
      : undefined;
  const opsAlert =
    (data.reconciliationLagBlocks ?? 0) > 6 ? ("amber" as const) : undefined;
  const procAlert =
    data.zmqHandlerPanicsLastHour > 0 ||
    data.zmqHandlerTimeoutsLastHour > 0 ||
    data.zmqDroppedLastHour > 0
      ? ("amber" as const)
      : undefined;

  return (
    <div className="flex flex-col gap-6 pt-4">
      <SectionBlock
        id="connections"
        hidden={hidden}
        label="Connections"
        icon={IconPlugConnected}
        alertColor={connAlert}
      >
        <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-3">
          <StatCard
            title="Payment detector"
            value={data.zmqConnected === 1 ? "Connected" : "Disconnected"}
            sub={
              data.zmqLastMessageAgeSec !== null
                ? `Last event ${Math.round(data.zmqLastMessageAgeSec)}s ago`
                : "Waiting for events"
            }
            icon={IconPlugConnected}
            iconColor={
              data.zmqConnected === 0
                ? "bg-destructive/10 text-destructive"
                : "bg-green-500/10 text-green-600"
            }
            status={data.zmqConnected === 0 ? "Down" : undefined}
            statusVariant="destructive"
            tooltip={{
              metricKey: "Payment detector",
              description:
                "Whether the system is actively listening for incoming Bitcoin transactions. If this goes down, the system won't notice new payments arriving.",
              thresholds: [
                { color: "green", label: "Good: connected, receiving events" },
                {
                  color: "red",
                  label: "Critical: disconnected — payments won't be detected",
                },
              ],
            }}
          />
          {data.rpcConnected !== null && (
            <StatCard
              title="Bitcoin connection"
              value={data.rpcConnected === 1 ? "Connected" : "Disconnected"}
              sub="Needed to send payments"
              icon={IconWifi}
              iconColor={
                data.rpcConnected === 0
                  ? "bg-destructive/10 text-destructive"
                  : "bg-green-500/10 text-green-600"
              }
              status={data.rpcConnected === 0 ? "Down" : undefined}
              statusVariant="destructive"
              tooltip={{
                metricKey: "Bitcoin node connection",
                description:
                  "Whether the system can communicate with the Bitcoin network. Needed to send transactions — withdrawals and payouts stop working if this goes down.",
                thresholds: [
                  { color: "green", label: "Good: connected" },
                  {
                    color: "red",
                    label: "Critical: disconnected — no outgoing payments",
                  },
                ],
              }}
            />
          )}
          {data.keypoolSize !== null && (() => {
            const noWallet = data.keypoolSize === -1;
            const exhausted = data.keypoolSize === 0;
            const critical = data.keypoolSize >= 0 && data.keypoolSize < 10;
            const low = data.keypoolSize >= 10 && data.keypoolSize < 100;
            return (
              <StatCard
                title="Address pool"
                value={
                  noWallet ? "No wallet" :
                  exhausted ? "Exhausted" :
                  data.keypoolSize.toLocaleString()
                }
                sub={
                  noWallet ? "Wallet not created yet" :
                  exhausted ? "Run keypoolrefill immediately" :
                  "Ready to assign to invoices"
                }
                icon={IconStack2}
                iconColor={
                  noWallet || exhausted || critical
                    ? "bg-destructive/10 text-destructive"
                    : low
                      ? "bg-amber-500/10 text-amber-600"
                      : "bg-green-500/10 text-green-600"
                }
                status={
                  noWallet ? "Setup needed" :
                  critical ? "CRITICAL" :
                  low ? "Refill needed" :
                  undefined
                }
                statusVariant={
                  noWallet || exhausted || critical ? "destructive" : "outline"
                }
                tooltip={{
                  metricKey: "Invoice address pool",
                  description:
                    noWallet
                      ? "No Bitcoin wallet has been created yet. Invoice creation requires a wallet. Run: bitcoin-cli createwallet \"store\""
                      : "How many Bitcoin payment addresses have been pre-generated and are ready to assign to new invoices. When this reaches zero, invoice creation stops working.",
                  thresholds: [
                    { color: "green", label: "Good: 1,000 or more" },
                    { color: "amber", label: "Low: under 100 — run keypoolrefill soon" },
                    { color: "red", label: "Critical: under 10 — new invoices will fail" },
                  ],
                }}
              />
            );
          })()}
        </div>
      </SectionBlock>

      <SectionBlock
        id="financialIntegrity"
        hidden={hidden}
        label="Financial integrity"
        icon={IconCoins}
        alertColor={integrityAlert}
      >
        <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-3">
          <ZTPulse active={driftCrit}>
            <StatCard
              title="Balance accuracy"
              value={
                data.balanceDriftSatoshis === null
                  ? "—"
                  : data.balanceDriftSatoshis === 0
                    ? "Matched ✓"
                    : `${data.balanceDriftSatoshis} sat off`
              }
              sub="Must match records exactly"
              icon={IconCoins}
              iconColor={
                driftCrit
                  ? "bg-destructive/10 text-destructive"
                  : "bg-green-500/10 text-green-600"
              }
              status={driftCrit ? "CRITICAL" : undefined}
              statusVariant="destructive"
              tooltip={{
                metricKey: "Balance accuracy",
                description:
                  "Checks whether the wallet balance matches what the system's internal records say it should be. Any discrepancy — even a small one — is a serious financial issue and needs immediate investigation.",
                thresholds: [
                  {
                    color: "green",
                    label: "Good: exactly 0 — everything matches",
                  },
                  {
                    color: "red",
                    label: "Any other value: CRITICAL — stop and investigate",
                  },
                ],
              }}
            />
          </ZTPulse>
          <ZTPulse active={holdCrit}>
            <StatCard
              title="Payments on hold"
              value={holdCrit ? "On hold" : "Flowing normally"}
              sub="Outgoing payments status"
              icon={IconAnchor}
              iconColor={
                holdCrit
                  ? "bg-destructive/10 text-destructive"
                  : "bg-green-500/10 text-green-600"
              }
              status={holdCrit ? "Sweeps paused" : undefined}
              statusVariant="destructive"
              tooltip={{
                metricKey: "Outgoing payments paused",
                description:
                  "Whether all outgoing payments are currently on hold. This activates automatically when a balance discrepancy is detected, to prevent further issues until it's resolved.",
                thresholds: [
                  {
                    color: "green",
                    label: "Good: inactive — payments flowing normally",
                  },
                  {
                    color: "red",
                    label: "Active: all outgoing payments are paused",
                  },
                ],
              }}
            />
          </ZTPulse>
          <ZTPulse active={reorgCrit}>
            <StatCard
              title="Blockchain rollbacks"
              value={data.reorgDetectedLastDay}
              sub="Last 24 hours"
              icon={IconRefreshAlert}
              iconColor={
                reorgCrit
                  ? "bg-destructive/10 text-destructive"
                  : "bg-green-500/10 text-green-600"
              }
              status={reorgCrit ? "Verify txns" : undefined}
              statusVariant="destructive"
              tooltip={{
                metricKey: "Blockchain rollbacks",
                description:
                  "Whether the Bitcoin blockchain has undone any recently confirmed transactions in the last 24 hours. This is rare but can mean some payments need to be re-verified.",
                thresholds: [
                  { color: "green", label: "Good: 0 — no rollbacks" },
                  {
                    color: "red",
                    label: "Any rollback: manually verify recent payments",
                  },
                ],
              }}
            />
          </ZTPulse>
          <ZTPulse active={payoutCrit}>
            <StatCard
              title="Failed withdrawals"
              value={data.payoutFailuresLastHour}
              sub="Last hour"
              icon={IconSend}
              iconColor={
                payoutCrit
                  ? "bg-destructive/10 text-destructive"
                  : "bg-green-500/10 text-green-600"
              }
              status={payoutCrit ? "CRITICAL" : undefined}
              statusVariant="destructive"
              tooltip={{
                metricKey: "Failed withdrawals",
                description:
                  "Outgoing payments that couldn't be sent to the Bitcoin network in the last hour. Any failure means a withdrawal didn't go through and needs attention.",
                thresholds: [
                  { color: "green", label: "Good: 0" },
                  {
                    color: "red",
                    label:
                      "Any failure: CRITICAL — check connection and wallet",
                  },
                ],
              }}
            />
          </ZTPulse>
        </div>
      </SectionBlock>

      <SectionBlock
        id="invoices"
        hidden={hidden}
        label="Invoices"
        icon={IconReceipt}
        alertColor={invoiceAlert}
      >
        <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-3">
          <StatCard
            title="Awaiting payment"
            value={data.invoiceStatePending}
            sub="Not yet confirmed"
            icon={IconClock}
            iconColor="bg-amber-500/10 text-amber-600"
            tooltip={{
              metricKey: "Payments waiting",
              description:
                "Payments that have been received but are still waiting for enough blockchain confirmations. This is normal — they usually settle within minutes.",
            }}
          />
          <StatCard
            title="Paid & settled"
            value={data.invoiceStateConfirmed}
            sub="Fully confirmed"
            icon={IconReceipt}
            iconColor="bg-green-500/10 text-green-600"
            tooltip={{
              metricKey: "Settled payments",
              description:
                "Payments that are fully confirmed on the blockchain and have been settled. Revenue has been recognised.",
            }}
          />
          <StatCard
            title="Expired unpaid"
            value={data.invoiceStateExpired}
            sub="Nobody paid in time"
            icon={IconTimeDuration15}
            iconColor={
              data.invoiceStateExpired > 0
                ? "bg-amber-500/10 text-amber-600"
                : "bg-primary/10 text-primary"
            }
            tooltip={{
              metricKey: "Timed-out payment requests",
              description:
                "Payment requests that nobody paid before the deadline expired. Some are normal. A spike may mean the payment window is too short.",
            }}
          />
          <StatCard
            title="Detection speed"
            value={
              data.invoiceDetectionP95Sec !== null
                ? `${data.invoiceDetectionP95Sec.toFixed(1)}s`
                : "—"
            }
            sub={
              <LatencyRows
                p50={data.invoiceDetectionP50Sec}
                p95={data.invoiceDetectionP95Sec}
              />
            }
            icon={IconChartLine}
            iconColor={
              (data.invoiceDetectionP95Sec ?? 0) > 60
                ? "bg-amber-500/10 text-amber-600"
                : "bg-green-500/10 text-green-600"
            }
            status={
              (data.invoiceDetectionP95Sec ?? 0) > 60 ? "Slow" : undefined
            }
            statusVariant="outline"
            tooltip={{
              metricKey: "Payment detection speed",
              description:
                "How quickly the system spots an incoming payment. Slower detection means users wait longer to see their payment acknowledged.",
              thresholds: [
                { color: "green", label: "Good: under 30 seconds" },
                {
                  color: "amber",
                  label: "Slow: over 60 seconds — users may be waiting",
                },
              ],
            }}
          />
          <StatCard
            title="Exchange rate age"
            value={
              data.rateFeedStalenessSec !== null
                ? `${Math.round(data.rateFeedStalenessSec)}s ago`
                : "—"
            }
            sub="Last price update"
            icon={IconCloudDownload}
            iconColor={
              (data.rateFeedStalenessSec ?? 0) > 300
                ? "bg-amber-500/10 text-amber-600"
                : "bg-green-500/10 text-green-600"
            }
            status={
              (data.rateFeedStalenessSec ?? 0) > 300 ? "Stale" : undefined
            }
            statusVariant="outline"
            tooltip={{
              metricKey: "Exchange rate freshness",
              description:
                "How old the last Bitcoin price update is. Stale rates mean payment amounts in local currency could be slightly off.",
              thresholds: [
                {
                  color: "green",
                  label: "Good: updated within the last 60 seconds",
                },
                {
                  color: "red",
                  label: "Critical: over 5 minutes old — pricing may be wrong",
                },
              ],
            }}
          />
          <StatCard
            title="Last wallet backup"
            value={
              data.walletBackupAgeSec !== null
                ? data.walletBackupAgeSec > 3600
                  ? `${(data.walletBackupAgeSec / 3600).toFixed(1)}h ago`
                  : `${Math.round(data.walletBackupAgeSec / 60)}m ago`
                : "—"
            }
            sub="Funds protected since"
            icon={IconCircuitBattery}
            iconColor={
              (data.walletBackupAgeSec ?? 0) > 86_400
                ? "bg-amber-500/10 text-amber-600"
                : "bg-green-500/10 text-green-600"
            }
            status={
              (data.walletBackupAgeSec ?? 0) > 86_400 ? "> 24h" : undefined
            }
            statusVariant="outline"
            tooltip={{
              metricKey: "Last wallet backup",
              description:
                "How long ago the wallet was last backed up. Without a recent backup, funds could be unrecoverable if something goes wrong with the server.",
              thresholds: [
                {
                  color: "green",
                  label: "Good: backed up within the last 6 hours",
                },
                {
                  color: "red",
                  label: "Critical: over 24 hours — recovery risk",
                },
              ],
            }}
          />
        </div>
        <div className="mt-3 w-full max-w-xs">
          <InvoiceStateChart
            pending={data.invoiceStatePending}
            confirmed={data.invoiceStateConfirmed}
            expired={data.invoiceStateExpired}
          />
        </div>
      </SectionBlock>

      <SectionBlock
        id="operations"
        hidden={hidden}
        label="Operations"
        icon={IconGauge}
        alertColor={opsAlert}
      >
        <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-3">
          <StatCard
            title="Live listeners"
            value={data.sseConnectionsActive}
            sub="Receiving payment updates"
            icon={IconActivity}
            iconColor="bg-primary/10 text-primary"
            tooltip={{
              metricKey: "Live payment listeners",
              description:
                "The number of users or services currently receiving live Bitcoin payment notifications from this server.",
            }}
          />
          <StatCard
            title="Network fee rate"
            value={
              data.feeEstimate1Block !== null
                ? `${data.feeEstimate1Block.toFixed(1)} sat/vB`
                : "—"
            }
            sub={
              <FeeRows
                f1={data.feeEstimate1Block}
                f6={data.feeEstimate6Block}
              />
            }
            icon={IconBolt}
            iconColor="bg-primary/10 text-primary"
            tooltip={{
              metricKey: "Network fee rate",
              description:
                "The current cost to send a Bitcoin transaction on the network. Used to calculate fees for outgoing payments.",
            }}
          />
          <StatCard
            title="Wallet fragments"
            value={
              data.utxoCount !== null ? data.utxoCount.toLocaleString() : "—"
            }
            sub="Coin pieces in wallet"
            icon={IconStack2}
            iconColor="bg-primary/10 text-primary"
            tooltip={{
              metricKey: "Wallet coin fragments",
              description:
                "The number of separate received amounts sitting in the wallet. A very high count can slow down sending transactions.",
              thresholds: [
                { color: "green", label: "Good: low and stable" },
                {
                  color: "amber",
                  label: "High: growing fast — may need consolidation",
                },
              ],
            }}
          />
          <StatCard
            title="Accounting delay"
            value={
              data.reconciliationLagBlocks !== null
                ? data.reconciliationLagBlocks === 0
                  ? "Up to date"
                  : `${data.reconciliationLagBlocks} blocks behind`
                : "—"
            }
            sub="Balance sync status"
            icon={IconGauge}
            iconColor={
              (data.reconciliationLagBlocks ?? 0) > 6
                ? "bg-amber-500/10 text-amber-600"
                : "bg-green-500/10 text-green-600"
            }
            status={
              (data.reconciliationLagBlocks ?? 0) > 6 ? "Behind" : undefined
            }
            statusVariant="outline"
            tooltip={{
              metricKey: "Accounting delay",
              description:
                "How far behind the system's balance accounting is compared to the live blockchain. Falling behind means account balances shown to users may be temporarily out of date.",
              thresholds: [
                { color: "green", label: "Good: 0–2 blocks behind" },
                {
                  color: "amber",
                  label: "Behind: over 6 blocks — investigate",
                },
              ],
            }}
          />
        </div>
      </SectionBlock>

      <SectionBlock
        id="processorInternals"
        hidden={hidden}
        label="Processor internals"
        icon={IconServer}
        alertColor={procAlert}
      >
        <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-3">
          <StatCard
            title="Processor crashes"
            value={data.zmqHandlerPanicsLastHour}
            sub="Self-recovered, last hour"
            icon={IconBug}
            iconColor={
              data.zmqHandlerPanicsLastHour > 0
                ? "bg-amber-500/10 text-amber-600"
                : "bg-green-500/10 text-green-600"
            }
            status={
              data.zmqHandlerPanicsLastHour > 0 ? "Check logs" : undefined
            }
            statusVariant="outline"
            tooltip={{
              metricKey: "Payment processor crashes",
              description:
                "Times the payment event processor crashed and recovered itself in the last hour. Each crash is logged — check the logs to find what caused it.",
              thresholds: [
                { color: "green", label: "Good: 0" },
                {
                  color: "amber",
                  label: "Any crash: check the logs immediately",
                },
              ],
            }}
          />
          <StatCard
            title="Processor timeouts"
            value={data.zmqHandlerTimeoutsLastHour}
            sub="Cancelled, last hour"
            icon={IconTimeDuration15}
            iconColor={
              data.zmqHandlerTimeoutsLastHour > 0
                ? "bg-amber-500/10 text-amber-600"
                : "bg-green-500/10 text-green-600"
            }
            status={
              data.zmqHandlerTimeoutsLastHour > 0 ? "Stuck process?" : undefined
            }
            statusVariant="outline"
            tooltip={{
              metricKey: "Payment processor timeouts",
              description:
                "Times a payment handler took too long and was cancelled. The underlying process may still be running in the background.",
              thresholds: [
                { color: "green", label: "Good: 0" },
                {
                  color: "amber",
                  label: "Any value: check if a process is stuck",
                },
              ],
            }}
          />
          <StatCard
            title="Active processors"
            value={data.zmqHandlerGoroutines}
            sub="Processing right now"
            icon={IconServer}
            iconColor="bg-primary/10 text-primary"
            tooltip={{
              metricKey: "Active payment processors",
              description:
                "How many payment events are being processed right now. A high steady value means events are piling up faster than they can be handled.",
            }}
          />
          <StatCard
            title="Missed events"
            value={data.zmqDroppedLastHour}
            sub="Lost payments, last hour"
            icon={IconAlertTriangle}
            iconColor={
              data.zmqDroppedLastHour > 0
                ? "bg-amber-500/10 text-amber-600"
                : "bg-green-500/10 text-green-600"
            }
            status={data.zmqDroppedLastHour > 0 ? "Investigate" : undefined}
            statusVariant="outline"
            tooltip={{
              metricKey: "Missed payment events",
              description:
                "Bitcoin transaction events that were lost before the system could process them. Any missed events could mean a payment went undetected.",
              thresholds: [
                { color: "green", label: "Good: 0" },
                { color: "amber", label: "Any value: investigate immediately" },
              ],
            }}
          />
        </div>
      </SectionBlock>
    </div>
  );
}

// ── Job queue ─────────────────────────────────────────────────────────────────

function JobsSection({
  data,
  hidden,
}: {
  data: SecuritySnapshot;
  hidden: Set<string>;
}) {
  const healthAlert =
    data.deadJobsTotal > 0
      ? ("red" as const)
      : data.jobsFailedLastHour > 0 || data.jobsRequeuedLastHour > 0
        ? ("amber" as const)
        : undefined;

  const throughputAlert =
    (data.jobDurationP95Sec ?? 0) > 120 ? ("amber" as const) : undefined;

  return (
    <div className="flex flex-col gap-6 pt-4">
      <SectionBlock
        id="health"
        hidden={hidden}
        label="Health"
        icon={IconBug}
        alertColor={healthAlert}
      >
        <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-3">
          <StatCard
            title="Failed jobs"
            value={data.jobsFailedLastHour}
            sub="Last hour"
            icon={IconBug}
            iconColor={
              data.jobsFailedLastHour > 0
                ? "bg-amber-500/10 text-amber-600"
                : "bg-green-500/10 text-green-600"
            }
            status={data.jobsFailedLastHour > 0 ? "Non-zero" : undefined}
            statusVariant="outline"
            tooltip={{
              metricKey: "Failed background jobs",
              description:
                "Background tasks that hit an error in the last hour. They're retried automatically, so occasional failures aren't always urgent — but persistent failures need attention.",
              thresholds: [
                { color: "green", label: "Good: 0" },
                {
                  color: "amber",
                  label: "Non-zero: check whether retries are succeeding",
                },
              ],
            }}
          />
          <StatCard
            title="Permanently failed"
            value={data.deadJobsTotal}
            sub="Gave up retrying"
            icon={IconAlertTriangle}
            iconColor={
              data.deadJobsTotal > 0
                ? "bg-destructive/10 text-destructive"
                : "bg-green-500/10 text-green-600"
            }
            status={data.deadJobsTotal > 0 ? "Review" : undefined}
            statusVariant={data.deadJobsTotal > 0 ? "destructive" : "secondary"}
            tooltip={{
              metricKey: "Permanently failed jobs",
              description:
                "Background tasks that failed too many times and gave up completely. These need manual attention — some work may be incomplete or data may need recovery.",
              thresholds: [
                { color: "green", label: "Good: exactly 0" },
                {
                  color: "red",
                  label: "Any value: needs immediate manual review",
                },
              ],
            }}
          />
          <StatCard
            title="Restarted jobs"
            value={data.jobsRequeuedLastHour}
            sub="Worker crashed, last hour"
            icon={IconReload}
            iconColor={
              data.jobsRequeuedLastHour > 0
                ? "bg-amber-500/10 text-amber-600"
                : "bg-green-500/10 text-green-600"
            }
            status={data.jobsRequeuedLastHour > 0 ? "Worker crash?" : undefined}
            statusVariant="outline"
            tooltip={{
              metricKey: "Restarted jobs",
              description:
                "Background tasks that were picked up by a worker but never completed, so the system put them back in the queue. Usually means a worker crashed or timed out mid-task.",
              thresholds: [
                { color: "green", label: "Good: 0" },
                {
                  color: "amber",
                  label: "Any value: a worker likely crashed — check the logs",
                },
              ],
            }}
          />
        </div>
      </SectionBlock>

      <SectionBlock
        id="throughput"
        hidden={hidden}
        label="Throughput"
        icon={IconGauge}
        alertColor={throughputAlert}
      >
        <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-3">
          <StatCard
            title="Jobs queued"
            value={data.jobsSubmittedLastHour}
            sub="Added last hour"
            icon={IconSend}
            iconColor="bg-primary/10 text-primary"
            tooltip={{
              metricKey: "Jobs queued",
              description:
                "Total background tasks added to the queue in the last hour. Shows how much work the system is generating across all job types.",
            }}
          />
          <StatCard
            title="Slowest job time"
            value={
              data.jobDurationP95Sec !== null
                ? `${data.jobDurationP95Sec.toFixed(1)}s`
                : "—"
            }
            sub="Slowest 5%, last hour"
            icon={IconGauge}
            iconColor={
              (data.jobDurationP95Sec ?? 0) > 120
                ? "bg-amber-500/10 text-amber-600"
                : "bg-primary/10 text-primary"
            }
            status={(data.jobDurationP95Sec ?? 0) > 120 ? "Slow" : undefined}
            statusVariant="outline"
            tooltip={{
              metricKey: "Slowest job time",
              description:
                "How long the slowest 5% of background jobs take to finish. Most jobs are faster — this highlights your outliers. Very slow jobs can hold up other work in the queue.",
              thresholds: [
                {
                  color: "green",
                  label: "Good: under 60 seconds for most job types",
                },
                {
                  color: "amber",
                  label:
                    "Slow: over 2 minutes — find which job type is causing it",
                },
              ],
            }}
          />
        </div>
      </SectionBlock>
    </div>
  );
}

// ══════════════════════════════════════════════════════════════════════════════
// § MAIN COMPONENT
// ══════════════════════════════════════════════════════════════════════════════

interface SecurityDashboardProps {
  initialData: SecuritySnapshot;
  mockScenario?: string;
}

export function SecurityDashboard({
  initialData,
  mockScenario,
}: SecurityDashboardProps) {
  const [data, setData] = useState(initialData);
  const [src, setSrc] = useState<"prometheus" | "mock">(
    mockScenario ? "mock" : "prometheus",
  );
  const [refreshing, startTransition] = useTransition();

  // useDeferredValue (React 19) — lets the browser stay responsive during
  // each 15 s re-render by scheduling card updates as background work.
  // When deferredData lags behind data we know a render is in flight,
  // so we dim the panel slightly to signal "updating" without freezing.
  const deferredData = useDeferredValue(data);
  const isStale = data !== deferredData;

  const onPoll = useEffectEvent(async () => {
    const r = await fetchSnapshot(mockScenario);
    if (r) {
      setData(r.data);
      setSrc(r.source);
    }
  });

  useEffect(() => {
    const id = setInterval(() => startTransition(onPoll), POLL_MS);
    return () => clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const [activeTab, setActiveTab] = useState("infra");
  const [hiddenSections, setHiddenSections] = useState<HiddenMap>(() => {
    if (typeof window === "undefined") {
      return Object.fromEntries(
        (Object.keys(TAB_SECTIONS) as TabId[]).map((k) => [
          k,
          new Set<string>(DEFAULT_HIDDEN[k]),
        ]),
      ) as HiddenMap;
    }
    return loadHiddenSections();
  });

  const toggleSection = useCallback((tabId: TabId, sectionId: string) => {
    setHiddenSections((prev) => {
      const next = { ...prev, [tabId]: new Set(prev[tabId]) };
      if (next[tabId].has(sectionId)) next[tabId].delete(sectionId);
      else next[tabId].add(sectionId);
      saveHiddenSections(next);
      return next;
    });
  }, []);

  const btc = deferredData.zmqConnected !== null;
  const tabStatus = getTabStatuses(deferredData);
  const anomalyAlert = deferredData.anomalies?.some(
    (a) => a.severity === "critical",
  )
    ? ("red" as const)
    : deferredData.anomalies?.some((a) => a.severity === "warning")
      ? ("amber" as const)
      : undefined;

  return (
    <div className="flex flex-col gap-6">
      <DashboardHeader
        overall={data.overall}
        services={data.services}
        fetchedAt={data.fetchedAt}
        prometheusReachable={data.prometheusReachable}
        refreshing={refreshing}
        dataSource={src}
      />

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-[1fr_280px] lg:items-start">
        {/* Subtle opacity pulse while deferred re-render is in flight */}
        <div
          className={cn(
            "flex flex-col gap-4 transition-opacity duration-500",
            isStale && "opacity-50",
          )}
        >
          <Tabs value={activeTab}>
            {/* Custom tab bar — caret sits beside each tab button, not inside it,
                 to avoid the <button> inside <button> hydration error. */}
            <div role="tablist" className="flex w-full border-b border-border">
              {(
                [
                  {
                    id: "infra",
                    label: "Infrastructure",
                    status: tabStatus.infra,
                  },
                  { id: "auth", label: "Auth", status: tabStatus.auth },
                  ...(btc
                    ? [
                        {
                          id: "bitcoin",
                          label: "Bitcoin",
                          status: tabStatus.bitcoin,
                        },
                      ]
                    : []),
                  { id: "jobs", label: "Job queue", status: tabStatus.jobs },
                ] as { id: TabId; label: string; status: TabStatus }[]
              ).map((tab) => (
                <div
                  key={tab.id}
                  className="group/tab relative flex items-center"
                >
                  <button
                    role="tab"
                    aria-selected={activeTab === tab.id}
                    onClick={() => setActiveTab(tab.id)}
                    className={cn(
                      "relative flex h-9 flex-1 items-center justify-center gap-1.5 px-2 text-sm font-medium whitespace-nowrap",
                      "text-foreground/60 transition-colors hover:text-foreground",
                      "after:absolute after:bottom-0 after:inset-x-0 after:h-0.5 after:rounded-t-sm",
                      "after:bg-primary after:opacity-0 after:transition-opacity",
                      "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset",
                      activeTab === tab.id &&
                        "text-foreground after:opacity-100",
                    )}
                  >
                    <TabDot status={tab.status} />
                    {tab.label}
                  </button>
                  <div className="opacity-0 transition-opacity group-hover/tab:opacity-100">
                    <SectionTogglePopover
                      tabId={tab.id}
                      hidden={hiddenSections[tab.id]}
                      onToggle={toggleSection}
                    />
                  </div>
                </div>
              ))}
            </div>

            {/* Activity (React 19.2) — keeps inactive panels mounted but
                 deprioritises their work in the React scheduler, equivalent
                 to the Tabs keepMounted + display:none pattern but with
                 proper scheduler awareness for background updates. */}
            <TabsContent value="infra">
              <Activity mode={activeTab === "infra" ? "visible" : "hidden"}>
                <InfraSection
                  data={deferredData}
                  hidden={hiddenSections.infra}
                />
              </Activity>
            </TabsContent>
            <TabsContent value="auth">
              <Activity mode={activeTab === "auth" ? "visible" : "hidden"}>
                <AuthSection data={deferredData} hidden={hiddenSections.auth} />
              </Activity>
            </TabsContent>
            {btc && (
              <TabsContent value="bitcoin">
                <Activity mode={activeTab === "bitcoin" ? "visible" : "hidden"}>
                  <BitcoinSection
                    data={deferredData}
                    hidden={hiddenSections.bitcoin}
                  />
                </Activity>
              </TabsContent>
            )}
            <TabsContent value="jobs">
              <Activity mode={activeTab === "jobs" ? "visible" : "hidden"}>
                <JobsSection data={deferredData} hidden={hiddenSections.jobs} />
              </Activity>
            </TabsContent>
          </Tabs>

          <PageSection
            label="Security events"
            icon={IconBell}
            alertColor={anomalyAlert}
          >
            <AnomalyFeed anomalies={deferredData.anomalies} />
          </PageSection>

          <PageSection label="Errors by component" icon={IconChartBar}>
            <ErrorBarChart data={deferredData.errorsByComponent} />
          </PageSection>
        </div>

        <aside className="lg:sticky lg:top-4">
          <ServiceGrid services={deferredData.services} />
        </aside>
      </div>
    </div>
  );
}
