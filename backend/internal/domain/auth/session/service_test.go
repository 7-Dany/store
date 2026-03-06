package session_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/7-Dany/store/backend/internal/domain/auth/session"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/stretchr/testify/require"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newServiceWithStore(store session.Storer) *session.Service {
	return session.NewService(store)
}

func validStoredToken() session.StoredRefreshToken {
	return session.StoredRefreshToken{
		JTI:       authsharedtest.RandomUUID(),
		UserID:    authsharedtest.RandomUUID(),
		SessionID: authsharedtest.RandomUUID(),
		FamilyID:  authsharedtest.RandomUUID(),
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
		IsRevoked: false,
	}
}

// ── TestRotateRefreshToken ────────────────────────────────────────────────────

func TestRotateRefreshToken(t *testing.T) {
	t.Parallel()

	wantRotated := session.RotatedSession{
		NewRefreshJTI: authsharedtest.RandomUUID(),
		RefreshExpiry: time.Now().Add(30 * 24 * time.Hour),
	}

	t.Run("success returns rotated session", func(t *testing.T) {
		t.Parallel()
		token := validStoredToken()
		store := &authsharedtest.SessionFakeStorer{
			GetRefreshTokenByJTIFn: func(_ context.Context, _ [16]byte) (session.StoredRefreshToken, error) {
				return token, nil
			},
			RotateRefreshTokenTxFn: func(_ context.Context, _ session.RotateTxInput) (session.RotatedSession, error) {
				return wantRotated, nil
			},
		}
		svc := newServiceWithStore(store)
		// Pass jti as [16]byte directly — no uuid string round-trip (DESIGN 6).
		got, err := svc.RotateRefreshToken(context.Background(), authsharedtest.RandomUUID(), "1.2.3.4", "agent")
		require.NoError(t, err)
		require.Equal(t, wantRotated.NewRefreshJTI, got.NewRefreshJTI)
	})

	t.Run("token not found returns ErrInvalidToken", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.SessionFakeStorer{
			GetRefreshTokenByJTIFn: func(_ context.Context, _ [16]byte) (session.StoredRefreshToken, error) {
				return session.StoredRefreshToken{}, authshared.ErrTokenNotFound
			},
		}
		svc := newServiceWithStore(store)
		_, err := svc.RotateRefreshToken(context.Background(), authsharedtest.RandomUUID(), "", "")
		require.ErrorIs(t, err, authshared.ErrInvalidToken)
	})

	t.Run("reuse detected revokes family and returns ErrTokenReuseDetected", func(t *testing.T) {
		t.Parallel()
		token := validStoredToken()
		token.IsRevoked = true
		var revokedFamilyID [16]byte
		var revokeReason string
		store := &authsharedtest.SessionFakeStorer{
			GetRefreshTokenByJTIFn: func(_ context.Context, _ [16]byte) (session.StoredRefreshToken, error) {
				return token, nil
			},
			RevokeFamilyTokensTxFn: func(_ context.Context, _ [16]byte, familyID [16]byte, reason string) error {
				revokedFamilyID = familyID
				revokeReason = reason
				return nil
			},
		}
		svc := newServiceWithStore(store)
		_, err := svc.RotateRefreshToken(context.Background(), authsharedtest.RandomUUID(), "", "")
		require.ErrorIs(t, err, authshared.ErrTokenReuseDetected)
		require.Equal(t, "reuse_detected", revokeReason)
		require.Equal(t, token.FamilyID, revokedFamilyID)
	})

	t.Run("reuse detection: RevokeFamilyTokens error is logged but ErrTokenReuseDetected still returned", func(t *testing.T) {
		t.Parallel()
		token := validStoredToken()
		token.IsRevoked = true
		store := &authsharedtest.SessionFakeStorer{
			GetRefreshTokenByJTIFn: func(_ context.Context, _ [16]byte) (session.StoredRefreshToken, error) {
				return token, nil
			},
			RevokeFamilyTokensTxFn: func(_ context.Context, _, _ [16]byte, _ string) error {
				return errors.New("db error during revoke")
			},
		}
		svc := newServiceWithStore(store)
		_, err := svc.RotateRefreshToken(context.Background(), authsharedtest.RandomUUID(), "", "")
		// Even when RevokeFamilyTokens fails, ErrTokenReuseDetected must be returned.
		require.ErrorIs(t, err, authshared.ErrTokenReuseDetected)
	})

	t.Run("GetRefreshTokenByJTI unexpected error → wrapped, WriteRefreshFailedAuditTx NOT called", func(t *testing.T) {
		t.Parallel()
		dbErr := errors.New("db error")
		var auditCalled bool
		store := &authsharedtest.SessionFakeStorer{
			GetRefreshTokenByJTIFn: func(_ context.Context, _ [16]byte) (session.StoredRefreshToken, error) {
				return session.StoredRefreshToken{}, dbErr
			},
			WriteRefreshFailedAuditTxFn: func(_ context.Context, _, _ string) error {
				auditCalled = true
				return nil
			},
		}
		svc := newServiceWithStore(store)
		_, err := svc.RotateRefreshToken(context.Background(), authsharedtest.RandomUUID(), "", "")
		require.ErrorIs(t, err, dbErr)
		require.False(t, auditCalled, "WriteRefreshFailedAuditTx must NOT be called on unexpected error")
	})

	t.Run("expired token → WriteRefreshFailedAuditTx called with WithoutCancel context", func(t *testing.T) {
		t.Parallel()
		tok := validStoredToken()
		tok.ExpiresAt = time.Now().Add(-time.Minute)
		var capturedCtx context.Context
		store := &authsharedtest.SessionFakeStorer{
			GetRefreshTokenByJTIFn: func(_ context.Context, _ [16]byte) (session.StoredRefreshToken, error) {
				return tok, nil
			},
			WriteRefreshFailedAuditTxFn: func(ctx context.Context, _, _ string) error {
				capturedCtx = ctx
				return nil
			},
		}
		parentCtx, cancel := context.WithCancel(context.Background())
		cancel() // cancelled before the service call
		svc := newServiceWithStore(store)
		_, err := svc.RotateRefreshToken(parentCtx, authsharedtest.RandomUUID(), "", "")
		require.ErrorIs(t, err, authshared.ErrInvalidToken)
		require.NotNil(t, capturedCtx, "WriteRefreshFailedAuditTx must have been called")
		select {
		case <-capturedCtx.Done():
			t.Fatal("WriteRefreshFailedAuditTx received a cancelled context; expected context.WithoutCancel")
		default:
		}
	})

	t.Run("GetUserVerifiedAndLocked → ErrUserNotFound → ErrInvalidToken", func(t *testing.T) {
		t.Parallel()
		tok := validStoredToken()
		store := &authsharedtest.SessionFakeStorer{
			GetRefreshTokenByJTIFn: func(_ context.Context, _ [16]byte) (session.StoredRefreshToken, error) {
				return tok, nil
			},
			GetUserVerifiedAndLockedFn: func(_ context.Context, _ [16]byte) (session.UserStatusResult, error) {
				return session.UserStatusResult{}, authshared.ErrUserNotFound
			},
		}
		svc := newServiceWithStore(store)
		_, err := svc.RotateRefreshToken(context.Background(), authsharedtest.RandomUUID(), "", "")
		require.ErrorIs(t, err, authshared.ErrInvalidToken)
	})

	t.Run("GetUserVerifiedAndLocked unexpected error → wrapped", func(t *testing.T) {
		t.Parallel()
		dbErr := errors.New("db error")
		tok := validStoredToken()
		store := &authsharedtest.SessionFakeStorer{
			GetRefreshTokenByJTIFn: func(_ context.Context, _ [16]byte) (session.StoredRefreshToken, error) {
				return tok, nil
			},
			GetUserVerifiedAndLockedFn: func(_ context.Context, _ [16]byte) (session.UserStatusResult, error) {
				return session.UserStatusResult{}, dbErr
			},
		}
		svc := newServiceWithStore(store)
		_, err := svc.RotateRefreshToken(context.Background(), authsharedtest.RandomUUID(), "", "")
		require.ErrorIs(t, err, dbErr)
	})

	t.Run("GetUserVerifiedAndLocked IsLocked=true → ErrAccountLocked", func(t *testing.T) {
		t.Parallel()
		tok := validStoredToken()
		store := &authsharedtest.SessionFakeStorer{
			GetRefreshTokenByJTIFn: func(_ context.Context, _ [16]byte) (session.StoredRefreshToken, error) {
				return tok, nil
			},
			GetUserVerifiedAndLockedFn: func(_ context.Context, _ [16]byte) (session.UserStatusResult, error) {
				return session.UserStatusResult{IsLocked: true, IsActive: true}, nil
			},
		}
		svc := newServiceWithStore(store)
		_, err := svc.RotateRefreshToken(context.Background(), authsharedtest.RandomUUID(), "", "")
		require.ErrorIs(t, err, authshared.ErrAccountLocked)
	})

	t.Run("GetUserVerifiedAndLocked IsActive=false → ErrAccountInactive", func(t *testing.T) {
		t.Parallel()
		tok := validStoredToken()
		store := &authsharedtest.SessionFakeStorer{
			GetRefreshTokenByJTIFn: func(_ context.Context, _ [16]byte) (session.StoredRefreshToken, error) {
				return tok, nil
			},
			GetUserVerifiedAndLockedFn: func(_ context.Context, _ [16]byte) (session.UserStatusResult, error) {
				return session.UserStatusResult{IsActive: false}, nil
			},
		}
		svc := newServiceWithStore(store)
		_, err := svc.RotateRefreshToken(context.Background(), authsharedtest.RandomUUID(), "", "")
		require.ErrorIs(t, err, authshared.ErrAccountInactive)
	})

	t.Run("RotateRefreshTokenTx returns ErrTokenAlreadyConsumed → ErrInvalidToken (Phase 1 fix)", func(t *testing.T) {
		t.Parallel()
		tok := validStoredToken()
		store := &authsharedtest.SessionFakeStorer{
			GetRefreshTokenByJTIFn: func(_ context.Context, _ [16]byte) (session.StoredRefreshToken, error) {
				return tok, nil
			},
			RotateRefreshTokenTxFn: func(_ context.Context, _ session.RotateTxInput) (session.RotatedSession, error) {
				return session.RotatedSession{}, authshared.ErrTokenAlreadyConsumed
			},
		}
		svc := newServiceWithStore(store)
		_, err := svc.RotateRefreshToken(context.Background(), authsharedtest.RandomUUID(), "", "")
		require.ErrorIs(t, err, authshared.ErrInvalidToken)
	})

	t.Run("RotateRefreshTokenTx error is wrapped", func(t *testing.T) {
		t.Parallel()
		storeErr := errors.New("rotate tx failed")
		token := validStoredToken()
		store := &authsharedtest.SessionFakeStorer{
			GetRefreshTokenByJTIFn: func(_ context.Context, _ [16]byte) (session.StoredRefreshToken, error) {
				return token, nil
			},
			RotateRefreshTokenTxFn: func(_ context.Context, _ session.RotateTxInput) (session.RotatedSession, error) {
				return session.RotatedSession{}, storeErr
			},
		}
		svc := newServiceWithStore(store)
		_, err := svc.RotateRefreshToken(context.Background(), authsharedtest.RandomUUID(), "", "")
		require.ErrorIs(t, err, storeErr)
	})

	t.Run("expired token returns ErrInvalidToken", func(t *testing.T) {
		t.Parallel()
		token := validStoredToken()
		token.ExpiresAt = time.Now().Add(-time.Minute)
		store := &authsharedtest.SessionFakeStorer{
			GetRefreshTokenByJTIFn: func(_ context.Context, _ [16]byte) (session.StoredRefreshToken, error) {
				return token, nil
			},
		}
		svc := newServiceWithStore(store)
		_, err := svc.RotateRefreshToken(context.Background(), authsharedtest.RandomUUID(), "", "")
		require.ErrorIs(t, err, authshared.ErrInvalidToken)
	})
}

func TestRotateRefreshToken_ReuseDetected_RevokeFamilyUsesWithoutCancel(t *testing.T) {
	t.Parallel()
	// Verify that RevokeFamilyTokensTx is called with a context that is NOT
	// cancelled when the parent context is cancelled. This enforces ADR-004:
	// a client-timed disconnect must not abort the family revocation.

	token := validStoredToken()
	token.IsRevoked = true

	// capturedCtx records the context passed to RevokeFamilyTokensTx.
	var capturedCtx context.Context
	store := &authsharedtest.SessionFakeStorer{
		GetRefreshTokenByJTIFn: func(_ context.Context, _ [16]byte) (session.StoredRefreshToken, error) {
			return token, nil
		},
		RevokeFamilyTokensTxFn: func(ctx context.Context, _, _ [16]byte, _ string) error {
			capturedCtx = ctx
			return nil
		},
	}

	// Create a parent context and cancel it immediately to simulate a disconnect.
	parentCtx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before the service call

	svc := newServiceWithStore(store)
	_, err := svc.RotateRefreshToken(parentCtx, authsharedtest.RandomUUID(), "", "")
	require.ErrorIs(t, err, authshared.ErrTokenReuseDetected)
	require.NotNil(t, capturedCtx, "RevokeFamilyTokensTx must have been called")

	// The context passed to RevokeFamilyTokensTx must not be cancelled even
	// though the parent context was cancelled before the call.
	select {
	case <-capturedCtx.Done():
		t.Fatal("RevokeFamilyTokensTx received a cancelled context; expected context.WithoutCancel")
	default:
		// Context is still live — WithoutCancel worked correctly.
	}
}

// TestRotateRefreshToken_TokenNotFound_WritesRefreshFailedAudit verifies that
// WriteRefreshFailedAuditTx is invoked when GetRefreshTokenByJTI returns
// ErrTokenNotFound (fake_storer.go:316–320 coverage).
func TestRotateRefreshToken_TokenNotFound_WritesRefreshFailedAudit(t *testing.T) {
	t.Parallel()

	var auditCalled bool
	store := &authsharedtest.SessionFakeStorer{
		GetRefreshTokenByJTIFn: func(_ context.Context, _ [16]byte) (session.StoredRefreshToken, error) {
			return session.StoredRefreshToken{}, authshared.ErrTokenNotFound
		},
		WriteRefreshFailedAuditTxFn: func(_ context.Context, _, _ string) error {
			auditCalled = true
			return nil
		},
	}
	svc := newServiceWithStore(store)
	_, err := svc.RotateRefreshToken(context.Background(), authsharedtest.RandomUUID(), "1.2.3.4", "agent")
	require.ErrorIs(t, err, authshared.ErrInvalidToken)
	require.True(t, auditCalled, "WriteRefreshFailedAuditTx must be called on ErrTokenNotFound")
}

// ── TestLogout ─────────────────────────────────────────────────────────────────

func TestLogout(t *testing.T) {
	t.Parallel()

	t.Run("success calls LogoutTx", func(t *testing.T) {
		t.Parallel()
		var called bool
		store := &authsharedtest.SessionFakeStorer{
			LogoutTxFn: func(_ context.Context, _ session.LogoutTxInput) error {
				called = true
				return nil
			},
		}
		svc := newServiceWithStore(store)
		err := svc.Logout(context.Background(), session.LogoutTxInput{JTI: authsharedtest.RandomUUID()})
		require.NoError(t, err)
		require.True(t, called)
	})

	t.Run("LogoutTx error is logged but nil is returned", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.SessionFakeStorer{
			LogoutTxFn: func(_ context.Context, _ session.LogoutTxInput) error {
				return errors.New("db error")
			},
		}
		svc := newServiceWithStore(store)
		// Errors from LogoutTx are intentionally swallowed so the handler always
		// clears the cookie and returns 204 regardless of DB state.
		err := svc.Logout(context.Background(), session.LogoutTxInput{JTI: authsharedtest.RandomUUID()})
		require.NoError(t, err)
	})
}

// ── TestLogout_WithoutCancelContext ─────────────────────────────────────────────

// TestLogout_WithoutCancelContext verifies that service.Logout calls LogoutTx
// with a context that is NOT cancelled when the parent context is cancelled.
// Mirrors the existing TestRotateRefreshToken_ReuseDetected_RevokeFamilyUsesWithoutCancel
// pattern.
func TestLogout_WithoutCancelContext(t *testing.T) {
	t.Parallel()
	var capturedCtx context.Context
	store := &authsharedtest.SessionFakeStorer{
		LogoutTxFn: func(ctx context.Context, _ session.LogoutTxInput) error {
			capturedCtx = ctx
			return nil
		},
	}

	parentCtx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before the service call

	svc := newServiceWithStore(store)
	err := svc.Logout(parentCtx, session.LogoutTxInput{JTI: authsharedtest.RandomUUID()})
	require.NoError(t, err)
	require.NotNil(t, capturedCtx, "LogoutTx must have been called")

	// The context passed to LogoutTx must not be cancelled even though the
	// parent context was cancelled before the call (ADR-004 invariant).
	select {
	case <-capturedCtx.Done():
		t.Fatal("LogoutTx received a cancelled context; expected context.WithoutCancel")
	default:
	}
}

// ── TestRevokeAllUserTokens_WithoutCancelContext ───────────────────────────────

// TestRevokeAllUserTokens_WithoutCancelContext verifies that
// service.RevokeAllUserTokens calls RevokeAllUserTokensTx with a context that
// is NOT cancelled when the parent context is cancelled (Phase 1.2 fix).
func TestRevokeAllUserTokens_WithoutCancelContext(t *testing.T) {
	t.Parallel()
	var capturedCtx context.Context
	store := &authsharedtest.SessionFakeStorer{
		RevokeAllUserTokensTxFn: func(ctx context.Context, _ [16]byte, _, _, _ string) error {
			capturedCtx = ctx
			return nil
		},
	}

	parentCtx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before the service call

	svc := newServiceWithStore(store)
	err := svc.RevokeAllUserTokens(parentCtx, authsharedtest.RandomUUID(), "", "")
	require.NoError(t, err)
	require.NotNil(t, capturedCtx, "RevokeAllUserTokensTx must have been called")

	// The context passed to RevokeAllUserTokensTx must not be cancelled (ADR-004).
	select {
	case <-capturedCtx.Done():
		t.Fatal("RevokeAllUserTokensTx received a cancelled context; expected context.WithoutCancel")
	default:
	}
}

// ── TestRevokeAllUserTokens ────────────────────────────────────────────────────

func TestRevokeAllUserTokens(t *testing.T) {
	t.Parallel()

	t.Run("delegates to store with hardcoded reason", func(t *testing.T) {
		t.Parallel()
		var gotReason string
		store := &authsharedtest.SessionFakeStorer{
			RevokeAllUserTokensTxFn: func(_ context.Context, _ [16]byte, reason, _, _ string) error {
				gotReason = reason
				return nil
			},
		}
		svc := newServiceWithStore(store)
		require.NoError(t, svc.RevokeAllUserTokens(context.Background(), authsharedtest.RandomUUID(), "", ""))
		require.Equal(t, "forced_logout", gotReason)
	})

	t.Run("store error is wrapped", func(t *testing.T) {
		t.Parallel()
		storeErr := errors.New("revoke error")
		store := &authsharedtest.SessionFakeStorer{
			RevokeAllUserTokensTxFn: func(_ context.Context, _ [16]byte, _, _, _ string) error { return storeErr },
		}
		svc := newServiceWithStore(store)
		err := svc.RevokeAllUserTokens(context.Background(), authsharedtest.RandomUUID(), "", "")
		require.ErrorIs(t, err, storeErr)
	})
}
