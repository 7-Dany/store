"use client";

import { useCallback, useTransition } from "react";
import * as authApi from "@/lib/api/auth";
import { useCountdown } from "../use-countdown";

/** 60-second cooldown between resend requests (matches backend 60 s suppression window). */
const RESEND_COOLDOWN_SECONDS = 60;

/**
 * Resend hook for the forgot-password OTP step.
 * Calls POST /auth/forgot-password (anti-enumeration, always 202).
 */
export function useForgotPasswordResend() {
  const [isPending, startTransition] = useTransition();
  const countdown = useCountdown();

  const resend = useCallback(
    (email: string) => {
      if (countdown.isActive || isPending) return;

      startTransition(async () => {
        try {
          await authApi.forgotPassword({ email: email.trim() });
        } finally {
          countdown.start(RESEND_COOLDOWN_SECONDS);
        }
      });
    },
    [countdown, isPending],
  );

  return { resend, isPending, countdown };
}
