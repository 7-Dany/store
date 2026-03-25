"use client";

import { useCallback, useReducer, useTransition } from "react";
import * as profileApi from "@/lib/api/profile";
import { parseApiError, errorMessage } from "@/lib/auth/errors";
import { useCountdown } from "@/hooks/shared/use-countdown";

// ─── State machine ────────────────────────────────────────────────────────────
// Step 1: User enters new email → POST /profile/me/email → OTP to CURRENT email
// Step 2: OTP from current address → POST /profile/me/email/verify → grant_token + OTP to NEW email
// Step 3: OTP from new address + grant_token → PUT /profile/me/email → done, all sessions revoked

export type EmailChangeStep =
  | "request"          // step 1 form
  | "otp_current"      // step 2 form
  | "otp_new"          // step 3 form
  | "done";

interface State {
  step: EmailChangeStep;
  loading: boolean;
  newEmail: string;
  grantToken: string;
  grantExpiresIn: number;
  error: string | null;
}

type Action =
  | { type: "REQUESTING" }
  | { type: "OTP_CURRENT_SENT"; newEmail: string }
  | { type: "VERIFYING_CURRENT" }
  | { type: "OTP_NEW_SENT"; grantToken: string; expiresIn: number }
  | { type: "CONFIRMING" }
  | { type: "DONE" }
  | { type: "ERROR"; error: string }
  | { type: "CLEAR_ERROR" }
  | { type: "RESET" };

function reducer(state: State, action: Action): State {
  switch (action.type) {
    case "REQUESTING":
      return { ...state, loading: true, error: null };
    case "OTP_CURRENT_SENT":
      return { ...state, loading: false, step: "otp_current", newEmail: action.newEmail, error: null };
    case "VERIFYING_CURRENT":
      return { ...state, loading: true, error: null };
    case "OTP_NEW_SENT":
      return { ...state, loading: false, step: "otp_new", grantToken: action.grantToken, grantExpiresIn: action.expiresIn, error: null };
    case "CONFIRMING":
      return { ...state, loading: true, error: null };
    case "DONE":
      return { ...state, loading: false, step: "done", error: null };
    case "ERROR":
      return { ...state, loading: false, error: action.error };
    case "CLEAR_ERROR":
      return { ...state, error: null };
    case "RESET":
      return { step: "request", loading: false, newEmail: "", grantToken: "", grantExpiresIn: 0, error: null };
  }
}

// ─── Hook ─────────────────────────────────────────────────────────────────────

export function useChangeEmail() {
  const [state, dispatch] = useReducer(reducer, {
    step: "request",
    loading: false,
    newEmail: "",
    grantToken: "",
    grantExpiresIn: 0,
    error: null,
  });
  const [, startTransition] = useTransition();
  const countdown = useCountdown();

  const reset      = useCallback(() => dispatch({ type: "RESET" }),       []);
  const clearError = useCallback(() => dispatch({ type: "CLEAR_ERROR" }), []);

  /** Step 1 — request email change (sends OTP to current email). */
  const requestChange = useCallback((newEmail: string) => {
    dispatch({ type: "REQUESTING" });
    startTransition(async () => {
      try {
        await profileApi.requestEmailChange({ new_email: newEmail.trim() });
        dispatch({ type: "OTP_CURRENT_SENT", newEmail: newEmail.trim() });
        countdown.start(120);
      } catch (e) {
        const err = parseApiError(e);
        dispatch({ type: "ERROR", error: errorMessage(err, "email_change_request") });
      }
    });
  }, [countdown]);

  /** Step 1 resend — re-request while on OTP current step. */
  const resendCurrentOtp = useCallback(() => {
    if (countdown.isActive) return;
    startTransition(async () => {
      try {
        await profileApi.requestEmailChange({ new_email: state.newEmail });
        countdown.start(120);
      } catch (e) {
        const err = parseApiError(e);
        dispatch({ type: "ERROR", error: errorMessage(err, "email_change_request") });
      }
    });
  }, [state.newEmail, countdown]);

  /** Step 2 — verify current email OTP → receive grant_token. */
  const verifyCurrentOtp = useCallback((code: string) => {
    dispatch({ type: "VERIFYING_CURRENT" });
    startTransition(async () => {
      try {
        const { data } = await profileApi.verifyCurrentEmail({ code });
        dispatch({ type: "OTP_NEW_SENT", grantToken: data.grant_token, expiresIn: data.expires_in });
        countdown.start(data.expires_in);
      } catch (e) {
        const err = parseApiError(e);
        dispatch({ type: "ERROR", error: errorMessage(err, "email_change_verify") });
      }
    });
  }, [countdown]);

  /** Step 3 — confirm with new email OTP + grant token. */
  const confirmChange = useCallback((code: string) => {
    dispatch({ type: "CONFIRMING" });
    startTransition(async () => {
      try {
        await profileApi.confirmEmailChange({ grant_token: state.grantToken, code });
        dispatch({ type: "DONE" });
        // All sessions are revoked server-side. Use a hard navigation to
        // /api/auth/logout (GET) which clears the httpOnly cookies and
        // redirects to /login. A soft router.push won't work here because
        // the proxy would see the stale cookie and redirect back to /dashboard.
        setTimeout(() => { window.location.href = "/api/auth/logout"; }, 2000);
      } catch (e) {
        const err = parseApiError(e);
        dispatch({ type: "ERROR", error: errorMessage(err, "email_change_confirm") });
      }
    });
  }, [state.grantToken]);

  return {
    state,
    countdown,
    reset,
    clearError,
    requestChange,
    resendCurrentOtp,
    verifyCurrentOtp,
    confirmChange,
  };
}
