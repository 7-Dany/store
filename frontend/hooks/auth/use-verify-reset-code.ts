"use client";

import { useCallback, useState, useTransition } from "react";
import * as authApi from "@/lib/api/auth";
import { parseApiError, authErrorMessage } from "@/lib/auth/errors";

interface Options {
  onSuccess: (resetToken: string) => void;
}

export function useVerifyResetCode({ onSuccess }: Options) {
  const [error, setError] = useState<string | null>(null);
  const [isPending, startTransition] = useTransition();

  const execute = useCallback(
    (email: string, code: string) => {
      setError(null);
      startTransition(async () => {
        try {
          const { data } = await authApi.verifyResetCode({
            email: email.trim(),
            code,
          });
          onSuccess(data.reset_token);
        } catch (e) {
          setError(authErrorMessage(parseApiError(e), "verify_reset"));
        }
      });
    },
    [onSuccess],
  );

  return { execute, isPending, error, clearError: () => setError(null) };
}
