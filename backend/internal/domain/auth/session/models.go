// Package session provides the HTTP handler, service, and store for
// refresh-token rotation, logout, and all bulk token-revocation operations.
// It implements RFC 6819 token reuse detection (ADR-011).
package session

import "time"

// StoredRefreshToken is the store-layer representation of a refresh_tokens row.
// IsRevoked is derived from whether revoked_at IS NOT NULL in the DB.
type StoredRefreshToken struct {
	JTI       [16]byte
	UserID    [16]byte
	SessionID [16]byte
	FamilyID  [16]byte
	ExpiresAt time.Time
	IsRevoked bool
}

// RotateTxInput carries the data needed by store.RotateRefreshTokenTx to
// atomically revoke the current token, insert its child, update last_login_at,
// and write the audit log.
type RotateTxInput struct {
	CurrentJTI [16]byte // JTI being consumed (marked 'rotated')
	UserID     [16]byte
	SessionID  [16]byte
	FamilyID   [16]byte
	IPAddress  string
	UserAgent  string
}

// RotatedSession is returned by store.RotateRefreshTokenTx on success.
// The caller uses NewRefreshJTI and RefreshExpiry to sign the new refresh JWT.
type RotatedSession struct {
	NewRefreshJTI [16]byte
	RefreshExpiry time.Time
}

// UserStatusResult is returned by store.GetUserVerifiedAndLocked.
type UserStatusResult struct {
	EmailVerified bool
	IsLocked      bool
	IsActive      bool
}

// LogoutTxInput carries the data needed by store.LogoutTx to atomically
// revoke the token, end the session, and write the audit log.
// All UUIDs are parsed by the handler layer before constructing this struct.
type LogoutTxInput struct {
	JTI       [16]byte
	SessionID [16]byte
	UserID    [16]byte
	IPAddress string
	UserAgent string
}


