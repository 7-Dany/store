"use client";

import { useCallback, useTransition } from "react";
import { useRouter } from "next/navigation";
import * as authApi from "@/lib/api/auth";
import { parseApiError, authErrorMessage } from "@/lib/auth/errors";

/** sessionStorage key used to hand credentials to the verify-email page. */
export const PENDING_LOGIN_KEY = "__pending_login__";

interface Options {
  onError?: (message: string) => void;
}

export function useLogin({ onError }: Options = {}) {
  const router = useRouter();
  const [isPending, startTransition] = useTransition();

  const execute = useCallback(
    (identifier: string, password: string) => {
      startTransition(async () => {
        try {
          await authApi.login({ identifier: identifier.trim(), password });
          router.push("/dashboard");
        } catch (e) {
          const err = parseApiError(e);

          if (err.status === 423) {
            router.push(
              `/unlock?email=${encodeURIComponent(identifier.trim())}`,
            );
            return;
          }

          if (err.status === 403 && err.code === "email_not_verified") {
            sessionStorage.setItem(
              PENDING_LOGIN_KEY,
              JSON.stringify({ identifier: identifier.trim(), password }),
            );
            try {
              await authApi.resendVerification({ email: identifier.trim() });
            } catch {
              // Ignore — verify page is still the right destination.
            }
            router.push(
              `/verify-email?email=${encodeURIComponent(identifier.trim())}`,
            );
            return;
          }

          onError?.(authErrorMessage(err, "login"));
        }
      });
    },
    [router, onError],
  );

  return { execute, isPending };
}
