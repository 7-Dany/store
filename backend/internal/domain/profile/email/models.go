// Package email provides the HTTP handler, service, and store for the
// three-step email-change flow:
//   POST /email/request-change   — step 1: OTP sent to current address
//   POST /email/verify-current   — step 2: verifies current address, issues grant token
//   POST /email/confirm-change   — step 3: OTP sent to new address, swaps the email
package email

// EmailChangeRequestInput is the service-layer input for step 1
// (POST /email/request-change).
// UserID is a [16]byte parsed from the JWT user_id claim by the handler.
type EmailChangeRequestInput struct {
	UserID    [16]byte
	NewEmail  string
	IPAddress string
	UserAgent string
}

// EmailChangeVerifyCurrentInput is the service-layer input for step 2
// (POST /email/verify-current).
type EmailChangeVerifyCurrentInput struct {
	UserID    [16]byte
	Code      string
	IPAddress string
	UserAgent string
}

// EmailChangeRequestResult is returned by Service.RequestEmailChange on success.
// CurrentEmail is the user's existing email address (OTP is sent there).
// RawCode is the plaintext OTP the handler should enqueue for delivery.
type EmailChangeRequestResult struct {
	CurrentEmail string
	RawCode      string
}

// EmailChangeVerifyCurrentResult is the service result for step 2 on success.
// GrantToken is the opaque UUID the client holds for step 3.
// ExpiresIn is always 600 seconds.
// NewEmail is the address the new-email OTP is sent to (for the handler's mail enqueue).
// NewEmailRawCode is the plaintext OTP for the new address.
type EmailChangeVerifyCurrentResult struct {
	GrantToken      string
	ExpiresIn       int // always 600
	NewEmail        string
	NewEmailRawCode string
}

// ConfirmEmailChangeResult is returned by Service.ConfirmEmailChange on success.
// OldEmail is the address that was replaced; the handler enqueues a notification there.
type ConfirmEmailChangeResult struct {
	OldEmail string
}

// EmailChangeConfirmInput is the service-layer input for step 3
// (POST /email/confirm-change).
// AccessJTI is the JTI of the caller's current access token, extracted by the
// handler from the JWT claims and used to blocklist the token after a
// successful email change.
type EmailChangeConfirmInput struct {
	UserID     [16]byte
	GrantToken string
	Code       string
	IPAddress  string
	UserAgent  string
	AccessJTI  string
}
