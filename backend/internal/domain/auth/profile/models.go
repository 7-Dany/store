// Package profile provides the HTTP handler, service, and store for authenticated user profile operations.
package profile

import "time"

// UserProfile is the store-layer representation of a user's public profile.
type UserProfile struct {
	ID            [16]byte
	Email         string
	DisplayName   string
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

// ActiveSession is the store-layer representation of an open user session.
type ActiveSession struct {
	ID           [16]byte
	IPAddress    string
	UserAgent    string
	StartedAt    time.Time
	LastActiveAt time.Time
	// IsCurrent is NOT here — it is a handler-layer concern computed from the
	// JWT session claim and set directly on sessionJSON in handler.go.
}
