"use client";

import { useCallback, useTransition } from "react";
import { useRouter } from "next/navigation";
import * as authApi from "@/lib/api/auth";
import { parseApiError, authErrorMessage } from "@/lib/auth/errors";
import { PENDING_LOGIN_KEY } from "./use-login";

interface Options {
  /** Called with a user-facing error message when verification fails. */
  onError?: (message: string) => void;
}

export function useVerifyEmail({ onError }: Options = {}) {
  const router = useRouter();
  const [isPending, startTransition] = useTransition();

  const execute = useCallback(
    (email: string, code: string) => {
      startTransition(async () => {
        try {
          await authApi.verifyEmail({ email: email.trim(), code });

          // Auto-login if credentials were stashed from a failed login attempt.
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
              // Login failed after verification — fall through to manual sign-in.
            }
          }

          router.push(
            `/login?verified=1&email=${encodeURIComponent(email.trim())}`,
          );
        } catch (e) {
          const msg = authErrorMessage(parseApiError(e), "verify");
          onError?.(msg);
        }
      });
    },
    [router, onError],
  );

  return { execute, isPending };
}
