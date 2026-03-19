import { apiClient, publicClient } from "./http/client";
import type {
  LoginPayload,
  LoginResponse,
  RegisterPayload,
  VerifyEmailPayload,
  ResendVerificationPayload,
  ForgotPasswordPayload,
  VerifyResetCodePayload,
  VerifyResetCodeResponse,
  ResetPasswordPayload,
  ChangePasswordPayload,
  RequestUnlockPayload,
  ConfirmUnlockPayload,
  RefreshResponse,
  MessageResponse,
} from "./types";

export const login = (payload: LoginPayload) =>
  apiClient.post<LoginResponse>("/auth/login", payload);

export const register = (payload: RegisterPayload) =>
  apiClient.post<MessageResponse>("/auth/register", payload);

export const verifyEmail = (payload: VerifyEmailPayload) =>
  publicClient.post<MessageResponse>("/auth/verification", payload);

export const resendVerification = (payload: ResendVerificationPayload) =>
  publicClient.post<MessageResponse>("/auth/verification/resend", payload);

export const forgotPassword = (payload: ForgotPasswordPayload) =>
  publicClient.post<MessageResponse>("/auth/password/reset", payload);

export const verifyResetCode = (payload: VerifyResetCodePayload) =>
  publicClient.post<VerifyResetCodeResponse>("/auth/password/reset/verify", payload);

export const resetPassword = (payload: ResetPasswordPayload) =>
  publicClient.put<MessageResponse>("/auth/password/reset", payload);

export const requestUnlock = (payload: RequestUnlockPayload) =>
  publicClient.post<MessageResponse>("/auth/unlock", payload);

export const confirmUnlock = (payload: ConfirmUnlockPayload) =>
  publicClient.put<MessageResponse>("/auth/unlock", payload);

export const changePassword = (payload: ChangePasswordPayload) =>
  apiClient.patch<MessageResponse>("/auth/password", payload);

export const logout = () => apiClient.post<void>("/auth/logout");

export const refreshTokens = () =>
  apiClient.post<RefreshResponse>("/auth/refresh");
