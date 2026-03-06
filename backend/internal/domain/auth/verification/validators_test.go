package verification_test

import (
	"testing"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/7-Dany/store/backend/internal/domain/auth/verification"
	"github.com/stretchr/testify/require"
)

// ── validateVerifyEmailRequest ────────────────────────────────────────────────

func TestValidateVerifyEmailRequest_HappyPath(t *testing.T) {
	t.Parallel()
	req, err := verification.ValidateVerifyEmailForTest("User@EXAMPLE.COM", "123456")
	require.NoError(t, err)
	require.Equal(t, "user@example.com", req.Email)
}

func TestValidateVerifyEmailRequest_EmptyEmail(t *testing.T) {
	t.Parallel()
	_, err := verification.ValidateVerifyEmailForTest("", "123456")
	require.ErrorIs(t, err, authshared.ErrEmailEmpty)
}

func TestValidateVerifyEmailRequest_EmptyCode(t *testing.T) {
	t.Parallel()
	_, err := verification.ValidateVerifyEmailForTest("a@example.com", "")
	require.ErrorIs(t, err, authshared.ErrCodeEmpty)
}

func TestValidateVerifyEmailRequest_CodeNonNumeric(t *testing.T) {
	t.Parallel()
	_, err := verification.ValidateVerifyEmailForTest("a@example.com", "abc123")
	require.ErrorIs(t, err, authshared.ErrCodeInvalidFormat)
}

func TestValidateVerifyEmailRequest_CodeTooShort(t *testing.T) {
	t.Parallel()
	_, err := verification.ValidateVerifyEmailForTest("a@example.com", "12345")
	require.ErrorIs(t, err, authshared.ErrCodeInvalidFormat)
}

func TestValidateVerifyEmailRequest_CodeTooLong(t *testing.T) {
	t.Parallel()
	_, err := verification.ValidateVerifyEmailForTest("a@example.com", "1234567")
	require.ErrorIs(t, err, authshared.ErrCodeInvalidFormat)
}

// ── validateResendRequest ─────────────────────────────────────────────────────

func TestValidateResendRequest_HappyPath(t *testing.T) {
	t.Parallel()
	req, err := verification.ValidateResendForTest("User@EXAMPLE.COM")
	require.NoError(t, err)
	require.Equal(t, "user@example.com", req.Email)
}

func TestValidateResendRequest_EmptyEmail(t *testing.T) {
	t.Parallel()
	_, err := verification.ValidateResendForTest("")
	require.ErrorIs(t, err, authshared.ErrEmailEmpty)
}
