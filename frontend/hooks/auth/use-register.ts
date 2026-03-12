"use client";

import { useCallback, useState, useTransition } from "react";
import * as authApi from "@/lib/api/auth";
import { parseApiError, authErrorMessage } from "@/lib/auth/errors";
import { PENDING_LOGIN_KEY } from "./use-login";

interface Options {
  /** Called when registration succeeds (or mail delivery failed — account still created). */
  onSuccess: () => void;
  /** Called specifically on 409 email_taken so the form can navigate back to the email step. */
  onEmailTaken: () => void;
}

export function useRegister({ onSuccess, onEmailTaken }: Options) {
  const [error, setError] = useState<string | null>(null);
  const [isPending, startTransition] = useTransition();

  const execute = useCallback(
    (name: string, email: string, password: string) => {
      setError(null);
      startTransition(async () => {
        try {
          await authApi.register({
            display_name: name.trim(),
            email: email.trim(),
            password,
          });
          // Stash credentials so useVerifyEmail can auto-login after OTP confirmation,
          // giving the same seamless flow as the email-not-verified login path.
          sessionStorage.setItem(
            PENDING_LOGIN_KEY,
            JSON.stringify({ identifier: email.trim(), password }),
          );
          onSuccess();
        } catch (e) {
          const err = parseApiError(e);

          // 503 = account created but email dispatch failed — still advance to OTP
          if (err.status === 503) {
            sessionStorage.setItem(
              PENDING_LOGIN_KEY,
              JSON.stringify({ identifier: email.trim(), password }),
            );
            onSuccess();
            return;
          }

          if (err.status === 409 && err.code === "email_taken") {
            onEmailTaken();
            return;
          }

          setError(authErrorMessage(err, "register"));
        }
      });
    },
    [onSuccess, onEmailTaken],
  );

  return {
    execute,
    isPending,
    error,
    clearError: () => setError(null),
  };
}
