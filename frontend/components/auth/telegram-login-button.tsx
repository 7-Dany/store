"use client";

import { useEffect, useRef, useTransition } from "react";
import { IconBrandTelegram, IconLoader2 } from "@tabler/icons-react";
import { cn } from "@/lib/utils";

type TelegramUser = {
  id: number;
  first_name: string;
  last_name?: string;
  username?: string;
  photo_url?: string;
  auth_date: number;
  hash: string;
};

export function TelegramLoginButton() {
  const widgetRef = useRef<HTMLDivElement>(null);
  const errorRef = useRef<HTMLParagraphElement>(null);
  const [isPending, startTransition] = useTransition();

  const botUsername = process.env.NEXT_PUBLIC_TELEGRAM_BOT_USERNAME;

  useEffect(() => {
    if (!botUsername || !widgetRef.current) return;

    (
      window as Window & { onTelegramAuth?: (user: TelegramUser) => void }
    ).onTelegramAuth = (user) => {
      startTransition(async () => {
        if (errorRef.current) errorRef.current.textContent = "";

        const res = await fetch("/api/oauth/telegram", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(user),
        });

        if (res.ok) {
          const { redirectUrl } = (await res.json()) as { redirectUrl: string };
          window.location.href = redirectUrl;
          return;
        }

        const err = (await res.json().catch(() => null)) as {
          message?: string;
        } | null;
        if (errorRef.current) {
          errorRef.current.textContent =
            err?.message ?? "Authentication failed. Please try again.";
        }
      });
    };

    const script = document.createElement("script");
    script.src = "https://telegram.org/js/telegram-widget.js?22";
    script.setAttribute("data-telegram-login", botUsername);
    script.setAttribute("data-size", "large");
    script.setAttribute("data-onauth", "onTelegramAuth(user)");
    script.setAttribute("data-request-access", "write");
    script.async = true;

    // Stretch iframe to cover the wrapper so it intercepts clicks.
    script.onload = () => {
      const iframe = widgetRef.current?.querySelector("iframe");
      if (iframe) {
        iframe.style.cssText =
          "position:absolute;inset:0;width:100%;height:100%;opacity:0;cursor:pointer;border:none;";
      }
    };

    widgetRef.current.appendChild(script);

    return () => {
      delete (window as Window & { onTelegramAuth?: unknown }).onTelegramAuth;
      const c = widgetRef.current;
      if (c?.contains(script)) c.removeChild(script);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (!botUsername) {
    return (
      <div className="flex w-full items-center justify-center gap-2 rounded-lg border border-dashed border-amber-300 bg-amber-50 px-4 py-2 text-xs font-medium text-amber-700 dark:border-amber-800 dark:bg-amber-950/50 dark:text-amber-400">
        ⚠️ Set{" "}
        <code className="font-mono">NEXT_PUBLIC_TELEGRAM_BOT_USERNAME</code> in
        .env.local
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-1.5">
      {/*
        Hover fix: The `group` class is on this outer wrapper div.
        The invisible iframe sits inside it (absolute, full size, pointer-events-auto).
        When the user hovers, the pointer is over the iframe which is INSIDE the group —
        so `group-hover:` on the visible button div fires correctly.
        The visible button has `pointer-events-none` so clicks fall through to the iframe.
      */}
      <div className="group relative transition-transform duration-150 active:scale-[0.99]">
        {/* Visible button — pointer-events-none so clicks reach the iframe */}
        <div
          aria-label="Continue with Telegram"
          className={cn(
            "flex w-full items-center justify-center gap-3",
            "h-10 rounded-4xl border border-border bg-input/30 px-4",
            "text-sm font-medium text-muted-foreground",
            "transition-colors duration-150 pointer-events-none",
            // group-hover fires because the iframe (inside group) receives the pointer
            "group-hover:bg-input/50",
          )}
        >
          {isPending ? (
            <>
              <IconLoader2
                size={18}
                stroke={1.75}
                className="shrink-0 animate-spin"
              />
              <span>Signing in…</span>
            </>
          ) : (
            <>
              <IconBrandTelegram
                size={18}
                stroke={1.75}
                className="shrink-0 text-muted-foreground"
              />
              <span>Continue with Telegram</span>
            </>
          )}
        </div>

        {/* Invisible Telegram iframe — receives clicks, triggers the auth popup */}
        <div ref={widgetRef} aria-hidden="true" className="absolute inset-0" />
      </div>

      {/* Error — written via DOM ref to skip a re-render */}
      <p
        ref={errorRef}
        role="alert"
        aria-live="polite"
        className="hidden text-center text-xs text-destructive non-empty:block"
      />
    </div>
  );
}
