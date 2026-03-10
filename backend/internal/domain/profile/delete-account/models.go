package deleteaccount

import (
	"time"

	"github.com/7-Dany/store/backend/internal/db"
)

// DeletionUser is the minimal user view returned by store.GetUserForDeletion.
type DeletionUser struct {
	ID           [16]byte
	Email        *string    // nil for Telegram-only accounts
	PasswordHash *string    // nil for OAuth-only accounts
	DeletedAt    *time.Time // non-nil if deletion is already pending
}

// UserAuthMethods holds the result of GetUserAuthMethods, used to dispatch
// the correct confirmation path in the handler (D-11).
type UserAuthMethods struct {
	HasPassword   bool
	IdentityCount int
}

// DeleteWithPasswordInput holds the caller-supplied data for service.DeleteWithPassword.
type DeleteWithPasswordInput struct {
	UserID    string
	Password  string
	IPAddress string
	UserAgent string
}

// ScheduleDeletionInput holds the caller-supplied data for service.ScheduleDeletion.
// Provider is the auth provider that confirmed the deletion; used for the audit row.
// Pass db.AuthProviderEmail for password and email-OTP paths, db.AuthProviderTelegram
// for the Telegram confirmation path.
type ScheduleDeletionInput struct {
	UserID    string
	IPAddress string
	UserAgent string
	Provider  db.AuthProvider
}

// SendDeletionOTPInput holds the caller-supplied data for service.SendDeletionOTP.
type SendDeletionOTPInput struct {
	UserID     string
	Email      string
	TTLSeconds float64 // authoritative TTL from config.OTPTokenTTL.Seconds()
	IPAddress  string
	UserAgent  string
}

// SendDeletionOTPResult is returned by store.SendDeletionOTPTx.
// RawCode is the plaintext OTP code that the service must dispatch by email.
type SendDeletionOTPResult struct {
	RawCode string
}

// ConfirmOTPDeletionInput holds the caller-supplied data for service.ConfirmOTPDeletion.
type ConfirmOTPDeletionInput struct {
	UserID    string
	Code      string
	IPAddress string
	UserAgent string
}

// ConfirmTelegramDeletionInput holds the caller-supplied data for service.ConfirmTelegramDeletion.
type ConfirmTelegramDeletionInput struct {
	UserID       string
	TelegramAuth TelegramAuthPayload
	IPAddress    string
	UserAgent    string
}

// CancelDeletionInput holds the caller-supplied data for service.CancelDeletion.
type CancelDeletionInput struct {
	UserID    string
	IPAddress string
	UserAgent string
}

// DeletionScheduled is the result returned by ScheduleDeletion and Confirm* methods.
// ScheduledDeletionAt is deleted_at + 30 days.
type DeletionScheduled struct {
	ScheduledDeletionAt time.Time
}

// DeletionMethodResult is returned by GetDeletionMethod.
// Method is one of "password", "email_otp", or "telegram" and tells the
// client which confirmation UI to render before the user initiates deletion.
//
// Derivation rule (matches empty-body dispatch in handler.Delete):
//
//	"password"  — HasPassword = true  (supply password field)
//	"email_otp" — HasPassword = false AND user.Email != nil  (expect OTP email)
//	"telegram"  — HasPassword = false AND user.Email == nil  (Telegram widget)
type DeletionMethodResult struct {
	Method string // "password" | "email_otp" | "telegram"
}
