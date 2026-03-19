"use client";

import { useCallback, useTransition } from "react";
import { toast } from "@/components/ui/sonner";
import * as oauthApi from "@/lib/api/oauth";
import { parseApiError, errorMessage } from "@/lib/auth/errors";

type Provider = "google" | "telegram";

function capitalize(s: string) {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

export function useUnlinkProvider(onSuccess: (provider: Provider) => void) {
  const [isPending, startTransition] = useTransition();

  const unlink = useCallback(
    (provider: Provider) => {
      startTransition(async () => {
        try {
          if (provider === "google") {
            await oauthApi.unlinkGoogle();
          } else {
            await oauthApi.unlinkTelegram();
          }
          toast.success(`${capitalize(provider)} account unlinked.`);
          onSuccess(provider);
        } catch (e) {
          const err = parseApiError(e);
          const ctx = provider === "google" ? "google_unlink" : "telegram_unlink";
          toast.error(errorMessage(err, ctx));
        }
      });
    },
    [onSuccess],
  );

  return { unlink, isPending };
}
