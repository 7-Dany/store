"use client";

import { useState, useEffect, useCallback } from "react";

/**
 * Generic countdown timer.
 * Drives the OTP resend cooldown and any other time-gated actions.
 */
export function useCountdown() {
  const [seconds, setSeconds] = useState(0);

  useEffect(() => {
    if (seconds <= 0) return;
    const id = setInterval(() => setSeconds((s) => Math.max(0, s - 1)), 1000);
    return () => clearInterval(id);
  }, [seconds]);

  const start = useCallback((duration: number) => setSeconds(duration), []);
  const reset = useCallback(() => setSeconds(0), []);
  const isActive = seconds > 0;

  /** "1:45" format */
  const formatted = `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;

  return { seconds, isActive, formatted, start, reset };
}
