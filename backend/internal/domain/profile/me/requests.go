package me

import "time"

// updateProfileRequest is the JSON body for PATCH /me/profile.
// Both fields are optional pointers; a nil pointer means "do not change".
// An explicit null in JSON decodes to nil (same as omitting the field).
type updateProfileRequest struct {
	DisplayName *string `json:"display_name"`
	AvatarURL   *string `json:"avatar_url"`
}

// updateProfileResponse is the JSON response body for a successful PATCH /me/profile.
type updateProfileResponse struct {
	Message string `json:"message"`
}

// meResponse is the JSON response body for GET /me.
type meResponse struct {
	ID            string     `json:"id"`
	Email         string     `json:"email"`
	DisplayName   string     `json:"display_name"`
	Username      string     `json:"username,omitempty"`
	AvatarURL     string     `json:"avatar_url,omitempty"`
	EmailVerified bool       `json:"email_verified"`
	IsActive      bool       `json:"is_active"`
	IsLocked      bool       `json:"is_locked"`
	LastLoginAt   *time.Time `json:"last_login_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// identityItem is a single entry in the GET /me/identities response.
// Nullable fields use omitempty — they are omitted entirely when nil.
type identityItem struct {
	Provider      string     `json:"provider"`
	ProviderUID   string     `json:"provider_uid"`
	ProviderEmail *string    `json:"provider_email,omitempty"`
	DisplayName   *string    `json:"display_name,omitempty"`
	AvatarURL     *string    `json:"avatar_url,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// identitiesResponse is the JSON body for GET /me/identities.
// Identities is always an array — never null; empty array when none linked.
type identitiesResponse struct {
	Identities []identityItem `json:"identities"`
}
