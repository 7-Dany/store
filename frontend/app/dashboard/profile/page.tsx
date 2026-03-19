import { cookies } from "next/headers";
import Link from "next/link";
import { fetchProfile } from "@/lib/api/profile";
import { ProfileHero } from "@/features/dashboard/components/profile/profile-hero";
import { AccountMetaCard } from "@/features/dashboard/components/profile/account-meta-card";
import { LinkedNotice } from "@/features/dashboard/components/profile/linked-notice";
import { IconSettings } from "@tabler/icons-react";
import { Card, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";

export const metadata = { title: "Profile — Store" };

export default async function ProfilePage({
  searchParams,
}: {
  searchParams: Promise<{ linked?: string }>;
}) {
  const { linked } = await searchParams;
  const cookieStore = await cookies();
  const token = cookieStore.get("session")?.value ?? "";
  const profile = await fetchProfile(token);

  return (
    <div className="mx-auto flex max-w-2xl flex-col gap-6 p-6 lg:p-8">
      {linked && <LinkedNotice provider={linked} />}
      <ProfileHero profile={profile} />
      <AccountMetaCard profile={profile} />

      <Card>
        <CardContent className="flex items-center justify-between pt-6">
          <div className="flex flex-col gap-0.5">
            <CardTitle className="text-base">Manage your account</CardTitle>
            <CardDescription>
              Edit profile, password, sessions and connected accounts.
            </CardDescription>
          </div>
          <Link href="/dashboard/settings">
            <Button variant="outline" size="sm" className="shrink-0">
              <IconSettings size={14} stroke={2} data-icon="inline-start" />
              Settings
            </Button>
          </Link>
        </CardContent>
      </Card>
    </div>
  );
}
