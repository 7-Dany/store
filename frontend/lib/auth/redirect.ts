export function sanitizeRelativePath(
  raw: string | null | undefined,
  fallback = "/dashboard",
): string {
  if (!raw) return fallback;
  if (!raw.startsWith("/") || raw.startsWith("//") || raw.includes(":")) {
    return fallback;
  }
  return raw;
}

export function loginUrlForCurrentPage(): string {
  if (typeof window === "undefined") {
    return "/login";
  }

  const from = `${window.location.pathname}${window.location.search}${window.location.hash}`;
  return `/login?from=${encodeURIComponent(from)}`;
}
