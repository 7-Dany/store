package email

import (
	"net/mail"
	"regexp"
	"strings"

	"golang.org/x/net/idna"
)

const maxEmailLen = 254

// reOTPCode matches exactly six ASCII decimal digits.
var reOTPCode = regexp.MustCompile(`^\d{6}$`)

// NormaliseAndValidateNewEmail trims whitespace, lowercases, applies IDNA
// normalisation to the domain, and validates the resulting address.
//
// Normalisation steps:
//  1. Trim leading/trailing whitespace.
//  2. Lowercase the entire address.
//  3. Parse with net/mail to reject display-name forms ("Name <addr>") and
//     structurally invalid addresses.
//  4. Apply idna.Lookup.ToASCII to the domain (RFC 5891 §5.4).
//  5. Enforce individual DNS label length (≤ 63 octets, RFC 1035 §2.3.4).
//  6. Re-check total byte length after IDNA expansion.
//
// Returns the normalised address on success, or a sentinel error on failure:
//   - ErrInvalidEmailFormat for any structural or IDNA failure.
//   - ErrEmailTooLong if the normalised address exceeds 254 bytes.
func NormaliseAndValidateNewEmail(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))

	if s == "" {
		return "", ErrInvalidEmailFormat
	}
	if len(s) > maxEmailLen {
		return "", ErrEmailTooLong
	}

	parsed, err := mail.ParseAddress(s)
	if err != nil || parsed.Name != "" {
		return "", ErrInvalidEmailFormat
	}

	parts := strings.SplitN(parsed.Address, "@", 2)
	if len(parts) != 2 {
		return "", ErrInvalidEmailFormat
	}

	// Apply IDNA Lookup profile — rejects invalid labels, enforces structure.
	asciiDomain, err := idna.Lookup.ToASCII(parts[1])
	if err != nil {
		return "", ErrInvalidEmailFormat
	}

	// idna.Lookup does not enforce the 63-octet DNS label limit for pure-ASCII
	// labels; check explicitly (RFC 1035 §2.3.4, RFC 5891 §5.4).
	for _, label := range strings.Split(asciiDomain, ".") {
		if len(label) > 63 {
			return "", ErrInvalidEmailFormat
		}
	}

	normalised := strings.ToLower(parts[0] + "@" + asciiDomain)

	// Re-check length after IDNA expansion: unicode domains can grow when
	// converted to punycode, pushing a previously-valid address past 254 bytes.
	if len(normalised) > maxEmailLen {
		return "", ErrEmailTooLong
	}

	return normalised, nil
}

// ValidateOTPCode returns ErrInvalidCodeFormat if code is not exactly 6 ASCII digits.
func ValidateOTPCode(code string) error {
	if !reOTPCode.MatchString(code) {
		return ErrInvalidCodeFormat
	}
	return nil
}

// ValidateGrantToken returns ErrGrantTokenEmpty if token is blank after trimming.
func ValidateGrantToken(token string) error {
	if strings.TrimSpace(token) == "" {
		return ErrGrantTokenEmpty
	}
	return nil
}
