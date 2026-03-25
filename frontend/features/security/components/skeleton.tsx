import { Skeleton } from "@/components/ui/skeleton";
import { Card, CardContent, CardHeader } from "@/components/ui/card";

/** Loading skeleton that mirrors the SecurityDashboard layout */
export function SecurityDashboardSkeleton() {
  return (
    <div className="flex flex-col gap-6">
      {/* Health banner */}
      <Skeleton className="h-15 w-full rounded-2xl" />

      {/* Section heading */}
      <div className="flex items-center gap-2">
        <Skeleton className="size-5 rounded-md" />
        <Skeleton className="h-3 w-24 rounded-full" />
      </div>

      {/* Stat row 1 */}
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        {Array.from({ length: 4 }).map((_, i) => (
          <Card key={i}>
            <CardHeader>
              <div className="flex items-center justify-between">
                <Skeleton className="h-4 w-24" />
                <Skeleton className="size-8 rounded-lg" />
              </div>
            </CardHeader>
            <CardContent className="flex flex-col gap-2">
              <Skeleton className="h-7 w-16" />
              <Skeleton className="h-3 w-28" />
            </CardContent>
          </Card>
        ))}
      </div>

      {/* Section heading */}
      <div className="flex items-center gap-2">
        <Skeleton className="size-5 rounded-md" />
        <Skeleton className="h-3 w-28 rounded-full" />
      </div>

      {/* Stat row 2 */}
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        {Array.from({ length: 4 }).map((_, i) => (
          <Card key={i}>
            <CardHeader>
              <div className="flex items-center justify-between">
                <Skeleton className="h-4 w-24" />
                <Skeleton className="size-8 rounded-lg" />
              </div>
            </CardHeader>
            <CardContent className="flex flex-col gap-2">
              <Skeleton className="h-7 w-16" />
              <Skeleton className="h-3 w-28" />
            </CardContent>
          </Card>
        ))}
      </div>

      {/* Section heading */}
      <div className="flex items-center gap-2">
        <Skeleton className="size-5 rounded-md" />
        <Skeleton className="h-3 w-32 rounded-full" />
      </div>

      {/* Charts row */}
      <div className="grid gap-4 lg:grid-cols-3">
        {Array.from({ length: 3 }).map((_, i) => (
          <Card key={i}>
            <CardHeader>
              <Skeleton className="h-4 w-32" />
              <Skeleton className="h-3 w-48" />
            </CardHeader>
            <CardContent>
              <Skeleton className="h-40 w-full rounded-xl" />
            </CardContent>
          </Card>
        ))}
      </div>

      {/* Section heading */}
      <div className="flex items-center gap-2">
        <Skeleton className="size-5 rounded-md" />
        <Skeleton className="h-3 w-20 rounded-full" />
      </div>

      {/* Bottom row */}
      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <Skeleton className="h-4 w-32" />
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            {Array.from({ length: 5 }).map((_, i) => (
              <Skeleton key={i} className="h-12 w-full rounded-xl" />
            ))}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <Skeleton className="h-4 w-40" />
          </CardHeader>
          <CardContent>
            <Skeleton className="h-50 w-full rounded-xl" />
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
