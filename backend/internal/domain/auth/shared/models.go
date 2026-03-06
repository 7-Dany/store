package authshared

import (
	"time"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/google/uuid"
)

// VerificationToken is the store-layer representation of a one-time token row.
// Used by verification, unlock, and password feature packages.
type VerificationToken struct {
	ID          [16]byte
	UserID      [16]byte
	Email       string
	CodeHash    string
	Attempts    int16
	MaxAttempts int16
	ExpiresAt   time.Time
}

// OTPIssuanceResult is returned by any service method that generates a new OTP
// and needs to hand the raw code back to the handler for email delivery.
// An empty RawCode signals that the request was silently suppressed
// (anti-enumeration path).
type OTPIssuanceResult struct {
	UserID  string
	Email   string
	RawCode string
}

// NewVerificationToken constructs a VerificationToken from the raw field values
// returned by a GetXxxToken query row. Callers use this to avoid repeating the
// same seven-field mapping in every store that consumes an OTP token.
//
// All time values must be passed in UTC (call .Time.UTC() on pgtype.Timestamptz).
func NewVerificationToken(
	id [16]byte,
	userID [16]byte,
	email string,
	codeHash string,
	attempts int16,
	maxAttempts int16,
	expiresAt time.Time,
) VerificationToken {
	return VerificationToken{
		ID:          id,
		UserID:      userID,
		Email:       email,
		CodeHash:    codeHash,
		Attempts:    attempts,
		MaxAttempts: maxAttempts,
		ExpiresAt:   expiresAt,
	}
}

// NewOTPIssuanceResult constructs an OTPIssuanceResult from a raw [16]byte user ID,
// an email address, and a raw OTP code. It converts the [16]byte to the standard
// UUID string form required by the handler layer.
func NewOTPIssuanceResult(userID [16]byte, email, rawCode string) OTPIssuanceResult {
	return OTPIssuanceResult{
		UserID:  uuid.UUID(userID).String(),
		Email:   email,
		RawCode: rawCode,
	}
}

// OTPTokenInput carries the data needed to create a new OTP token in a
// transaction. Used by the password-reset and account-unlock flows.
type OTPTokenInput struct {
	UserID    [16]byte
	Email     string
	IPAddress string
	UserAgent string
	CodeHash  string
	TTL       time.Duration
}

// IncrementInput carries the data needed by store.IncrementAttemptsTx to
// record a failed OTP attempt and write the audit log entry.
// Used by verification, unlock, and password feature packages.
type IncrementInput struct {
	TokenID      [16]byte
	UserID       [16]byte
	Attempts     int16 // current attempt count before increment
	MaxAttempts  int16
	IPAddress    string
	UserAgent    string
	AttemptEvent audit.EventType // must not be empty; IncrementAttemptsTx returns an error when zero
}
