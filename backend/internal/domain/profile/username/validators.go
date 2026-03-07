package username

import (
	"regexp"
	"strings"
)

// charsetRe matches strings composed entirely of lowercase letters, digits, and underscores.
var charsetRe = regexp.MustCompile(`^[a-z0-9_]+$`)

// NormaliseAndValidateUsername trims whitespace, lowercases the input, and
// validates it against the username format rules:
//   - 3–30 characters
//   - only [a-z0-9_] after normalisation
//   - must not start or end with '_'
//   - must not contain consecutive '__'
//
// Returns the normalised username on success, or a sentinel error on failure.
func NormaliseAndValidateUsername(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))

	if len(s) == 0 {
		return "", ErrUsernameEmpty
	}
	if len(s) < 3 {
		return "", ErrUsernameTooShort
	}
	if len(s) > 30 {
		return "", ErrUsernameTooLong
	}
	if !charsetRe.MatchString(s) {
		return "", ErrUsernameInvalidChars
	}
	// Must not start or end with '_', and must not contain consecutive '__'.
	if s[0] == '_' || s[len(s)-1] == '_' || strings.Contains(s, "__") {
		return "", ErrUsernameInvalidFormat
	}

	return s, nil
}
