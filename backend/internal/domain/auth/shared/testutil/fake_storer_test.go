package authsharedtest_test

import (
	"context"
	"errors"
	"testing"
	"time"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/auth/login"
	"github.com/7-Dany/store/backend/internal/domain/auth/password"
	"github.com/7-Dany/store/backend/internal/domain/auth/profile"
	"github.com/7-Dany/store/backend/internal/domain/auth/register"
	"github.com/7-Dany/store/backend/internal/domain/auth/session"
	"github.com/7-Dany/store/backend/internal/domain/auth/unlock"
	"github.com/7-Dany/store/backend/internal/domain/auth/verification"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────────
// LoginFakeStorer — nil-Fn default-return paths
// ─────────────────────────────────────────────────────────────────────────────

func TestLoginFakeStorer_GetUserForLogin_NilFn_ReturnsZero(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.LoginFakeStorer{}
	got, err := f.GetUserForLogin(context.Background(), "email@test.com")
	require.NoError(t, err)
	require.Equal(t, login.LoginUser{}, got)
}

func TestLoginFakeStorer_LoginTx_NilFn_ReturnsZero(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.LoginFakeStorer{}
	got, err := f.LoginTx(context.Background(), login.LoginTxInput{})
	require.NoError(t, err)
	require.Equal(t, login.LoggedInSession{}, got)
}

func TestLoginFakeStorer_IncrementLoginFailuresTx_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.LoginFakeStorer{}
	err := f.IncrementLoginFailuresTx(context.Background(), [16]byte{}, "127.0.0.1", "agent")
	require.NoError(t, err)
}

func TestLoginFakeStorer_ResetLoginFailuresTx_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.LoginFakeStorer{}
	err := f.ResetLoginFailuresTx(context.Background(), [16]byte{})
	require.NoError(t, err)
}

func TestLoginFakeStorer_WriteLoginFailedAuditTx_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.LoginFakeStorer{}
	err := f.WriteLoginFailedAuditTx(context.Background(), [16]byte{}, "bad_password", "127.0.0.1", "agent")
	require.NoError(t, err)
}

// ─────────────────────────────────────────────────────────────────────────────
// PasswordFakeStorer — nil-Fn default-return paths
// ─────────────────────────────────────────────────────────────────────────────

func TestPasswordFakeStorer_GetUserForPasswordReset_NilFn_ReturnsZero(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.PasswordFakeStorer{}
	got, err := f.GetUserForPasswordReset(context.Background(), "email@test.com")
	require.NoError(t, err)
	require.Equal(t, password.GetUserForPasswordResetResult{}, got)
}

func TestPasswordFakeStorer_RequestPasswordResetTx_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.PasswordFakeStorer{}
	err := f.RequestPasswordResetTx(context.Background(), authshared.OTPTokenInput{})
	require.NoError(t, err)
}

func TestPasswordFakeStorer_ConsumeAndUpdatePasswordTx_NilFn_ReturnsZero(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.PasswordFakeStorer{}
	got, err := f.ConsumeAndUpdatePasswordTx(context.Background(), password.ConsumeAndUpdateInput{}, nil)
	require.NoError(t, err)
	require.Equal(t, [16]byte{}, got)
}

func TestPasswordFakeStorer_IncrementAttemptsTx_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.PasswordFakeStorer{}
	err := f.IncrementAttemptsTx(context.Background(), authshared.IncrementInput{})
	require.NoError(t, err)
}

// ─────────────────────────────────────────────────────────────────────────────
// ProfileFakeStorer — nil-Fn default-return paths
// ─────────────────────────────────────────────────────────────────────────────

func TestProfileFakeStorer_GetUserProfile_NilFn_ReturnsZero(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.ProfileFakeStorer{}
	got, err := f.GetUserProfile(context.Background(), [16]byte{})
	require.NoError(t, err)
	require.Equal(t, profile.UserProfile{}, got)
}

func TestProfileFakeStorer_GetActiveSessions_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.ProfileFakeStorer{}
	got, err := f.GetActiveSessions(context.Background(), [16]byte{})
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestProfileFakeStorer_RevokeSessionTx_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.ProfileFakeStorer{}
	err := f.RevokeSessionTx(context.Background(), [16]byte{}, [16]byte{}, "127.0.0.1", "agent")
	require.NoError(t, err)
}

func TestPasswordFakeStorer_GetUserPasswordHash_NilFn_ReturnsZero(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.PasswordFakeStorer{}
	got, err := f.GetUserPasswordHash(context.Background(), [16]byte{})
	require.NoError(t, err)
	require.Equal(t, password.CurrentCredentials{}, got)
}

func TestPasswordFakeStorer_UpdatePasswordHashTx_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.PasswordFakeStorer{}
	err := f.UpdatePasswordHashTx(context.Background(), [16]byte{}, "newhash", "127.0.0.1", "agent")
	require.NoError(t, err)
}

func TestPasswordFakeStorer_WritePasswordChangeFailedAuditTx_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.PasswordFakeStorer{}
	err := f.WritePasswordChangeFailedAuditTx(context.Background(), [16]byte{}, "127.0.0.1", "agent")
	require.NoError(t, err)
}

// ─────────────────────────────────────────────────────────────────────────────
// RegisterFakeStorer — nil-Fn default-return paths
// ─────────────────────────────────────────────────────────────────────────────

func TestRegisterFakeStorer_CreateUserTx_NilFn_ReturnsZero(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.RegisterFakeStorer{}
	got, err := f.CreateUserTx(context.Background(), register.CreateUserInput{})
	require.NoError(t, err)
	require.Equal(t, register.CreatedUser{}, got)
}

func TestRegisterFakeStorer_WriteRegisterFailedAuditTx_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.RegisterFakeStorer{}
	err := f.WriteRegisterFailedAuditTx(context.Background(), [16]byte{}, "127.0.0.1", "agent")
	require.NoError(t, err)
}

// ─────────────────────────────────────────────────────────────────────────────
// SessionFakeStorer — nil-Fn default-return paths
// ─────────────────────────────────────────────────────────────────────────────

func TestSessionFakeStorer_GetRefreshTokenByJTI_NilFn_ReturnsZero(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.SessionFakeStorer{}
	got, err := f.GetRefreshTokenByJTI(context.Background(), [16]byte{})
	require.NoError(t, err)
	require.Equal(t, session.StoredRefreshToken{}, got)
}

func TestSessionFakeStorer_RotateRefreshTokenTx_NilFn_ReturnsZero(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.SessionFakeStorer{}
	got, err := f.RotateRefreshTokenTx(context.Background(), session.RotateTxInput{})
	require.NoError(t, err)
	require.Equal(t, session.RotatedSession{}, got)
}

func TestSessionFakeStorer_RevokeFamilyTokens_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.SessionFakeStorer{}
	err := f.RevokeFamilyTokensTx(context.Background(), [16]byte{}, [16]byte{}, "reuse_detected")
	require.NoError(t, err)
}

func TestSessionFakeStorer_RevokeAllUserTokens_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.SessionFakeStorer{}
	err := f.RevokeAllUserTokensTx(context.Background(), [16]byte{}, "logout_all", "127.0.0.1", "agent")
	require.NoError(t, err)
}

func TestSessionFakeStorer_LogoutTx_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.SessionFakeStorer{}
	err := f.LogoutTx(context.Background(), session.LogoutTxInput{})
	require.NoError(t, err)
}

func TestSessionFakeStorer_WriteRefreshFailedAuditTx_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.SessionFakeStorer{}
	err := f.WriteRefreshFailedAuditTx(context.Background(), "127.0.0.1", "agent")
	require.NoError(t, err)
}

// ─────────────────────────────────────────────────────────────────────────────
// UnlockFakeStorer — nil-Fn default-return paths
// ─────────────────────────────────────────────────────────────────────────────

func TestUnlockFakeStorer_GetUserForUnlock_NilFn_ReturnsZero(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.UnlockFakeStorer{}
	got, err := f.GetUserForUnlock(context.Background(), "email@test.com")
	require.NoError(t, err)
	require.Equal(t, unlock.UnlockUser{}, got)
}

func TestUnlockFakeStorer_RequestUnlockTx_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.UnlockFakeStorer{}
	err := f.RequestUnlockTx(context.Background(), unlock.RequestUnlockStoreInput{})
	require.NoError(t, err)
}

func TestUnlockFakeStorer_ConsumeUnlockTokenTx_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.UnlockFakeStorer{}
	err := f.ConsumeUnlockTokenTx(context.Background(), "email@test.com", nil)
	require.NoError(t, err)
}

func TestUnlockFakeStorer_UnlockAccountTx_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.UnlockFakeStorer{}
	err := f.UnlockAccountTx(context.Background(), [16]byte{}, "127.0.0.1", "agent")
	require.NoError(t, err)
}

func TestUnlockFakeStorer_IncrementAttemptsTx_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.UnlockFakeStorer{}
	err := f.IncrementAttemptsTx(context.Background(), authshared.IncrementInput{})
	require.NoError(t, err)
}

// ─────────────────────────────────────────────────────────────────────────────
// VerificationFakeStorer — nil-Fn default-return paths
// ─────────────────────────────────────────────────────────────────────────────

func TestVerificationFakeStorer_GetLatestTokenCreatedAt_NilFn_ReturnsZero(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.VerificationFakeStorer{}
	got, err := f.GetLatestTokenCreatedAt(context.Background(), [16]byte{})
	require.NoError(t, err)
	require.Equal(t, time.Time{}, got)
}

func TestVerificationFakeStorer_GetUserForResend_NilFn_ReturnsZero(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.VerificationFakeStorer{}
	got, err := f.GetUserForResend(context.Background(), "email@test.com")
	require.NoError(t, err)
	require.Equal(t, verification.ResendUser{}, got)
}

func TestVerificationFakeStorer_IncrementAttemptsTx_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.VerificationFakeStorer{}
	err := f.IncrementAttemptsTx(context.Background(), authshared.IncrementInput{})
	require.NoError(t, err)
}

func TestVerificationFakeStorer_ResendVerificationTx_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.VerificationFakeStorer{}
	err := f.ResendVerificationTx(context.Background(), verification.ResendStoreInput{}, "codehash")
	require.NoError(t, err)
}

func TestVerificationFakeStorer_VerifyEmailTx_NilFn_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &authsharedtest.VerificationFakeStorer{}
	err := f.VerifyEmailTx(context.Background(), "email@test.com", "127.0.0.1", "agent", nil)
	require.NoError(t, err)
}

// ═══════════════════════════════════════════════════════════════════════════════
// Fn-path tests — delegate to the registered closure
// ═══════════════════════════════════════════════════════════════════════════════

// ── LoginFakeStorer Fn paths ──────────────────────────────────────────────────────

func TestLoginFakeStorer_GetUserForLogin_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.LoginFakeStorer{
		GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) {
			return login.LoginUser{}, sentinel
		},
	}
	_, err := f.GetUserForLogin(context.Background(), "x")
	require.ErrorIs(t, err, sentinel)
}

func TestLoginFakeStorer_LoginTx_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.LoginFakeStorer{
		LoginTxFn: func(_ context.Context, _ login.LoginTxInput) (login.LoggedInSession, error) {
			return login.LoggedInSession{}, sentinel
		},
	}
	_, err := f.LoginTx(context.Background(), login.LoginTxInput{})
	require.ErrorIs(t, err, sentinel)
}

func TestLoginFakeStorer_IncrementLoginFailuresTx_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.LoginFakeStorer{
		IncrementLoginFailuresTxFn: func(_ context.Context, _ [16]byte, _, _ string) error { return sentinel },
	}
	require.ErrorIs(t, f.IncrementLoginFailuresTx(context.Background(), [16]byte{}, "", ""), sentinel)
}

func TestLoginFakeStorer_ResetLoginFailuresTx_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.LoginFakeStorer{
		ResetLoginFailuresTxFn: func(_ context.Context, _ [16]byte) error { return sentinel },
	}
	require.ErrorIs(t, f.ResetLoginFailuresTx(context.Background(), [16]byte{}), sentinel)
}

func TestLoginFakeStorer_WriteLoginFailedAuditTx_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.LoginFakeStorer{
		WriteLoginFailedAuditTxFn: func(_ context.Context, _ [16]byte, _, _, _ string) error { return sentinel },
	}
	require.ErrorIs(t, f.WriteLoginFailedAuditTx(context.Background(), [16]byte{}, "", "", ""), sentinel)
}

// ── PasswordFakeStorer Fn paths ───────────────────────────────────────────────

func TestPasswordFakeStorer_GetUserForPasswordReset_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.PasswordFakeStorer{
		GetUserForPasswordResetFn: func(_ context.Context, _ string) (password.GetUserForPasswordResetResult, error) {
			return password.GetUserForPasswordResetResult{}, sentinel
		},
	}
	_, err := f.GetUserForPasswordReset(context.Background(), "x")
	require.ErrorIs(t, err, sentinel)
}

func TestPasswordFakeStorer_RequestPasswordResetTx_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.PasswordFakeStorer{
		RequestPasswordResetTxFn: func(_ context.Context, _ password.RequestPasswordResetStoreInput) error {
			return sentinel
		},
	}
	require.ErrorIs(t, f.RequestPasswordResetTx(context.Background(), password.RequestPasswordResetStoreInput{}), sentinel)
}

func TestPasswordFakeStorer_ConsumeAndUpdatePasswordTx_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.PasswordFakeStorer{
		ConsumeAndUpdatePasswordTxFn: func(_ context.Context, _ password.ConsumeAndUpdateInput, _ func(authshared.VerificationToken) error) ([16]byte, error) {
			return [16]byte{}, sentinel
		},
	}
	_, err := f.ConsumeAndUpdatePasswordTx(context.Background(), password.ConsumeAndUpdateInput{}, nil)
	require.ErrorIs(t, err, sentinel)
}

func TestPasswordFakeStorer_IncrementAttemptsTx_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.PasswordFakeStorer{
		IncrementAttemptsTxFn: func(_ context.Context, _ authshared.IncrementInput) error { return sentinel },
	}
	require.ErrorIs(t, f.IncrementAttemptsTx(context.Background(), authshared.IncrementInput{}), sentinel)
}

// ── ProfileFakeStorer Fn paths ────────────────────────────────────────────────

func TestProfileFakeStorer_GetUserProfile_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.ProfileFakeStorer{
		GetUserProfileFn: func(_ context.Context, _ [16]byte) (profile.UserProfile, error) {
			return profile.UserProfile{}, sentinel
		},
	}
	_, err := f.GetUserProfile(context.Background(), [16]byte{})
	require.ErrorIs(t, err, sentinel)
}

func TestProfileFakeStorer_GetActiveSessions_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.ProfileFakeStorer{
		GetActiveSessionsFn: func(_ context.Context, _ [16]byte) ([]profile.ActiveSession, error) {
			return nil, sentinel
		},
	}
	_, err := f.GetActiveSessions(context.Background(), [16]byte{})
	require.ErrorIs(t, err, sentinel)
}

func TestProfileFakeStorer_RevokeSessionTx_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.ProfileFakeStorer{
		RevokeSessionTxFn: func(_ context.Context, _, _ [16]byte, _, _ string) error { return sentinel },
	}
	require.ErrorIs(t, f.RevokeSessionTx(context.Background(), [16]byte{}, [16]byte{}, "", ""), sentinel)
}

func TestPasswordFakeStorer_GetUserPasswordHash_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.PasswordFakeStorer{
		GetUserPasswordHashFn: func(_ context.Context, _ [16]byte) (password.CurrentCredentials, error) {
			return password.CurrentCredentials{}, sentinel
		},
	}
	_, err := f.GetUserPasswordHash(context.Background(), [16]byte{})
	require.ErrorIs(t, err, sentinel)
}

func TestPasswordFakeStorer_UpdatePasswordHashTx_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.PasswordFakeStorer{
		UpdatePasswordHashTxFn: func(_ context.Context, _ [16]byte, _, _, _ string) error { return sentinel },
	}
	require.ErrorIs(t, f.UpdatePasswordHashTx(context.Background(), [16]byte{}, "", "", ""), sentinel)
}

func TestPasswordFakeStorer_WritePasswordChangeFailedAuditTx_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.PasswordFakeStorer{
		WritePasswordChangeFailedAuditTxFn: func(_ context.Context, _ [16]byte, _, _ string) error { return sentinel },
	}
	require.ErrorIs(t, f.WritePasswordChangeFailedAuditTx(context.Background(), [16]byte{}, "", ""), sentinel)
}

// ── RegisterFakeStorer Fn paths ───────────────────────────────────────────────

func TestRegisterFakeStorer_CreateUserTx_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.RegisterFakeStorer{
		CreateUserTxFn: func(_ context.Context, _ register.CreateUserInput) (register.CreatedUser, error) {
			return register.CreatedUser{}, sentinel
		},
	}
	_, err := f.CreateUserTx(context.Background(), register.CreateUserInput{})
	require.ErrorIs(t, err, sentinel)
}

func TestRegisterFakeStorer_WriteRegisterFailedAuditTx_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.RegisterFakeStorer{
		WriteRegisterFailedAuditTxFn: func(_ context.Context, _ [16]byte, _, _ string) error { return sentinel },
	}
	require.ErrorIs(t, f.WriteRegisterFailedAuditTx(context.Background(), [16]byte{}, "", ""), sentinel)
}

// ── SessionFakeStorer Fn paths ───────────────────────────────────────────────

func TestSessionFakeStorer_GetRefreshTokenByJTI_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.SessionFakeStorer{
		GetRefreshTokenByJTIFn: func(_ context.Context, _ [16]byte) (session.StoredRefreshToken, error) {
			return session.StoredRefreshToken{}, sentinel
		},
	}
	_, err := f.GetRefreshTokenByJTI(context.Background(), [16]byte{})
	require.ErrorIs(t, err, sentinel)
}

func TestSessionFakeStorer_RotateRefreshTokenTx_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.SessionFakeStorer{
		RotateRefreshTokenTxFn: func(_ context.Context, _ session.RotateTxInput) (session.RotatedSession, error) {
			return session.RotatedSession{}, sentinel
		},
	}
	_, err := f.RotateRefreshTokenTx(context.Background(), session.RotateTxInput{})
	require.ErrorIs(t, err, sentinel)
}

func TestSessionFakeStorer_RevokeFamilyTokens_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.SessionFakeStorer{
		RevokeFamilyTokensTxFn: func(_ context.Context, _, _ [16]byte, _ string) error { return sentinel },
	}
	require.ErrorIs(t, f.RevokeFamilyTokensTx(context.Background(), [16]byte{}, [16]byte{}, ""), sentinel)
}

func TestSessionFakeStorer_RevokeAllUserTokens_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.SessionFakeStorer{
		RevokeAllUserTokensTxFn: func(_ context.Context, _ [16]byte, _, _, _ string) error { return sentinel },
	}
	require.ErrorIs(t, f.RevokeAllUserTokensTx(context.Background(), [16]byte{}, "", "", ""), sentinel)
}

func TestSessionFakeStorer_LogoutTx_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.SessionFakeStorer{
		LogoutTxFn: func(_ context.Context, _ session.LogoutTxInput) error { return sentinel },
	}
	require.ErrorIs(t, f.LogoutTx(context.Background(), session.LogoutTxInput{}), sentinel)
}

func TestSessionFakeStorer_WriteRefreshFailedAuditTx_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.SessionFakeStorer{
		WriteRefreshFailedAuditTxFn: func(_ context.Context, _, _ string) error { return sentinel },
	}
	require.ErrorIs(t, f.WriteRefreshFailedAuditTx(context.Background(), "", ""), sentinel)
}

// ── UnlockFakeStorer Fn paths ─────────────────────────────────────────────────

func TestUnlockFakeStorer_GetUserForUnlock_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.UnlockFakeStorer{
		GetUserForUnlockFn: func(_ context.Context, _ string) (unlock.UnlockUser, error) {
			return unlock.UnlockUser{}, sentinel
		},
	}
	_, err := f.GetUserForUnlock(context.Background(), "x")
	require.ErrorIs(t, err, sentinel)
}

func TestUnlockFakeStorer_RequestUnlockTx_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.UnlockFakeStorer{
		RequestUnlockTxFn: func(_ context.Context, _ unlock.RequestUnlockStoreInput) error {
			return sentinel
		},
	}
	require.ErrorIs(t, f.RequestUnlockTx(context.Background(), unlock.RequestUnlockStoreInput{}), sentinel)
}

func TestUnlockFakeStorer_ConsumeUnlockTokenTx_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.UnlockFakeStorer{
		ConsumeUnlockTokenTxFn: func(_ context.Context, _ string, _ func(authshared.VerificationToken) error) error {
			return sentinel
		},
	}
	require.ErrorIs(t, f.ConsumeUnlockTokenTx(context.Background(), "x", nil), sentinel)
}

func TestUnlockFakeStorer_UnlockAccountTx_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.UnlockFakeStorer{
		UnlockAccountTxFn: func(_ context.Context, _ [16]byte, _, _ string) error { return sentinel },
	}
	require.ErrorIs(t, f.UnlockAccountTx(context.Background(), [16]byte{}, "", ""), sentinel)
}

func TestUnlockFakeStorer_IncrementAttemptsTx_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.UnlockFakeStorer{
		IncrementAttemptsTxFn: func(_ context.Context, _ authshared.IncrementInput) error { return sentinel },
	}
	require.ErrorIs(t, f.IncrementAttemptsTx(context.Background(), authshared.IncrementInput{}), sentinel)
}

// ── VerificationFakeStorer Fn paths ───────────────────────────────────────────

func TestVerificationFakeStorer_GetLatestTokenCreatedAt_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.VerificationFakeStorer{
		GetLatestTokenCreatedAtFn: func(_ context.Context, _ [16]byte) (time.Time, error) {
			return time.Time{}, sentinel
		},
	}
	_, err := f.GetLatestTokenCreatedAt(context.Background(), [16]byte{})
	require.ErrorIs(t, err, sentinel)
}

func TestVerificationFakeStorer_GetUserForResend_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.VerificationFakeStorer{
		GetUserForResendFn: func(_ context.Context, _ string) (verification.ResendUser, error) {
			return verification.ResendUser{}, sentinel
		},
	}
	_, err := f.GetUserForResend(context.Background(), "x")
	require.ErrorIs(t, err, sentinel)
}

func TestVerificationFakeStorer_IncrementAttemptsTx_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.VerificationFakeStorer{
		IncrementAttemptsTxFn: func(_ context.Context, _ authshared.IncrementInput) error { return sentinel },
	}
	require.ErrorIs(t, f.IncrementAttemptsTx(context.Background(), authshared.IncrementInput{}), sentinel)
}

func TestVerificationFakeStorer_ResendVerificationTx_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.VerificationFakeStorer{
		ResendVerificationTxFn: func(_ context.Context, _ verification.ResendStoreInput, _ string) error {
			return sentinel
		},
	}
	require.ErrorIs(t, f.ResendVerificationTx(context.Background(), verification.ResendStoreInput{}, ""), sentinel)
}

func TestVerificationFakeStorer_VerifyEmailTx_FnCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	f := &authsharedtest.VerificationFakeStorer{
		VerifyEmailTxFn: func(_ context.Context, _, _, _ string, _ func(authshared.VerificationToken) error) error {
			return sentinel
		},
	}
	require.ErrorIs(t, f.VerifyEmailTx(context.Background(), "", "", "", nil), sentinel)
}
