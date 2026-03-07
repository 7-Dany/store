package register_test

import (
	"strings"
	"testing"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/7-Dany/store/backend/internal/domain/auth/register"
	"github.com/stretchr/testify/require"
)

// TestValidateAndNormalise exercises every validation and normalisation branch
// in validateAndNormalise via the exported helpers in export_test.go.
//
// Rules under test:
//
//	display_name : trimmed, non-empty, ≤100 runes, no ASCII control chars (< 0x20)
//	email        : trimmed, lowercased, IDNA-normalised, ≤254 bytes pre- and post-IDNA,
//	               valid local@domain.tld format, no display-name wrapper, DNS labels ≤63 chars
//	password     : non-empty, 8–72 bytes, uppercase, lowercase, digit, symbol
func TestValidateAndNormalise(t *testing.T) {
	t.Parallel()

	// ── display_name ──────────────────────────────────────────────────────────

	t.Run("display_name", func(t *testing.T) {
		t.Parallel()

		t.Run("empty → ErrDisplayNameEmpty", func(t *testing.T) {
			t.Parallel()
			req := register.ExportedRegisterRequest("", "alice@example.com", "P@ssw0rd!1")
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrDisplayNameEmpty)
		})

		t.Run("over 100 runes → ErrDisplayNameTooLong", func(t *testing.T) {
			t.Parallel()
			// 101 ASCII runes — rune count, not byte count, is checked.
			longName := strings.Repeat("a", 101)
			req := register.ExportedRegisterRequest(longName, "alice@example.com", "P@ssw0rd!1")
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrDisplayNameTooLong)
		})

		t.Run("control character → ErrDisplayNameInvalid", func(t *testing.T) {
			t.Parallel()
			// 0x01 is an ASCII control character (< 0x20). The validator rejects
			// all code points in the range [0x00, 0x1F].
			req := register.ExportedRegisterRequest("alice\x01", "alice@example.com", "P@ssw0rd!1")
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrDisplayNameInvalid)
		})

		t.Run("exactly 100 runes → nil", func(t *testing.T) {
			t.Parallel()
			// 100 ASCII runes — exactly at the limit, must be accepted.
			name := strings.Repeat("a", 100)
			req := register.ExportedRegisterRequest(name, "alice@example.com", "P@ssw0rd!1")
			err := register.ExportedValidateAndNormalise(&req)
			require.NoError(t, err, "display_name of exactly 100 runes must be accepted")
		})

		t.Run("NUL byte → ErrDisplayNameInvalid", func(t *testing.T) {
			t.Parallel()
			// 0x00 is the lowest code point in the [0x00, 0x1F] rejected range.
			req := register.ExportedRegisterRequest("alice\x00", "alice@example.com", "P@ssw0rd!1")
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrDisplayNameInvalid,
				"NUL byte (0x00) must be rejected as an ASCII control character")
		})

		t.Run("leading/trailing whitespace → trimmed in-place", func(t *testing.T) {
			t.Parallel()
			req := register.ExportedRegisterRequest("  alice  ", "alice@example.com", "P@ssw0rd!1")
			err := register.ExportedValidateAndNormalise(&req)
			require.NoError(t, err)
			require.Equal(t, "alice", req.DisplayName,
				"leading and trailing whitespace must be stripped from display_name")
		})
	})

	// ── email ─────────────────────────────────────────────────────────────────

	t.Run("email", func(t *testing.T) {
		t.Parallel()

		t.Run("uppercased → lowercased in-place", func(t *testing.T) {
			t.Parallel()
			req := register.ExportedRegisterRequest("Alice", "ALICE@EXAMPLE.COM", "P@ssw0rd!1")
			err := register.ExportedValidateAndNormalise(&req)
			require.NoError(t, err)
			require.Equal(t, "alice@example.com", req.Email,
				"email must be fully lowercased after normalisation")
		})

		t.Run("leading/trailing whitespace → trimmed in-place", func(t *testing.T) {
			t.Parallel()
			req := register.ExportedRegisterRequest("Alice", "  alice@example.com  ", "P@ssw0rd!1")
			err := register.ExportedValidateAndNormalise(&req)
			require.NoError(t, err)
			require.Equal(t, "alice@example.com", req.Email,
				"leading and trailing whitespace must be stripped from email")
		})

		t.Run("invalid format → ErrEmailInvalid", func(t *testing.T) {
			t.Parallel()
			req := register.ExportedRegisterRequest("Alice", "not-an-email", "P@ssw0rd!1")
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrEmailInvalid)
		})

		t.Run("over 254 bytes → ErrEmailTooLong", func(t *testing.T) {
			t.Parallel()
			// 243 'a's + "@example.com" = 255 bytes — one over the RFC 5321 limit.
			local := strings.Repeat("a", 243)
			req := register.ExportedRegisterRequest("Alice", local+"@example.com", "P@ssw0rd!1")
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrEmailTooLong)
		})

		t.Run("display-name format → ErrEmailInvalid", func(t *testing.T) {
			t.Parallel()
			// RFC 5322 display-name syntax such as "Bob <bob@example.com>" is
			// rejected: the space before '<' fails the reEmail sanity check.
			req := register.ExportedRegisterRequest("Alice", "Bob <bob@example.com>", "P@ssw0rd!1")
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrEmailInvalid)
		})

		t.Run("IDNA unicode domain → ASCII punycode", func(t *testing.T) {
			t.Parallel()
			// "münchen.de" contains a non-ASCII character (ü). After IDNA Lookup
			// normalisation the domain must appear as its punycode equivalent
			// ("xn--mnchen-3ya.de").
			req := register.ExportedRegisterRequest("Alice", "user@münchen.de", "P@ssw0rd!1")
			err := register.ExportedValidateAndNormalise(&req)
			require.NoError(t, err)
			require.Contains(t, req.Email, "xn--",
				"unicode domain must be converted to punycode ACE form")
		})

		t.Run("DNS label over 63 chars → ErrEmailInvalid", func(t *testing.T) {
			t.Parallel()
			// idna.Lookup.ToASCII (IDNA Lookup profile, RFC 5891 §5.4) rejects
			// labels exceeding 63 octets, including pure-ASCII labels.
			longLabel := strings.Repeat("a", 64)
			req := register.ExportedRegisterRequest("Alice", "user@"+longLabel+".com", "P@ssw0rd!1")
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrEmailInvalid)
		})

		t.Run("domain with hyphen-leading label → ErrEmailInvalid (idna rejects)", func(t *testing.T) {
			t.Parallel()
			// A DNS label may not start with a hyphen (RFC 5891 §5.4). mail.ParseAddress
			// accepts the address, but idna.Lookup.ToASCII returns an error, which
			// validateAndNormalise maps to ErrEmailInvalid. This exercises the idna
			// error-path (separate from label-length enforcement).
			req := register.ExportedRegisterRequest("Alice", "user@-invalid.com", "P@ssw0rd!1")
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrEmailInvalid,
				"domain starting with a hyphen must be rejected via idna error path")
		})

		t.Run("post-IDNA expansion over 254 bytes → ErrEmailTooLong", func(t *testing.T) {
			t.Parallel()
			// Construct an email that fits within 254 UTF-8 bytes before IDNA
			// conversion but exceeds 254 bytes after punycode expansion, while the
			// resulting domain (including label dots) stays ≤ 253 bytes so that
			// idna.Lookup.ToASCII does not reject it on DNS domain-length grounds.
			//
			// Construction:
			//   local part : "a" × 60          (60 bytes)
			//   domain     : "münchen." × 12 + "com"
			//     "münchen" in UTF-8 = 8 bytes (ü is 2 bytes); with dot = 9 bytes
			//     pre-IDNA domain  = 12×9 + 3 = 111 bytes
			//     pre-IDNA total   = 60 + 1 + 111 = 172 ≤ 254  ✓
			//     "xn--mnchen-3ya" after punycode = 14 chars; with dot = 15 chars
			//     post-IDNA domain = 12×15 - 1 + 3 = 182 chars  ≤ 253  ✓ (idna accepts)
			//     post-IDNA total  = 60 + 1 + 182 = 243 bytes
			//   Hmm, 243 ≤ 254. Let’s use a longer local to push past 254:
			//   local : "a" × 73, domain : "münchen." × 12 + "com"
			//     pre-IDNA  = 73 + 1 + 111 = 185 ≤ 254  ✓
			//     post-IDNA = 73 + 1 + 182 = 256 > 254  ✓
			local := strings.Repeat("a", 73)
			domain := strings.Repeat("münchen.", 12) + "com"
			req := register.ExportedRegisterRequest("Alice", local+"@"+domain, "P@ssw0rd!1")
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrEmailTooLong,
				"email exceeding 254 bytes after IDNA expansion must be rejected as ErrEmailTooLong")
		})
	})

	// ── password ──────────────────────────────────────────────────────────────

	t.Run("password", func(t *testing.T) {
		t.Parallel()

		t.Run("empty → ErrPasswordEmpty", func(t *testing.T) {
			t.Parallel()
			req := register.ExportedRegisterRequest("Alice", "alice@example.com", "")
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrPasswordEmpty)
		})

		t.Run("too short (< 8 bytes) → ErrPasswordTooShort", func(t *testing.T) {
			t.Parallel()
			// "Ab1!" is 4 bytes — has upper, digit, symbol, but is too short.
			req := register.ExportedRegisterRequest("Alice", "alice@example.com", "Ab1!")
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrPasswordTooShort)
		})

		t.Run("no uppercase letter → ErrPasswordNoUpper", func(t *testing.T) {
			t.Parallel()
			// Has lowercase, digit, and symbol but no uppercase letter.
			req := register.ExportedRegisterRequest("Alice", "alice@example.com", "p@ssw0rd!1")
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrPasswordNoUpper)
		})

		t.Run("no lowercase letter → ErrPasswordNoLower", func(t *testing.T) {
			t.Parallel()
			// Has uppercase, digit, and symbol but no lowercase letter.
			req := register.ExportedRegisterRequest("Alice", "alice@example.com", "P@SSW0RD!1")
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrPasswordNoLower)
		})

		t.Run("no digit → ErrPasswordNoDigit", func(t *testing.T) {
			t.Parallel()
			// Has uppercase, lowercase, and symbol but no digit.
			req := register.ExportedRegisterRequest("Alice", "alice@example.com", "P@ssword!!")
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrPasswordNoDigit)
		})

		t.Run("no symbol → ErrPasswordNoSymbol", func(t *testing.T) {
			t.Parallel()
			// Has uppercase, lowercase, and digit but no symbol character.
			req := register.ExportedRegisterRequest("Alice", "alice@example.com", "Passw0rd11")
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrPasswordNoSymbol)
		})

		t.Run("too long (> 72 bytes) → ErrPasswordTooLong", func(t *testing.T) {
			t.Parallel()
			// 73 bytes — one over bcrypt's hard truncation limit.
			// authshared.ValidatePassword catches this before any hashing occurs.
			long := strings.Repeat("A1!", 25) // 75 bytes, well over 72
			req := register.ExportedRegisterRequest("Alice", "alice@example.com", long)
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrPasswordTooLong)
		})

		t.Run("valid password → nil", func(t *testing.T) {
			t.Parallel()
			// Satisfies all rules: length ≥8, uppercase, lowercase, digit, symbol.
			req := register.ExportedRegisterRequest("Alice", "alice@example.com", "P@ssw0rd!1")
			err := register.ExportedValidateAndNormalise(&req)
			require.NoError(t, err)
		})
	})

	// ── username ──────────────────────────────────────────────────────────────

	t.Run("username", func(t *testing.T) {
		t.Parallel()

		t.Run("omitted (empty) → nil (optional field)", func(t *testing.T) {
			t.Parallel()
			// Username is optional at registration. An empty string must be accepted
			// without triggering any validation error.
			req := register.ExportedRegisterRequest("Alice", "alice@example.com", "P@ssw0rd!1")
			err := register.ExportedValidateAndNormalise(&req)
			require.NoError(t, err, "empty username must be accepted as the optional-field path")
		})

		t.Run("too short (< 3 chars) → ErrUsernameTooShort", func(t *testing.T) {
			t.Parallel()
			req := register.ExportedRegisterRequestWithUsername("Alice", "alice@example.com", "P@ssw0rd!1", "ab")
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrUsernameTooShort)
		})

		t.Run("exactly 3 chars → nil (lower boundary accepted)", func(t *testing.T) {
			t.Parallel()
			req := register.ExportedRegisterRequestWithUsername("Alice", "alice@example.com", "P@ssw0rd!1", "abc")
			err := register.ExportedValidateAndNormalise(&req)
			require.NoError(t, err, "username of exactly 3 characters must be accepted")
		})

		t.Run("too long (> 30 chars) → ErrUsernameTooLong", func(t *testing.T) {
			t.Parallel()
			long := strings.Repeat("a", 31)
			req := register.ExportedRegisterRequestWithUsername("Alice", "alice@example.com", "P@ssw0rd!1", long)
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrUsernameTooLong)
		})

		t.Run("exactly 30 chars → nil (upper boundary accepted)", func(t *testing.T) {
			t.Parallel()
			req := register.ExportedRegisterRequestWithUsername("Alice", "alice@example.com", "P@ssw0rd!1", strings.Repeat("a", 30))
			err := register.ExportedValidateAndNormalise(&req)
			require.NoError(t, err, "username of exactly 30 characters must be accepted")
		})

		t.Run("invalid chars (symbol) → ErrUsernameInvalidChars", func(t *testing.T) {
			t.Parallel()
			req := register.ExportedRegisterRequestWithUsername("Alice", "alice@example.com", "P@ssw0rd!1", "alice!")
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrUsernameInvalidChars)
		})

		t.Run("invalid chars (hyphen) → ErrUsernameInvalidChars", func(t *testing.T) {
			t.Parallel()
			// Hyphens are not in [a-z0-9_]; a common mistake to allow them.
			req := register.ExportedRegisterRequestWithUsername("Alice", "alice@example.com", "P@ssw0rd!1", "alice-bob")
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrUsernameInvalidChars)
		})

		t.Run("leading underscore → ErrUsernameInvalidFormat", func(t *testing.T) {
			t.Parallel()
			req := register.ExportedRegisterRequestWithUsername("Alice", "alice@example.com", "P@ssw0rd!1", "_alice")
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrUsernameInvalidFormat)
		})

		t.Run("trailing underscore → ErrUsernameInvalidFormat", func(t *testing.T) {
			t.Parallel()
			req := register.ExportedRegisterRequestWithUsername("Alice", "alice@example.com", "P@ssw0rd!1", "alice_")
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrUsernameInvalidFormat)
		})

		t.Run("consecutive underscores → ErrUsernameInvalidFormat", func(t *testing.T) {
			t.Parallel()
			req := register.ExportedRegisterRequestWithUsername("Alice", "alice@example.com", "P@ssw0rd!1", "alice__bob")
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrUsernameInvalidFormat)
		})

		t.Run("uppercase letters → lowercased in-place", func(t *testing.T) {
			t.Parallel()
			req := register.ExportedRegisterRequestWithUsername("Alice", "alice@example.com", "P@ssw0rd!1", "ALICE123")
			err := register.ExportedValidateAndNormalise(&req)
			require.NoError(t, err)
			require.Equal(t, "alice123", req.Username,
				"username must be fully lowercased after normalisation")
		})

		t.Run("leading/trailing whitespace → trimmed and accepted", func(t *testing.T) {
			t.Parallel()
			req := register.ExportedRegisterRequestWithUsername("Alice", "alice@example.com", "P@ssw0rd!1", "  alice  ")
			err := register.ExportedValidateAndNormalise(&req)
			require.NoError(t, err)
			require.Equal(t, "alice", req.Username,
				"leading and trailing whitespace must be stripped from username")
		})

		t.Run("whitespace-only → ErrUsernameEmpty", func(t *testing.T) {
			t.Parallel()
			// After trimming, a whitespace-only value collapses to empty. The
			// validator must return ErrUsernameEmpty, not ErrUsernameTooShort,
			// because the field is logically absent after normalisation.
			req := register.ExportedRegisterRequestWithUsername("Alice", "alice@example.com", "P@ssw0rd!1", "   ")
			err := register.ExportedValidateAndNormalise(&req)
			require.ErrorIs(t, err, authshared.ErrUsernameEmpty)
		})
	})
}
