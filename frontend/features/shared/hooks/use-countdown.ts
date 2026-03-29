"use client";

import { useState, useEffect, useCallback, useRef } from "react";

/**
 * Generic countdown timer.
 * Uses a ref for the running flag so the interval is created once and
 * cleaned up only when the countdown finishes or is reset — not on every tick.
 */
export function useCountdown() {
  const [seconds, setSeconds] = useState(0);
  const runningRef = useRef(false);

  useEffect(() => {
    if (seconds <= 0) {
      runningRef.current = false;
      return;
    }

    runningRef.current = true;
    const id = setInterval(() => {
      setSeconds((s) => {
        const next = Math.max(0, s - 1);
        if (next === 0) runningRef.current = false;
        return next;
      });
    }, 1000);

    return () => clearInterval(id);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runningRef.current]);

  const start = useCallback((duration: number) => {
    setSeconds(duration);
  }, []);

  const reset = useCallback(() => {
    runningRef.current = false;
    setSeconds(0);
  }, []);

  const isActive = seconds > 0;
  const formatted = `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;

  return { seconds, isActive, formatted, start, reset };
}
