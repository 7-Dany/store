import { Skeleton } from "@/components/ui/skeleton";

/**
 * TransactionLifecycleDashboardSkeleton
 *
 * Mirrors the actual dashboard layout exactly:
 *   Header row (icon + title + stream button)
 *   Tab bar (2 tabs)
 *   Panel body (form card + watched-list / tx-form card)
 */
export function TransactionLifecycleDashboardSkeleton() {
  return (
    <div className="flex flex-col gap-6">
      {/* ── Header row ── */}
      <div className="flex flex-wrap items-start justify-between gap-x-3 gap-y-2">
        <div className="flex items-center gap-3">
          <Skeleton className="size-10 rounded-xl" />
          <div className="flex flex-col gap-1.5">
            <Skeleton className="h-5 w-44 rounded-md" />
            <Skeleton className="h-3 w-60 rounded-md" />
          </div>
        </div>
        {/* Stream trigger button */}
        <Skeleton className="size-10 rounded-xl" />
      </div>

      {/* ── Tab bar ── */}
      <div className="flex w-full border-b border-border">
        <Skeleton className="h-9 w-24 rounded-none" />
        <Skeleton className="ml-1 h-9 w-24 rounded-none" />
      </div>

      {/* ── Panel body: capacity bar + form card ── */}
      <div className="flex flex-col gap-5 pt-4">
        {/* Capacity bar card */}
        <div className="flex flex-col gap-2 rounded-xl border border-border bg-card px-4 py-3.5">
          <div className="flex items-center justify-between">
            <Skeleton className="h-3.5 w-28 rounded" />
            <Skeleton className="h-3.5 w-14 rounded" />
          </div>
          <Skeleton className="h-1.5 w-full rounded-full" />
          <Skeleton className="h-3 w-36 rounded" />
        </div>

        {/* Form card */}
        <div className="flex flex-col gap-3 rounded-xl border border-border bg-card p-4">
          <Skeleton className="h-4 w-40 rounded" />
          <Skeleton className="h-3 w-64 rounded" />
          <Skeleton className="h-24 w-full rounded-lg" />
          <Skeleton className="h-8 w-full rounded-lg" />
        </div>

        {/* Empty watched-list placeholder */}
        <div className="flex flex-col gap-3">
          <div className="flex items-center gap-2">
            <Skeleton className="size-3 rounded-full" />
            <Skeleton className="h-3 w-28 rounded" />
          </div>
          <Skeleton className="h-24 w-full rounded-xl" />
        </div>
      </div>
    </div>
  );
}

export const BitcoinDashboardSkeleton = TransactionLifecycleDashboardSkeleton;
