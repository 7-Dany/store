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
    // Session cookie is missing or expired. If a refresh_token cookie is still
    // present, attempt a silent rotation before giving up — this is the common
    // case when the user returns after the 15-minute access token TTL.
    // The GET handler sets fresh cookies and redirects back here; on failure it
    // redirects to /login and clears the stale cookies.
    if (cookieStore.get("refresh_token")?.value) {
      redirect("/api/auth/refresh?from=/dashboard");
    }
    // No refresh token either — clean logout.
    redirect("/api/auth/logout");
  }

  const sidebarOpen = cookieStore.get("sidebar_state")?.value !== "false";

  const navUser = {
    display_name: profile.display_name,
    username: profile.username ?? null,
    email: profile.email,
    avatar_url: profile.avatar_url ?? null,
  };

  return (
    <SidebarProvider defaultOpen={sidebarOpen} className="h-svh overflow-hidden">
      <TokenRefresher />
      <AppSidebarClient user={navUser} />
      <SidebarInset className="overflow-hidden">
        <DashboardHeader />
        {profile?.scheduled_deletion_at && (
          <PendingDeletionBanner scheduledAt={profile.scheduled_deletion_at} />
        )}
        <div className="flex flex-1 flex-col overflow-hidden">
          {children}
        </div>
      </SidebarInset>
    </SidebarProvider>
  );
}
