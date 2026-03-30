"use client";

import { useCallback, useReducer, useTransition } from "react";
import { useRouter } from "next/navigation";
import * as authApi from "@/lib/api/auth";
import { parseApiError, authErrorMessage } from "@/lib/auth/errors";
import { useCountdown } from "@/features/shared/hooks/use-countdown";

// ─── State machine ────────────────────────────────────────────────────────────

export type UnlockStep = "email" | "code" | "done";

interface State {
  step: UnlockStep;
  email: string;
  error: string | null;
}

type Action =
  | { type: "CODE_SENT"; email: string }
  | { type: "DONE" }
  | { type: "ERROR"; error: string }
  | { type: "CLEAR_ERROR" };

function reducer(state: State, action: Action): State {
  switch (action.type) {
    case "CODE_SENT":   return { step: "code", email: action.email, error: null };
    case "DONE":        return { ...state, step: "done", error: null };
    case "ERROR":       return { ...state, error: action.error };
    case "CLEAR_ERROR": return { ...state, error: null };
  }
}

// ─── Hook ─────────────────────────────────────────────────────────────────────

export function useUnlock(initialEmail = "") {
  const router = useRouter();
  const [state, dispatch] = useReducer(reducer, {
    step: "email",
    email: initialEmail,
    error: null,
  });
  const [isPending, startTransition] = useTransition();
  const countdown = useCountdown();

  /** Step 1 — send the unlock code. Always 202, anti-enumeration. */
  const requestCode = useCallback(
    (email: string) => {
      if (countdown.isActive) return;
      dispatch({ type: "CLEAR_ERROR" });
      startTransition(async () => {
        try {
          await authApi.requestUnlock({ email: email.trim() });
          dispatch({ type: "CODE_SENT", email: email.trim() });
          countdown.start(60);
        } catch (e) {
          const err = parseApiError(e);
          dispatch({ type: "ERROR", error: authErrorMessage(err, "unlock_request") });
        }
      });
    },
    [countdown],
  );

  /** Step 1 again — resend from the code step. */
  const resendCode = useCallback(() => {
    if (countdown.isActive) return;
    dispatch({ type: "CLEAR_ERROR" });
    startTransition(async () => {
      try {
        await authApi.requestUnlock({ email: state.email });
        countdown.start(60);
      } catch {
        // Silently ignore — anti-enumeration endpoint always 202
      }
    });
  }, [state.email, countdown]);

  /** Step 2 — confirm the unlock code. */
  const confirmCode = useCallback(
    (code: string) => {
      dispatch({ type: "CLEAR_ERROR" });
      startTransition(async () => {
        try {
          await authApi.confirmUnlock({ email: state.email, code });
          dispatch({ type: "DONE" });
          setTimeout(() => router.push(`/login?email=${encodeURIComponent(state.email)}`), 1800);
        } catch (e) {
          const err = parseApiError(e);
          dispatch({ type: "ERROR", error: authErrorMessage(err, "unlock_confirm") });
        }
      });
    },
    [state.email, router],
  );

  return { state, isPending, countdown, requestCode, resendCode, confirmCode };
}
