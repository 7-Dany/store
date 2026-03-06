// Package password provides the HTTP handler, service, and store for the
// forgot-password and reset-password OTP flow.
package password

import authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"

// ── Input / result types ──────────────────────────────────────────────────────

// ForgotPasswordInput holds the caller-supplied data for
// service.RequestPasswordReset.
type ForgotPasswordInput struct {
	Email     string
	IPAddress string
	UserAgent string
}

// ResetPasswordInput holds the caller-supplied data for
// service.ConsumePasswordResetToken.
type ResetPasswordInput struct {
	Email       string
	Code        string
	NewPassword string
	IPAddress   string
	UserAgent   string
}

// GetUserForPasswordResetResult is the minimal user view returned by
// store.GetUserForPasswordReset.
type GetUserForPasswordResetResult struct {
	ID            [16]byte
	EmailVerified bool
	IsLocked      bool
	IsActive      bool
}

// ConsumeAndUpdateInput carries all inputs for Store.ConsumeAndUpdatePasswordTx.
type ConsumeAndUpdateInput struct {
	Email       string
	NewPassword string // plain text — used only for the same-password reuse check inside the TX
	NewHash     string // bcrypt hash pre-computed by the caller outside the TX
	IPAddress   string
	UserAgent   string
}

// ChangePasswordInput holds the caller-supplied data for service.UpdatePasswordHash.
type ChangePasswordInput struct {
	UserID      string
	OldPassword string
	NewPassword string
	IPAddress   string
	UserAgent   string
}

// CurrentCredentials is returned by store.GetUserPasswordHash.
type CurrentCredentials struct {
	PasswordHash string
}

// ── Shared aliases ────────────────────────────────────────────────────────────

// VerifyResetCodeInput holds the caller-supplied data for service.VerifyResetCode.
type VerifyResetCodeInput struct {
	Email     string
	Code      string
	IPAddress string
	UserAgent string
}

// RequestPasswordResetStoreInput is a type alias for authshared.OTPTokenInput.
// Existing call-sites require no changes.
type RequestPasswordResetStoreInput = authshared.OTPTokenInput
