// Package oauthsharedtest provides test-only helpers shared across all oauth
// feature sub-packages. It must never be imported by production code.
package oauthsharedtest

import (
	"context"

	"github.com/7-Dany/store/backend/internal/domain/oauth/google"
	"github.com/7-Dany/store/backend/internal/domain/oauth/telegram"
)

// ─────────────────────────────────────────────────────────────────────────────
// GoogleFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

// GoogleFakeServicer is a hand-written implementation of google.Servicer for
// handler unit tests. Each method delegates to its Fn field if non-nil,
// otherwise returns a safe zero-value default.
type GoogleFakeServicer struct {
	HandleCallbackFn func(ctx context.Context, in google.CallbackInput) (google.CallbackResult, error)
	UnlinkGoogleFn   func(ctx context.Context, userID [16]byte, ipAddress, userAgent string) error
}

// compile-time interface check.
var _ google.Servicer = (*GoogleFakeServicer)(nil)

// HandleCallback delegates to HandleCallbackFn if set.
// Default: returns zero CallbackResult and nil error.
func (f *GoogleFakeServicer) HandleCallback(ctx context.Context, in google.CallbackInput) (google.CallbackResult, error) {
	if f.HandleCallbackFn != nil {
		return f.HandleCallbackFn(ctx, in)
	}
	return google.CallbackResult{}, nil
}

// UnlinkGoogle delegates to UnlinkGoogleFn if set.
// Default: returns nil error.
func (f *GoogleFakeServicer) UnlinkGoogle(ctx context.Context, userID [16]byte, ipAddress, userAgent string) error {
	if f.UnlinkGoogleFn != nil {
		return f.UnlinkGoogleFn(ctx, userID, ipAddress, userAgent)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// TelegramFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

// TelegramFakeServicer is a hand-written implementation of telegram.Servicer for
// handler unit tests. Each method delegates to its Fn field if non-nil,
// otherwise returns a safe zero-value default.
type TelegramFakeServicer struct {
	HandleCallbackFn func(ctx context.Context, in telegram.CallbackInput) (telegram.CallbackResult, error)
	LinkTelegramFn   func(ctx context.Context, in telegram.LinkInput) error
	UnlinkTelegramFn func(ctx context.Context, userID [16]byte, ipAddress, userAgent string) error
}

// compile-time interface check.
var _ telegram.Servicer = (*TelegramFakeServicer)(nil)

// HandleCallback delegates to HandleCallbackFn if set.
// Default: returns zero CallbackResult and nil error.
func (f *TelegramFakeServicer) HandleCallback(ctx context.Context, in telegram.CallbackInput) (telegram.CallbackResult, error) {
	if f.HandleCallbackFn != nil {
		return f.HandleCallbackFn(ctx, in)
	}
	return telegram.CallbackResult{}, nil
}

// LinkTelegram delegates to LinkTelegramFn if set.
// Default: returns nil error.
func (f *TelegramFakeServicer) LinkTelegram(ctx context.Context, in telegram.LinkInput) error {
	if f.LinkTelegramFn != nil {
		return f.LinkTelegramFn(ctx, in)
	}
	return nil
}

// UnlinkTelegram delegates to UnlinkTelegramFn if set.
// Default: returns nil error.
func (f *TelegramFakeServicer) UnlinkTelegram(ctx context.Context, userID [16]byte, ipAddress, userAgent string) error {
	if f.UnlinkTelegramFn != nil {
		return f.UnlinkTelegramFn(ctx, userID, ipAddress, userAgent)
	}
	return nil
}
