// Package telegram handles Telegram Login Widget authentication: callback, link,
// and unlink.
package telegram

import "errors"

// ErrInvalidTelegramSignature is returned when the HMAC-SHA256 signature on
// the Telegram widget payload does not match the expected value.
var ErrInvalidTelegramSignature = errors.New("invalid telegram signature")

// ErrTelegramAuthDateExpired is returned when the auth_date field in the
// widget payload is more than 86400 seconds old or more than 60 seconds in
// the future (replay protection).
var ErrTelegramAuthDateExpired = errors.New("telegram auth_date too old or in future")

// ErrProviderAlreadyLinked is returned when the authenticated user already has
// a Telegram identity linked to their account.
var ErrProviderAlreadyLinked = errors.New("telegram account already linked to this user")

// ErrProviderUIDTaken is returned when the Telegram user ID in the widget
// payload is already linked to a different platform account.
var ErrProviderUIDTaken = errors.New("telegram account already linked to another user")

// ErrProviderNotLinked is returned when the authenticated user does not have
// a Telegram identity linked and an unlink is requested.
var ErrProviderNotLinked = errors.New("no telegram identity linked to this account")
