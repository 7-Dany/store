import { Skeleton } from "@/components/ui/skeleton";

export function TransactionLifecycleDashboardSkeleton() {
  return (
    <div className="flex flex-col gap-6">
      {/* Header */}
      <div className="flex items-center gap-3">
        <Skeleton className="size-10 rounded-xl" />
        <div className="flex flex-col gap-1.5">
          <Skeleton className="h-5 w-40 rounded-md" />
          <Skeleton className="h-3.5 w-28 rounded-md" />
        </div>
        <div className="ml-auto">
          <Skeleton className="h-7 w-36 rounded-full" />
        </div>
      </div>

      {/* Tab bar */}
      <div className="flex gap-1 border-b border-border pb-0">
        {[100, 80, 90].map((w, i) => (
          <Skeleton key={i} className="h-9 rounded-none" style={{ width: w }} />
        ))}
      </div>

      {/* Cards grid */}
      <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-3">
        {Array.from({ length: 4 }).map((_, i) => (
          <div
            key={i}
            className="flex flex-col gap-3 rounded-xl border border-border bg-card p-4"
          >
            <div className="flex items-center justify-between">
              <Skeleton className="h-3.5 w-24 rounded" />
              <Skeleton className="size-8 rounded-lg" />
            </div>
            <Skeleton className="h-6 w-20 rounded" />
            <Skeleton className="h-3 w-32 rounded" />
          </div>
        ))}
      </div>

      {/* Event feed */}
      <div className="flex flex-col gap-2 rounded-xl border border-border bg-card p-4">
        <Skeleton className="h-4 w-28 rounded" />
        <div className="flex flex-col gap-2 pt-1">
          {Array.from({ length: 5 }).map((_, i) => (
            <div key={i} className="flex items-center gap-3">
              <Skeleton className="size-2 rounded-full" />
              <Skeleton className="h-3 w-24 rounded" />
              <Skeleton className="h-3 flex-1 rounded" />
              <Skeleton className="h-3 w-14 rounded" />
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

export const BitcoinDashboardSkeleton = TransactionLifecycleDashboardSkeleton;
