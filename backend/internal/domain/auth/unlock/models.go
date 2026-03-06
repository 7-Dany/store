// Package unlock provides the HTTP handler, service, and store for the
// self-service account-unlock OTP flow: users request an unlock email,
// then confirm with a 6-digit code.
package unlock

import (
	"time"
)

// RequestUnlockInput holds the caller-supplied data for Service.RequestUnlock.
type RequestUnlockInput struct {
	Email     string
	IPAddress string
	UserAgent string
}

// ConfirmUnlockInput holds the caller-supplied data for Service.ConsumeUnlockToken.
type ConfirmUnlockInput struct {
	Email     string
	Code      string
	IPAddress string
	UserAgent string
}

// UnlockUser is the minimal user view returned by Store.GetUserForUnlock.
type UnlockUser struct {
	ID               [16]byte
	EmailVerified    bool
	IsLocked         bool       // set by OTP brute-force exhaustion (LockAccount)
	AdminLocked      bool       // set exclusively by admin action (RBAC); OTP unlock must not clear this
	LoginLockedUntil *time.Time // nil = not time-locked
}

// RequestUnlockStoreInput carries the data needed by Store.RequestUnlockTx
// to create a new OTP token in a transaction. TTL is passed as a duration so
// the store can compute expires_at inside PostgreSQL (NOW() + ttl), avoiding
// application/DB clock skew that would violate chk_ott_au_ttl_max.
type RequestUnlockStoreInput struct {
	UserID    [16]byte
	Email     string
	IPAddress string
	UserAgent string
	CodeHash  string
	TTL       time.Duration
}
