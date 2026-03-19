import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/** Capitalise the first character of a string. */
export function capitalize(s: string): string {
  if (!s) return s;
  return s.charAt(0).toUpperCase() + s.slice(1);
}

/**
 * Derive 1–2 uppercase initials from a display name.
 * "Alice Martin" → "AM", "Alice" → "A", "" → "U"
 */
export function getInitials(displayName: string): string {
  if (!displayName) return "U";
  return displayName
    .split(" ")
    .map((w) => w[0])
    .slice(0, 2)
    .join("")
    .toUpperCase();
}

/**
 * Format an ISO date string for display.
 * @param iso   ISO 8601 date string
 * @param opts  Intl.DateTimeFormatOptions — defaults to { month: "long", year: "numeric" }
 */
export function formatDate(
  iso: string,
  opts: Intl.DateTimeFormatOptions = { month: "long", year: "numeric" },
): string {
  return new Date(iso).toLocaleDateString("en-US", opts);
}
