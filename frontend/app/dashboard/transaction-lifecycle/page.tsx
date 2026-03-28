import type { Metadata } from "next";
import { Suspense } from "react";
import { TransactionLifecycleDashboard } from "@/features/bitcoin/components/bitcoin-dashboard";
import { TransactionLifecycleDashboardSkeleton } from "@/features/bitcoin/components/skeleton";

export const metadata: Metadata = { title: "Transaction Lifecycle — Store" };

export const dynamic = "force-dynamic";

export default function TransactionLifecyclePage() {
  return (
    <div className="mx-auto w-full min-w-80 sm:min-w-120 md:min-w-160 lg:min-w-200 max-w-300 p-6 lg:p-8">
      <Suspense fallback={<TransactionLifecycleDashboardSkeleton />}>
        <TransactionLifecycleDashboard />
      </Suspense>
    </div>
  );
}
