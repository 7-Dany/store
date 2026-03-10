package deleteaccount

import "errors"

// ErrAlreadyPendingDeletion is returned when the user's deleted_at is already set.
// Maps to 409 already_pending_deletion.
var ErrAlreadyPendingDeletion = errors.New("account is already scheduled for deletion")

// ErrNotPendingDeletion is returned by CancelDeletion when deleted_at is NULL.
// Maps to 409 not_pending_deletion.
var ErrNotPendingDeletion = errors.New("account is not scheduled for deletion")

// ErrInvalidTelegramAuth is returned when Telegram HMAC verification fails
// or when auth_date is more than 86400 seconds old (replay protection).
// Maps to 401 invalid_telegram_auth.
var ErrInvalidTelegramAuth = errors.New("telegram authentication is invalid or expired")

// ErrTelegramIdentityMismatch is returned when the Telegram payload id does
// not match the provider_uid stored in user_identities for this user.
// Maps to 401 telegram_identity_mismatch.
var ErrTelegramIdentityMismatch = errors.New("telegram identity does not match linked account")
