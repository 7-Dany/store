// Package google handles Google OAuth authentication: initiate, callback, and unlink.
package google

import "errors"

// ErrTokenExchangeFailed is returned when the code-exchange request to Google's
// token endpoint fails (network error, invalid code, or non-2xx response).
var ErrTokenExchangeFailed = errors.New("google token exchange failed")

// ErrInvalidIDToken is returned when the ID token returned by Google fails
// oidc verification — invalid signature, wrong audience, or expired.
var ErrInvalidIDToken = errors.New("google id token verification failed")
