"use client";

import { useCallback, useTransition } from "react";
import { toast } from "@/components/ui/sonner";
import * as oauthApi from "@/lib/api/oauth";
import { parseApiError, errorMessage } from "@/lib/auth/errors";
import type { TelegramAuthPayload } from "@/lib/api/types";

export function useLinkTelegram(onSuccess: () => void) {
  const [isPending, startTransition] = useTransition();

  const link = useCallback(
    (payload: TelegramAuthPayload) => {
      startTransition(async () => {
        try {
          await oauthApi.linkTelegram(payload);
          toast.success("Telegram account linked.");
          onSuccess();
        } catch (e) {
          const err = parseApiError(e);
          toast.error(errorMessage(err, "telegram_link"));
        }
      });
    },
    [onSuccess],
  );

  return { link, isPending };
}
