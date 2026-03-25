"use client";

import { useEffect, useRef, useState } from "react";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  IconBrandGoogle,
  IconBrandTelegram,
  IconLoader2,
  IconUnlink,
  IconLink,
} from "@tabler/icons-react";
import { cn } from "@/lib/utils";
import { useUnlinkProvider } from "@/features/profile/hooks/use-unlink-provider";
import { useLinkTelegram } from "@/features/profile/hooks/use-link-telegram";
import type { OAuthIdentity, TelegramAuthPayload } from "@/lib/api/types";

interface Props {
  identities: OAuthIdentity[];
  authMethodCount: number;
}

type ProviderKey = "google" | "telegram";

const PROVIDER_META: Record<
  ProviderKey,
  { label: string; Icon: React.ElementType }
> = {
  google: {
    label: "Google",
    Icon: IconBrandGoogle,
  },
  telegram: {
    label: "Telegram",
    Icon: IconBrandTelegram,
  },
};

export function LinkedAccounts({ identities: initialIdentities, authMethodCount }: Props) {
  const [identities, setIdentities] = useState(initialIdentities);

  function handleUnlinked(provider: ProviderKey) {
    setIdentities((prev) => prev.filter((id) => id.provider !== provider));
  }

  function handleLinked(identity: OAuthIdentity) {
    setIdentities((prev) => [
      ...prev.filter((id) => id.provider !== identity.provider),
      identity,
    ]);
  }

  const liveCount = authMethodCount - initialIdentities.length + identities.length;
  const { unlink, isPending: unlinking } = useUnlinkProvider(handleUnlinked);

  return (
    <div className="flex flex-col gap-2.5">
      {(["google", "telegram"] as ProviderKey[]).map((provider) => {
        const identity = identities.find((id) => id.provider === provider);
        const isLinked = !!identity;
        const isOnlyMethod = liveCount <= 1 && isLinked;

        return (
          <ProviderRow
            key={provider}
            provider={provider}
            identity={identity}
            isLinked={isLinked}
            isOnlyMethod={isOnlyMethod}
            unlinking={unlinking}
            onUnlink={() => unlink(provider)}
            onLinked={handleLinked}
          />
        );
      })}
    </div>
  );
}

interface ProviderRowProps {
  provider: ProviderKey;
  identity: OAuthIdentity | undefined;
  isLinked: boolean;
  isOnlyMethod: boolean;
  unlinking: boolean;
  onUnlink: () => void;
  onLinked: (identity: OAuthIdentity) => void;
}

function ProviderRow({ provider, identity, isLinked, isOnlyMethod, unlinking, onUnlink, onLinked }: ProviderRowProps) {
  const { label, Icon } = PROVIDER_META[provider];

  if (isLinked && identity) {
    const displayName = identity.display_name ?? label;
    const email = identity.provider_email;
    const initials = displayName.charAt(0).toUpperCase();

    return (
      <div className="group/row relative flex items-center gap-3 rounded-xl border border-border bg-accent/5 p-3 transition-colors">
        <div className="relative shrink-0">
          <Avatar className="size-10 ring-2 ring-background">
            {identity.avatar_url ? <AvatarImage src={identity.avatar_url} alt={displayName} /> : null}
            <AvatarFallback className="text-sm font-semibold">{initials}</AvatarFallback>
          </Avatar>
          <div className="absolute -bottom-1 -right-1 flex size-5 items-center justify-center rounded-full border-2 border-background bg-card shadow-sm">
            <Icon size={11} stroke={2} className="text-primary" />
          </div>
        </div>

        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium text-foreground truncate leading-tight">{displayName}</span>
            <Badge variant="secondary" className="shrink-0 text-[10px] font-medium">Connected</Badge>
          </div>
          {email && <span className="truncate text-xs text-muted-foreground">{email}</span>}
        </div>

        <Button
          variant="ghost" size="sm"
          onClick={onUnlink}
          disabled={unlinking || isOnlyMethod}
          title={isOnlyMethod ? "Can't remove your only sign-in method" : `Unlink ${label}`}
          className={cn(
            "shrink-0 gap-1.5 text-xs text-muted-foreground",
            "opacity-0 transition-opacity group-hover/row:opacity-100",
            "hover:bg-destructive/10 hover:text-destructive",
            isOnlyMethod && "pointer-events-none",
          )}
        >
          {unlinking ? <IconLoader2 size={13} stroke={2} className="animate-spin" /> : <IconUnlink size={13} stroke={2} />}
          Unlink
        </Button>
      </div>
    );
  }

  return (
    <div className="flex items-center gap-3 rounded-xl border border-border bg-muted/20 px-3 py-3">
      <div className="flex size-10 shrink-0 items-center justify-center rounded-full border border-border bg-card">
        <Icon size={18} stroke={1.75} className="text-muted-foreground" />
      </div>
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="text-sm font-medium text-foreground">{label}</span>
        <span className="text-xs text-muted-foreground">Not connected</span>
      </div>
      {provider === "google" ? <LinkGoogleButton /> : <LinkTelegramButton onLinked={onLinked} />}
    </div>
  );
}

function LinkGoogleButton() {
  return (
    <a
      href="/api/oauth/google/link"
      className={cn(
        "inline-flex h-8 shrink-0 items-center gap-1.5 rounded-lg px-3",
        "text-xs font-medium border border-border bg-card transition-colors",
        "hover:bg-muted hover:text-foreground text-muted-foreground",
      )}
    >
      <IconLink size={13} stroke={2} />
      Connect
    </a>
  );
}

interface LinkTelegramButtonProps {
  onLinked: (identity: OAuthIdentity) => void;
}

function LinkTelegramButton({ onLinked }: LinkTelegramButtonProps) {
  const widgetRef = useRef<HTMLDivElement>(null);
  const { link, isPending } = useLinkTelegram(() => {
    onLinked({
      provider: "telegram",
      provider_uid: "",
      provider_email: null,
      display_name: null,
      avatar_url: null,
      created_at: new Date().toISOString(),
    });
  });

  const botUsername = process.env.NEXT_PUBLIC_TELEGRAM_BOT_USERNAME;

  useEffect(() => {
    if (!botUsername || !widgetRef.current) return;

    (window as Window & { onTelegramLinkAuth?: (user: TelegramAuthPayload) => void }).onTelegramLinkAuth =
      (user) => link(user);

    const script = document.createElement("script");
    script.src = "https://telegram.org/js/telegram-widget.js?22";
    script.setAttribute("data-telegram-login", botUsername);
    script.setAttribute("data-size", "large");
    script.setAttribute("data-onauth", "onTelegramLinkAuth(user)");
    script.setAttribute("data-request-access", "write");
    script.async = true;
    script.onload = () => {
      const iframe = widgetRef.current?.querySelector("iframe");
      if (iframe) {
        iframe.style.cssText =
          "position:absolute;inset:0;width:100%;height:100%;opacity:0;cursor:pointer;border:none;";
      }
    };
    widgetRef.current.appendChild(script);

    return () => {
      delete (window as Window & { onTelegramLinkAuth?: unknown }).onTelegramLinkAuth;
      const c = widgetRef.current;
      if (c?.contains(script)) c.removeChild(script);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (!botUsername) return null;

  return (
    <div className="group relative shrink-0 transition-transform active:scale-[0.98]">
      <div className={cn(
        "inline-flex h-8 items-center gap-1.5 rounded-lg px-3 pointer-events-none",
        "text-xs font-medium text-muted-foreground",
        "border border-border bg-card transition-colors",
        "group-hover:bg-muted group-hover:text-foreground",
      )}>
        {isPending ? <IconLoader2 size={13} stroke={2} className="animate-spin" /> : <IconLink size={13} stroke={2} />}
        Connect
      </div>
      <div ref={widgetRef} aria-hidden className="absolute inset-0" />
    </div>
  );
}
