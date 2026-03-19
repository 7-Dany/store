"use client";

import { useCallback, useTransition } from "react";
import { toast } from "sonner";
import * as authApi from "@/lib/api/auth";
import { useCountdown } from "@/hooks/shared/use-countdown";

const RESEND_COOLDOWN_SECONDS = 60;

export function useForgotPasswordResend() {
  const [isPending, startTransition] = useTransition();
  const countdown = useCountdown();

  const resend = useCallback(
    (email: string) => {
      if (countdown.isActive || isPending) return;

      startTransition(async () => {
        try {
          await authApi.forgotPassword({ email: email.trim() });
          toast.success("If that email is registered, a new reset code has been sent.");
        } catch {
          toast.error("Couldn't send the code. Check your connection and try again.");
        } finally {
          countdown.start(RESEND_COOLDOWN_SECONDS);
        }
      });
    },
    [countdown, isPending],
  );

  return { resend, isPending, countdown };
}
