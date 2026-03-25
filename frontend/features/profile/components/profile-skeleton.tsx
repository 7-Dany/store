import {
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
  CardContent,
} from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Separator } from "@/components/ui/separator";

export function ProfilePageSkeleton() {
  return (
    <div className="mx-auto flex max-w-2xl flex-col gap-6 p-6 lg:p-8">
      {/* Profile Hero Skeleton */}
      <div className="relative overflow-hidden rounded-2xl border border-border bg-card ring-1 ring-foreground/5">
        <div className="profile-hero-gradient relative h-32 w-full">
          <div className="profile-hero-noise absolute inset-0 opacity-[0.15] mix-blend-overlay" />
        </div>
        <div className="px-6 pb-5">
          <div className="-mt-11 mb-4 flex items-end justify-between">
            <Skeleton className="size-22 rounded-full" />

            <div className="flex gap-2 pb-1">
              <Skeleton className="h-6 w-20" />
            </div>
          </div>
          <div className="flex flex-col gap-0.5">
            <Skeleton className="h-6 w-48" />
            <Skeleton className="h-4 w-56 mt-1" />
            <Skeleton className="h-3 w-32 mt-1" />
          </div>
          <Separator className="my-4" />
          <div className="flex flex-wrap items-center gap-x-5 gap-y-1.5">
            <Skeleton className="h-3 w-40" />
            <Skeleton className="h-3 w-36" />
          </div>
        </div>
      </div>

      {/* Account Meta Card Skeleton */}
      <Card>
        <CardHeader>
          <CardTitle>Account details</CardTitle>
          <CardDescription>Read-only account metadata.</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col">
          {[1, 2, 3].map((i) => (
            <div key={i}>
              <div className="flex items-center gap-3 py-2.5">
                <Skeleton className="size-7 rounded-md" />
                <Skeleton className="h-3 w-20 flex-1" />
                <Skeleton className="h-3 w-24" />
              </div>
              {i < 3 && <Separator />}
            </div>
          ))}
        </CardContent>
      </Card>

      {/* Settings Link Card Skeleton */}
      <Card>
        <CardContent className="flex items-center justify-between pt-6">
          <div className="flex flex-col gap-0.5">
            <Skeleton className="h-4 w-40" />
            <Skeleton className="h-3 w-60 mt-1" />
          </div>
          <Skeleton className="h-8 w-24" />
        </CardContent>
      </Card>
    </div>
  );
}
