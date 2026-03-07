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
