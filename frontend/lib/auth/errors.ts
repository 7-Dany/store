import { isAxiosError } from "axios";
import type { ApiErrorBody } from "@/lib/api/types";

/**
 * Normalised error thrown by all API functions.
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

export type AuthContext =
  | "login"
  | "register"
  | "verify"
  | "resend"
  | "forgot"
  | "verify_reset"
  | "reset"
  | "change_password"
  | "unlock_request"
  | "unlock_confirm";

export type ProfileContext =
  | "get_profile"
  | "update_profile"
  | "update_username"
  | "check_username"
  | "get_sessions"
  | "revoke_session"
  | "email_change_request"
  | "email_change_verify"
  | "email_change_confirm"
  | "set_password"
  | "delete_account"
  | "cancel_deletion";

export type OAuthContext =
  | "google_unlink"
  | "telegram_callback"
  | "telegram_link"
  | "telegram_unlink";

export type AdminContext =
  | "lock_user"
  | "unlock_user"
  | "get_lock_status"
  | "assign_role"
  | "get_role"
  | "remove_role"
  | "grant_permission"
  | "list_permissions"
  | "revoke_permission";

export type RbacContext =
  | "list_roles"
  | "create_role"
  | "update_role"
  | "delete_role"
  | "role_permissions"
  | "add_role_permission"
  | "remove_role_permission"
  | "list_permissions"
  | "assign_owner"
  | "initiate_transfer"
  | "accept_transfer"
  | "cancel_transfer";

type ErrorContext = AuthContext | ProfileContext | OAuthContext | AdminContext | RbacContext;

/**
 * Maps a parsed ApiError to the human-readable message shown in the UI.
 * All error copy lives here — never scattered across hooks or components.
 */
export function errorMessage(error: ApiError, ctx: ErrorContext): string {
  const { status, code, message } = error;

  if (status === 0) return message;
  if (status === 502 || status === 503)
    return "Service temporarily unavailable. Please try again.";

  if (status === 429) {
    if (code === "login_locked")
      return "Too many failed attempts. Please wait before trying again.";
    if (code === "cooldown_active")
      return "Please wait before requesting another code.";
    if (code === "deletion_token_cooldown")
      return "A deletion code was already sent. Please wait before requesting another.";
    if (code === "too_many_attempts")
      return "Too many incorrect attempts. Please request a new code.";
    return "Too many requests. Please slow down.";
  }

  if (status === 403 && code === "forbidden")
    return "You don't have permission to perform this action.";
  if (status === 401 && (code === "missing_token" || code === "unauthorized"))
    return "Your session has expired. Please sign in again.";
  if (status === 401 && code === "token_revoked")
    return "Your session was revoked. Please sign in again.";
  if (status === 401 && code === "token_reuse_detected")
    return "A security issue was detected with your session. Please sign in again.";

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
        return "Your account is locked. Please contact support or use the unlock flow.";
      break;

    case "register":
      if (status === 409) {
        if (code === "email_taken") return "An account with that email already exists.";
        if (code === "username_taken") return "That username is already taken.";
      }
      if (status === 422) return message;
      break;

    case "verify":
      if (status === 422) return "Incorrect or expired code. Request a new one below.";
      if (status === 423) return "Account locked. Please contact support.";
      break;

    case "resend": break;
    case "forgot": break;

    case "verify_reset":
      if (status === 410) return "This reset code has expired. Please request a new one.";
      if (status === 422) return "Incorrect or expired code. Check it and try again.";
      break;

    case "reset":
      if (status === 422) return message;
      break;

    case "change_password":
      if (status === 401 && code === "invalid_credentials")
        return "Your current password is incorrect.";
      if (status === 422) return message;
      break;

    case "unlock_request": break;

    case "unlock_confirm":
      if (status === 422) return "Incorrect or expired unlock code. Request a new one.";
      if (status === 423)
        return "This account has been admin-locked. Please contact support.";
      break;

    case "update_profile":
      if (status === 422) return message;
      break;

    case "update_username":
      if (status === 409 && code === "username_taken") return "That username is already taken.";
      if (status === 422 && code === "same_username") return "That is already your current username.";
      if (status === 422) return message;
      break;

    case "email_change_request":
      if (status === 409 && code === "email_taken") return "That email address is already registered.";
      if (status === 422 && code === "same_email") return "That is already your current email address.";
      if (status === 422) return message;
      break;

    case "email_change_verify":
      if (status === 422) return "Incorrect or expired code. Check it and try again.";
      break;

    case "email_change_confirm":
      if (status === 409 && code === "email_taken")
        return "That email address was just registered by another account. Please start over.";
      if (status === 422 && code === "invalid_grant_token")
        return "Your verification session has expired. Please start the email change again.";
      if (status === 422) return "Incorrect or expired code. Check it and try again.";
      break;

    case "set_password":
      if (status === 422 && code === "password_already_set")
        return "Your account already has a password. Use the change password option instead.";
      if (status === 422) return message;
      break;

    case "delete_account":
      if (status === 401 && code === "invalid_credentials") return "Incorrect password.";
      if (status === 401 && code === "invalid_telegram_auth")
        return "Telegram authentication failed or expired. Please try again.";
      if (status === 409 && code === "already_pending_deletion")
        return "Your account is already scheduled for deletion.";
      if (status === 422) return message;
      break;

    case "cancel_deletion":
      if (status === 409 && code === "not_pending_deletion")
        return "There is no pending deletion to cancel.";
      break;

    case "google_unlink":
      if (status === 404) return "No Google account is linked to your profile.";
      if (status === 422 && code === "last_auth_method")
        return "Google is your only sign-in method. Set a password or link another provider first.";
      break;

    case "telegram_callback":
      if (status === 401 && code === "invalid_signature")
        return "Telegram authentication failed. Please try again.";
      if (status === 401 && code === "auth_date_expired")
        return "The Telegram session expired. Please re-open the login widget.";
      if (status === 403 && code === "account_inactive")
        return "This account has been suspended. Contact support.";
      if (status === 409 && code === "provider_uid_taken")
        return "This Telegram account is already linked to a different profile.";
      if (status === 423) return "This account is locked. Please contact support.";
      break;

    case "telegram_link":
      if (status === 401 && code === "invalid_signature")
        return "Telegram authentication failed. Please try again.";
      if (status === 401 && code === "auth_date_expired")
        return "The Telegram session expired. Please re-open the widget.";
      if (status === 409 && code === "provider_already_linked")
        return "You already have a Telegram account linked.";
      if (status === 409 && code === "provider_uid_taken")
        return "This Telegram account is linked to a different profile.";
      break;

    case "telegram_unlink":
      if (status === 404) return "No Telegram account is linked to your profile.";
      if (status === 409 && code === "last_auth_method")
        return "Telegram is your only sign-in method. Set a password or link another provider first.";
      break;

    case "lock_user":
      if (status === 409 && code === "cannot_lock_self") return "You cannot lock your own account.";
      if (status === 409 && code === "cannot_lock_owner") return "Owner accounts cannot be admin-locked.";
      if (status === 422) return message;
      break;

    case "assign_role":
      if (status === 409 && code === "cannot_modify_own_role") return "You cannot modify your own role assignment.";
      if (status === 409 && code === "cannot_reassign_owner") return "The owner role cannot be assigned via this route.";
      if (status === 422) return message;
      break;

    case "remove_role":
      if (status === 409 && code === "cannot_modify_own_role") return "You cannot remove your own role assignment.";
      if (status === 409 && code === "last_owner_removal") return "Cannot remove the last active owner.";
      if (status === 404 && code === "user_role_not_found") return "This user has no active role assignment.";
      break;

    case "grant_permission":
      if (status === 403 && code === "privilege_escalation")
        return "You cannot grant a permission you do not hold yourself.";
      if (status === 409 && code === "permission_already_granted")
        return "This user already has an active grant for this permission.";
      if (status === 422) return message;
      break;

    case "revoke_permission":
      if (status === 404 && code === "grant_not_found") return "Permission grant not found.";
      break;

    case "create_role":
    case "update_role":
      if (status === 409 && code === "system_role_immutable") return "System roles cannot be modified.";
      if (status === 422) return message;
      break;

    case "delete_role":
      if (status === 409 && code === "system_role_immutable") return "System roles cannot be deleted.";
      break;

    case "add_role_permission":
      if (status === 409 && code === "grant_already_exists")
        return "This permission is already assigned to the role. Remove it first to change its settings.";
      if (status === 422) return message;
      break;

    case "assign_owner":
      if (status === 403) return "Invalid bootstrap secret.";
      if (status === 409 && code === "owner_already_exists") return "An active owner already exists.";
      if (status === 422) return message;
      break;

    case "initiate_transfer":
      if (status === 409 && code === "transfer_already_pending")
        return "A transfer is already pending. Cancel it before starting a new one.";
      if (status === 409 && code === "user_is_already_owner") return "That user is already the owner.";
      if (status === 409 && code === "cannot_transfer_to_self") return "You cannot transfer ownership to yourself.";
      if (status === 422) return message;
      break;

    case "accept_transfer":
      if (status === 410 && code === "token_invalid")
        return "This transfer token is invalid, expired, or has already been used.";
      if (status === 409 && code === "initiator_not_owner")
        return "The initiating user no longer holds the owner role.";
      if (status === 422 && code === "user_not_eligible")
        return "Your account is no longer eligible to accept this transfer.";
      break;

    case "cancel_transfer":
      if (status === 404 && code === "no_pending_transfer")
        return "There is no pending ownership transfer to cancel.";
      break;
  }

  return message || "Something went wrong. Please try again.";
}

/** @deprecated Use `errorMessage` with the broader `ErrorContext` type. */
export function authErrorMessage(error: ApiError, ctx: AuthContext): string {
  return errorMessage(error, ctx);
}
