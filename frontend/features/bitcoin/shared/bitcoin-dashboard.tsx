"use client";

/**
 * TransactionLifecycleDashboard — two-tab workspace for transaction flow ops.
 *
 * Tabs:
 *  1. Addresses   — register / manage watched Bitcoin addresses
 *  2. Tx Lookup   — single and batch on-chain transaction status
 *
 * The live event stream stays mounted in the background at all times and is
 * opened via the trigger button in the header.
 *
 * React 19.2 patterns:
 *  - Activity           — keeps inactive tab panels mounted but hidden; the
 *                         React scheduler deprioritises their updates
 *  - useEffectEvent     — stable stream callbacks (in the hook)
 *  - No forwardRef      — removed per React 19 guidance
 *  - No useMemo/useCallback — React Compiler (enabled in next.config.ts) handles
 *                             memoisation automatically
 *
 * Next.js 16.2 patterns:
 *  - dynamic()          — lazy-loads each heavy tab panel so the initial bundle
 *                         only contains the shell + skeleton
 *
 * shadcn/ui patterns:
 *  - Tabs / TabsList / TabsTrigger / TabsContent — replaces the hand-rolled
 *    tab bar from the previous revision (fixes ARIA, keyboard nav, focus ring)
 */

import { useState, Activity } from "react";
import dynamic from "next/dynamic";
import { IconCurrencyBitcoin, IconEye, IconReceipt } from "@tabler/icons-react";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { Skeleton } from "@/components/ui/skeleton";
import { TransactionLifecycleDashboardSkeleton } from "./skeleton";
import { EventStreamPanel } from "./event-stream";
import { useEventStreamController } from "@/features/bitcoin/events/hooks/use-event-stream";

// ── Lazy tab panels ────────────────────────────────────────────────────────────

function PanelSkeleton() {
  return (
    <div className="flex flex-col gap-3 pt-4">
      {Array.from({ length: 3 }).map((_, i) => (
        <Skeleton
          key={i}
          className="h-20 rounded-xl"
          style={{ opacity: 1 - i * 0.2 }}
        />
      ))}
    </div>
  );
}

const WatchPanel = dynamic(
  () => import("../address/components/watch-panel").then((m) => m.WatchPanel),
  { ssr: false, loading: () => <PanelSkeleton /> },
);

const TxPanel = dynamic(
  () => import("../tx/components/tx-panel").then((m) => m.TxPanel),
  { ssr: false, loading: () => <PanelSkeleton /> },
);

// ── Component ──────────────────────────────────────────────────────────────────

export function TransactionLifecycleDashboard() {
  const [activeTab, setActiveTab] = useState<string>("addresses");
  // Raised by the SSE stream when stream_requires_reregistration arrives.
  const [needsReregistration, setNeedsReregistration] = useState(false);

  const stream = useEventStreamController({
    onReregistrationNeeded: () => setNeedsReregistration(true),
  });

  return (
    <div className="flex flex-col gap-6">
      {/* ── Page header ── */}
      <div className="flex flex-wrap items-start justify-between gap-x-3 gap-y-2">
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

        <EventStreamPanel
          connState={stream.connState}
          events={stream.events}
          error={stream.error}
          retryCount={stream.retryCount}
          lastHeartbeatAt={stream.lastHeartbeatAt}
          onRetryNow={stream.retryNow}
          onClearEvents={stream.clearEvents}
          needsReregistration={needsReregistration}
        />
      </div>

      {/* ── Tabs ──
          variant="line" renders the underline-style tab bar matching the
          previous hand-rolled design. TabsContent wraps Activity so each
          panel keeps its state while hidden.                              ── */}
      <Tabs
        value={activeTab}
        onValueChange={(value) => {
          setActiveTab(value);
          if (value === "addresses") setNeedsReregistration(false);
        }}
      >
        <TabsList variant="line" className="w-full justify-start">
          <TabsTrigger value="addresses" className="gap-2">
            <IconEye size={14} stroke={1.75} />
            Addresses
            {needsReregistration && (
              <span
                aria-label="Re-registration required"
                className="size-1.5 rounded-full bg-amber-500 ring-2 ring-background animate-pulse"
              />
            )}
          </TabsTrigger>

          <TabsTrigger value="txlookup" className="gap-2">
            <IconReceipt size={14} stroke={1.75} />
            Tx Lookup
          </TabsTrigger>
        </TabsList>

        {/* Addresses panel */}
        <TabsContent value="addresses">
          <Activity mode={activeTab === "addresses" ? "visible" : "hidden"}>
            <WatchPanel />
          </Activity>
        </TabsContent>

        {/* Tx Lookup panel */}
        <TabsContent value="txlookup">
          <Activity mode={activeTab === "txlookup" ? "visible" : "hidden"}>
            <TxPanel />
          </Activity>
        </TabsContent>
      </Tabs>
    </div>
  );
}

export const BitcoinDashboard = TransactionLifecycleDashboard;
export { TransactionLifecycleDashboardSkeleton as BitcoinDashboardSkeleton };
