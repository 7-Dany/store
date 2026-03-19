"use client";

import { useCallback, useTransition } from "react";
import * as authApi from "@/lib/api/auth";
import { parseApiError, authErrorMessage } from "@/lib/auth/errors";

interface Options {
  onSuccess: () => void;
  onError?: (message: string) => void;
}

export function useForgotPassword({ onSuccess, onError }: Options) {
  const [isPending, startTransition] = useTransition();

  const execute = useCallback(
    (email: string) => {
      startTransition(async () => {
        try {
          // Always 202 — anti-enumeration. Advance regardless.
          await authApi.forgotPassword({ email: email.trim() });
          onSuccess();
        } catch (e) {
          onError?.(authErrorMessage(parseApiError(e), "forgot"));
        }
      });
    },
    [onSuccess, onError],
  );

  return { execute, isPending };
}
