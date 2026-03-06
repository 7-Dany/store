package setpassword

import "errors"

// ErrPasswordAlreadySet is returned when the caller already has a password_hash
// on their account. POST /set-password is only valid for OAuth-only accounts.
// Use POST /change-password to update an existing password.
var ErrPasswordAlreadySet = errors.New("account already has a password — use change-password to update it")
