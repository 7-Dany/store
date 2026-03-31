export function readSetCookieHeaders(headers: Headers): string[] {
  const withGetSetCookie = headers as Headers & {
    getSetCookie?: () => string[];
  };

  if (typeof withGetSetCookie.getSetCookie === "function") {
    const values = withGetSetCookie.getSetCookie();
    if (Array.isArray(values) && values.length > 0) {
      return values;
    }
  }

  const combined = headers.get("set-cookie");
  return combined ? [combined] : [];
}

export function extractCookieValue(
  setCookieHeaders: string[] | string | null | undefined,
  cookieName: string,
): string | null {
  const headers = Array.isArray(setCookieHeaders)
    ? setCookieHeaders
    : setCookieHeaders
      ? [setCookieHeaders]
      : [];

  const escaped = cookieName.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const pattern = new RegExp(`(?:^|,\\s*)${escaped}=([^;]+)`);

  for (const header of headers) {
    const match = header.match(pattern);
    if (match) {
      return match[1];
    }
  }

  return null;
}

export function extractRequestCookie(
  cookieHeader: string | null | undefined,
  cookieName: string,
): string | null {
  if (!cookieHeader) return null;

  const escaped = cookieName.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const pattern = new RegExp(`(?:^|;\\s*)${escaped}=([^;]*)`, "g");

  let match: RegExpExecArray | null;
  let lastNonEmpty: string | null = null;

  while ((match = pattern.exec(cookieHeader)) !== null) {
    const value = match[1] ?? "";
    if (value) {
      lastNonEmpty = value;
    }
  }

  return lastNonEmpty;
}
