package authshared

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

const (
	minPasswordLen = 8
	// maxPasswordLen matches bcrypt's hard truncation boundary.
	maxPasswordLen = 72
)

// reOTPCode matches exactly six ASCII decimal digits.
var reOTPCode = regexp.MustCompile(`^\d{6}$`)

// reEmail is a minimal sanity check: local@domain.tld where each segment is
// at least one character. It intentionally does not enforce RFC 5321 in full —
// the DB unique index and SMTP delivery are the authoritative validators.
// Length is checked before this regex runs (§2.22 ReDoS guard).
var reEmail = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// ParseUserID parses a standard UUID string into a [16]byte.
//
// logPrefix must be the caller's qualified method name (e.g.
// "profile.GetUserProfile") and is used to produce a wrapped error that
// matches Conventions §3.4 error-wrapping style.
func ParseUserID(logPrefix, userID string) ([16]byte, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return [16]byte{}, fmt.Errorf("%s: parse user id: %w", logPrefix, err)
	}
	return [16]byte(uid), nil
}

// NormaliseEmail trims whitespace, lowercases, and validates the result.
// Returns ErrEmailEmpty if blank after trimming, ErrEmailTooLong if the
// normalised address exceeds 254 bytes, or ErrEmailInvalid if the address
// does not match the expected local@domain.tld format.
func NormaliseEmail(s string) (string, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return "", ErrEmailEmpty
	}
	if len(s) > 254 {
		return "", ErrEmailTooLong
	}
	// Format check comes after length so the regex never runs on very long
	// inputs — preserving the ReDoS-safe guard ordering (RULES.md §2.22).
	if !reEmail.MatchString(s) {
		return "", ErrEmailInvalid
	}
	return s, nil
}

// ValidatePassword validates a new password against the strength rules:
// length (8–72 bytes), uppercase, lowercase, digit, and symbol.
// Returns the first sentinel error encountered. Does NOT trim: whitespace is
// a valid password character.
func ValidatePassword(password string) error {
	switch n := len(password); {
	case n == 0:
		return ErrPasswordEmpty
	case n < minPasswordLen:
		return ErrPasswordTooShort
	case n > maxPasswordLen:
		return ErrPasswordTooLong
	}
	return checkPasswordStrength(password)
}

// ValidateOTPCode returns an error if code is not exactly six ASCII digits.
func ValidateOTPCode(code string) error {
	if code == "" {
		return ErrCodeEmpty
	}
	if !reOTPCode.MatchString(code) {
		return ErrCodeInvalidFormat
	}
	return nil
}

// IsPasswordStrengthError reports whether err is one of the seven
// password-strength validation sentinels.
func IsPasswordStrengthError(err error) bool {
	return errors.Is(err, ErrPasswordEmpty) ||
		errors.Is(err, ErrPasswordTooShort) ||
		errors.Is(err, ErrPasswordTooLong) ||
		errors.Is(err, ErrPasswordNoUpper) ||
		errors.Is(err, ErrPasswordNoLower) ||
		errors.Is(err, ErrPasswordNoDigit) ||
		errors.Is(err, ErrPasswordNoSymbol)
}

// checkPasswordStrength enforces character-class diversity.
func checkPasswordStrength(password string) error {
	var hasUpper, hasLower, hasDigit, hasSymbol bool
	for _, r := range password {
		switch {
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= 'a' && r <= 'z':
			hasLower = true
		case r >= '0' && r <= '9':
			hasDigit = true
		default:
			if r > ' ' {
				hasSymbol = true
			}
		}
	}
	switch {
	case !hasUpper:
		return ErrPasswordNoUpper
	case !hasLower:
		return ErrPasswordNoLower
	case !hasDigit:
		return ErrPasswordNoDigit
	case !hasSymbol:
		return ErrPasswordNoSymbol
	}
	return nil
}
