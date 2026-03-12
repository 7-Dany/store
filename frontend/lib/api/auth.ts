import { apiClient, publicClient } from "./client";

// ─── Request / Response types ────────────────────────────────────────────────

export interface LoginPayload {
  identifier: string;
  password: string;
}

export interface RegisterPayload {
  display_name: string;
  email: string;
  password: string;
}

export interface VerifyEmailPayload {
  email: string;
  code: string;
}

export interface ResendVerificationPayload {
  email: string;
}

export interface ForgotPasswordPayload {
  email: string;
}

export interface VerifyResetCodePayload {
  email: string;
  code: string;
}

export interface VerifyResetCodeResponse {
  reset_token: string;
  expires_in: number;
}

export interface ResetPasswordPayload {
  reset_token: string;
  new_password: string;
}

export interface MessageResponse {
  message: string;
}

// ─── API calls ───────────────────────────────────────────────────────────────

/**
 * POST /api/auth/login  (proxied)
 * On success the Next.js route sets the `session` httpOnly cookie.
 */
export const login = (payload: LoginPayload) =>
  apiClient.post<void>("/auth/login", payload);

/**
 * POST /api/auth/register  (proxied)
 * Creates the account and dispatches a 6-digit OTP to the email.
 */
export const register = (payload: RegisterPayload) =>
  apiClient.post<MessageResponse>("/auth/register", payload);

/**
 * POST /auth/verify-email  (direct — no session cookie involved)
 * Verifies the 6-digit OTP. On success the account becomes active.
 */
export const verifyEmail = (payload: VerifyEmailPayload) =>
  publicClient.post<MessageResponse>("/auth/verify-email", payload);

/**
 * POST /auth/resend-verification  (direct — no session cookie involved)
 * Dispatches a fresh OTP. Always returns 202 — never infer account state.
 */
export const resendVerification = (payload: ResendVerificationPayload) =>
  publicClient.post<MessageResponse>("/auth/resend-verification", payload);

/**
 * POST /auth/forgot-password  (direct — always 202, anti-enumeration)
 */
export const forgotPassword = (payload: ForgotPasswordPayload) =>
  publicClient.post<MessageResponse>("/auth/forgot-password", payload);

/**
 * POST /auth/verify-reset-code  (direct — validates OTP, returns grant token)
 */
export const verifyResetCode = (payload: VerifyResetCodePayload) =>
  publicClient.post<VerifyResetCodeResponse>("/auth/verify-reset-code", payload);

/**
 * POST /auth/reset-password  (direct — consumes grant token, sets new password)
 */
export const resetPassword = (payload: ResetPasswordPayload) =>
  publicClient.post<MessageResponse>("/auth/reset-password", payload);
