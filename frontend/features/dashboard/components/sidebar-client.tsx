"use client";

import dynamic from "next/dynamic";
import Link from "next/link";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
} from "@/components/ui/sidebar";
import { NavUserSkeleton } from "@/features/dashboard/components/sidebar";
import { IconShoppingBag, IconLayoutDashboard, IconShoppingCart, IconPackage, IconUsers, IconChartBar, IconSettings } from "@tabler/icons-react";
import type { NavUser } from "@/features/dashboard/components/sidebar";

// Nav items duplicated here so the loading fallback can render real links.
// The source of truth remains NAV_ITEMS in sidebar.tsx.
const NAV_ITEMS = [
  { href: "/dashboard", icon: IconLayoutDashboard, label: "Overview" },
  { href: "/dashboard/orders", icon: IconShoppingCart, label: "Orders" },
  { href: "/dashboard/products", icon: IconPackage, label: "Products" },
  { href: "/dashboard/customers", icon: IconUsers, label: "Customers" },
  { href: "/dashboard/analytics", icon: IconChartBar, label: "Analytics" },
  { href: "/dashboard/settings", icon: IconSettings, label: "Settings" },
];

// ─── Loading fallback ─────────────────────────────────────────────────────────
//
// Shown while the AppSidebar JS chunk loads (ssr: false).
// Header and nav are rendered as real interactive links — they're static and
// don't need any data. Only the footer user section shows a skeleton, since
// that's the only part that depends on user data.

function SidebarLoadingFallback() {
  return (
    <Sidebar collapsible="icon">
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton size="lg" tooltip="Store" render={<Link href="/dashboard" />}>
              <div className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary text-primary-foreground">
                <IconShoppingBag className="size-4" />
              </div>
              <div className="grid flex-1 text-left text-sm leading-tight">
                <span className="truncate font-semibold">Store</span>
                <span className="truncate text-xs text-muted-foreground">Admin</span>
              </div>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>

      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupLabel>Platform</SidebarGroupLabel>
          <SidebarMenu>
            {NAV_ITEMS.map(({ href, icon: Icon, label }) => (
              <SidebarMenuItem key={href}>
                <SidebarMenuButton tooltip={label} render={<Link href={href} />}>
                  <Icon />
                  <span>{label}</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
            ))}
          </SidebarMenu>
        </SidebarGroup>
      </SidebarContent>

      {/* Only the user section is unknown at this point — skeleton just here */}
      <SidebarFooter>
        <NavUserSkeleton />
      </SidebarFooter>

      <SidebarRail />
    </Sidebar>
  );
}

// Skip SSR for the real sidebar — base-ui Tooltip generates IDs server-side
// that never match the client counter, causing unavoidable hydration mismatches.
// `ssr: false` is only allowed inside a Client Component.
const AppSidebarDynamic = dynamic(
  () => import("@/features/dashboard/components/sidebar").then((m) => m.AppSidebar),
  {
    ssr: false,
    loading: () => <SidebarLoadingFallback />,
  },
);

export function AppSidebarClient({ user }: { user: NavUser | null }) {
  return <AppSidebarDynamic user={user} />;
}
