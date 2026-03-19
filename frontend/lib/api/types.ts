// ─── Shared primitives ────────────────────────────────────────────────────────

export interface MessageResponse {
  message: string;
}

export interface ApiErrorBody {
  code: string;
  message: string;
}

// ─── Auth ─────────────────────────────────────────────────────────────────────

export interface LoginPayload {
  identifier: string;
  password: string;
}

export interface LoginResponse {
  access_token: string;
  refresh_token: string;
  refresh_expiry: string;
  expires_in: number;
  /** Present only when the account has a pending deletion scheduled. */
  scheduled_deletion_at?: string;
}

export interface RegisterPayload {
  display_name: string;
  email: string;
  password: string;
  username?: string;
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

export interface ChangePasswordPayload {
  old_password: string;
  new_password: string;
}

export interface RequestUnlockPayload {
  email: string;
}

export interface ConfirmUnlockPayload {
  email: string;
  code: string;
}

export interface RefreshResponse {
  access_token: string;
  refresh_token: string;
  refresh_expiry: string;
  expires_in: number;
}

// ─── Profile ──────────────────────────────────────────────────────────────────

export interface UserProfile {
  id: string;
  email: string;
  display_name: string;
  username?: string;
  avatar_url?: string;
  email_verified: boolean;
  is_active: boolean;
  is_locked: boolean;
  last_login_at?: string;
  created_at: string;
  scheduled_deletion_at?: string;
}

export interface UpdateProfilePayload {
  display_name?: string | null;
  avatar_url?: string | null;
}

export interface OAuthIdentity {
  provider: "google" | "telegram";
  provider_uid: string;
  provider_email: string | null;
  display_name: string | null;
  avatar_url: string | null;
  created_at: string;
}

export interface IdentitiesResponse {
  identities: OAuthIdentity[];
}

export interface UpdateUsernamePayload {
  username: string;
}

export interface UsernameAvailableResponse {
  available: boolean;
}

export interface Session {
  id: string;
  ip_address: string;
  user_agent: string;
  started_at: string;
  last_active_at: string;
  is_current: boolean;
}

export interface SessionsResponse {
  sessions: Session[];
}

export interface RequestEmailChangePayload {
  new_email: string;
}

export interface VerifyCurrentEmailPayload {
  code: string;
}

export interface VerifyCurrentEmailResponse {
  grant_token: string;
  expires_in: number;
}

export interface ConfirmEmailChangePayload {
  grant_token: string;
  code: string;
}

export type DeletionMethod = "password" | "email_otp" | "telegram";

export interface DeletionMethodResponse {
  deletion_method: DeletionMethod;
}

export interface TelegramAuthPayload {
  id: number;
  first_name?: string;
  last_name?: string;
  username?: string;
  photo_url?: string;
  auth_date: number;
  hash: string;
}

export interface DeleteAccountPayload {
  /** Path A – password-protected accounts. */
  password?: string;
  /** Path B step 2 – email OTP confirmation. */
  code?: string;
  /** Path C step 2 – Telegram re-auth. */
  telegram_auth?: TelegramAuthPayload;
}

export interface DeleteAccountResponse {
  message: string;
  /** Present on 200 (deletion scheduled). */
  scheduled_deletion_at?: string;
  /** Present on 202 (OTP sent). */
  auth_method?: DeletionMethod;
  /** Present on 202 (OTP sent). */
  expires_in?: number;
}

export interface SetPasswordPayload {
  new_password: string;
}

// ─── OAuth ────────────────────────────────────────────────────────────────────

export interface TelegramCallbackResponse {
  access_token: string;
  token_type: string;
  expires_in: number;
}

// ─── Admin – User Lock ────────────────────────────────────────────────────────

export interface LockUserPayload {
  reason: string;
}

export interface LockStatusResponse {
  user_id: string;
  admin_locked: boolean;
  locked_by: string | null;
  locked_reason: string | null;
  locked_at: string | null;
  is_locked: boolean;
  login_locked_until: string | null;
}

// ─── Admin – User Roles ───────────────────────────────────────────────────────

export interface AssignUserRolePayload {
  role_id: string;
  granted_reason: string;
  expires_at?: string;
}

export interface UserRoleResponse {
  user_id: string;
  role_id: string;
  role_name: string;
  is_owner_role: boolean;
  granted_reason: string;
  granted_at: string;
  expires_at: string | null;
}

// ─── Admin – User Permissions ─────────────────────────────────────────────────

export interface GrantUserPermissionPayload {
  permission_id: string;
  granted_reason: string;
  expires_at: string;
  scope?: "own" | "all";
  conditions?: Record<string, unknown>;
}

export interface UserPermissionGrant {
  id: string;
  canonical_name: string;
  name: string;
  resource_type: string;
  scope: "own" | "all";
  conditions: Record<string, unknown>;
  expires_at: string;
  granted_at: string;
  granted_reason: string;
}

export interface UserPermissionsResponse {
  permissions: UserPermissionGrant[];
}

// ─── RBAC – Roles ─────────────────────────────────────────────────────────────

export interface Role {
  id: string;
  name: string;
  description?: string;
  is_system_role: boolean;
  is_owner_role: boolean;
  is_active: boolean;
  created_at: string;
}

export interface RolesResponse {
  roles: Role[];
}

export interface CreateRolePayload {
  name: string;
  description?: string;
}

export interface UpdateRolePayload {
  name?: string;
  description?: string;
}

// ─── RBAC – Role Permissions ──────────────────────────────────────────────────

export type AccessType = "direct" | "conditional" | "request" | "denied";
export type PermissionScope = "own" | "all";

export interface AddRolePermissionPayload {
  permission_id: string;
  access_type: AccessType;
  scope: PermissionScope;
  granted_reason: string;
  conditions?: Record<string, unknown>;
}

export interface RolePermissionGrant {
  permission_id: string;
  canonical_name: string;
  resource_type: string;
  name: string;
  access_type: AccessType;
  scope: PermissionScope;
  conditions: Record<string, unknown>;
  granted_at: string;
}

export interface RolePermissionsResponse {
  permissions: RolePermissionGrant[];
}

// ─── RBAC – Permissions ───────────────────────────────────────────────────────

export type ScopePolicy = "none" | "own" | "all" | "any";

export interface PermissionCapabilities {
  scope_policy: ScopePolicy;
  access_types: AccessType[];
}

export interface Permission {
  id: string;
  canonical_name: string;
  resource_type: string;
  name: string;
  description?: string;
  capabilities: PermissionCapabilities;
}

export interface PermissionsResponse {
  permissions: Permission[];
}

export interface PermissionGroup {
  id: string;
  name: string;
  display_label?: string;
  icon?: string;
  color_hex?: string;
  display_order: number;
  is_visible: boolean;
  members: Permission[];
}

export interface PermissionGroupsResponse {
  groups: PermissionGroup[];
}

// ─── RBAC – Owner ─────────────────────────────────────────────────────────────

export interface AssignOwnerPayload {
  secret: string;
}

export interface AssignOwnerResponse {
  user_id: string;
  role_name: string;
  granted_at: string;
}

export interface InitiateTransferPayload {
  target_user_id: string;
}

export interface InitiateTransferResponse {
  transfer_id: string;
  target_user_id: string;
  expires_at: string;
}

export interface AcceptTransferPayload {
  token: string;
}

export interface AcceptTransferResponse {
  new_owner_id: string;
  previous_owner_id: string;
  transferred_at: string;
}

// ─── Stats ────────────────────────────────────────────────────────────────────

export interface HealthResponse {
  status: string;
}
