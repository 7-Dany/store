import { isAxiosError } from "axios";

// ─── Error shape ─────────────────────────────────────────────────────────────

interface ApiErrorBody {
  code: string;
  message: string;
}

/**
 * Normalised error thrown by all auth API functions.
 * Every `catch` block in hooks deals with this shape only.
 */
export class ApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

// ─── Parser ──────────────────────────────────────────────────────────────────

/** Convert any thrown value into a typed ApiError. */
export function parseApiError(error: unknown): ApiError {
  if (error instanceof ApiError) return error;

  if (isAxiosError<ApiErrorBody>(error)) {
    if (error.response) {
      const { status, data } = error.response;
      return new ApiError(
        status,
        data?.code ?? "unknown",
        data?.message ?? "An error occurred.",
      );
    }

    // Network / timeout
    return new ApiError(
      0,
      "network_error",
      "Could not reach the server. Check your connection.",
    );
  }

  return new ApiError(0, "unknown", "An unexpected error occurred.");
}

// ─── User-facing messages ─────────────────────────────────────────────────────

type AuthContext = "login" | "register" | "verify" | "resend" | "forgot" | "verify_reset" | "reset";

/**
 * Maps a parsed ApiError to the human-readable message shown in the UI.
 * All error copy lives here — never scattered across hooks or components.
 */
export function authErrorMessage(error: ApiError, ctx: AuthContext): string {
  const { status, code, message } = error;

  if (status === 0) return message; // network / unknown
  if (status === 502)
    return "Service temporarily unavailable. Please try again.";

  if (status === 429) {
    if (code === "login_locked")
      return "Too many failed attempts. Please wait before trying again.";
    return "Too many requests. Please slow down.";
  }

  switch (ctx) {
    case "login":
      if (status === 401) return "Incorrect email/username or password.";
      if (status === 403) {
        if (code === "email_not_verified")
          return "Your email isn't verified yet. Check your inbox.";
        if (code === "account_inactive")
          return "Your account has been suspended. Contact support.";
      }
      if (status === 423)
        return "Your account is locked. Please contact support.";
      break;

    case "register":
      if (status === 409) {
        if (code === "email_taken")
          return "An account with that email already exists.";
        if (code === "username_taken") return "That username is already taken.";
      }
      if (status === 422) return message; // field-level message from backend
      break;

    case "verify":
      if (status === 422)
        return "Incorrect or expired code. Request a new one below.";
      if (status === 423) return "Account locked. Please contact support.";
      if (status === 429)
        return "Too many incorrect attempts. Please request a new code.";
      break;

    case "resend":
      // 202 is always returned — only network errors surface here
      break;

    case "forgot":
      // 202 is always returned — anti-enumeration, no user-visible error
      if (status === 429) return "Too many requests. Please wait a few minutes before trying again.";
      break;

    case "verify_reset":
      if (status === 410) return "This reset code has expired. Please request a new one.";
      if (status === 422) return "Incorrect or expired code. Check it and try again.";
      if (status === 429) return "Too many incorrect attempts. Please request a new reset code.";
      break;

    case "reset":
      if (status === 422) {
        if (code === "validation_error") return message; // e.g. "new password must differ"
      }
      break;
  }

  return message || "Something went wrong. Please try again.";
}
