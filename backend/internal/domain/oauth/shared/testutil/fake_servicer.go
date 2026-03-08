// Package oauthsharedtest provides test-only helpers shared across all oauth
// feature sub-packages. It must never be imported by production code.
package oauthsharedtest

import (
	"context"

	"github.com/7-Dany/store/backend/internal/domain/oauth/google"
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
