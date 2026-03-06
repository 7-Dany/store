package login

import (
	"strings"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
)

// maxIdentifierBytes is the maximum number of bytes accepted for the identifier
// field (email or username) after normalisation. 254 matches the RFC 5321
// maximum email address length and is a reasonable upper bound for usernames.
const maxIdentifierBytes = 254

// validateLoginRequest normalises and validates the login payload in-place.
//
// Normalisation: Identifier is trimmed. When it contains '@' it is also
// lowercased so that login is case-insensitive for email addresses. Usernames
// do NOT contain '@' by convention, so they are left as-is, preserving their
// case-sensitive semantics.
//
// Intentionally minimal: password strength is NOT re-validated on login, and
// identifier format is NOT validated, to avoid leaking enumeration info.
func validateLoginRequest(req *loginRequest) error {
	req.Identifier = strings.TrimSpace(req.Identifier)
	if strings.ContainsRune(req.Identifier, '@') {
		req.Identifier = strings.ToLower(req.Identifier)
	}
	if req.Identifier == "" {
		return authshared.ErrIdentifierEmpty
	}
	if len(req.Identifier) > maxIdentifierBytes {
		return authshared.ErrIdentifierTooLong
	}
	if req.Password == "" {
		return authshared.ErrPasswordEmpty
	}
	return nil
}
