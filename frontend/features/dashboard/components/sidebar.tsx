"use client";

import * as React from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useTheme } from "next-themes";

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
  useSidebar,
} from "@/components/ui/sidebar";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Skeleton } from "@/components/ui/skeleton";

import {
  IconLayoutDashboard,
  IconShoppingCart,
  IconPackage,
  IconUsers,
  IconChartBar,
  IconSettings,
  IconShoppingBag,
  IconSelector,
  IconUser,
  IconLogout,
  IconSun,
  IconMoon,
  IconDeviceDesktop,
  IconLoader2,
  IconCheck,
  IconPalette,
  IconShieldCheck,
} from "@tabler/icons-react";

// ─── Types ────────────────────────────────────────────────────────────────────

export interface NavUser {
  display_name: string;
  username: string | null;
  email: string;
  avatar_url: string | null;
}

// ─── Nav items ────────────────────────────────────────────────────────────────

const NAV_ITEMS = [
  { href: "/dashboard",          icon: IconLayoutDashboard, label: "Overview" },
  { href: "/dashboard/orders",   icon: IconShoppingCart,    label: "Orders" },
  { href: "/dashboard/products", icon: IconPackage,         label: "Products" },
  { href: "/dashboard/customers",icon: IconUsers,           label: "Customers" },
  { href: "/dashboard/analytics",icon: IconChartBar,        label: "Analytics" },
  { href: "/dashboard/settings", icon: IconSettings,        label: "Settings" },
];

const NAV_SYSTEM = [
  { href: "/dashboard/security", icon: IconShieldCheck, label: "System Health" },
];

// ─── Helpers ──────────────────────────────────────────────────────────────────

function getInitials(displayName: string): string {
  return displayName
    .split(" ")
    .map((w) => w[0])
    .slice(0, 2)
    .join("")
    .toUpperCase();
}

// ─── NavUser skeleton ─────────────────────────────────────────────────────────

export function NavUserSkeleton() {
  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <SidebarMenuButton size="lg" disabled>
          <Skeleton className="size-8 shrink-0 rounded-lg" />
          <div className="grid flex-1 gap-1.5">
            <Skeleton className="h-3.5 w-28 rounded-md" />
            <Skeleton className="h-3 w-20 rounded-md" />
          </div>
          <Skeleton className="ml-auto size-4 shrink-0 rounded-md" />
        </SidebarMenuButton>
      </SidebarMenuItem>
    </SidebarMenu>
  );
}

// ─── NavUser ──────────────────────────────────────────────────────────────────

function NavUserMenu({ user }: { user: NavUser | null }) {
  const { isMobile } = useSidebar();
  const router = useRouter();
  const { theme, setTheme } = useTheme();
  const [loggingOut, setLoggingOut] = React.useState(false);

  const initials = getInitials(user?.display_name ?? "U");

  const themeOptions = [
    { key: "light", label: "Light", Icon: IconSun },
    { key: "dark",  label: "Dark",  Icon: IconMoon },
    { key: "system",label: "System",Icon: IconDeviceDesktop },
  ] as const;

  async function handleLogout() {
    setLoggingOut(true);
    try {
      await fetch("/api/auth/logout", { method: "POST" });
    } finally {
      router.push("/login");
      router.refresh();
    }
  }

  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <SidebarMenuButton
                size="lg"
                className="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground"
              />
            }
          >
            <Avatar className="size-8 rounded-lg">
              {user?.avatar_url && (
                <AvatarImage src={user.avatar_url} alt={user.display_name} />
              )}
              <AvatarFallback className="rounded-lg text-xs font-semibold">
                {initials}
              </AvatarFallback>
            </Avatar>
            <div className="grid flex-1 text-left text-sm leading-tight">
              <span className="truncate font-medium">
                {user?.display_name ?? "User"}
              </span>
              <span className="truncate text-xs text-muted-foreground">
                {user?.username ? `#${user.username}` : (user?.email ?? "")}
              </span>
            </div>
            <IconSelector className="ml-auto size-4 shrink-0 text-muted-foreground" />
          </DropdownMenuTrigger>

          <DropdownMenuContent
            className="w-(--radix-dropdown-menu-trigger-width) min-w-56"
            side={isMobile ? "bottom" : "top"}
            align="center"
            sideOffset={4}
          >
            <DropdownMenuLabel className="p-0 font-normal">
              <div className="flex items-center gap-2 px-1 py-1.5">
                <Avatar className="size-8 rounded-lg">
                  {user?.avatar_url && (
                    <AvatarImage src={user.avatar_url} alt={user.display_name} />
                  )}
                  <AvatarFallback className="rounded-lg text-xs font-semibold">
                    {initials}
                  </AvatarFallback>
                </Avatar>
                <div className="grid flex-1 text-left text-sm leading-tight">
                  <span className="truncate font-medium">
                    {user?.display_name ?? "User"}
                  </span>
                  <span className="truncate text-xs text-muted-foreground">
                    {user?.username ? `#${user.username}` : (user?.email ?? "")}
                  </span>
                </div>
              </div>
            </DropdownMenuLabel>

            <DropdownMenuSeparator />

            <DropdownMenuGroup>
              <DropdownMenuItem onClick={() => router.push("/dashboard/profile")}>
                <IconUser />
                Profile
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => router.push("/dashboard/settings")}>
                <IconSettings />
                Settings
              </DropdownMenuItem>
            </DropdownMenuGroup>

            <DropdownMenuSeparator />

            <DropdownMenuSub>
              <DropdownMenuSubTrigger>
                <IconPalette />
                Theme
              </DropdownMenuSubTrigger>
              <DropdownMenuSubContent>
                {themeOptions.map(({ key, label, Icon }) => (
                  <DropdownMenuItem key={key} onClick={() => setTheme(key)}>
                    <Icon />
                    {label}
                    <span suppressHydrationWarning>
                      {theme === key && (
                        <IconCheck className="ml-auto size-3.5 text-primary" />
                      )}
                    </span>
                  </DropdownMenuItem>
                ))}
              </DropdownMenuSubContent>
            </DropdownMenuSub>

            <DropdownMenuSeparator />

            <DropdownMenuItem
              variant="destructive"
              onClick={handleLogout}
              disabled={loggingOut}
            >
              {loggingOut ? (
                <IconLoader2 className="animate-spin" />
              ) : (
                <IconLogout />
              )}
              {loggingOut ? "Signing out…" : "Sign out"}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarMenuItem>
    </SidebarMenu>
  );
}

// ─── AppSidebar ───────────────────────────────────────────────────────────────

export function AppSidebar({ user }: { user: NavUser | null }) {
  const pathname = usePathname();

  function isActive(href: string) {
    if (href === "/dashboard") return pathname === "/dashboard";
    return pathname === href || pathname.startsWith(`${href}/`);
  }

  return (
    <Sidebar collapsible="icon" defaultChecked={false}>
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              size="lg"
              tooltip="Store"
              render={<Link href="/dashboard" />}
            >
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
        {/* Platform nav */}
        <SidebarGroup>
          <SidebarGroupLabel>Platform</SidebarGroupLabel>
          <SidebarMenu>
            {NAV_ITEMS.map(({ href, icon: Icon, label }) => (
              <SidebarMenuItem key={href}>
                <SidebarMenuButton
                  isActive={isActive(href)}
                  tooltip={label}
                  render={<Link href={href} />}
                >
                  <Icon />
                  <span>{label}</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
            ))}
          </SidebarMenu>
        </SidebarGroup>

        {/* System nav */}
        <SidebarGroup>
          <SidebarGroupLabel>System</SidebarGroupLabel>
          <SidebarMenu>
            {NAV_SYSTEM.map(({ href, icon: Icon, label }) => (
              <SidebarMenuItem key={href}>
                <SidebarMenuButton
                  isActive={isActive(href)}
                  tooltip={label}
                  render={<Link href={href} />}
                >
                  <Icon />
                  <span>{label}</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
            ))}
          </SidebarMenu>
        </SidebarGroup>
      </SidebarContent>

      <SidebarFooter>
        <NavUserMenu user={user} />
      </SidebarFooter>

      <SidebarRail />
    </Sidebar>
  );
}
