import { SettingsNav } from "@/features/settings/components/settings-nav";
import { SettingsMobileNav } from "@/features/settings/components/settings-mobile-nav";

export default async function SettingsLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <div className="flex h-full overflow-hidden">
      {/*
       * Settings sidebar — same width (w-64 = 16rem) and CSS variable scope
       * as the app sidebar so SidebarMenuButton active styles render identically.
       * [--radius:var(--radius-xl)] is required for the rounded active background.
       */}
      <aside className="hidden lg:flex h-full w-64 shrink-0 flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground [--radius:var(--radius-xl)]">
        <div className="flex-1 overflow-y-auto">
          <SettingsNav />
        </div>
      </aside>

      {/* Content */}
      <div data-settings-scroll className="flex-1 overflow-y-auto">
        <main className="mx-auto max-w-2xl px-4 py-6 sm:px-8 sm:py-10">
          {/* Mobile navigation */}
          <div className="mb-6 lg:hidden">
            <SettingsMobileNav />
          </div>
          {children}
        </main>
      </div>
    </div>
  );
}
