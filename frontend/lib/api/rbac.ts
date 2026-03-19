import { apiClient, publicClient } from "./http/client";
import type {
  Role,
  RolesResponse,
  CreateRolePayload,
  UpdateRolePayload,
  AddRolePermissionPayload,
  RolePermissionsResponse,
  PermissionsResponse,
  PermissionGroupsResponse,
  AssignOwnerPayload,
  AssignOwnerResponse,
  InitiateTransferPayload,
  InitiateTransferResponse,
  AcceptTransferPayload,
  AcceptTransferResponse,
} from "./types";

export const listRoles = () => apiClient.get<RolesResponse>("/rbac/roles");

export const createRole = (payload: CreateRolePayload) =>
  apiClient.post<Role>("/rbac/roles", payload);

export const getRole = (id: string) =>
  apiClient.get<Role>(`/rbac/roles/${id}`);

export const updateRole = (id: string, payload: UpdateRolePayload) =>
  apiClient.patch<Role>(`/rbac/roles/${id}`, payload);

export const deleteRole = (id: string) =>
  apiClient.delete<void>(`/rbac/roles/${id}`);

export const listRolePermissions = (id: string) =>
  apiClient.get<RolePermissionsResponse>(`/rbac/roles/${id}/permissions`);

export const addRolePermission = (id: string, payload: AddRolePermissionPayload) =>
  apiClient.post<void>(`/rbac/roles/${id}/permissions`, payload);

export const removeRolePermission = (id: string, permId: string) =>
  apiClient.delete<void>(`/rbac/roles/${id}/permissions/${permId}`);

export const listPermissions = () =>
  apiClient.get<PermissionsResponse>("/rbac/permissions");

export const listPermissionGroups = () =>
  apiClient.get<PermissionGroupsResponse>("/rbac/permissions/groups");

export const assignOwner = (payload: AssignOwnerPayload) =>
  apiClient.put<AssignOwnerResponse>("/rbac/owner/assign", payload);

export const initiateOwnerTransfer = (payload: InitiateTransferPayload) =>
  apiClient.post<InitiateTransferResponse>("/rbac/owner/transfer", payload);

export const acceptOwnerTransfer = (payload: AcceptTransferPayload) =>
  publicClient.put<AcceptTransferResponse>("/rbac/owner/transfer", payload);

export const cancelOwnerTransfer = () =>
  apiClient.delete<void>("/rbac/owner/transfer");
