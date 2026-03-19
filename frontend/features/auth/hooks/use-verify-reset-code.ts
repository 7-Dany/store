"use client";

import { useCallback, useTransition } from "react";
import * as authApi from "@/lib/api/auth";
import { parseApiError, authErrorMessage } from "@/lib/auth/errors";

interface Options {
  onSuccess: (resetToken: string) => void;
  onError?: (message: string) => void;
}

export function useVerifyResetCode({ onSuccess, onError }: Options) {
  const [isPending, startTransition] = useTransition();

  const execute = useCallback(
    (email: string, code: string) => {
      startTransition(async () => {
        try {
          const { data } = await authApi.verifyResetCode({
            email: email.trim(),
            code,
          });
          onSuccess(data.reset_token);
        } catch (e) {
          onError?.(authErrorMessage(parseApiError(e), "verify_reset"));
        }
      });
    },
    [onSuccess, onError],
  );

  return { execute, isPending };
}
