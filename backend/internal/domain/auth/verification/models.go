// Package verification provides email address confirmation and resend flows.
package verification

import "time"

// VerifyEmailInput holds the data needed to validate an email verification OTP.
type VerifyEmailInput struct {
	Email     string
	Code      string
	IPAddress string
	UserAgent string
}

// ResendInput holds the data needed to issue a fresh verification OTP.
type ResendInput struct {
	Email     string
	IPAddress string
	UserAgent string
}

// ResendUser holds the minimal user fields needed to gate a resend request.
type ResendUser struct {
	ID            [16]byte
	EmailVerified bool
	IsLocked      bool
}

// ResendStoreInput carries the user and context data for ResendVerificationTx.
// TTL is the OTP token lifetime; sourced from config.Config.OTPValidMinutes.
type ResendStoreInput struct {
	UserID    [16]byte
	Email     string
	IPAddress string
	UserAgent string
	TTL       time.Duration
}
