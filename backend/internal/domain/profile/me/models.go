// Package me provides the HTTP handler, service, and store for the
// authenticated user's own profile endpoints (GET /me, PATCH /me/profile).
package me

import "time"

// UserProfile is the store-layer representation of a user's public profile.
type UserProfile struct {
	ID            [16]byte
	Email         string
	DisplayName   string
	Username      string // empty string when the user has no username set
	AvatarURL     string
	EmailVerified bool
	IsActive      bool
	IsLocked      bool // OTP brute-force lock; cleared by the self-service unlock flow
	// AdminLocked is an admin-action lock. Currently populated but not exposed via GET /me; reserved for a future admin endpoint.
	AdminLocked bool
	LastLoginAt   *time.Time
	CreatedAt     time.Time
}

// UpdateProfileInput is the service-layer input for PATCH /me/profile.
// Pointer fields use nil to mean "do not update this field".
type UpdateProfileInput struct {
	UserID      [16]byte
	DisplayName *string // nil → leave unchanged
	AvatarURL   *string // nil → leave unchanged
	IPAddress   string
	UserAgent   string
}
