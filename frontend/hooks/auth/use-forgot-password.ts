"use client";

import { useCallback, useState, useTransition } from "react";
import * as authApi from "@/lib/api/auth";
import { parseApiError, authErrorMessage } from "@/lib/auth/errors";

interface Options {
  onSuccess: () => void;
}

export function useForgotPassword({ onSuccess }: Options) {
  const [error, setError] = useState<string | null>(null);
  const [isPending, startTransition] = useTransition();

  const execute = useCallback(
    (email: string) => {
      setError(null);
      startTransition(async () => {
        try {
          // Always 202 — anti-enumeration. We advance regardless.
          await authApi.forgotPassword({ email: email.trim() });
          onSuccess();
        } catch (e) {
          const err = parseApiError(e);
          setError(authErrorMessage(err, "forgot"));
        }
      });
    },
    [onSuccess],
  );

  return { execute, isPending, error, clearError: () => setError(null) };
}
