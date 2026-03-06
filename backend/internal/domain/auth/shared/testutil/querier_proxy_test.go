package authsharedtest_test

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/7-Dany/store/backend/internal/db"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────────
// nopQuerier — a zero-value implementation of db.Querier.
//
// Every method returns its zero value and a nil error.  Used as the Base for
// BaseQuerierProxy so that delegation-path tests and Fail-flag tests can all
// exercise the proxy without a real database connection.
// ─────────────────────────────────────────────────────────────────────────────

type nopQuerier struct{}

func (n *nopQuerier) ConsumeEmailVerificationToken(_ context.Context, _ pgtype.UUID) (int64, error) {
	return 0, nil
}
func (n *nopQuerier) ConsumePasswordResetToken(_ context.Context, _ pgtype.UUID) (int64, error) {
	return 0, nil
}
func (n *nopQuerier) ConsumeUnlockToken(_ context.Context, _ pgtype.UUID) (int64, error) {
	return 0, nil
}
func (n *nopQuerier) CreateEmailVerificationToken(_ context.Context, _ db.CreateEmailVerificationTokenParams) (db.CreateEmailVerificationTokenRow, error) {
	return db.CreateEmailVerificationTokenRow{}, nil
}
func (n *nopQuerier) CreatePasswordResetToken(_ context.Context, _ db.CreatePasswordResetTokenParams) (db.CreatePasswordResetTokenRow, error) {
	return db.CreatePasswordResetTokenRow{}, nil
}
func (n *nopQuerier) CreateRefreshToken(_ context.Context, _ db.CreateRefreshTokenParams) (db.CreateRefreshTokenRow, error) {
	return db.CreateRefreshTokenRow{}, nil
}
func (n *nopQuerier) CreateRotatedRefreshToken(_ context.Context, _ db.CreateRotatedRefreshTokenParams) (db.CreateRotatedRefreshTokenRow, error) {
	return db.CreateRotatedRefreshTokenRow{}, nil
}
func (n *nopQuerier) CreateUnlockToken(_ context.Context, _ db.CreateUnlockTokenParams) (db.CreateUnlockTokenRow, error) {
	return db.CreateUnlockTokenRow{}, nil
}
func (n *nopQuerier) CreateUser(_ context.Context, _ db.CreateUserParams) (db.CreateUserRow, error) {
	return db.CreateUserRow{}, nil
}
func (n *nopQuerier) CreateUserSession(_ context.Context, _ db.CreateUserSessionParams) (db.CreateUserSessionRow, error) {
	return db.CreateUserSessionRow{}, nil
}
func (n *nopQuerier) EndAllUserSessions(_ context.Context, _ pgtype.UUID) error  { return nil }
func (n *nopQuerier) EndUserSession(_ context.Context, _ pgtype.UUID) error       { return nil }
func (n *nopQuerier) GetActiveSessions(_ context.Context, _ pgtype.UUID) ([]db.GetActiveSessionsRow, error) {
	return nil, nil
}
func (n *nopQuerier) GetEmailVerificationToken(_ context.Context, _ string) (db.GetEmailVerificationTokenRow, error) {
	return db.GetEmailVerificationTokenRow{}, nil
}
func (n *nopQuerier) GetLatestVerificationTokenCreatedAt(_ context.Context, _ pgtype.UUID) (time.Time, error) {
	return time.Time{}, nil
}
func (n *nopQuerier) GetPasswordResetTokenCreatedAt(_ context.Context, _ string) (time.Time, error) {
	return time.Time{}, nil
}
func (n *nopQuerier) GetPasswordResetToken(_ context.Context, _ string) (db.GetPasswordResetTokenRow, error) {
	return db.GetPasswordResetTokenRow{}, nil
}
func (n *nopQuerier) GetPasswordResetTokenForVerify(_ context.Context, _ string) (db.GetPasswordResetTokenForVerifyRow, error) {
	return db.GetPasswordResetTokenForVerifyRow{}, nil
}
func (n *nopQuerier) GetRefreshTokenByJTI(_ context.Context, _ pgtype.UUID) (db.GetRefreshTokenByJTIRow, error) {
	return db.GetRefreshTokenByJTIRow{}, nil
}
func (n *nopQuerier) GetSessionByID(_ context.Context, _ pgtype.UUID) (db.GetSessionByIDRow, error) {
	return db.GetSessionByIDRow{}, nil
}
func (n *nopQuerier) GetUnlockToken(_ context.Context, _ string) (db.GetUnlockTokenRow, error) {
	return db.GetUnlockTokenRow{}, nil
}
func (n *nopQuerier) HasConsumedUnlockToken(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (n *nopQuerier) GetUserEmailVerified(_ context.Context, _ pgtype.Text) (bool, error) {
	return false, nil
}
func (n *nopQuerier) GetUserForLogin(_ context.Context, _ pgtype.Text) (db.GetUserForLoginRow, error) {
	return db.GetUserForLoginRow{}, nil
}
func (n *nopQuerier) GetUserForPasswordReset(_ context.Context, _ pgtype.Text) (db.GetUserForPasswordResetRow, error) {
	return db.GetUserForPasswordResetRow{}, nil
}
func (n *nopQuerier) GetUserForResend(_ context.Context, _ pgtype.Text) (db.GetUserForResendRow, error) {
	return db.GetUserForResendRow{}, nil
}
func (n *nopQuerier) GetUserForUnlock(_ context.Context, _ pgtype.Text) (db.GetUserForUnlockRow, error) {
	return db.GetUserForUnlockRow{}, nil
}
func (n *nopQuerier) GetUserPasswordHash(_ context.Context, _ pgtype.UUID) (db.GetUserPasswordHashRow, error) {
	return db.GetUserPasswordHashRow{}, nil
}
func (n *nopQuerier) GetUserProfile(_ context.Context, _ pgtype.UUID) (db.GetUserProfileRow, error) {
	return db.GetUserProfileRow{}, nil
}
func (n *nopQuerier) GetUserVerifiedAndLocked(_ context.Context, _ pgtype.UUID) (db.GetUserVerifiedAndLockedRow, error) {
	return db.GetUserVerifiedAndLockedRow{}, nil
}
func (n *nopQuerier) IncrementLoginFailures(_ context.Context, _ pgtype.UUID) (db.IncrementLoginFailuresRow, error) {
	return db.IncrementLoginFailuresRow{}, nil
}
func (n *nopQuerier) IncrementVerificationAttempts(_ context.Context, _ pgtype.UUID) (int16, error) {
	return 0, nil
}
func (n *nopQuerier) InsertAuditLog(_ context.Context, _ db.InsertAuditLogParams) error { return nil }
func (n *nopQuerier) InvalidateAllUserPasswordResetTokens(_ context.Context, _ pgtype.UUID) error {
	return nil
}
func (n *nopQuerier) InvalidateAllUserTokens(_ context.Context, _ pgtype.UUID) error { return nil }
func (n *nopQuerier) LockAccount(_ context.Context, _ pgtype.UUID) (int64, error)     { return 0, nil }
func (n *nopQuerier) MarkEmailVerified(_ context.Context, _ pgtype.UUID) (int64, error) {
	return 0, nil
}
func (n *nopQuerier) ResetLoginFailures(_ context.Context, _ pgtype.UUID) error { return nil }
func (n *nopQuerier) RevokeAllUserRefreshTokens(_ context.Context, _ db.RevokeAllUserRefreshTokensParams) error {
	return nil
}
func (n *nopQuerier) RevokeFamilyRefreshTokens(_ context.Context, _ db.RevokeFamilyRefreshTokensParams) error {
	return nil
}
func (n *nopQuerier) RevokePreVerificationTokens(_ context.Context, _ pgtype.UUID) error { return nil }
func (n *nopQuerier) RevokeRefreshTokenByJTI(_ context.Context, _ db.RevokeRefreshTokenByJTIParams) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (n *nopQuerier) RevokeSessionRefreshTokens(_ context.Context, _ pgtype.UUID) error { return nil }
func (n *nopQuerier) UnlockAccount(_ context.Context, _ pgtype.UUID) error               { return nil }
func (n *nopQuerier) UpdateLastLoginAt(_ context.Context, _ pgtype.UUID) error           { return nil }
func (n *nopQuerier) UpdatePasswordHash(_ context.Context, _ db.UpdatePasswordHashParams) error {
	return nil
}
func (n *nopQuerier) IncrementChangePasswordFailures(_ context.Context, _ pgtype.UUID) (int16, error) {
	return 0, nil
}
func (n *nopQuerier) ResetChangePasswordFailures(_ context.Context, _ pgtype.UUID) error { return nil }
func (n *nopQuerier) UpdateSessionLastActive(_ context.Context, _ pgtype.UUID) error { return nil }
func (n *nopQuerier) UpdateUserProfile(_ context.Context, _ db.UpdateUserProfileParams) error {
	return nil
}

// compile-time guard
var _ db.Querier = (*nopQuerier)(nil)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// nopProxy returns a *QuerierProxy backed by a nopQuerier.
func nopProxy() *authsharedtest.QuerierProxy {
	return authsharedtest.NewQuerierProxy(&nopQuerier{})
}

var bg = context.Background()
var anyUUID = pgtype.UUID{}

// ─────────────────────────────────────────────────────────────────────────────
// Legacy InsertAuditLog tests (kept from the original file)
// ─────────────────────────────────────────────────────────────────────────────

// insertAuditLogStub is a minimal concrete implementation of db.Querier that
// only provides InsertAuditLog (always returning nil). It does NOT embed
// db.Querier as an interface field, which would leave a nil pointer that could
// panic if any unoverridden method were accidentally called.
//
// All tests here exercise only InsertAuditLog via QuerierProxy, so no
// other methods need to be implemented.
type insertAuditLogStub struct {
	// Embed QuerierProxy with a nil Base so that the compiler is satisfied
	// by the remaining db.Querier methods; any unexpected call will panic with
	// a clear nil-pointer message rather than a silent interface-nil dereference.
	authsharedtest.QuerierProxy
}

func (s *insertAuditLogStub) InsertAuditLog(_ context.Context, _ db.InsertAuditLogParams) error {
	return nil
}

// legacyProxy is a convenience constructor that wires insertAuditLogStub as the
// Base, keeping original test bodies free of repeated boilerplate.
func legacyProxy() *authsharedtest.QuerierProxy {
	stub := &insertAuditLogStub{}
	return authsharedtest.NewQuerierProxy(stub)
}

func TestQuerierProxy_InsertAuditLog_AlwaysIncrementsCount(t *testing.T) {
	t.Parallel()

	proxy := legacyProxy()

	require.NoError(t, proxy.InsertAuditLog(bg, db.InsertAuditLogParams{}))
	require.Equal(t, 1, proxy.InsertAuditLogCallCount)

	require.NoError(t, proxy.InsertAuditLog(bg, db.InsertAuditLogParams{}))
	require.Equal(t, 2, proxy.InsertAuditLogCallCount)
}

func TestQuerierProxy_InsertAuditLog_FailEveryCall(t *testing.T) {
	t.Parallel()

	proxy := legacyProxy()
	proxy.FailInsertAuditLog = true // InsertAuditLogFailOnCall == 0 → every call fails

	err := proxy.InsertAuditLog(bg, db.InsertAuditLogParams{})
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
	require.Equal(t, 1, proxy.InsertAuditLogCallCount)

	err = proxy.InsertAuditLog(bg, db.InsertAuditLogParams{})
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
	require.Equal(t, 2, proxy.InsertAuditLogCallCount)
}

func TestQuerierProxy_InsertAuditLog_FailOnSpecificCall(t *testing.T) {
	t.Parallel()

	proxy := legacyProxy()
	proxy.FailInsertAuditLog = true
	proxy.InsertAuditLogFailOnCall = 2 // only the 2nd call fails

	// 1st call — should succeed.
	err := proxy.InsertAuditLog(bg, db.InsertAuditLogParams{})
	require.NoError(t, err)
	require.Equal(t, 1, proxy.InsertAuditLogCallCount)

	// 2nd call — should fail.
	err = proxy.InsertAuditLog(bg, db.InsertAuditLogParams{})
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
	require.Equal(t, 2, proxy.InsertAuditLogCallCount)

	// 3rd call — should succeed again (only the Nth call fails).
	err = proxy.InsertAuditLog(bg, db.InsertAuditLogParams{})
	require.NoError(t, err)
	require.Equal(t, 3, proxy.InsertAuditLogCallCount)
}

// ─────────────────────────────────────────────────────────────────────────────
// Fail-injection tests — one per Fail* flag
// ─────────────────────────────────────────────────────────────────────────────

func TestQuerierProxy_FailEndAllUserSessions(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailEndAllUserSessions = true
	err := proxy.EndAllUserSessions(bg, anyUUID)
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailIncrementVerificationAttempts(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailIncrementVerificationAttempts = true
	_, err := proxy.IncrementVerificationAttempts(bg, anyUUID)
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailInsertAuditLog_Always(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailInsertAuditLog = true // InsertAuditLogFailOnCall == 0 → every call fails
	err := proxy.InsertAuditLog(bg, db.InsertAuditLogParams{})
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailInsertAuditLog_OnNthCall(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailInsertAuditLog = true
	proxy.InsertAuditLogFailOnCall = 2

	// First call must succeed (delegate to nopQuerier → nil).
	err := proxy.InsertAuditLog(bg, db.InsertAuditLogParams{})
	require.NoError(t, err, "call 1 should succeed")

	// Second call must return ErrProxy.
	err = proxy.InsertAuditLog(bg, db.InsertAuditLogParams{})
	require.ErrorIs(t, err, authsharedtest.ErrProxy, "call 2 should fail")
}

// TestQuerierProxy_FailInsertAuditLog_SkipWhenCallMismatch exercises the
// branch where FailInsertAuditLog=true but the current call count does not yet
// match InsertAuditLogFailOnCall, so the call is forwarded to Base instead of
// returning ErrProxy.
func TestQuerierProxy_FailInsertAuditLog_SkipWhenCallMismatch(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailInsertAuditLog = true
	proxy.InsertAuditLogFailOnCall = 3 // will only fail on 3rd call

	// Call 1 — count(1) ≠ failOnCall(3) → delegate → nil.
	err := proxy.InsertAuditLog(bg, db.InsertAuditLogParams{})
	require.NoError(t, err, "call 1 should not return ErrProxy")

	// Call 2 — count(2) ≠ failOnCall(3) → delegate → nil.
	err = proxy.InsertAuditLog(bg, db.InsertAuditLogParams{})
	require.NoError(t, err, "call 2 should not return ErrProxy")
}

func TestQuerierProxy_FailLockAccount(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailLockAccount = true
	_, err := proxy.LockAccount(bg, anyUUID)
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailRevokeAllUserRefreshTokens(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailRevokeAllUserRefreshTokens = true
	err := proxy.RevokeAllUserRefreshTokens(bg, db.RevokeAllUserRefreshTokensParams{})
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailUpdatePasswordHash(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailUpdatePasswordHash = true
	err := proxy.UpdatePasswordHash(bg, db.UpdatePasswordHashParams{})
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

// ─────────────────────────────────────────────────────────────────────────────
// Delegation tests — methods with no Fail* flag forward to Base
// ─────────────────────────────────────────────────────────────────────────────

// TestQuerierProxy_Delegation verifies that every method without a Fail*
// guard simply delegates to Base. We use a nopQuerier (returns nil/zero) as
// Base and assert that the proxy returns the same nil error.
func TestQuerierProxy_Delegation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	id := pgtype.UUID{}

	t.Run("ConsumeEmailVerificationToken", func(t *testing.T) {
		t.Parallel()
		proxy := nopProxy()
		_, err := proxy.ConsumeEmailVerificationToken(ctx, id)
		require.NoError(t, err)
	})

	t.Run("ConsumePasswordResetToken", func(t *testing.T) {
		t.Parallel()
		proxy := nopProxy()
		_, err := proxy.ConsumePasswordResetToken(ctx, id)
		require.NoError(t, err)
	})

	t.Run("ConsumeUnlockToken", func(t *testing.T) {
		t.Parallel()
		proxy := nopProxy()
		_, err := proxy.ConsumeUnlockToken(ctx, id)
		require.NoError(t, err)
	})

	t.Run("LockAccount_FlagFalse", func(t *testing.T) {
		t.Parallel()
		proxy := nopProxy()
		proxy.FailLockAccount = false
		_, err := proxy.LockAccount(ctx, id)
		require.NoError(t, err)
	})

	t.Run("UnlockAccount", func(t *testing.T) {
		t.Parallel()
		proxy := nopProxy()
		err := proxy.UnlockAccount(ctx, id)
		require.NoError(t, err)
	})

	t.Run("UpdateLastLoginAt", func(t *testing.T) {
		t.Parallel()
		proxy := nopProxy()
		err := proxy.UpdateLastLoginAt(ctx, id)
		require.NoError(t, err)
	})

	t.Run("UpdateSessionLastActive", func(t *testing.T) {
		t.Parallel()
		proxy := nopProxy()
		err := proxy.UpdateSessionLastActive(ctx, id)
		require.NoError(t, err)
	})

	t.Run("GetUserVerifiedAndLocked", func(t *testing.T) {
		t.Parallel()
		proxy := nopProxy()
		_, err := proxy.GetUserVerifiedAndLocked(ctx, id)
		require.NoError(t, err)
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Additional delegation coverage — remaining pass-through methods
// ─────────────────────────────────────────────────────────────────────────────

func TestQuerierProxy_EndUserSession_Delegates(t *testing.T) {
	t.Parallel()
	require.NoError(t, nopProxy().EndUserSession(bg, anyUUID))
}

func TestQuerierProxy_EndAllUserSessions_FlagFalse_Delegates(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailEndAllUserSessions = false
	require.NoError(t, proxy.EndAllUserSessions(bg, anyUUID))
}

func TestQuerierProxy_IncrementVerificationAttempts_FlagFalse_Delegates(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailIncrementVerificationAttempts = false
	_, err := proxy.IncrementVerificationAttempts(bg, anyUUID)
	require.NoError(t, err)
}

func TestQuerierProxy_InsertAuditLog_FlagFalse_Delegates(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailInsertAuditLog = false
	require.NoError(t, proxy.InsertAuditLog(bg, db.InsertAuditLogParams{}))
}

func TestQuerierProxy_RevokeAllUserRefreshTokens_FlagFalse_Delegates(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailRevokeAllUserRefreshTokens = false
	require.NoError(t, proxy.RevokeAllUserRefreshTokens(bg, db.RevokeAllUserRefreshTokensParams{}))
}

func TestQuerierProxy_UpdatePasswordHash_FlagFalse_Delegates(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailUpdatePasswordHash = false
	require.NoError(t, proxy.UpdatePasswordHash(bg, db.UpdatePasswordHashParams{}))
}

func TestQuerierProxy_GetActiveSessions_Delegates(t *testing.T) {
	t.Parallel()
	_, err := nopProxy().GetActiveSessions(bg, anyUUID)
	require.NoError(t, err)
}

func TestQuerierProxy_GetUserProfile_Delegates(t *testing.T) {
	t.Parallel()
	_, err := nopProxy().GetUserProfile(bg, anyUUID)
	require.NoError(t, err)
}

func TestQuerierProxy_InvalidateAllUserPasswordResetTokens_Delegates(t *testing.T) {
	t.Parallel()
	require.NoError(t, nopProxy().InvalidateAllUserPasswordResetTokens(bg, anyUUID))
}

func TestQuerierProxy_InvalidateAllUserTokens_Delegates(t *testing.T) {
	t.Parallel()
	require.NoError(t, nopProxy().InvalidateAllUserTokens(bg, anyUUID))
}

func TestQuerierProxy_MarkEmailVerified_Delegates(t *testing.T) {
	t.Parallel()
	_, err := nopProxy().MarkEmailVerified(bg, anyUUID)
	require.NoError(t, err)
}

func TestQuerierProxy_ResetLoginFailures_Delegates(t *testing.T) {
	t.Parallel()
	require.NoError(t, nopProxy().ResetLoginFailures(bg, anyUUID))
}

func TestQuerierProxy_RevokePreVerificationTokens_Delegates(t *testing.T) {
	t.Parallel()
	require.NoError(t, nopProxy().RevokePreVerificationTokens(bg, anyUUID))
}

func TestQuerierProxy_RevokeSessionRefreshTokens_Delegates(t *testing.T) {
	t.Parallel()
	require.NoError(t, nopProxy().RevokeSessionRefreshTokens(bg, anyUUID))
}

// ─────────────────────────────────────────────────────────────────────────────
// Ensure nopQuerier-typed return values flow through the proxy unchanged
// ─────────────────────────────────────────────────────────────────────────────

// sentinelQuerier overrides ConsumeEmailVerificationToken to return a known
// non-zero value, letting us verify the proxy forwards the Base result.
type sentinelQuerier struct {
	nopQuerier
}

func (s *sentinelQuerier) ConsumeEmailVerificationToken(_ context.Context, _ pgtype.UUID) (int64, error) {
	return 99, nil
}

func (s *sentinelQuerier) GetLatestVerificationTokenCreatedAt(_ context.Context, _ pgtype.UUID) (time.Time, error) {
	return time.Unix(12345, 0), nil
}

// nopAddr is a valid IP used for params that require netip.Addr.
var nopAddr = func() *netip.Addr { a := netip.MustParseAddr("127.0.0.1"); return &a }()

func TestQuerierProxy_ForwardsSentinelValue(t *testing.T) {
	t.Parallel()
	proxy := authsharedtest.NewQuerierProxy(&sentinelQuerier{})

	rows, err := proxy.ConsumeEmailVerificationToken(bg, anyUUID)
	require.NoError(t, err)
	require.Equal(t, int64(99), rows, "proxy must forward Base's return value unchanged")

	ts, err := proxy.GetLatestVerificationTokenCreatedAt(bg, anyUUID)
	require.NoError(t, err)
	require.Equal(t, time.Unix(12345, 0), ts)
}

// ─────────────────────────────────────────────────────────────────────────────
// Delegation tests — Create* methods
// ─────────────────────────────────────────────────────────────────────────────

func TestQuerierProxy_CreateEmailVerificationToken_Delegates(t *testing.T) {
	t.Parallel()
	_, err := nopProxy().CreateEmailVerificationToken(bg, db.CreateEmailVerificationTokenParams{})
	require.NoError(t, err)
}

func TestQuerierProxy_CreatePasswordResetToken_Delegates(t *testing.T) {
	t.Parallel()
	_, err := nopProxy().CreatePasswordResetToken(bg, db.CreatePasswordResetTokenParams{})
	require.NoError(t, err)
}

func TestQuerierProxy_CreateRefreshToken_Delegates(t *testing.T) {
	t.Parallel()
	_, err := nopProxy().CreateRefreshToken(bg, db.CreateRefreshTokenParams{})
	require.NoError(t, err)
}

func TestQuerierProxy_CreateRotatedRefreshToken_Delegates(t *testing.T) {
	t.Parallel()
	_, err := nopProxy().CreateRotatedRefreshToken(bg, db.CreateRotatedRefreshTokenParams{})
	require.NoError(t, err)
}

func TestQuerierProxy_CreateUnlockToken_Delegates(t *testing.T) {
	t.Parallel()
	_, err := nopProxy().CreateUnlockToken(bg, db.CreateUnlockTokenParams{})
	require.NoError(t, err)
}

func TestQuerierProxy_CreateUser_Delegates(t *testing.T) {
	t.Parallel()
	_, err := nopProxy().CreateUser(bg, db.CreateUserParams{})
	require.NoError(t, err)
}

func TestQuerierProxy_CreateUserSession_Delegates(t *testing.T) {
	t.Parallel()
	_, err := nopProxy().CreateUserSession(bg, db.CreateUserSessionParams{})
	require.NoError(t, err)
}

// ─────────────────────────────────────────────────────────────────────────────
// Delegation tests — Get* methods
// ─────────────────────────────────────────────────────────────────────────────

func TestQuerierProxy_GetEmailVerificationToken_Delegates(t *testing.T) {
	t.Parallel()
	_, err := nopProxy().GetEmailVerificationToken(bg, "test@example.com")
	require.NoError(t, err)
}

func TestQuerierProxy_GetPasswordResetToken_Delegates(t *testing.T) {
	t.Parallel()
	_, err := nopProxy().GetPasswordResetToken(bg, "test@example.com")
	require.NoError(t, err)
}

func TestQuerierProxy_GetRefreshTokenByJTI_Delegates(t *testing.T) {
	t.Parallel()
	_, err := nopProxy().GetRefreshTokenByJTI(bg, anyUUID)
	require.NoError(t, err)
}

func TestQuerierProxy_GetSessionByID_Delegates(t *testing.T) {
	t.Parallel()
	_, err := nopProxy().GetSessionByID(bg, anyUUID)
	require.NoError(t, err)
}

func TestQuerierProxy_GetUnlockToken_Delegates(t *testing.T) {
	t.Parallel()
	_, err := nopProxy().GetUnlockToken(bg, "test@example.com")
	require.NoError(t, err)
}

func TestQuerierProxy_GetUserEmailVerified_Delegates(t *testing.T) {
	t.Parallel()
	_, err := nopProxy().GetUserEmailVerified(bg, pgtype.Text{String: "test@example.com", Valid: true})
	require.NoError(t, err)
}

func TestQuerierProxy_GetUserForLogin_Delegates(t *testing.T) {
	t.Parallel()
	_, err := nopProxy().GetUserForLogin(bg, pgtype.Text{String: "test@example.com", Valid: true})
	require.NoError(t, err)
}

func TestQuerierProxy_GetUserForPasswordReset_Delegates(t *testing.T) {
	t.Parallel()
	_, err := nopProxy().GetUserForPasswordReset(bg, pgtype.Text{String: "test@example.com", Valid: true})
	require.NoError(t, err)
}

func TestQuerierProxy_GetUserForResend_Delegates(t *testing.T) {
	t.Parallel()
	_, err := nopProxy().GetUserForResend(bg, pgtype.Text{String: "test@example.com", Valid: true})
	require.NoError(t, err)
}

func TestQuerierProxy_GetUserForUnlock_Delegates(t *testing.T) {
	t.Parallel()
	_, err := nopProxy().GetUserForUnlock(bg, pgtype.Text{String: "test@example.com", Valid: true})
	require.NoError(t, err)
}

func TestQuerierProxy_GetUserPasswordHash_Delegates(t *testing.T) {
	t.Parallel()
	_, err := nopProxy().GetUserPasswordHash(bg, anyUUID)
	require.NoError(t, err)
}

// ─────────────────────────────────────────────────────────────────────────────
// Delegation tests — remaining non-flag methods
// ─────────────────────────────────────────────────────────────────────────────

func TestQuerierProxy_IncrementLoginFailures_Delegates(t *testing.T) {
	t.Parallel()
	_, err := nopProxy().IncrementLoginFailures(bg, anyUUID)
	require.NoError(t, err)
}

func TestQuerierProxy_RevokeFamilyRefreshTokens_Delegates(t *testing.T) {
	t.Parallel()
	require.NoError(t, nopProxy().RevokeFamilyRefreshTokens(bg, db.RevokeFamilyRefreshTokensParams{}))
}

func TestQuerierProxy_RevokeRefreshTokenByJTI_Delegates(t *testing.T) {
	t.Parallel()
	_, err := nopProxy().RevokeRefreshTokenByJTI(bg, db.RevokeRefreshTokenByJTIParams{})
	require.NoError(t, err)
}

// ─────────────────────────────────────────────────────────────────────────────
// Phase 3 — Fail* flag injection tests (previously 0 coverage)
// ─────────────────────────────────────────────────────────────────────────────

// ── Login ────────────────────────────────────────────────────────────────────

func TestQuerierProxy_FailGetUserForLogin(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailGetUserForLogin = true
	_, err := proxy.GetUserForLogin(bg, pgtype.Text{})
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailCreateUserSession(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailCreateUserSession = true
	_, err := proxy.CreateUserSession(bg, db.CreateUserSessionParams{})
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailCreateRefreshToken(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailCreateRefreshToken = true
	_, err := proxy.CreateRefreshToken(bg, db.CreateRefreshTokenParams{})
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailUpdateLastLoginAt(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailUpdateLastLoginAt = true
	err := proxy.UpdateLastLoginAt(bg, anyUUID)
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailIncrementLoginFailures(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailIncrementLoginFailures = true
	_, err := proxy.IncrementLoginFailures(bg, anyUUID)
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailResetLoginFailures(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailResetLoginFailures = true
	err := proxy.ResetLoginFailures(bg, anyUUID)
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

// ── Password reset ────────────────────────────────────────────────────────────

func TestQuerierProxy_FailGetUserForPasswordReset(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailGetUserForPasswordReset = true
	_, err := proxy.GetUserForPasswordReset(bg, pgtype.Text{})
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailInvalidateAllUserPasswordResetTokens(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailInvalidateAllUserPasswordResetTokens = true
	err := proxy.InvalidateAllUserPasswordResetTokens(bg, anyUUID)
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailCreatePasswordResetToken(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailCreatePasswordResetToken = true
	_, err := proxy.CreatePasswordResetToken(bg, db.CreatePasswordResetTokenParams{})
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailGetPasswordResetToken(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailGetPasswordResetToken = true
	_, err := proxy.GetPasswordResetToken(bg, "test@test.com")
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailConsumePasswordResetToken(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailConsumePasswordResetToken = true
	_, err := proxy.ConsumePasswordResetToken(bg, anyUUID)
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_ConsumePasswordResetTokenZero(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.ConsumePasswordResetTokenZero = true
	n, err := proxy.ConsumePasswordResetToken(bg, anyUUID)
	require.NoError(t, err)
	require.Equal(t, int64(0), n)
}

// ── Profile / sessions ────────────────────────────────────────────────────────

func TestQuerierProxy_FailGetUserProfile(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailGetUserProfile = true
	_, err := proxy.GetUserProfile(bg, anyUUID)
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailGetActiveSessions(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailGetActiveSessions = true
	_, err := proxy.GetActiveSessions(bg, anyUUID)
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailGetSessionByID(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailGetSessionByID = true
	_, err := proxy.GetSessionByID(bg, anyUUID)
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailEndUserSession(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailEndUserSession = true
	err := proxy.EndUserSession(bg, anyUUID)
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailRevokeSessionRefreshTokens(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailRevokeSessionRefreshTokens = true
	err := proxy.RevokeSessionRefreshTokens(bg, anyUUID)
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailGetUserPasswordHash(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailGetUserPasswordHash = true
	_, err := proxy.GetUserPasswordHash(bg, anyUUID)
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

// ── Register ──────────────────────────────────────────────────────────────────

func TestQuerierProxy_FailCreateUser(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailCreateUser = true
	_, err := proxy.CreateUser(bg, db.CreateUserParams{})
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailCreateEmailVerificationToken(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailCreateEmailVerificationToken = true
	_, err := proxy.CreateEmailVerificationToken(bg, db.CreateEmailVerificationTokenParams{})
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

// ── Session / refresh token rotation ─────────────────────────────────────────

func TestQuerierProxy_FailGetRefreshTokenByJTI(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailGetRefreshTokenByJTI = true
	_, err := proxy.GetRefreshTokenByJTI(bg, anyUUID)
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailRevokeRefreshTokenByJTI(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailRevokeRefreshTokenByJTI = true
	_, err := proxy.RevokeRefreshTokenByJTI(bg, db.RevokeRefreshTokenByJTIParams{})
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailCreateRotatedRefreshToken(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailCreateRotatedRefreshToken = true
	_, err := proxy.CreateRotatedRefreshToken(bg, db.CreateRotatedRefreshTokenParams{})
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailUpdateSessionLastActive(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailUpdateSessionLastActive = true
	err := proxy.UpdateSessionLastActive(bg, anyUUID)
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailRevokeFamilyRefreshTokens(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailRevokeFamilyRefreshTokens = true
	err := proxy.RevokeFamilyRefreshTokens(bg, db.RevokeFamilyRefreshTokensParams{})
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

// ── Unlock ────────────────────────────────────────────────────────────────────

func TestQuerierProxy_FailGetUserForUnlock(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailGetUserForUnlock = true
	_, err := proxy.GetUserForUnlock(bg, pgtype.Text{})
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailGetUnlockToken(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailGetUnlockToken = true
	_, err := proxy.GetUnlockToken(bg, "test@test.com")
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailConsumeUnlockToken(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailConsumeUnlockToken = true
	_, err := proxy.ConsumeUnlockToken(bg, anyUUID)
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_ConsumeUnlockTokenZero(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.ConsumeUnlockTokenZero = true
	n, err := proxy.ConsumeUnlockToken(bg, anyUUID)
	require.NoError(t, err)
	require.Equal(t, int64(0), n)
}

func TestQuerierProxy_FailCreateUnlockToken(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailCreateUnlockToken = true
	_, err := proxy.CreateUnlockToken(bg, db.CreateUnlockTokenParams{})
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailUnlockAccount(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailUnlockAccount = true
	err := proxy.UnlockAccount(bg, anyUUID)
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_HasConsumedUnlockToken_Delegates(t *testing.T) {
	t.Parallel()
	_, err := nopProxy().HasConsumedUnlockToken(bg, "test@test.com")
	require.NoError(t, err)
}

func TestQuerierProxy_FailHasConsumedUnlockToken(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailHasConsumedUnlockToken = true
	_, err := proxy.HasConsumedUnlockToken(bg, "test@test.com")
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

// ── Email verification ────────────────────────────────────────────────────────

func TestQuerierProxy_FailGetEmailVerificationToken(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailGetEmailVerificationToken = true
	_, err := proxy.GetEmailVerificationToken(bg, "test@test.com")
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailConsumeEmailVerificationToken(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailConsumeEmailVerificationToken = true
	_, err := proxy.ConsumeEmailVerificationToken(bg, anyUUID)
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailRevokePreVerificationTokens(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailRevokePreVerificationTokens = true
	err := proxy.RevokePreVerificationTokens(bg, anyUUID)
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailMarkEmailVerified(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailMarkEmailVerified = true
	_, err := proxy.MarkEmailVerified(bg, anyUUID)
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailGetUserVerifiedAndLocked(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailGetUserVerifiedAndLocked = true
	_, err := proxy.GetUserVerifiedAndLocked(bg, anyUUID)
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailGetUserForResend(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailGetUserForResend = true
	_, err := proxy.GetUserForResend(bg, pgtype.Text{})
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailGetLatestVerificationTokenCreatedAt(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailGetLatestVerificationTokenCreatedAt = true
	_, err := proxy.GetLatestVerificationTokenCreatedAt(bg, anyUUID)
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_FailInvalidateAllUserTokens(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailInvalidateAllUserTokens = true
	err := proxy.InvalidateAllUserTokens(bg, anyUUID)
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

// ── GetPasswordResetTokenCreatedAt ───────────────────────────────────────────────────

func TestQuerierProxy_FailGetPasswordResetTokenCreatedAt(t *testing.T) {
	t.Parallel()
	proxy := nopProxy()
	proxy.FailGetPasswordResetTokenCreatedAt = true
	_, err := proxy.GetPasswordResetTokenCreatedAt(bg, "x@example.com")
	require.ErrorIs(t, err, authsharedtest.ErrProxy)
}

func TestQuerierProxy_GetPasswordResetTokenCreatedAt_Delegates(t *testing.T) {
	t.Parallel()
	// nopQuerier.GetPasswordResetTokenCreatedAt returns time.Time{}, nil.
	_, err := nopProxy().GetPasswordResetTokenCreatedAt(bg, "x@example.com")
	require.NoError(t, err)
}
