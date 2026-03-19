"use client";

import { useEffect } from "react";
import { useEffectEvent } from "react";
import { toast } from "sonner";

const OAUTH_ERROR_MESSAGES: Record<string, string> = {
  oauth_session_expired: "The sign-in session expired. Please try again.",
  oauth_cancelled: "Sign-in was cancelled. Please try again.",
  google_link_failed: "Couldn't link your Google account. Please try again.",
  account_locked: "Your account is locked. Use the unlock flow to regain access.",
  account_inactive: "Your account has been suspended. Please contact support.",
};

interface Props {
  reset?: boolean;
  verified?: boolean;
  error?: string;
}

export function LoginNotices({ reset, verified, error }: Props) {
  const fireNotices = useEffectEvent(() => {
    if (reset)    toast.success("Password reset successfully. Sign in with your new password.");
    if (verified) toast.success("Email verified! You can now sign in.");
    if (error) {
      const msg = OAUTH_ERROR_MESSAGES[error] ?? "Something went wrong. Please try again.";
      toast.error(msg);
    }
  });

  useEffect(() => {
    fireNotices();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  return null;
}
