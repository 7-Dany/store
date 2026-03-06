package authsharedtest_test

import (
	"context"
	"errors"
	"testing"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/auth/login"
	"github.com/7-Dany/store/backend/internal/domain/auth/password"
	"github.com/7-Dany/store/backend/internal/domain/auth/register"
	"github.com/7-Dany/store/backend/internal/domain/auth/session"
	"github.com/7-Dany/store/backend/internal/domain/auth/unlock"
	"github.com/7-Dany/store/backend/internal/domain/auth/verification"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────────
// LoginFakeServicer — nil-Fn default-return paths
// ─────────────────────────────────────────────────────────────────────────────

func TestLoginFakeServicer_Login_NilFn_ReturnsZero(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.LoginFakeServicer{}
	got, err := f.Login(context.Background(), login.LoginInput{})
	require.NoError(t, err)
	require.Equal(t, login.LoggedInSession{}, got)
}

// ─────────────────────────────────────────────────────────────────────────────
// PasswordFakeServicer — nil-Fn default-return paths
// ─────────────────────────────────────────────────────────────────────────────

func TestPasswordFakeServicer_RequestPasswordReset_NilFn_ReturnsZero(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.PasswordFakeServicer{}
	got, err := f.RequestPasswordReset(context.Background(), password.ForgotPasswordInput{})
	require.NoError(t, err)
	require.Equal(t, authshared.OTPIssuanceResult{}, got)
}

func TestPasswordFakeServicer_ConsumePasswordResetToken_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.PasswordFakeServicer{}
	_, err := f.ConsumePasswordResetToken(context.Background(), password.ResetPasswordInput{})
	require.NoError(t, err)
}

// ─────────────────────────────────────────────────────────────────────────────
// RegisterFakeServicer — nil-Fn default-return (canned result)
// ─────────────────────────────────────────────────────────────────────────────

func TestRegisterFakeServicer_Register_NilFn_ReturnsCannedResult(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.RegisterFakeServicer{}
	got, err := f.Register(context.Background(), register.RegisterInput{Email: "alice@example.com"})
	require.NoError(t, err)
	require.Equal(t, "00000000-0000-0000-0000-000000000001", got.UserID)
	require.Equal(t, "alice@example.com", got.Email)
	require.Equal(t, "123456", got.RawCode)
}

// ─────────────────────────────────────────────────────────────────────────────
// SessionFakeServicer — nil-Fn default-return paths
// ─────────────────────────────────────────────────────────────────────────────

func TestSessionFakeServicer_RotateRefreshToken_NilFn_ReturnsZero(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.SessionFakeServicer{}
	got, err := f.RotateRefreshToken(context.Background(), [16]byte{}, "127.0.0.1", "agent")
	require.NoError(t, err)
	require.Equal(t, session.RotatedSession{}, got)
}

func TestSessionFakeServicer_Logout_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.SessionFakeServicer{}
	err := f.Logout(context.Background(), session.LogoutTxInput{})
	require.NoError(t, err)
}

// ─────────────────────────────────────────────────────────────────────────────
// UnlockFakeServicer — nil-Fn default-return paths
// ─────────────────────────────────────────────────────────────────────────────

func TestUnlockFakeServicer_RequestUnlock_NilFn_ReturnsZero(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.UnlockFakeServicer{}
	got, err := f.RequestUnlock(context.Background(), unlock.RequestUnlockInput{})
	require.NoError(t, err)
	require.Equal(t, authshared.OTPIssuanceResult{}, got)
}

func TestUnlockFakeServicer_ConsumeUnlockToken_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.UnlockFakeServicer{}
	err := f.ConsumeUnlockToken(context.Background(), unlock.ConfirmUnlockInput{})
	require.NoError(t, err)
}

// ─────────────────────────────────────────────────────────────────────────────
// VerificationFakeServicer — nil-Fn default-return paths
// ─────────────────────────────────────────────────────────────────────────────

func TestVerificationFakeServicer_VerifyEmail_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.VerificationFakeServicer{}
	err := f.VerifyEmail(context.Background(), verification.VerifyEmailInput{})
	require.NoError(t, err)
}

func TestVerificationFakeServicer_ResendVerification_NilFn_ReturnsZero(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.VerificationFakeServicer{}
	got, err := f.ResendVerification(context.Background(), verification.ResendInput{})
	require.NoError(t, err)
	require.Equal(t, authshared.OTPIssuanceResult{}, got)
}

// ═══════════════════════════════════════════════════════════════════════════════
// Fn-path tests — delegate to the registered closure
// ═══════════════════════════════════════════════════════════════════════════════

// ── LoginFakeServicer Fn path ────────────────────────────────────────────────────

func TestLoginFakeServicer_Login_FnCalled(t *testing.T) {
	t.Parallel()
	want := login.LoggedInSession{}
	sentinel := errors.New("sentinel")
	f := &authsharedtest.LoginFakeServicer{
		LoginFn: func(_ context.Context, _ login.LoginInput) (login.LoggedInSession, error) {
			return want, sentinel
		},
	}
	got, err := f.Login(context.Background(), login.LoginInput{})
	require.ErrorIs(t, err, sentinel)
	require.Equal(t, want, got)
}

// ── PasswordFakeServicer Fn paths ─────────────────────────────────────────────

func TestPasswordFakeServicer_RequestPasswordReset_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.PasswordFakeServicer{
		RequestPasswordResetFn: func(_ context.Context, _ password.ForgotPasswordInput) (authshared.OTPIssuanceResult, error) {
			return authshared.OTPIssuanceResult{}, sentinel
		},
	}
	_, err := f.RequestPasswordReset(context.Background(), password.ForgotPasswordInput{})
	require.ErrorIs(t, err, sentinel)
}

func TestPasswordFakeServicer_ConsumePasswordResetToken_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.PasswordFakeServicer{
		ConsumePasswordResetTokenFn: func(_ context.Context, _ password.ResetPasswordInput) ([16]byte, error) {
			return [16]byte{}, sentinel
		},
	}
	_, err := f.ConsumePasswordResetToken(context.Background(), password.ResetPasswordInput{})
	require.ErrorIs(t, err, sentinel)
}

// ── RegisterFakeServicer Fn path ───────────────────────────────────────────────

func TestRegisterFakeServicer_Register_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.RegisterFakeServicer{
		RegisterFn: func(_ context.Context, _ register.RegisterInput) (register.RegisterResult, error) {
			return register.RegisterResult{}, sentinel
		},
	}
	_, err := f.Register(context.Background(), register.RegisterInput{})
	require.ErrorIs(t, err, sentinel)
}

// ── SessionFakeServicer Fn paths ───────────────────────────────────────────────

func TestSessionFakeServicer_RotateRefreshToken_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.SessionFakeServicer{
		RotateRefreshTokenFn: func(_ context.Context, _ [16]byte, _, _ string) (session.RotatedSession, error) {
			return session.RotatedSession{}, sentinel
		},
	}
	_, err := f.RotateRefreshToken(context.Background(), [16]byte{}, "", "")
	require.ErrorIs(t, err, sentinel)
}

func TestSessionFakeServicer_Logout_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.SessionFakeServicer{
		LogoutFn: func(_ context.Context, _ session.LogoutTxInput) error { return sentinel },
	}
	require.ErrorIs(t, f.Logout(context.Background(), session.LogoutTxInput{}), sentinel)
}

// ── UnlockFakeServicer Fn paths ─────────────────────────────────────────────────

func TestUnlockFakeServicer_RequestUnlock_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.UnlockFakeServicer{
		RequestUnlockFn: func(_ context.Context, _ unlock.RequestUnlockInput) (authshared.OTPIssuanceResult, error) {
			return authshared.OTPIssuanceResult{}, sentinel
		},
	}
	_, err := f.RequestUnlock(context.Background(), unlock.RequestUnlockInput{})
	require.ErrorIs(t, err, sentinel)
}

func TestUnlockFakeServicer_ConsumeUnlockToken_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.UnlockFakeServicer{
		ConsumeUnlockTokenFn: func(_ context.Context, _ unlock.ConfirmUnlockInput) error { return sentinel },
	}
	require.ErrorIs(t, f.ConsumeUnlockToken(context.Background(), unlock.ConfirmUnlockInput{}), sentinel)
}

// ── VerificationFakeServicer Fn paths ──────────────────────────────────────────

func TestVerificationFakeServicer_VerifyEmail_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.VerificationFakeServicer{
		VerifyEmailFn: func(_ context.Context, _ verification.VerifyEmailInput) error { return sentinel },
	}
	require.ErrorIs(t, f.VerifyEmail(context.Background(), verification.VerifyEmailInput{}), sentinel)
}

func TestVerificationFakeServicer_ResendVerification_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.VerificationFakeServicer{
		ResendVerificationFn: func(_ context.Context, _ verification.ResendInput) (authshared.OTPIssuanceResult, error) {
			return authshared.OTPIssuanceResult{}, sentinel
		},
	}
	_, err := f.ResendVerification(context.Background(), verification.ResendInput{})
	require.ErrorIs(t, err, sentinel)
}
