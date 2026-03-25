import { cookies } from "next/headers";
import {
  fetchProfile,
  fetchIdentities,
  fetchSessions,
  fetchDeletionMethod,
} from "@/lib/api/profile";
import { EditInfoCard } from "@/features/profile/components/edit-info-card";
import { LinkedAccounts } from "@/features/profile/components/linked-accounts";
import { LinkedNotice } from "@/features/profile/components/linked-notice";
import { DangerZone } from "@/features/profile/components/danger-zone";
import {
  ChangePasswordCard,
  SetPasswordCard,
} from "@/features/settings/components/password-card";
import { SessionsList } from "@/features/settings/components/sessions-list";
import { Metadata } from "next";

export const metadata: Metadata = { title: "Settings — Store" };

function Section({
  id,
  title,
  description,
  children,
  destructive = false,
}: {
  id: string;
  title: string;
  description?: string;
  children: React.ReactNode;
  destructive?: boolean;
}) {
  return (
    <section id={id} className="scroll-mt-4 flex flex-col">
      <div className="flex flex-col gap-0.5 mb-1">
        <h2
          className={`text-sm font-semibold ${
            destructive ? "text-destructive" : "text-foreground"
          }`}
        >
          {title}
        </h2>
        {description && (
          <p className="text-xs text-muted-foreground leading-relaxed">
            {description}
          </p>
        )}
      </div>
      {children}
    </section>
  );
}

export default async function SettingsPage({
  searchParams,
}: {
  searchParams: Promise<{ linked?: string }>;
}) {
  const { linked } = await searchParams;
  const cookieStore = await cookies();
  const token = cookieStore.get("session")?.value ?? "";

  const [profile, { identities }, { sessions }, deletionMethod] =
    await Promise.all([
      fetchProfile(token),
      fetchIdentities(token),
      fetchSessions(token),
      fetchDeletionMethod(token),
    ]);

  const hasPassword = deletionMethod === "password";
  const authMethodCount = identities.length + (hasPassword ? 1 : 0);

  return (
    <div className="flex flex-col divide-y divide-border">
      {linked && <LinkedNotice provider={linked} />}

      <div>
        <Section
          id="profile"
          title="Profile"
          description="Your public identity across the dashboard."
        >
          <EditInfoCard profile={profile} />
        </Section>
      </div>

      <div className="py-8">
        <Section
          id="password"
          title="Password"
          description={
            hasPassword
              ? "Update your password. All other sessions will be signed out on change."
              : "Add a password so you can also sign in with your email address."
          }
        >
          {hasPassword ? <ChangePasswordCard /> : <SetPasswordCard />}
        </Section>
      </div>

      <div className="py-8">
        <Section
          id="sessions"
          title="Sessions"
          description="All devices currently signed in to your account."
        >
          <SessionsList sessions={sessions} />
        </Section>
      </div>

      <div className="py-8">
        <Section
          id="connections"
          title="Connected accounts"
          description="Link OAuth providers as additional sign-in methods. At least one is required."
        >
          <LinkedAccounts
            identities={identities}
            authMethodCount={authMethodCount}
          />
        </Section>
      </div>

      <div className="py-8">
        <Section
          id="danger"
          title="Danger zone"
          description="Permanent actions that cannot be undone."
          destructive
        >
          <DangerZone />
        </Section>
      </div>
    </div>
  );
}
