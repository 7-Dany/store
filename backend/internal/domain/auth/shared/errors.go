// Package authshared holds primitives used by more than one auth feature sub-package.
// It must never import any feature package (register, login, profile, …).
package authshared

import (
	"errors"
	"time"
)

// ── Cross-feature sentinel errors ───────────────────────────────────────────

// ErrUserNotFound is returned when the user record cannot be located.
var ErrUserNotFound = errors.New("user not found")

// ErrTokenNotFound is returned when no matching one-time token row exists.
var ErrTokenNotFound = errors.New("token not found")

// ErrTokenExpired is returned when the one-time token's expiry timestamp is
// in the past at the time of verification.
var ErrTokenExpired = errors.New("token has expired")

// ErrTokenAlreadyUsed is returned by store methods when the consume query
// affects 0 rows, indicating a concurrent request already consumed this token.
var ErrTokenAlreadyUsed = errors.New("token has already been used")

// ErrTokenAlreadyConsumed is an alias for ErrTokenAlreadyUsed, used by the
// session store when a rotate operation finds the token was already rotated
// by a concurrent request (0 rows affected on the revoke UPDATE).
var ErrTokenAlreadyConsumed = ErrTokenAlreadyUsed

// ErrTooManyAttempts is returned when the token's attempt counter has reached
// the maximum allowed value before a correct code was supplied.
var ErrTooManyAttempts = errors.New("too many verification attempts")

// ErrInvalidCode is returned when the supplied OTP does not match the stored hash.
var ErrInvalidCode = errors.New("invalid verification code")

// ErrAccountLocked is returned when the account is OTP-locked (is_locked = TRUE)
// and the handler returns HTTP 423. Distinct from ErrAdminLocked so clients can
// show different guidance (self-unlock vs. contact support).
var ErrAccountLocked = errors.New("account locked — please use the unlock flow")

// ErrAdminLocked is returned when the account has been admin-locked
// (admin_locked = TRUE). The user-facing unlock OTP flow cannot clear this;
// only an admin action via the RBAC routes can. Handler returns HTTP 423.
var ErrAdminLocked = errors.New("account locked by admin — contact support")

// ErrEmailTaken is returned when the requested email address is already registered.
var ErrEmailTaken = errors.New("email address is already registered")

// ErrUsernameTaken is returned when the requested username is already registered.
var ErrUsernameTaken = errors.New("username is already taken")

// ErrUsernameEmpty is returned when the username field is absent or blank after trimming.
var ErrUsernameEmpty = errors.New("username is required")

// ErrUsernameTooShort is returned when the username is shorter than 3 characters.
var ErrUsernameTooShort = errors.New("username must be at least 3 characters")

// ErrUsernameTooLong is returned when the username exceeds 30 characters.
var ErrUsernameTooLong = errors.New("username must not exceed 30 characters")

// ErrUsernameInvalidChars is returned when the username contains characters
// outside the allowed set [a-z0-9_].
var ErrUsernameInvalidChars = errors.New("username may only contain lowercase letters, digits, and underscores")

// ErrUsernameInvalidFormat is returned when the username starts or ends with an
// underscore, or contains consecutive underscores.
var ErrUsernameInvalidFormat = errors.New("username must not start or end with an underscore, and must not contain consecutive underscores")

// ErrResetTokenCooldown is returned by store.RequestPasswordResetTx when a
// partial-unique-index violation signals that an active password-reset token
// already exists for this user. The service treats this as a silent no-op
// (anti-enumeration).
var ErrResetTokenCooldown = errors.New("a password reset token was recently issued")

// ErrAlreadyVerified is returned when MarkEmailVerified is a no-op because
// email_verified was already TRUE. Treated as idempotent success by the handler.
var ErrAlreadyVerified = errors.New("email address already verified")

// ErrInvalidToken is returned by service.Refresh when the presented refresh
// token is unknown, already revoked, or expired. Maps to HTTP 401 with error
// code "invalid_token".
var ErrInvalidToken = errors.New("invalid or expired token")

// ErrTokenReuseDetected is returned by service.Refresh when the presented
// token has already been consumed. Maps to HTTP 401 with error code
// "token_reuse_detected".
var ErrTokenReuseDetected = errors.New("token reuse detected — all sessions for this device have been revoked")

// ErrSessionNotFound is returned when the target session row does not exist.
var ErrSessionNotFound = errors.New("session not found")

// ErrInvalidCredentials is returned when the supplied email/password pair does
// not match any active account.
var ErrInvalidCredentials = errors.New("invalid email or password")

// ErrEmailNotVerified is returned when a user attempts to log in before
// confirming their email address.
var ErrEmailNotVerified = errors.New("email address not verified — please check your inbox")

// ErrAccountInactive is returned when a login is attempted on a suspended account.
var ErrAccountInactive = errors.New("account is suspended")

// errLoginLockedMsg is the canonical error message for login lockout.
// Declared as a const so both ErrLoginLocked and LoginLockedError.Error()
// return the identical string without a method call in errors.go.
const errLoginLockedMsg = "too many failed login attempts — please wait before trying again"

// ErrLoginLocked is returned when the account has a time-based login lockout.
// The handler returns HTTP 429 with a Retry-After header.
var ErrLoginLocked = errors.New(errLoginLockedMsg)

// ── Input-validation sentinel errors ────────────────────────────────────────

// ErrDisplayNameEmpty is returned when display_name is absent or blank.
var ErrDisplayNameEmpty = errors.New("display_name is required")

// ErrDisplayNameTooLong is returned when display_name exceeds 100 characters.
var ErrDisplayNameTooLong = errors.New("display_name must not exceed 100 characters")

// ErrDisplayNameInvalid is returned when display_name contains characters
// outside the allowed set.
var ErrDisplayNameInvalid = errors.New("display_name contains invalid characters")

// ErrEmailEmpty is returned when the email field is absent or blank after trimming.
var ErrEmailEmpty = errors.New("email is required")

// ErrEmailTooLong is returned when the normalised email address exceeds 254 bytes.
var ErrEmailTooLong = errors.New("email address must not exceed 254 characters")

// ErrEmailInvalid is returned when the email address fails format validation.
var ErrEmailInvalid = errors.New("email address is invalid")

// ErrIdentifierTooLong is returned when the identifier (email address or username)
// exceeds the maximum accepted byte length after normalisation.
var ErrIdentifierTooLong = errors.New("identifier must not exceed 254 characters")

// ErrPasswordEmpty is returned when the password field is absent or blank.
var ErrPasswordEmpty = errors.New("password is required")

// ErrPasswordTooShort is returned when the password is shorter than 8 bytes.
var ErrPasswordTooShort = errors.New("password must be at least 8 characters")

// ErrPasswordTooLong is returned when the password exceeds bcrypt's 72-byte
// hard truncation boundary.
var ErrPasswordTooLong = errors.New("password must not exceed 72 characters")

// ErrPasswordNoUpper is returned when the password contains no uppercase letter.
var ErrPasswordNoUpper = errors.New("password must contain at least one uppercase letter")

// ErrPasswordNoLower is returned when the password contains no lowercase letter.
var ErrPasswordNoLower = errors.New("password must contain at least one lowercase letter")

// ErrPasswordNoDigit is returned when the password contains no decimal digit.
var ErrPasswordNoDigit = errors.New("password must contain at least one digit (0-9)")

// ErrPasswordNoSymbol is returned when the password contains no symbol character.
var ErrPasswordNoSymbol = errors.New("password must contain at least one symbol (e.g. !@#$%)")

// ErrCodeEmpty is returned when the OTP code field is absent or blank.
var ErrCodeEmpty = errors.New("code is required")

// ErrCodeInvalidFormat is returned when the OTP code is not exactly 6 digits.
var ErrCodeInvalidFormat = errors.New("code must be exactly 6 digits")

// ErrUserIDEmpty is returned when the user_id field is absent or blank.
var ErrUserIDEmpty = errors.New("user_id is required")

// ErrIdentifierEmpty is returned when a required identifier field is absent or blank.
var ErrIdentifierEmpty = errors.New("identifier is required")

// ErrNewPasswordEmpty is returned when the new password field is absent or blank.
var ErrNewPasswordEmpty = errors.New("password is required")

// ── Typed errors ─────────────────────────────────────────────────────────────

// LoginLockedError is returned by service.Login when login_locked_until is set
// and still in the future. It wraps ErrLoginLocked and carries RetryAfter so
// the HTTP handler can set the Retry-After response header precisely.
type LoginLockedError struct {
	RetryAfter time.Duration
}

// Error returns the ErrLoginLocked error message.
func (e *LoginLockedError) Error() string { return errLoginLockedMsg }

// Unwrap returns ErrLoginLocked so errors.Is works with the sentinel.
func (e *LoginLockedError) Unwrap() error { return ErrLoginLocked }
