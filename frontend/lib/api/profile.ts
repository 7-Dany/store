import { cache } from "react";
import { apiClient, publicClient } from "./http/client";
import type {
  UserProfile,
  UpdateProfilePayload,
  IdentitiesResponse,
  UpdateUsernamePayload,
  UsernameAvailableResponse,
  SessionsResponse,
  RequestEmailChangePayload,
  VerifyCurrentEmailPayload,
  VerifyCurrentEmailResponse,
  ConfirmEmailChangePayload,
  DeletionMethodResponse,
  DeleteAccountPayload,
  DeleteAccountResponse,
  SetPasswordPayload,
  MessageResponse,
} from "./types";

const API_BASE = process.env.API_BASE_URL ?? "http://localhost:8080/api/v1";

// ─── Server-side helpers ──────────────────────────────────────────────────────

export const fetchProfile = cache(async (token: string): Promise<UserProfile | null> => {
  if (!token) return null;
  try {
    const res = await fetch(`${API_BASE}/profile/me`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as UserProfile;
  } catch {
    return null;
  }
});

export const fetchIdentities = cache(async (token: string): Promise<IdentitiesResponse> => {
  if (!token) return { identities: [] };
  try {
    const res = await fetch(`${API_BASE}/profile/me/identities`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return { identities: [] };
    return (await res.json()) as IdentitiesResponse;
  } catch {
    return { identities: [] };
  }
});

export const fetchSessions = cache(async (token: string): Promise<SessionsResponse> => {
  if (!token) return { sessions: [] };
  try {
    const res = await fetch(`${API_BASE}/profile/me/sessions`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return { sessions: [] };
    return (await res.json()) as SessionsResponse;
  } catch {
    return { sessions: [] };
  }
});

export const fetchDeletionMethod = cache(async (token: string): Promise<string | null> => {
  if (!token) return null;
  try {
    const res = await fetch(`${API_BASE}/profile/me/deletion`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return ((await res.json()) as { deletion_method?: string }).deletion_method ?? null;
  } catch {
    return null;
  }
});

// ─── Client-side API ──────────────────────────────────────────────────────────

export const getProfile = () => apiClient.get<UserProfile>("/profile/me");

export const updateProfile = (payload: UpdateProfilePayload) =>
  apiClient.patch<MessageResponse>("/profile/me", payload);

export const getIdentities = () =>
  apiClient.get<IdentitiesResponse>("/profile/me/identities");

export const checkUsernameAvailable = (username: string) =>
  publicClient.get<UsernameAvailableResponse>("/profile/me/username/available", {
    params: { username },
  });

export const updateUsername = (payload: UpdateUsernamePayload) =>
  apiClient.patch<MessageResponse>("/profile/me/username", payload);

export const getSessions = () =>
  apiClient.get<SessionsResponse>("/profile/me/sessions");

export const revokeSession = (sessionId: string) =>
  apiClient.delete<void>(`/profile/me/sessions/${sessionId}`);

export const requestEmailChange = (payload: RequestEmailChangePayload) =>
  apiClient.post<MessageResponse>("/profile/me/email", payload);

export const verifyCurrentEmail = (payload: VerifyCurrentEmailPayload) =>
  apiClient.post<VerifyCurrentEmailResponse>("/profile/me/email/verify", payload);

export const confirmEmailChange = (payload: ConfirmEmailChangePayload) =>
  apiClient.put<MessageResponse>("/profile/me/email", payload);

export const getDeletionMethod = () =>
  apiClient.get<DeletionMethodResponse>("/profile/me/deletion");

export const deleteAccount = (payload: DeleteAccountPayload = {}) =>
  apiClient.delete<DeleteAccountResponse>("/profile/me", { data: payload });

export const cancelDeletion = () =>
  apiClient.delete<MessageResponse>("/profile/me/deletion");

export const setPassword = (payload: SetPasswordPayload) =>
  apiClient.post<MessageResponse>("/profile/me/password", payload);
