import { apiClient, publicClient } from "./http/client";
import type {
  TelegramAuthPayload,
  TelegramCallbackResponse,
  MessageResponse,
} from "./types";

export function getGoogleOAuthUrl(): string {
  const base = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080/api/v1";
  return `${base}/oauth/google`;
}

export const unlinkGoogle = () =>
  apiClient.delete<void>("/oauth/google");

export const telegramCallback = (payload: TelegramAuthPayload) =>
  publicClient.post<TelegramCallbackResponse>("/oauth/telegram/callback", payload);

export const linkTelegram = (payload: TelegramAuthPayload) =>
  apiClient.put<void>("/oauth/telegram", payload);

export const unlinkTelegram = () =>
  apiClient.delete<void>("/oauth/telegram");
