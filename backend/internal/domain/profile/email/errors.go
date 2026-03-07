package email

import "errors"

// ── Validation sentinels — returned by validators, mapped to 422 by the handler ──

// ErrInvalidEmailFormat is returned when the new email address fails format validation.
var ErrInvalidEmailFormat = errors.New("email address is invalid")

// ErrEmailTooLong is returned when the normalised email address exceeds 254 bytes.
var ErrEmailTooLong = errors.New("email must not exceed 254 bytes")

// ErrInvalidCodeFormat is returned when the OTP code is not exactly 6 ASCII digits.
var ErrInvalidCodeFormat = errors.New("code must be exactly 6 digits")

// ErrGrantTokenEmpty is returned when the grant_token field is absent or blank.
var ErrGrantTokenEmpty = errors.New("grant_token is required")

// ── Flow sentinels — returned by the service, mapped to 4xx by the handler ──

// ErrSameEmail is returned when the requested new email is identical to the current one.
var ErrSameEmail = errors.New("new email is the same as your current email")

// ErrEmailTaken is returned when the requested new email is already registered
// to another active account.
var ErrEmailTaken = errors.New("email already registered")

// ErrCooldownActive is returned when a new email-change request is submitted before
// the 2-minute cooldown from the previous request has elapsed.
var ErrCooldownActive = errors.New("please wait before requesting another code")

// ErrGrantTokenInvalid is returned when the grant token presented in step 3 is
// not found in the KV store or has already expired.
var ErrGrantTokenInvalid = errors.New("grant token is invalid or expired")

// OTP sentinels — authshared already exports ErrTokenNotFound, ErrTokenExpired,
// ErrTokenAlreadyUsed, ErrInvalidCode, and ErrTooManyAttempts.
// Import and use authshared directly; do not redefine them here.
