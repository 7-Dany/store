package register

import (
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
)

// ── Validation constants ───────────────────────────────────────────────────────

const (
	maxDisplayNameLen = 100
	maxEmailLen       = 254
)

// ── Register validation ───────────────────────────────────────────────────────

// validateAndNormalise normalises req in-place then validates it.
//
// Normalisation: display_name is space-trimmed; email is lowercased, trimmed,
// and its domain is converted to ASCII (IDNA). Password is intentionally NOT
// trimmed — whitespace is a valid part of a password. Username, if provided,
// is lowercased and trimmed.
//
// Validation order: display_name → email → password → username (optional).
// Returns the first authshared.ErrXxx sentinel encountered.
func validateAndNormalise(req *registerRequest) error {
	req.DisplayName = strings.TrimSpace(req.DisplayName)

	switch {
	case req.DisplayName == "":
		return authshared.ErrDisplayNameEmpty
	case utf8.RuneCountInString(req.DisplayName) > maxDisplayNameLen:
		return authshared.ErrDisplayNameTooLong
	}

	// Reject any ASCII control character (0x00–0x1F), not just NUL.
	// These can corrupt display rendering, logs, and downstream text processors.
	if strings.IndexFunc(req.DisplayName, func(r rune) bool { return r < 0x20 }) != -1 {
		return authshared.ErrDisplayNameInvalid
	}

	email, err := authshared.NormaliseEmail(req.Email)
	if err != nil {
		return err
	}
	req.Email = email

	// Unreachable: authshared.NormaliseEmail already returns ErrEmailTooLong for
	// any input exceeding 254 bytes; on a successful return req.Email is guaranteed
	// to be ≤ 254 bytes so this case is never entered.
	switch {
	case len(req.Email) > maxEmailLen:
		return authshared.ErrEmailTooLong
	}

	parsed, err := mail.ParseAddress(req.Email)
	if err != nil || parsed.Name != "" {
		return authshared.ErrEmailInvalid
	}

	parts := strings.SplitN(parsed.Address, "@", 2)
	// Security: idna.Lookup.ToASCII applies the IDNA Lookup profile (RFC 5891
	// §5.4), which rejects labels exceeding 63 octets and domains with invalid
	// structure — including pure-ASCII labels that violate DNS label length
	// limits. A non-nil error is mapped to ErrEmailInvalid so the caller
	// receives a consistent sentinel regardless of the IDNA failure reason.
	asciiDomain, err := idna.Lookup.ToASCII(parts[1])
	if err != nil {
		return authshared.ErrEmailInvalid
	}
	// idna.Lookup does not enforce the 63-octet DNS label limit for pure-ASCII
	// labels, so we check it explicitly (RFC 1035 §2.3.4, RFC 5891 §5.4).
	for _, label := range strings.Split(asciiDomain, ".") {
		if len(label) > 63 {
			return authshared.ErrEmailInvalid
		}
	}
	req.Email = strings.ToLower(parts[0] + "@" + asciiDomain)

	// Re-check length after IDNA normalisation: a unicode domain can expand
	// significantly when converted to its ASCII-compatible encoding (punycode),
	// making an email that was ≤254 bytes before conversion exceed the RFC 5321
	// limit afterwards.
	if len(req.Email) > maxEmailLen {
		return authshared.ErrEmailTooLong
	}

	if err := authshared.ValidatePassword(req.Password); err != nil {
		return err
	}

	// Username is optional at registration. If provided, normalise and validate it.
	if req.Username != "" {
		norm, err := normaliseAndValidateUsername(req.Username)
		if err != nil {
			return err
		}
		req.Username = norm
	}
	return nil
}

// usernameCharsetRe mirrors the rule in profile/username — duplicated here to
// avoid a cross-domain import.
var usernameCharsetRe = regexp.MustCompile(`^[a-z0-9_]+$`)

// normaliseAndValidateUsername trims, lowercases, and validates a username.
// Returns the normalised value or a sentinel error.
func normaliseAndValidateUsername(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) == 0 {
		return "", authshared.ErrUsernameEmpty
	}
	if len(s) < 3 {
		return "", authshared.ErrUsernameTooShort
	}
	if len(s) > 30 {
		return "", authshared.ErrUsernameTooLong
	}
	if !usernameCharsetRe.MatchString(s) {
		return "", authshared.ErrUsernameInvalidChars
	}
	if s[0] == '_' || s[len(s)-1] == '_' || strings.Contains(s, "__") {
		return "", authshared.ErrUsernameInvalidFormat
	}
	return s, nil
}
