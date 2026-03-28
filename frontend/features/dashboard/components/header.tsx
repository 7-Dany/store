"use client";

import { usePathname } from "next/navigation";
import { SidebarTrigger } from "@/components/ui/sidebar";
import { Separator } from "@/components/ui/separator";
import { cn } from "@/lib/utils";

const BREADCRUMBS: Record<string, { parent: string; child?: string }> = {
  "/dashboard":          { parent: "Dashboard" },
  "/dashboard/profile":  { parent: "Dashboard", child: "Profile" },
  "/dashboard/settings": { parent: "Settings" },
  "/dashboard/security": { parent: "System", child: "System Health" },
  "/dashboard/bitcoin":  { parent: "System", child: "Transaction Lifecycle" },
  "/dashboard/transaction-lifecycle": { parent: "System", child: "Transaction Lifecycle" },
};

export function DashboardHeader() {
  const pathname = usePathname();
  const crumb = BREADCRUMBS[pathname] ?? { parent: "Dashboard" };
  const onSettings = pathname.startsWith("/dashboard/settings");

  return (
    <header
      className={cn(
        "sticky top-0 z-20 flex h-12 shrink-0 items-center gap-2 border-b px-4",
        onSettings
          ? "border-sidebar-border bg-sidebar text-sidebar-foreground"
          : "border-border bg-background",
      )}
    >
      <SidebarTrigger className="-ml-1" />
      <Separator
        orientation="vertical"
        className={cn("h-4", onSettings ? "bg-sidebar-border" : "bg-border")}
      />
      <span className={cn("text-sm", onSettings ? "text-sidebar-foreground/60" : "text-muted-foreground")}>
        {crumb.parent}
      </span>
      {crumb.child && (
        <>
          <span className={cn("text-sm", onSettings ? "text-sidebar-foreground/30" : "text-muted-foreground/40")}>/</span>
          <span className={cn("text-sm font-medium", onSettings ? "text-sidebar-foreground" : "text-foreground")}>
            {crumb.child}
          </span>
        </>
      )}
    </header>
  );
}
