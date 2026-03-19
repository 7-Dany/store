"use client";

import { useCallback, useTransition } from "react";
import { useRouter } from "next/navigation";
import * as authApi from "@/lib/api/auth";
import { parseApiError, authErrorMessage } from "@/lib/auth/errors";

interface Options {
  onError?: (message: string) => void;
}

export function useResetPassword({ onError }: Options = {}) {
  const router = useRouter();
  const [isPending, startTransition] = useTransition();

  const execute = useCallback(
    (resetToken: string, newPassword: string) => {
      startTransition(async () => {
        try {
          await authApi.resetPassword({
            reset_token: resetToken,
            new_password: newPassword,
          });
          // All sessions revoked server-side — send to login with success banner.
          router.push("/login?reset=1");
        } catch (e) {
          onError?.(authErrorMessage(parseApiError(e), "reset"));
        }
      });
    },
    [router, onError],
  );

  return { execute, isPending };
}
