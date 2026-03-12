import { cookies } from "next/headers";
import { fetchProfile } from "@/lib/api/profile";
import { AppSidebar } from "@/components/dashboard/sidebar";
import { SidebarProvider, SidebarInset, SidebarTrigger } from "@/components/ui/sidebar";
import { Separator } from "@/components/ui/separator";

export default async function DashboardLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const cookieStore = await cookies();
  const token = cookieStore.get("session")?.value ?? "";
  const user = await fetchProfile(token);

  const sidebarOpen = cookieStore.get("sidebar_state")?.value !== "false";

  return (
    // suppressHydrationWarning: base-ui Tooltip auto-generates IDs that
    // intentionally differ between SSR and client hydration.
    <SidebarProvider defaultOpen={sidebarOpen} suppressHydrationWarning>
      <AppSidebar user={user} />
      <SidebarInset>
        <header className="flex h-12 shrink-0 items-center gap-2 border-b border-border px-4">
          <SidebarTrigger className="-ml-1" />
          <Separator orientation="vertical" className="h-4" />
          <span className="text-sm text-muted-foreground">Dashboard</span>
        </header>
        <div className="flex flex-1 flex-col">
          {children}
        </div>
      </SidebarInset>
    </SidebarProvider>
  );
}
