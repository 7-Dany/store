"use client";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";
import {
  IconShieldCheck,
  IconCalendar,
  IconAt,
  IconCamera,
  IconLock,
  IconClock,
} from "@tabler/icons-react";
import { cn, getInitials, formatDate } from "@/lib/utils";
import type { UserProfile } from "@/lib/api/types";

interface Props {
  profile: UserProfile | null;
}

export function ProfileHero({ profile }: Props) {
  const initials = getInitials(profile?.display_name ?? "");

  return (
    <div className="relative overflow-hidden rounded-2xl border border-border bg-card ring-1 ring-foreground/5">
      {/* ── Gradient band ─────────────────────────────────────────── */}
      <div className="profile-hero-gradient relative h-32 w-full">
        <div className="profile-hero-noise absolute inset-0 opacity-[0.15] mix-blend-overlay" />
      </div>

      {/* ── Content area ──────────────────────────────────────────── */}
      <div className="px-6 pb-5">
        <div className="-mt-11 mb-4 flex items-end justify-between">
          <div className="group/avatar relative">
            <Avatar className="size-[88px] ring-4 ring-card shadow-lg">
              {profile?.avatar_url && (
                <AvatarImage
                  src={profile.avatar_url}
                  alt={profile?.display_name ?? "Profile picture"}
                />
              )}
              <AvatarFallback className="bg-primary text-primary-foreground text-2xl font-bold">
                {initials}
              </AvatarFallback>
            </Avatar>
            {/* Avatar upload coming soon - show when ready
            <button
              title="Change avatar"
              className={cn(
                "absolute inset-0 flex flex-col items-center justify-center rounded-full",
                "bg-black/0 transition-all duration-200 group-hover/avatar:bg-black/45",
              )}
            >
              <IconCamera
                size={20}
                stroke={1.75}
                className="text-white opacity-0 transition-opacity duration-200 group-hover/avatar:opacity-100"
              />
            </button>
            */}
          </div>

          {/* Status badges */}
          <div className="flex flex-wrap items-center gap-2 pb-1">
            {profile?.email_verified ? (
              <Badge variant="secondary" className="gap-1">
                <IconShieldCheck size={11} stroke={2.5} />
                Verified
              </Badge>
            ) : (
              <Badge variant="destructive" className="gap-1">Unverified</Badge>
            )}
            {profile?.is_locked && (
              <Badge variant="destructive" className="gap-1">
                <IconLock size={10} stroke={2.5} />
                Locked
              </Badge>
            )}
            {profile?.scheduled_deletion_at && (
              <Badge variant="outline" className="gap-1">
                <IconClock size={10} stroke={2.5} />
                Deletion pending
              </Badge>
            )}
          </div>
        </div>

        <div className="flex flex-col gap-0.5">
          <h1 className="text-xl font-semibold tracking-tight text-foreground">
            {profile?.display_name ?? "—"}
          </h1>
          <p className="text-sm text-muted-foreground">{profile?.email ?? "—"}</p>
          {profile?.username && (
            <span className="flex items-center gap-0.5 text-xs text-muted-foreground">
              <IconAt size={12} stroke={2} />
              {profile.username}
            </span>
          )}
        </div>

        {profile?.created_at && (
          <>
            <Separator className="my-4" />
            <div className="flex flex-wrap items-center gap-x-5 gap-y-1.5 text-xs text-muted-foreground">
              <span className="flex items-center gap-1.5">
                <IconCalendar size={12} stroke={2} />
                Member since {formatDate(profile.created_at)}
              </span>
              {profile.last_login_at && (
                <span className="flex items-center gap-1.5">
                  <span className="size-1 rounded-full bg-muted-foreground/40" />
                  Last seen{" "}
                  {formatDate(profile.last_login_at, {
                    month: "short",
                    day: "numeric",
                    year: "numeric",
                  })}
                </span>
              )}
            </div>
          </>
        )}
      </div>
    </div>
  );
}
