"use client";

import { useState, useEffect } from "react";

const MOBILE_QUERY = "(max-width: 767px)";

/**
 * Returns true when the viewport matches the mobile breakpoint.
 * Subscribes only to the matchMedia change event (a boolean flip) rather than
 * raw window.innerWidth, avoiding re-renders on every pixel change.
 * Returns false during SSR to avoid hydration mismatches.
 */
export function useIsMobile(): boolean {
  const [isMobile, setIsMobile] = useState(false);

  useEffect(() => {
    const mql = window.matchMedia(MOBILE_QUERY);
    // Set initial value after mount (client-only)
    setIsMobile(mql.matches);

    const onChange = (e: MediaQueryListEvent) => setIsMobile(e.matches);
    mql.addEventListener("change", onChange);
    return () => mql.removeEventListener("change", onChange);
  }, []);

  return isMobile;
}
