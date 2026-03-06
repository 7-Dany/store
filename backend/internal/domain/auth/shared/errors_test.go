package authshared_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
)

// ─── Sentinel errors — distinctness ──────────────────────────────────────────

func TestSentinelErrors_AreDistinct(t *testing.T) {
	t.Parallel()

	sentinels := []error{
		authshared.ErrUserNotFound,
		authshared.ErrTokenNotFound,
		authshared.ErrTokenExpired,
		authshared.ErrTokenAlreadyUsed,
		authshared.ErrTooManyAttempts,
		authshared.ErrInvalidCode,
		authshared.ErrAccountLocked,
		authshared.ErrAlreadyVerified,
		authshared.ErrInvalidToken,
		authshared.ErrTokenReuseDetected,
		authshared.ErrSessionNotFound,
		authshared.ErrInvalidCredentials,
		authshared.ErrEmailNotVerified,
		authshared.ErrAccountInactive,
		authshared.ErrLoginLocked,
		authshared.ErrDisplayNameEmpty,
		authshared.ErrDisplayNameTooLong,
		authshared.ErrDisplayNameInvalid,
		authshared.ErrEmailEmpty,
		authshared.ErrEmailTooLong,
		authshared.ErrEmailInvalid,
		authshared.ErrPasswordEmpty,
		authshared.ErrPasswordTooShort,
		authshared.ErrPasswordTooLong,
		authshared.ErrPasswordNoUpper,
		authshared.ErrPasswordNoLower,
		authshared.ErrPasswordNoDigit,
		authshared.ErrPasswordNoSymbol,
		authshared.ErrCodeEmpty,
		authshared.ErrCodeInvalidFormat,
		authshared.ErrUserIDEmpty,
		authshared.ErrIdentifierEmpty,
		authshared.ErrNewPasswordEmpty,
	}

	for _, err := range sentinels {
		require.NotEmpty(t, err.Error(), "sentinel %v has empty message", err)
	}

	for i := 0; i < len(sentinels); i++ {
		for j := i + 1; j < len(sentinels); j++ {
			require.False(t,
				errors.Is(sentinels[i], sentinels[j]),
				"sentinels[%d] (%v) and sentinels[%d] (%v) are the same pointer",
				i, sentinels[i], j, sentinels[j],
			)
		}
	}
}

// ─── LoginLockedError ─────────────────────────────────────────────────────────

func TestLoginLockedError_ErrorMessage(t *testing.T) {
	t.Parallel()
	e := &authshared.LoginLockedError{RetryAfter: 5 * time.Minute}
	require.Equal(t, authshared.ErrLoginLocked.Error(), e.Error())
}

func TestLoginLockedError_ErrorsIs_ErrLoginLocked(t *testing.T) {
	t.Parallel()
	e := &authshared.LoginLockedError{RetryAfter: 30 * time.Second}
	require.ErrorIs(t, e, authshared.ErrLoginLocked)
}

func TestLoginLockedError_ErrorsAs(t *testing.T) {
	t.Parallel()
	wrapped := &authshared.LoginLockedError{RetryAfter: 2 * time.Minute}
	var target *authshared.LoginLockedError
	require.ErrorAs(t, wrapped, &target)
	require.Equal(t, 2*time.Minute, target.RetryAfter)
}

func TestLoginLockedError_IsNot_OtherSentinels(t *testing.T) {
	t.Parallel()
	e := &authshared.LoginLockedError{RetryAfter: time.Second}
	require.False(t, errors.Is(e, authshared.ErrInvalidCredentials))
	require.False(t, errors.Is(e, authshared.ErrAccountLocked))
}

func TestLoginLockedError_Unwrap_ReturnsErrLoginLocked(t *testing.T) {
	t.Parallel()
	e := &authshared.LoginLockedError{RetryAfter: time.Minute}
	require.Equal(t, authshared.ErrLoginLocked, e.Unwrap())
}

func TestLoginLockedError_ZeroRetryAfter(t *testing.T) {
	t.Parallel()
	e := &authshared.LoginLockedError{}
	require.Zero(t, e.RetryAfter)
	require.ErrorIs(t, e, authshared.ErrLoginLocked)
}

// ─── IsPasswordStrengthError ─────────────────────────────────────────────────────

func TestIsPasswordStrengthError_ReturnsTrueForAllStrengthErrors(t *testing.T) {
	t.Parallel()
	strengthErrors := []error{
		authshared.ErrPasswordEmpty,
		authshared.ErrPasswordTooShort,
		authshared.ErrPasswordTooLong,
		authshared.ErrPasswordNoUpper,
		authshared.ErrPasswordNoLower,
		authshared.ErrPasswordNoDigit,
		authshared.ErrPasswordNoSymbol,
	}
	for _, err := range strengthErrors {
		require.True(t, authshared.IsPasswordStrengthError(err),
			"expected IsPasswordStrengthError(true) for %v", err)
	}
}

// TestErrTokenAlreadyConsumed_IsAliasForErrTokenAlreadyUsed verifies that
// ErrTokenAlreadyConsumed and ErrTokenAlreadyUsed are the same pointer so that
// errors.Is works in both directions — callers can match either sentinel.
func TestErrTokenAlreadyConsumed_IsAliasForErrTokenAlreadyUsed(t *testing.T) {
	t.Parallel()
	require.ErrorIs(t, authshared.ErrTokenAlreadyConsumed, authshared.ErrTokenAlreadyUsed,
		"ErrTokenAlreadyConsumed must satisfy errors.Is(ErrTokenAlreadyUsed)")
	require.ErrorIs(t, authshared.ErrTokenAlreadyUsed, authshared.ErrTokenAlreadyConsumed,
		"ErrTokenAlreadyUsed must satisfy errors.Is(ErrTokenAlreadyConsumed)")
}

func TestIsPasswordStrengthError_ReturnsFalseForNonStrengthErrors(t *testing.T) {
	t.Parallel()
	nonStrengthErrors := []error{
		authshared.ErrEmailEmpty,
		authshared.ErrEmailTooLong,
		authshared.ErrCodeEmpty,
		authshared.ErrUserNotFound,
		authshared.ErrTokenExpired,
		errors.New("unrelated error"),
	}
	for _, err := range nonStrengthErrors {
		require.False(t, authshared.IsPasswordStrengthError(err),
			"expected IsPasswordStrengthError(false) for %v", err)
	}
}
