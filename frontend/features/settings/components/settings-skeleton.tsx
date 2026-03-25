import { Skeleton } from "@/components/ui/skeleton";
import { Separator } from "@/components/ui/separator";

export function SettingsPageSkeleton() {
  return (
    <div className="flex flex-col divide-y divide-border">
      {/* Profile Section Skeleton */}
      <div className="py-8 first:pt-0">
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-0.5">
            <Skeleton className="h-4 w-16" />
            <Skeleton className="h-3 w-64 mt-1" />
          </div>
          <div className="flex flex-col items-center gap-3 py-6">
            <Skeleton className="size-20 rounded-full" />
            <div className="flex flex-col items-center gap-2">
              <Skeleton className="h-4 w-32" />
              <Skeleton className="h-3 w-40" />
            </div>
          </div>
          <Separator />
          <div className="flex flex-col gap-4">
            {[1, 2, 3].map((i) => (
              <div key={i} className="flex items-center gap-3 py-3">
                <Skeleton className="size-8 rounded-lg" />
                <div className="flex-1 space-y-2">
                  <Skeleton className="h-3 w-16" />
                  <Skeleton className="h-4 w-32" />
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>

      {/* Password Section Skeleton */}
      <div className="py-8">
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-0.5">
            <Skeleton className="h-4 w-20" />
            <Skeleton className="h-3 w-72 mt-1" />
          </div>
          <div className="flex flex-col gap-4">
            {[1, 2].map((i) => (
              <div key={i} className="flex flex-col gap-1.5">
                <Skeleton className="h-3 w-24" />
                <Skeleton className="h-10 w-full" />
              </div>
            ))}
          </div>
        </div>
      </div>

      {/* Sessions Section Skeleton */}
      <div className="py-8">
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-0.5">
            <Skeleton className="h-4 w-20" />
            <Skeleton className="h-3 w-64 mt-1" />
          </div>
          <div className="flex flex-col gap-3">
            {[1, 2].map((i) => (
              <div key={i} className="flex items-center gap-3">
                <Skeleton className="size-9 rounded-lg" />
                <div className="flex-1 space-y-2">
                  <Skeleton className="h-4 w-40" />
                  <Skeleton className="h-3 w-32" />
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}
