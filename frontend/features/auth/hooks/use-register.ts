"use client";

import { useCallback, useTransition } from "react";
import * as authApi from "@/lib/api/auth";
import { parseApiError, authErrorMessage } from "@/lib/auth/errors";
import { PENDING_LOGIN_KEY } from "./use-login";

interface Options {
  onSuccess: () => void;
  onEmailTaken: () => void;
  onError?: (message: string) => void;
}

export function useRegister({ onSuccess, onEmailTaken, onError }: Options) {
  const [isPending, startTransition] = useTransition();

  const execute = useCallback(
    (name: string, email: string, password: string) => {
      startTransition(async () => {
        try {
          await authApi.register({
            display_name: name.trim(),
            email: email.trim(),
            password,
          });
          sessionStorage.setItem(
            PENDING_LOGIN_KEY,
            JSON.stringify({ identifier: email.trim(), password }),
          );
          onSuccess();
        } catch (e) {
          const err = parseApiError(e);

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

          onError?.(authErrorMessage(err, "register"));
        }
      });
    },
    [onSuccess, onEmailTaken, onError],
  );

  return { execute, isPending };
}
