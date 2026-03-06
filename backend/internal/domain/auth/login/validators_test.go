package login_test

import (
	"strings"
	"testing"

	"github.com/7-Dany/store/backend/internal/domain/auth/login"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/stretchr/testify/require"
)

func TestValidateLoginRequest_HappyPath_Email(t *testing.T) {
	t.Parallel()
	req, err := login.ValidateLoginForTest("Alice@EXAMPLE.COM", "anypass")
	require.NoError(t, err)
	require.Equal(t, "alice@example.com", req.Identifier) // email lowercased
}

func TestValidateLoginRequest_HappyPath_Username(t *testing.T) {
	t.Parallel()
	req, err := login.ValidateLoginForTest("MyUser", "anypass")
	require.NoError(t, err)
	require.Equal(t, "MyUser", req.Identifier) // username case preserved
}

func TestValidateLoginRequest_EmptyIdentifier(t *testing.T) {
	t.Parallel()
	_, err := login.ValidateLoginForTest("", "pass")
	require.ErrorIs(t, err, authshared.ErrIdentifierEmpty)
}

func TestValidateLoginRequest_EmptyPassword(t *testing.T) {
	t.Parallel()
	_, err := login.ValidateLoginForTest("user@example.com", "")
	require.ErrorIs(t, err, authshared.ErrPasswordEmpty)
}

func TestValidateLoginRequest_WhitespaceOnlyIdentifier(t *testing.T) {
	t.Parallel()
	_, err := login.ValidateLoginForTest("   ", "pass")
	require.ErrorIs(t, err, authshared.ErrIdentifierEmpty)
}

func TestValidateLoginRequest_IdentifierTooLong(t *testing.T) {
	t.Parallel()
	// 255 bytes is one over the 254-byte cap (maxIdentifierBytes).
	identifier := strings.Repeat("a", 255)
	_, err := login.ValidateLoginForTest(identifier, "pass")
	require.ErrorIs(t, err, authshared.ErrIdentifierTooLong)
}

func TestValidateLoginRequest_IdentifierExactly254Bytes(t *testing.T) {
	t.Parallel()
	identifier := strings.Repeat("a", 254)
	req, err := login.ValidateLoginForTest(identifier, "pass")
	require.NoError(t, err)
	require.Equal(t, identifier, req.Identifier)
}

func TestValidateLoginRequest_IdentifierExactly253Bytes(t *testing.T) {
	t.Parallel()
	identifier := strings.Repeat("a", 253)
	req, err := login.ValidateLoginForTest(identifier, "pass")
	require.NoError(t, err)
	require.Equal(t, identifier, req.Identifier)
}

func TestValidateLoginRequest_EmailLeadingTrailingWhitespace(t *testing.T) {
	t.Parallel()
	req, err := login.ValidateLoginForTest("  Alice@EXAMPLE.COM  ", "pass")
	require.NoError(t, err)
	require.Equal(t, "alice@example.com", req.Identifier)
}

func TestValidateLoginRequest_UsernameLeadingTrailingWhitespace(t *testing.T) {
	t.Parallel()
	req, err := login.ValidateLoginForTest("  MyUser  ", "pass")
	require.NoError(t, err)
	require.Equal(t, "MyUser", req.Identifier)
}

func TestValidateLoginRequest_EmailUppercaseLocalPart(t *testing.T) {
	t.Parallel()
	req, err := login.ValidateLoginForTest("ALICE@example.com", "pass")
	require.NoError(t, err)
	require.Equal(t, "alice@example.com", req.Identifier)
}

func TestValidateLoginRequest_AtSignOnly(t *testing.T) {
	t.Parallel()
	// A single "@" is treated as an email (contains '@'), lowercased — still "@".
	req, err := login.ValidateLoginForTest("@", "pass")
	require.NoError(t, err)
	require.Equal(t, "@", req.Identifier)
}

func TestValidateLoginRequest_UsernameStyleWithAt(t *testing.T) {
	t.Parallel()
	// "User@Handle" contains '@', so it is treated as an email and fully lowercased.
	req, err := login.ValidateLoginForTest("User@Handle", "pass")
	require.NoError(t, err)
	require.Equal(t, "user@handle", req.Identifier)
}

func TestValidateLoginRequest_WeakPassword_NilError(t *testing.T) {
	t.Parallel()
	// Login does not re-validate password strength to avoid leaking enumeration info.
	_, err := login.ValidateLoginForTest("user@example.com", "weak")
	require.NoError(t, err)
}

func TestValidateLoginRequest_WhitespaceOnlyPassword_NilError(t *testing.T) {
	t.Parallel()
	// A whitespace-only password is non-empty, so it passes the empty check.
	_, err := login.ValidateLoginForTest("user@example.com", "   ")
	require.NoError(t, err)
}

func TestValidateLoginRequest_IdentifierTrimmedBeforeLengthCheck(t *testing.T) {
	t.Parallel()
	// 258 bytes before TrimSpace, 254 bytes after — must pass because
	// validateLoginRequest trims before checking length.
	identifier := "  " + strings.Repeat("a", 254) + "  "
	req, err := login.ValidateLoginForTest(identifier, "pass")
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("a", 254), req.Identifier)
}
