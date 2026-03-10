// Package login authenticates users by email/password and issues JWT access
// tokens with refresh-token cookies.
package login

import "time"

// LoginInput holds the validated fields for Service.Login.
type LoginInput struct {
	Identifier string // email or username, pre-normalised by the handler
	Password   string
	IPAddress  string
	UserAgent  string
}

// LoginUser is the minimal user view returned by Store.GetUserForLogin.
// It carries only the fields needed to authenticate and gate the login.
type LoginUser struct {
	ID               [16]byte
	Email            string     // may be empty for username-only accounts
	Username         string     // may be empty for email-only accounts
	PasswordHash     string
	IsActive         bool
	EmailVerified    bool
	IsLocked         bool
	LoginLockedUntil *time.Time // nil = not time-locked
	DeletedAt        *time.Time // non-nil if account is pending deletion (grace period active)
}

// LoginTxInput carries the data needed by Store.LoginTx to create a session,
// issue a refresh token, stamp last_login_at, and write the audit log.
type LoginTxInput struct {
	UserID    [16]byte
	IPAddress string
	UserAgent string
}

// LoggedInSession is returned by Store.LoginTx on success.
// All UUIDs are raw [16]byte; the handler converts them to strings for JWT claims.
type LoggedInSession struct {
	UserID              [16]byte
	SessionID           [16]byte
	RefreshJTI          [16]byte
	FamilyID            [16]byte
	RefreshExpiry       time.Time
	ScheduledDeletionAt *time.Time // non-nil when the account is pending deletion (D-04)
}


