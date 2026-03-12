"use client";

import { useCallback, useTransition } from "react";
import * as authApi from "@/lib/api/auth";
import { useCountdown } from "../use-countdown";

/** 2-minute cooldown between resend requests (mirrors backend constraint). */
const RESEND_COOLDOWN_SECONDS = 120;

export function useResendVerification() {
  const [isPending, startTransition] = useTransition();
  const countdown = useCountdown();

  /**
   * Silently fires the resend request and always starts the cooldown timer.
   * The backend always returns 202 regardless of outcome, so we never surface
   * an error — just start the 2-minute disable timer.
   */
  const resend = useCallback(
    (email: string) => {
      if (countdown.isActive || isPending) return;

      startTransition(async () => {
        try {
          await authApi.resendVerification({ email: email.trim() });
        } finally {
          // Always start the cooldown — even on network failure — to prevent
          // the user from hammering the button.
          countdown.start(RESEND_COOLDOWN_SECONDS);
        }
      });
    },
    [countdown, isPending],
  );

  return { resend, isPending, countdown };
}
