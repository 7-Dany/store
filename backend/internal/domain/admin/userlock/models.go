// Package userlock provides the HTTP handler, service, and store
// for admin-controlled user account locking.
package userlock

import "time"

// LockUserInput is the service-layer input for POST /lock.
type LockUserInput struct {
	Reason string
}

// LockUserTxInput is the store-layer input with parsed [16]byte IDs.
type LockUserTxInput struct {
	UserID   [16]byte
	LockedBy [16]byte
	Reason   string
}

// UserLockStatus is the service-layer representation of a user's lock state.
type UserLockStatus struct {
	UserID           string
	AdminLocked      bool
	LockedBy         *string    // nil when not locked
	LockedReason     *string    // nil when not locked
	LockedAt         *time.Time // nil when not locked
	IsLocked         bool       // OTP lock — separate from admin lock
	LoginLockedUntil *time.Time // nil when OTP lock not active
}
