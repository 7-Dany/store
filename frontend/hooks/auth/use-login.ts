"use client";

import { useCallback, useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import * as authApi from "@/lib/api/auth";
import { parseApiError, authErrorMessage } from "@/lib/auth/errors";

/** sessionStorage key used to hand credentials to the verify-email page. */
export const PENDING_LOGIN_KEY = "__pending_login__";

export function useLogin() {
  const router = useRouter();
  const [error, setError] = useState<string | null>(null);
  const [isPending, startTransition] = useTransition();

  const execute = useCallback(
    (identifier: string, password: string) => {
      setError(null);
      startTransition(async () => {
        try {
          await authApi.login({ identifier: identifier.trim(), password });
          router.push("/dashboard");
        } catch (e) {
          const err = parseApiError(e);

          // Unverified account — stash credentials so the verify page can
          // auto-login after the OTP is confirmed, then fire a fresh code.
          if (err.status === 403 && err.code === "email_not_verified") {
            sessionStorage.setItem(
              PENDING_LOGIN_KEY,
              JSON.stringify({ identifier: identifier.trim(), password }),
            );
            try {
              await authApi.resendVerification({ email: identifier.trim() });
            } catch {
              // Ignore — the verify page is still the right destination.
            }
            router.push(
              `/verify-email?email=${encodeURIComponent(identifier.trim())}`,
            );
            return;
          }

          setError(authErrorMessage(err, "login"));
        }
      });
    },
    [router],
  );

  return {
    execute,
    isPending,
    error,
    clearError: () => setError(null),
  };
}
