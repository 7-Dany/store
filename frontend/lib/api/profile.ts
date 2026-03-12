const API_BASE = process.env.API_BASE_URL ?? "http://localhost:8080/api/v1";

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

/**
 * Server-side only. Fetches the authenticated user's profile from the Go
 * backend using the session JWT. Returns null on any failure so callers can
 * degrade gracefully (e.g. show placeholder initials in the sidebar).
 */
export async function fetchProfile(token: string): Promise<UserProfile | null> {
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
}
