import { apiClient } from "./http/client";
import type {
  LockUserPayload,
  LockStatusResponse,
  AssignUserRolePayload,
  UserRoleResponse,
  GrantUserPermissionPayload,
  UserPermissionGrant,
  UserPermissionsResponse,
  MessageResponse,
} from "./types";

export const lockUser = (userId: string, payload: LockUserPayload) =>
  apiClient.post<void>(`/admin/users/${userId}/lock`, payload);

export const unlockUser = (userId: string) =>
  apiClient.delete<void>(`/admin/users/${userId}/lock`);

export const getLockStatus = (userId: string) =>
  apiClient.get<LockStatusResponse>(`/admin/users/${userId}/lock`);

export const assignUserRole = (userId: string, payload: AssignUserRolePayload) =>
  apiClient.put<UserRoleResponse>(`/admin/users/${userId}/role`, payload);

export const getUserRole = (userId: string) =>
  apiClient.get<UserRoleResponse>(`/admin/users/${userId}/role`);

export const removeUserRole = (userId: string) =>
  apiClient.delete<void>(`/admin/users/${userId}/role`);

export const grantUserPermission = (userId: string, payload: GrantUserPermissionPayload) =>
  apiClient.post<UserPermissionGrant>(`/admin/users/${userId}/permissions`, payload);

export const listUserPermissions = (userId: string) =>
  apiClient.get<UserPermissionsResponse>(`/admin/users/${userId}/permissions`);

export const revokeUserPermission = (userId: string, grantId: string) =>
  apiClient.delete<void>(`/admin/users/${userId}/permissions/${grantId}`);
