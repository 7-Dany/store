package bootstrap

import "errors"

// ErrUserNotActive is returned when the target user's account is not active.
var ErrUserNotActive = errors.New("user account is not active")

// ErrUserNotVerified is returned when the target user's email address has not
// been verified.
var ErrUserNotVerified = errors.New("user email address is not verified")

// ErrBootstrapSecretEmpty is returned when bootstrap_secret is absent from
// the request body.
var ErrBootstrapSecretEmpty = errors.New("bootstrap_secret is required")

// ErrInvalidBootstrapSecret is returned when bootstrap_secret does not match
// the value configured in BOOTSTRAP_SECRET.
var ErrInvalidBootstrapSecret = errors.New("invalid bootstrap secret")
