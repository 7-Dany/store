"use client";

import { useCallback, useReducer, useTransition } from "react";
import * as profileApi from "@/lib/api/profile";
import { parseApiError, errorMessage } from "@/lib/auth/errors";
import type { DeletionMethod } from "@/lib/api/types";

// ─── State machine ────────────────────────────────────────────────────────────

export type DeletionStep =
  | "idle"
  | "loading_method"
  | "confirm_password"
  | "confirm_otp_init"
  | "confirm_otp"
  | "confirm_telegram"
  | "deleting"
  | "done"
  | "error";

interface State {
  step: DeletionStep;
  method: DeletionMethod | null;
  error: string | null;
  otpExpiresIn: number | null;
}

type Action =
  | { type: "START" }
  | { type: "METHOD_LOADED"; method: DeletionMethod }
  | { type: "OTP_SENT"; expiresIn: number }
  | { type: "DELETING" }
  | { type: "DONE" }
  | { type: "ERROR"; error: string }
  | { type: "RESET" };

function reducer(state: State, action: Action): State {
  switch (action.type) {
    case "START":
      return { ...state, step: "loading_method", error: null };
    case "METHOD_LOADED":
      return {
        ...state,
        method: action.method,
        step:
          action.method === "password"    ? "confirm_password"  :
          action.method === "email_otp"   ? "confirm_otp_init"  :
                                            "confirm_telegram",
      };
    case "OTP_SENT":
      return { ...state, step: "confirm_otp", otpExpiresIn: action.expiresIn };
    case "DELETING":
      return { ...state, step: "deleting", error: null };
    case "DONE":
      return { ...state, step: "done" };
    case "ERROR":
      return { ...state, step: "error", error: action.error };
    case "RESET":
      return { step: "idle", method: null, error: null, otpExpiresIn: null };
  }
}

// ─── Hook ─────────────────────────────────────────────────────────────────────

interface Options {
  /** Called after the account is successfully scheduled for deletion. */
  onDeleted: () => void;
  /**
   * Called when an OTP is dispatched so the caller can start a countdown
   * directly in the event chain — no useEffect needed.
   */
  onOtpSent?: (expiresIn: number) => void;
}

export function useDeleteAccount({ onDeleted, onOtpSent }: Options) {
  const [state, dispatch] = useReducer(reducer, {
    step: "idle",
    method: null,
    error: null,
    otpExpiresIn: null,
  });
  const [, startTransition] = useTransition();

  /** Step 1 — load which confirmation method is required. */
  const start = useCallback(() => {
    dispatch({ type: "START" });
    startTransition(async () => {
      try {
        const res = await profileApi.getDeletionMethod();
        const method = res.data.deletion_method;
        dispatch({ type: "METHOD_LOADED", method });

        if (method === "email_otp") {
          const del = await profileApi.deleteAccount({});
          if (del.status === 202) {
            const expiresIn = del.data.expires_in ?? 900;
            dispatch({ type: "OTP_SENT", expiresIn });
            // Notify the caller so it can start a countdown without an effect.
            onOtpSent?.(expiresIn);
          }
        }
      } catch (e) {
        const err = parseApiError(e);
        dispatch({ type: "ERROR", error: errorMessage(err, "delete_account") });
      }
    });
  }, [onOtpSent]);

  /** Step 2a — password path. */
  const confirmWithPassword = useCallback(
    (password: string) => {
      dispatch({ type: "DELETING" });
      startTransition(async () => {
        try {
          await profileApi.deleteAccount({ password });
          dispatch({ type: "DONE" });
          onDeleted();
        } catch (e) {
          const err = parseApiError(e);
          dispatch({ type: "ERROR", error: errorMessage(err, "delete_account") });
        }
      });
    },
    [onDeleted],
  );

  /** Step 2b — email OTP path. */
  const confirmWithOtp = useCallback(
    (code: string) => {
      dispatch({ type: "DELETING" });
      startTransition(async () => {
        try {
          await profileApi.deleteAccount({ code });
          dispatch({ type: "DONE" });
          onDeleted();
        } catch (e) {
          const err = parseApiError(e);
          dispatch({ type: "ERROR", error: errorMessage(err, "delete_account") });
        }
      });
    },
    [onDeleted],
  );

  const reset = useCallback(() => dispatch({ type: "RESET" }), []);

  return { state, start, confirmWithPassword, confirmWithOtp, reset };
}
