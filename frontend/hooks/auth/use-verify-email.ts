"use client";

import { useCallback, useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import * as authApi from "@/lib/api/auth";
import { parseApiError, authErrorMessage } from "@/lib/auth/errors";
import { PENDING_LOGIN_KEY } from "./use-login";

export function useVerifyEmail() {
  const router = useRouter();
  const [error, setError] = useState<string | null>(null);
  const [isPending, startTransition] = useTransition();

  const execute = useCallback(
    (email: string, code: string) => {
      setError(null);
      startTransition(async () => {
        try {
          await authApi.verifyEmail({ email: email.trim(), code });

          // If the user arrived here from a failed login attempt, their
          // credentials are stashed in sessionStorage. Use them to log in
          // automatically so they land on the dashboard without extra steps.
          const raw = sessionStorage.getItem(PENDING_LOGIN_KEY);
          if (raw) {
            sessionStorage.removeItem(PENDING_LOGIN_KEY);
            try {
              const { identifier, password } = JSON.parse(raw) as {
                identifier: string;
                password: string;
              };
              await authApi.login({ identifier, password });
              router.push("/dashboard");
              return;
            } catch {
              // Login failed for some reason (e.g. account was locked after
              // verification). Fall through to the normal redirect so the
              // user can still sign in manually.
            }
          }

          // Fallback: came from the register flow — redirect to login with
          // the verified banner so the user knows to sign in.
          router.push(
            `/login?verified=1&email=${encodeURIComponent(email.trim())}`,
          );
        } catch (e) {
          setError(authErrorMessage(parseApiError(e), "verify"));
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
