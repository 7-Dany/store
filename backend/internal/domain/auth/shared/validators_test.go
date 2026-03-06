package authshared_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
)

// ─── ParseUserID ─────────────────────────────────────────────────────────────

func TestParseUserID_Valid(t *testing.T) {
	id := uuid.New()
	got, err := authshared.ParseUserID("profile.GetUserProfile", id.String())
	require.NoError(t, err)
	require.Equal(t, [16]byte(id), got)
}

func TestParseUserID_Invalid(t *testing.T) {
	_, err := authshared.ParseUserID("profile.GetUserProfile", "not-a-uuid")
	require.Error(t, err)
	require.ErrorContains(t, err, "profile.GetUserProfile: parse user id")
}

func TestParseUserID_Empty(t *testing.T) {
	_, err := authshared.ParseUserID("profile.GetUserProfile", "")
	require.Error(t, err)
}

// ─── ValidatePassword — boundary lengths ─────────────────────────────────────

func TestValidatePassword_BoundaryLengths(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		password string
		wantErr  error
	}{
		{"7 bytes (too short)", "Ab1!xxx", authshared.ErrPasswordTooShort},
		{"8 bytes (min ok)", "Ab1!xxxx", nil},
		{"72 bytes (max ok)", strings.Repeat("A", 68) + "b1!", nil},
		{"73 bytes (too long)", strings.Repeat("A", 70) + "b1!", authshared.ErrPasswordTooLong},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := authshared.ValidatePassword(tc.password)
			if tc.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.wantErr)
			}
		})
	}
}

func TestValidatePassword_Empty(t *testing.T) {
	t.Parallel()
	require.ErrorIs(t, authshared.ValidatePassword(""), authshared.ErrPasswordEmpty)
}

// ─── ValidatePassword — character class rules ─────────────────────────────────

func TestValidatePassword_NoUpper(t *testing.T) {
	t.Parallel()
	require.ErrorIs(t, authshared.ValidatePassword("nouppercase1!"), authshared.ErrPasswordNoUpper)
}

func TestValidatePassword_NoLower(t *testing.T) {
	t.Parallel()
	require.ErrorIs(t, authshared.ValidatePassword("NOLOWERCASE1!"), authshared.ErrPasswordNoLower)
}

func TestValidatePassword_NoDigit(t *testing.T) {
	t.Parallel()
	require.ErrorIs(t, authshared.ValidatePassword("NoDigitPass!"), authshared.ErrPasswordNoDigit)
}

func TestValidatePassword_NoSymbol(t *testing.T) {
	t.Parallel()
	require.ErrorIs(t, authshared.ValidatePassword("NoSymbol1A"), authshared.ErrPasswordNoSymbol)
}

func TestValidatePassword_AllClassesPresent(t *testing.T) {
	t.Parallel()
	require.NoError(t, authshared.ValidatePassword("Secure!P@ss1"))
}

// TestValidatePassword_SpaceNotCountedAsSymbol verifies that the space character
// is NOT treated as a symbol by checkPasswordStrength (the rule is r > ' ',
// which excludes space and all ASCII control characters). A password that
// contains spaces but no other symbol must return ErrPasswordNoSymbol.
func TestValidatePassword_SpaceNotCountedAsSymbol(t *testing.T) {
	t.Parallel()
	// "Aa1   xx" — 8 chars, upper, lower, digit, only spaces as "other" chars.
	require.ErrorIs(t, authshared.ValidatePassword("Aa1   xx"), authshared.ErrPasswordNoSymbol,
		"space must not satisfy the symbol requirement")
}

// ─── ValidateOTPCode ──────────────────────────────────────────────────────────

func TestValidateOTPCode_HappyPath(t *testing.T) {
	t.Parallel()
	require.NoError(t, authshared.ValidateOTPCode("123456"))
	require.NoError(t, authshared.ValidateOTPCode("000000"))
	require.NoError(t, authshared.ValidateOTPCode("999999"))
}

func TestValidateOTPCode_Empty(t *testing.T) {
	t.Parallel()
	require.ErrorIs(t, authshared.ValidateOTPCode(""), authshared.ErrCodeEmpty)
}

func TestValidateOTPCode_TooShort(t *testing.T) {
	t.Parallel()
	require.ErrorIs(t, authshared.ValidateOTPCode("12345"), authshared.ErrCodeInvalidFormat)
}

func TestValidateOTPCode_TooLong(t *testing.T) {
	t.Parallel()
	require.ErrorIs(t, authshared.ValidateOTPCode("1234567"), authshared.ErrCodeInvalidFormat)
}

func TestValidateOTPCode_NonNumeric(t *testing.T) {
	t.Parallel()
	require.ErrorIs(t, authshared.ValidateOTPCode("abc123"), authshared.ErrCodeInvalidFormat)
	require.ErrorIs(t, authshared.ValidateOTPCode("12345a"), authshared.ErrCodeInvalidFormat)
}

// ─── NormaliseEmail ──────────────────────────────────────────────────────────

func TestNormaliseEmail_Empty_ReturnsErrEmailEmpty(t *testing.T) {
	t.Parallel()
	_, err := authshared.NormaliseEmail("")
	require.ErrorIs(t, err, authshared.ErrEmailEmpty)
}

func TestNormaliseEmail_WhitespaceOnly_ReturnsErrEmailEmpty(t *testing.T) {
	t.Parallel()
	_, err := authshared.NormaliseEmail("   \t  ")
	require.ErrorIs(t, err, authshared.ErrEmailEmpty)
}

func TestNormaliseEmail_TrimsLeadingTrailingWhitespace(t *testing.T) {
	t.Parallel()
	got, err := authshared.NormaliseEmail("  user@example.com  ")
	require.NoError(t, err)
	require.Equal(t, "user@example.com", got)
}

func TestNormaliseEmail_LowercasesInput(t *testing.T) {
	t.Parallel()
	got, err := authshared.NormaliseEmail("User@EXAMPLE.COM")
	require.NoError(t, err)
	require.Equal(t, "user@example.com", got)
}

func TestNormaliseEmail_TooLong_ReturnsErrEmailTooLong(t *testing.T) {
	t.Parallel()
	// 250 'a' chars + "@x.co" = 255 bytes → exceeds 254-byte limit.
	long := strings.Repeat("a", 250) + "@x.co"
	require.Equal(t, 255, len(long))
	_, err := authshared.NormaliseEmail(long)
	require.ErrorIs(t, err, authshared.ErrEmailTooLong)
}

func TestNormaliseEmail_ExactlyMaxLength_Passes(t *testing.T) {
	t.Parallel()
	// 249 'a' chars + "@x.co" = 254 bytes → exactly at limit, must pass.
	exact := strings.Repeat("a", 249) + "@x.co"
	require.Equal(t, 254, len(exact))
	got, err := authshared.NormaliseEmail(exact)
	require.NoError(t, err)
	require.Equal(t, exact, got)
}

func TestNormaliseEmail_ValidEmail_ReturnsNormalisedValue(t *testing.T) {
	t.Parallel()
	got, err := authshared.NormaliseEmail("Alice@Example.Com")
	require.NoError(t, err)
	require.Equal(t, "alice@example.com", got)
}

// ─── NormaliseEmail — format validation ──────────────────────────────────────

func TestNormaliseEmail_NoAtSign_ReturnsErrEmailInvalid(t *testing.T) {
	t.Parallel()
	_, err := authshared.NormaliseEmail("notanemail")
	require.ErrorIs(t, err, authshared.ErrEmailInvalid)
}

func TestNormaliseEmail_AtWithNoLocalPart_ReturnsErrEmailInvalid(t *testing.T) {
	t.Parallel()
	_, err := authshared.NormaliseEmail("@example.com")
	require.ErrorIs(t, err, authshared.ErrEmailInvalid)
}

func TestNormaliseEmail_AtWithNoDomain_ReturnsErrEmailInvalid(t *testing.T) {
	t.Parallel()
	_, err := authshared.NormaliseEmail("user@")
	require.ErrorIs(t, err, authshared.ErrEmailInvalid)
}

func TestNormaliseEmail_NoDotInDomain_ReturnsErrEmailInvalid(t *testing.T) {
	t.Parallel()
	// "user@nodot" has no TLD separator — not a valid address structure.
	_, err := authshared.NormaliseEmail("user@nodot")
	require.ErrorIs(t, err, authshared.ErrEmailInvalid)
}

func TestNormaliseEmail_ValidAddress_PassesFormatCheck(t *testing.T) {
	t.Parallel()
	got, err := authshared.NormaliseEmail("user@example.com")
	require.NoError(t, err)
	require.Equal(t, "user@example.com", got)
}
