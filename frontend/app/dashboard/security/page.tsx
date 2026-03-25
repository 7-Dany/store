import { Suspense } from "react";
import type { Metadata } from "next";
import {
  fetchSecuritySnapshot,
  SecuritySnapshot,
} from "@/lib/api/telemetry/prometheus";
import { SecurityDashboard } from "@/features/security/components/security-dashboard";
import { SecurityDashboardSkeleton } from "@/features/security/components/skeleton";
import { getMockSnapshot } from "@/lib/api/telemetry/mock-snapshots";

export const metadata: Metadata = { title: "System Health — Store" };

// Force dynamic — this page shows live metrics, never cached
export const dynamic = "force-dynamic";

interface Props {
  searchParams: Promise<{ mock?: string }>;
}

async function SecurityDashboardData({ mock }: { mock?: string }) {
  const snapshot =
    (mock && getMockSnapshot(mock)) ?? (await fetchSecuritySnapshot());
  return (
    <SecurityDashboard
      initialData={snapshot as SecuritySnapshot}
      mockScenario={mock}
    />
  );
}

export default async function SecurityPage({ searchParams }: Props) {
  const { mock } = await searchParams;
  return (
    <div className="mx-auto w-full min-w-80 sm:min-w-120 md:min-w-160 lg:min-w-200 max-w-300 p-6 lg:p-8">
      <Suspense fallback={<SecurityDashboardSkeleton />}>
        <SecurityDashboardData mock={mock} />
      </Suspense>
    </div>
  );
}
