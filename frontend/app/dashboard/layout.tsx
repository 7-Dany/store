import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { fetchProfile } from "@/lib/api/profile";
import { SidebarProvider, SidebarInset } from "@/components/ui/sidebar";
import { AppSidebarClient } from "@/features/dashboard/components/sidebar-client";
import { DashboardHeader } from "@/features/dashboard/components/header";
import { PendingDeletionBanner } from "@/features/dashboard/components/pending-deletion-banner";
import { TokenRefresher } from "@/features/dashboard/components/token-refresher";

export default async function DashboardLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const cookieStore = await cookies();
  const token = cookieStore.get("session")?.value ?? "";
  const profile = await fetchProfile(token);

  if (!profile) {
    // Google OAuth initially lands with the backend-issued refresh cookie scoped
    // to /api/v1/auth, so /dashboard cannot read it directly. Always recover
    // through the /api/v1/auth alias so both the backend-scoped cookie and the
    // frontend root-scoped cookie can refresh the session.
    redirect("/api/v1/auth/refresh?from=/dashboard");
  }

  const sidebarOpen = cookieStore.get("sidebar_state")?.value !== "false";

  const navUser = {
    display_name: profile.display_name,
    username: profile.username ?? null,
    email: profile.email,
    avatar_url: profile.avatar_url ?? null,
  };

  return (
    // h-svh + overflow-hidden on the root: locks the viewport so the sidebar
    // never causes the *page* to scroll — only the content area scrolls.
    <SidebarProvider defaultOpen={sidebarOpen} className="h-svh overflow-hidden">
      <TokenRefresher />
      <AppSidebarClient user={navUser} />
      {/* SidebarInset fills the remaining width and clips its own overflow so
          the sticky header stays pinned while the content area scrolls below. */}
      <SidebarInset className="flex flex-col overflow-hidden">
        <DashboardHeader />
        {profile?.scheduled_deletion_at && (
          <PendingDeletionBanner scheduledAt={profile.scheduled_deletion_at} />
        )}
        {/* This div is the single scroll container for all page content.
            overflow-y-auto lets pages taller than the viewport scroll normally.
            flex-1 ensures it fills all remaining vertical space inside SidebarInset. */}
        <div className="flex flex-1 flex-col overflow-y-auto">
          {children}
        </div>
      </SidebarInset>
    </SidebarProvider>
  );
}
