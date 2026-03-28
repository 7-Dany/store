"use client";

/**
 * TransactionLifecycleDashboard — three-tab workspace for transaction flow ops.
 *
 * Tabs:
 *  1. Live Events — automatic SSE ownership + real-time event feed
 *  2. Addresses   — register / manage watched Bitcoin addresses
 *  3. Tx Lookup   — single and batch on-chain transaction status
 *
 * React 19.2 patterns:
 *  - Activity           — keeps inactive tab panels mounted but hidden; the
 *                         React scheduler deprioritises their updates, so the
 *                         SSE feed (Tab 1) never tears down when the user
 *                         switches to Tab 2 or 3.
 *  - useEffectEvent     — stable cross-tab callback for re-registration alerts
 *  - No forwardRef      — removed per React 19 guidance (ref as plain prop)
 *  - No useMemo/useCallback — React Compiler handles memoisation
 *
 * Next.js 16.2 patterns:
 *  - dynamic()          — lazy-loads each heavy tab panel so the initial bundle
 *                         only contains the shell + skeleton
 */

import { useState, Activity } from "react";
import dynamic from "next/dynamic";
import {
  IconCurrencyBitcoin,
  IconActivity,
  IconEye,
  IconReceipt,
} from "@tabler/icons-react";
import { cn } from "@/lib/utils";
import { TransactionLifecycleDashboardSkeleton } from "./skeleton";
import {
  EventStreamPanel,
  useEventStreamController,
} from "./event-stream";

// ── Lazy tab panels ───────────────────────────────────────────────────────────

const WatchPanel = dynamic(
  () => import("./watch-panel").then((m) => m.WatchPanel),
  { ssr: false, loading: () => <PanelSkeleton /> },
);

const TxPanel = dynamic(
  () => import("./tx-panel").then((m) => m.TxPanel),
  { ssr: false, loading: () => <PanelSkeleton /> },
);

function PanelSkeleton() {
  return (
    <div className="flex flex-col gap-3 pt-4">
      {Array.from({ length: 3 }).map((_, i) => (
        <div
          key={i}
          className="h-20 animate-pulse rounded-xl bg-muted"
          style={{ opacity: 1 - i * 0.2 }}
        />
      ))}
    </div>
  );
}

// ── Tab config ────────────────────────────────────────────────────────────────

type TabId = "stream" | "addresses" | "txlookup";

const TABS: { id: TabId; label: string; Icon: React.ElementType }[] = [
  { id: "stream",    label: "Live Events", Icon: IconActivity  },
  { id: "addresses", label: "Addresses",   Icon: IconEye       },
  { id: "txlookup",  label: "Tx Lookup",   Icon: IconReceipt   },
];

// ── TransactionLifecycleDashboard ────────────────────────────────────────────

export function TransactionLifecycleDashboard() {
  const [activeTab, setActiveTab] = useState<TabId>("stream");
  // Raised by the SSE stream when a stream_requires_reregistration event arrives;
  // prompts the user to switch to the Addresses tab to re-register.
  const [needsReregistration, setNeedsReregistration] = useState(false);
  const stream = useEventStreamController({
    onReregistrationNeeded: () => setNeedsReregistration(true),
  });

  return (
    <div className="flex flex-col gap-6">
      {/* ── Page header ── */}
      <div className="flex flex-wrap items-start gap-x-3 gap-y-2">
        <div className="flex items-center gap-3">
          <div className="relative flex size-10 shrink-0 items-center justify-center rounded-xl bg-amber-500/10 ring-1 ring-amber-500/30">
            <IconCurrencyBitcoin
              size={19}
              stroke={1.75}
              className="text-amber-600 dark:text-amber-400"
            />
          </div>
          <div>
            <h1 className="text-xl font-semibold tracking-tight text-foreground leading-none">
              Transaction Lifecycle
            </h1>
            <p className="mt-0.5 text-xs text-muted-foreground">
              Blockchain event intake · address monitoring · transaction lookup
            </p>
          </div>
        </div>
      </div>

      {/* ── Tab bar ── */}
      <div role="tablist" className="flex w-full border-b border-border">
        {TABS.map((tab) => {
          const isActive = activeTab === tab.id;
          const hasAlert = tab.id === "addresses" && needsReregistration;

          return (
            <button
              key={tab.id}
              role="tab"
              aria-selected={isActive}
              onClick={() => {
                setActiveTab(tab.id);
                if (tab.id === "addresses") setNeedsReregistration(false);
              }}
              className={cn(
                "relative flex h-9 items-center gap-2 px-3 text-sm font-medium whitespace-nowrap",
                "text-foreground/60 transition-colors hover:text-foreground",
                "after:absolute after:bottom-0 after:inset-x-0 after:h-0.5 after:rounded-t-sm",
                "after:bg-primary after:opacity-0 after:transition-opacity",
                "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset",
                isActive && "text-foreground after:opacity-100",
              )}
            >
              <tab.Icon size={14} stroke={1.75} />
              {tab.label}
              {/* Alert dot — shown when re-registration is needed */}
              {hasAlert && (
                <span className="size-1.5 rounded-full bg-amber-500 ring-2 ring-background animate-pulse" />
              )}
            </button>
          );
        })}
      </div>

      {/* ── Tab panels ──
          Activity (React 19.2) keeps all panels mounted so the SSE connection
          in the Stream tab is never torn down when the user switches tabs.
          The scheduler treats hidden panels as low-priority work.         ── */}

      <div className="relative">
        {/* Live Stream */}
        <div hidden={activeTab !== "stream"}>
          <Activity mode={activeTab === "stream" ? "visible" : "hidden"}>
            <EventStreamPanel
              connState={stream.connState}
              events={stream.events}
              error={stream.error}
              retryCount={stream.retryCount}
              lastHeartbeatAt={stream.lastHeartbeatAt}
              working={stream.working}
              onRetryNow={stream.retryNow}
              onClearEvents={stream.clearEvents}
              needsReregistration={needsReregistration}
            />
          </Activity>
        </div>

        {/* Addresses */}
        <div hidden={activeTab !== "addresses"}>
          <Activity mode={activeTab === "addresses" ? "visible" : "hidden"}>
            <WatchPanel />
          </Activity>
        </div>

        {/* Tx Lookup */}
        <div hidden={activeTab !== "txlookup"}>
          <Activity mode={activeTab === "txlookup" ? "visible" : "hidden"}>
            <TxPanel />
          </Activity>
        </div>
      </div>
    </div>
  );
}

export const BitcoinDashboard = TransactionLifecycleDashboard;
export { TransactionLifecycleDashboardSkeleton as BitcoinDashboardSkeleton };
