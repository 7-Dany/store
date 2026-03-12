"use client";

import { useCallback, useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import * as authApi from "@/lib/api/auth";
import { parseApiError, authErrorMessage } from "@/lib/auth/errors";

export function useResetPassword() {
  const router = useRouter();
  const [error, setError] = useState<string | null>(null);
  const [isPending, startTransition] = useTransition();

  const execute = useCallback(
    (resetToken: string, newPassword: string) => {
      setError(null);
      startTransition(async () => {
        try {
          await authApi.resetPassword({
            reset_token: resetToken,
            new_password: newPassword,
          });
          // All sessions revoked server-side — send to login with success banner.
          router.push("/login?reset=1");
        } catch (e) {
          setError(authErrorMessage(parseApiError(e), "reset"));
        }
      });
    },
    [router],
  );

  return { execute, isPending, error, clearError: () => setError(null) };
}
