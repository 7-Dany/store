// Package authsharedtest provides test-only helpers shared across all auth
// feature sub-packages. It must never be imported by production code.
package authsharedtest

import (
	"context"
	"errors"
	"time"

	"github.com/7-Dany/store/backend/internal/db"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrProxy is the sentinel error returned by any QuerierProxy when a Fail* flag is set.
var ErrProxy = errors.New("querier_proxy: injected error")

// compile-time check that *QuerierProxy satisfies db.Querier.
var _ db.Querier = (*QuerierProxy)(nil)

// QuerierProxy implements all db.Querier methods, delegating each call to
// Base unless the corresponding Fail* flag is set — in that case ErrProxy is
// returned immediately without calling Base.
type QuerierProxy struct {
	Base db.Querier

	// ── InsertAuditLog ───────────────────────────────────────────────────────
	// FailInsertAuditLog enables InsertAuditLog failure injection.
	FailInsertAuditLog bool
	// InsertAuditLogFailOnCall, when non-zero, causes InsertAuditLog to fail only
	// on the Nth call (1-based). When zero and FailInsertAuditLog is true, every
	// call fails.
	InsertAuditLogFailOnCall int
	// InsertAuditLogCallCount counts how many times InsertAuditLog has been called
	// since the proxy was constructed. Updated regardless of FailInsertAuditLog.
	InsertAuditLogCallCount int

	// ── Shared flags (originally in BaseQuerierProxy) ────────────────────────
	FailIncrementVerificationAttempts bool
	FailLockAccount                   bool
	FailUpdatePasswordHash            bool
	FailRevokeAllUserRefreshTokens    bool
	FailEndAllUserSessions            bool

	// ── login ────────────────────────────────────────────────────────────────
	FailGetUserForLogin        bool
	FailCreateUserSession      bool
	FailCreateRefreshToken     bool
	FailUpdateLastLoginAt      bool
	FailIncrementLoginFailures bool
	FailResetLoginFailures     bool

	// ── password ─────────────────────────────────────────────────────────────
	FailGetPasswordResetTokenForVerify       bool
	FailGetPasswordResetTokenCreatedAt       bool
	FailGetUserForPasswordReset              bool
	FailInvalidateAllUserPasswordResetTokens bool
	FailCreatePasswordResetToken             bool
	FailGetPasswordResetToken                bool
	FailConsumePasswordResetToken            bool
	ConsumePasswordResetTokenZero            bool // returns 0, nil before fail check
	FailGetUserPasswordHash                  bool // also used by change-password store methods
	FailIncrementChangePasswordFailures      bool
	FailResetChangePasswordFailures          bool

	// ── profile ──────────────────────────────────────────────────────────────
	FailGetUserProfile             bool
	FailGetActiveSessions          bool
	FailGetSessionByID             bool
	FailEndUserSession             bool
	FailRevokeSessionRefreshTokens bool
	FailUpdateUserProfile          bool

	// ── register ─────────────────────────────────────────────────────────────
	FailCreateUser                   bool
	FailCreateEmailVerificationToken bool

	// ── session ──────────────────────────────────────────────────────────────
	FailGetRefreshTokenByJTI      bool
	FailRevokeRefreshTokenByJTI   bool
	FailCreateRotatedRefreshToken bool
	FailUpdateSessionLastActive   bool
	FailRevokeFamilyRefreshTokens bool

	// ── unlock ───────────────────────────────────────────────────────────────
	FailGetUserForUnlock      bool
	FailGetUnlockToken        bool
	FailHasConsumedUnlockToken bool
	FailConsumeUnlockToken    bool
	ConsumeUnlockTokenZero    bool // returns 0, nil before fail check
	FailCreateUnlockToken     bool
	FailUnlockAccount         bool

	// ── set-password ─────────────────────────────────────────────────────────
	FailGetUserForSetPassword bool
	FailSetPasswordHash       bool

	// ── verification ─────────────────────────────────────────────────────────
	FailGetEmailVerificationToken           bool
	FailConsumeEmailVerificationToken       bool
	// ConsumeEmailVerificationTokenZero causes ConsumeEmailVerificationToken to
	// return (0, nil), simulating a concurrent consume that already used the token
	// (ErrTokenAlreadyUsed path in VerifyEmailTx). Checked before FailConsumeEmailVerificationToken.
	ConsumeEmailVerificationTokenZero       bool
	FailRevokePreVerificationTokens         bool
	FailMarkEmailVerified                   bool
	// MarkEmailVerifiedZero causes MarkEmailVerified to return (0, nil), simulating
	// a user row that is already verified or locked so VerifyEmailTx enters its
	// GetUserVerifiedAndLocked fallback. Checked before FailMarkEmailVerified.
	MarkEmailVerifiedZero                   bool
	FailGetUserVerifiedAndLocked            bool
	FailGetUserForResend                    bool
	FailGetLatestVerificationTokenCreatedAt bool
	FailInvalidateAllUserTokens             bool
}

// NewQuerierProxy constructs a QuerierProxy backed by base.
func NewQuerierProxy(base db.Querier) *QuerierProxy {
	return &QuerierProxy{Base: base}
}

// ── ConsumeEmailVerificationToken ────────────────────────────────────────────

func (b *QuerierProxy) ConsumeEmailVerificationToken(ctx context.Context, id pgtype.UUID) (int64, error) {
	if b.ConsumeEmailVerificationTokenZero {
		return 0, nil
	}
	if b.FailConsumeEmailVerificationToken {
		return 0, ErrProxy
	}
	return b.Base.ConsumeEmailVerificationToken(ctx, id)
}

// ── ConsumePasswordResetToken ────────────────────────────────────────────────

func (b *QuerierProxy) ConsumePasswordResetToken(ctx context.Context, id pgtype.UUID) (int64, error) {
	if b.ConsumePasswordResetTokenZero {
		return 0, nil
	}
	if b.FailConsumePasswordResetToken {
		return 0, ErrProxy
	}
	return b.Base.ConsumePasswordResetToken(ctx, id)
}

// ── ConsumeUnlockToken ───────────────────────────────────────────────────────

func (b *QuerierProxy) ConsumeUnlockToken(ctx context.Context, id pgtype.UUID) (int64, error) {
	if b.ConsumeUnlockTokenZero {
		return 0, nil
	}
	if b.FailConsumeUnlockToken {
		return 0, ErrProxy
	}
	return b.Base.ConsumeUnlockToken(ctx, id)
}

// ── CreateEmailVerificationToken ─────────────────────────────────────────────

func (b *QuerierProxy) CreateEmailVerificationToken(ctx context.Context, arg db.CreateEmailVerificationTokenParams) (db.CreateEmailVerificationTokenRow, error) {
	if b.FailCreateEmailVerificationToken {
		return db.CreateEmailVerificationTokenRow{}, ErrProxy
	}
	return b.Base.CreateEmailVerificationToken(ctx, arg)
}

// ── CreatePasswordResetToken ─────────────────────────────────────────────────

func (b *QuerierProxy) CreatePasswordResetToken(ctx context.Context, arg db.CreatePasswordResetTokenParams) (db.CreatePasswordResetTokenRow, error) {
	if b.FailCreatePasswordResetToken {
		return db.CreatePasswordResetTokenRow{}, ErrProxy
	}
	return b.Base.CreatePasswordResetToken(ctx, arg)
}

// ── CreateRefreshToken ───────────────────────────────────────────────────────

func (b *QuerierProxy) CreateRefreshToken(ctx context.Context, arg db.CreateRefreshTokenParams) (db.CreateRefreshTokenRow, error) {
	if b.FailCreateRefreshToken {
		return db.CreateRefreshTokenRow{}, ErrProxy
	}
	return b.Base.CreateRefreshToken(ctx, arg)
}

// ── CreateRotatedRefreshToken ────────────────────────────────────────────────

func (b *QuerierProxy) CreateRotatedRefreshToken(ctx context.Context, arg db.CreateRotatedRefreshTokenParams) (db.CreateRotatedRefreshTokenRow, error) {
	if b.FailCreateRotatedRefreshToken {
		return db.CreateRotatedRefreshTokenRow{}, ErrProxy
	}
	return b.Base.CreateRotatedRefreshToken(ctx, arg)
}

// ── CreateUnlockToken ────────────────────────────────────────────────────────

func (b *QuerierProxy) CreateUnlockToken(ctx context.Context, arg db.CreateUnlockTokenParams) (db.CreateUnlockTokenRow, error) {
	if b.FailCreateUnlockToken {
		return db.CreateUnlockTokenRow{}, ErrProxy
	}
	return b.Base.CreateUnlockToken(ctx, arg)
}

// ── CreateUser ───────────────────────────────────────────────────────────────

func (b *QuerierProxy) CreateUser(ctx context.Context, arg db.CreateUserParams) (db.CreateUserRow, error) {
	if b.FailCreateUser {
		return db.CreateUserRow{}, ErrProxy
	}
	return b.Base.CreateUser(ctx, arg)
}

// ── CreateUserSession ────────────────────────────────────────────────────────

func (b *QuerierProxy) CreateUserSession(ctx context.Context, arg db.CreateUserSessionParams) (db.CreateUserSessionRow, error) {
	if b.FailCreateUserSession {
		return db.CreateUserSessionRow{}, ErrProxy
	}
	return b.Base.CreateUserSession(ctx, arg)
}

// ── EndAllUserSessions ───────────────────────────────────────────────────────

func (b *QuerierProxy) EndAllUserSessions(ctx context.Context, userID pgtype.UUID) error {
	if b.FailEndAllUserSessions {
		return ErrProxy
	}
	return b.Base.EndAllUserSessions(ctx, userID)
}

// ── EndUserSession ───────────────────────────────────────────────────────────

func (b *QuerierProxy) EndUserSession(ctx context.Context, id pgtype.UUID) error {
	if b.FailEndUserSession {
		return ErrProxy
	}
	return b.Base.EndUserSession(ctx, id)
}

// ── GetActiveSessions ────────────────────────────────────────────────────────

func (b *QuerierProxy) GetActiveSessions(ctx context.Context, userID pgtype.UUID) ([]db.GetActiveSessionsRow, error) {
	if b.FailGetActiveSessions {
		return nil, ErrProxy
	}
	return b.Base.GetActiveSessions(ctx, userID)
}

// ── GetEmailVerificationToken ────────────────────────────────────────────────

func (b *QuerierProxy) GetEmailVerificationToken(ctx context.Context, email string) (db.GetEmailVerificationTokenRow, error) {
	if b.FailGetEmailVerificationToken {
		return db.GetEmailVerificationTokenRow{}, ErrProxy
	}
	return b.Base.GetEmailVerificationToken(ctx, email)
}

// ── GetLatestVerificationTokenCreatedAt ──────────────────────────────────────

func (b *QuerierProxy) GetLatestVerificationTokenCreatedAt(ctx context.Context, userID pgtype.UUID) (time.Time, error) {
	if b.FailGetLatestVerificationTokenCreatedAt {
		return time.Time{}, ErrProxy
	}
	return b.Base.GetLatestVerificationTokenCreatedAt(ctx, userID)
}

// ── GetPasswordResetTokenCreatedAt ─────────────────────────────────────────

func (b *QuerierProxy) GetPasswordResetTokenCreatedAt(ctx context.Context, email string) (time.Time, error) {
	if b.FailGetPasswordResetTokenCreatedAt {
		return time.Time{}, ErrProxy
	}
	return b.Base.GetPasswordResetTokenCreatedAt(ctx, email)
}

// ── GetPasswordResetToken ────────────────────────────────────────────────────

func (b *QuerierProxy) GetPasswordResetToken(ctx context.Context, email string) (db.GetPasswordResetTokenRow, error) {
	if b.FailGetPasswordResetToken {
		return db.GetPasswordResetTokenRow{}, ErrProxy
	}
	return b.Base.GetPasswordResetToken(ctx, email)
}

// ── GetPasswordResetTokenForVerify ───────────────────────────────────────────

func (b *QuerierProxy) GetPasswordResetTokenForVerify(ctx context.Context, email string) (db.GetPasswordResetTokenForVerifyRow, error) {
	if b.FailGetPasswordResetTokenForVerify {
		return db.GetPasswordResetTokenForVerifyRow{}, ErrProxy
	}
	return b.Base.GetPasswordResetTokenForVerify(ctx, email)
}

// ── GetRefreshTokenByJTI ─────────────────────────────────────────────────────

func (b *QuerierProxy) GetRefreshTokenByJTI(ctx context.Context, jti pgtype.UUID) (db.GetRefreshTokenByJTIRow, error) {
	if b.FailGetRefreshTokenByJTI {
		return db.GetRefreshTokenByJTIRow{}, ErrProxy
	}
	return b.Base.GetRefreshTokenByJTI(ctx, jti)
}

// ── GetSessionByID ───────────────────────────────────────────────────────────

func (b *QuerierProxy) GetSessionByID(ctx context.Context, id pgtype.UUID) (db.GetSessionByIDRow, error) {
	if b.FailGetSessionByID {
		return db.GetSessionByIDRow{}, ErrProxy
	}
	return b.Base.GetSessionByID(ctx, id)
}

// ── GetUnlockToken ───────────────────────────────────────────────────────────

func (b *QuerierProxy) GetUnlockToken(ctx context.Context, email string) (db.GetUnlockTokenRow, error) {
	if b.FailGetUnlockToken {
		return db.GetUnlockTokenRow{}, ErrProxy
	}
	return b.Base.GetUnlockToken(ctx, email)
}

// ── HasConsumedUnlockToken ───────────────────────────────────────────────────

func (b *QuerierProxy) HasConsumedUnlockToken(ctx context.Context, email string) (bool, error) {
	if b.FailHasConsumedUnlockToken {
		return false, ErrProxy
	}
	return b.Base.HasConsumedUnlockToken(ctx, email)
}

// ── GetUserEmailVerified ─────────────────────────────────────────────────────

func (b *QuerierProxy) GetUserEmailVerified(ctx context.Context, email pgtype.Text) (bool, error) {
	return b.Base.GetUserEmailVerified(ctx, email)
}

// ── GetUserForLogin ──────────────────────────────────────────────────────────

func (b *QuerierProxy) GetUserForLogin(ctx context.Context, id pgtype.Text) (db.GetUserForLoginRow, error) {
	if b.FailGetUserForLogin {
		return db.GetUserForLoginRow{}, ErrProxy
	}
	return b.Base.GetUserForLogin(ctx, id)
}

// ── GetUserForPasswordReset ──────────────────────────────────────────────────

func (b *QuerierProxy) GetUserForPasswordReset(ctx context.Context, email pgtype.Text) (db.GetUserForPasswordResetRow, error) {
	if b.FailGetUserForPasswordReset {
		return db.GetUserForPasswordResetRow{}, ErrProxy
	}
	return b.Base.GetUserForPasswordReset(ctx, email)
}

// ── GetUserForResend ─────────────────────────────────────────────────────────

func (b *QuerierProxy) GetUserForResend(ctx context.Context, email pgtype.Text) (db.GetUserForResendRow, error) {
	if b.FailGetUserForResend {
		return db.GetUserForResendRow{}, ErrProxy
	}
	return b.Base.GetUserForResend(ctx, email)
}

// ── GetUserForUnlock ─────────────────────────────────────────────────────────

func (b *QuerierProxy) GetUserForUnlock(ctx context.Context, email pgtype.Text) (db.GetUserForUnlockRow, error) {
	if b.FailGetUserForUnlock {
		return db.GetUserForUnlockRow{}, ErrProxy
	}
	return b.Base.GetUserForUnlock(ctx, email)
}

// ── GetUserPasswordHash ──────────────────────────────────────────────────────

func (b *QuerierProxy) GetUserPasswordHash(ctx context.Context, userID pgtype.UUID) (db.GetUserPasswordHashRow, error) {
	if b.FailGetUserPasswordHash {
		return db.GetUserPasswordHashRow{}, ErrProxy
	}
	return b.Base.GetUserPasswordHash(ctx, userID)
}

// ── GetUserProfile ───────────────────────────────────────────────────────────

func (b *QuerierProxy) GetUserProfile(ctx context.Context, userID pgtype.UUID) (db.GetUserProfileRow, error) {
	if b.FailGetUserProfile {
		return db.GetUserProfileRow{}, ErrProxy
	}
	return b.Base.GetUserProfile(ctx, userID)
}

// ── GetUserVerifiedAndLocked ─────────────────────────────────────────────────

func (b *QuerierProxy) GetUserVerifiedAndLocked(ctx context.Context, userID pgtype.UUID) (db.GetUserVerifiedAndLockedRow, error) {
	if b.FailGetUserVerifiedAndLocked {
		return db.GetUserVerifiedAndLockedRow{}, ErrProxy
	}
	return b.Base.GetUserVerifiedAndLocked(ctx, userID)
}

// ── IncrementLoginFailures ───────────────────────────────────────────────────

func (b *QuerierProxy) IncrementLoginFailures(ctx context.Context, userID pgtype.UUID) (db.IncrementLoginFailuresRow, error) {
	if b.FailIncrementLoginFailures {
		return db.IncrementLoginFailuresRow{}, ErrProxy
	}
	return b.Base.IncrementLoginFailures(ctx, userID)
}

// ── IncrementVerificationAttempts ───────────────────────────────────────────

func (b *QuerierProxy) IncrementVerificationAttempts(ctx context.Context, id pgtype.UUID) (int16, error) {
	if b.FailIncrementVerificationAttempts {
		return 0, ErrProxy
	}
	return b.Base.IncrementVerificationAttempts(ctx, id)
}

// ── InsertAuditLog ───────────────────────────────────────────────────────────

// InsertAuditLog delegates to Base unless FailInsertAuditLog is true.
// When InsertAuditLogFailOnCall is non-zero, failure is injected only on
// the matching call number; otherwise every call fails.
func (b *QuerierProxy) InsertAuditLog(ctx context.Context, arg db.InsertAuditLogParams) error {
	b.InsertAuditLogCallCount++
	if b.FailInsertAuditLog {
		if b.InsertAuditLogFailOnCall == 0 || b.InsertAuditLogCallCount == b.InsertAuditLogFailOnCall {
			return ErrProxy
		}
	}
	return b.Base.InsertAuditLog(ctx, arg)
}

// ── InvalidateAllUserPasswordResetTokens ─────────────────────────────────────

func (b *QuerierProxy) InvalidateAllUserPasswordResetTokens(ctx context.Context, userID pgtype.UUID) error {
	if b.FailInvalidateAllUserPasswordResetTokens {
		return ErrProxy
	}
	return b.Base.InvalidateAllUserPasswordResetTokens(ctx, userID)
}

// ── InvalidateAllUserTokens ──────────────────────────────────────────────────

func (b *QuerierProxy) InvalidateAllUserTokens(ctx context.Context, userID pgtype.UUID) error {
	if b.FailInvalidateAllUserTokens {
		return ErrProxy
	}
	return b.Base.InvalidateAllUserTokens(ctx, userID)
}

// ── LockAccount ──────────────────────────────────────────────────────────────

func (b *QuerierProxy) LockAccount(ctx context.Context, userID pgtype.UUID) (int64, error) {
	if b.FailLockAccount {
		return 0, ErrProxy
	}
	return b.Base.LockAccount(ctx, userID)
}

// ── MarkEmailVerified ────────────────────────────────────────────────────────

func (b *QuerierProxy) MarkEmailVerified(ctx context.Context, userID pgtype.UUID) (int64, error) {
	if b.MarkEmailVerifiedZero {
		return 0, nil
	}
	if b.FailMarkEmailVerified {
		return 0, ErrProxy
	}
	return b.Base.MarkEmailVerified(ctx, userID)
}

// ── ResetLoginFailures ───────────────────────────────────────────────────────

func (b *QuerierProxy) ResetLoginFailures(ctx context.Context, userID pgtype.UUID) error {
	if b.FailResetLoginFailures {
		return ErrProxy
	}
	return b.Base.ResetLoginFailures(ctx, userID)
}

// ── RevokeAllUserRefreshTokens ───────────────────────────────────────────────

func (b *QuerierProxy) RevokeAllUserRefreshTokens(ctx context.Context, arg db.RevokeAllUserRefreshTokensParams) error {
	if b.FailRevokeAllUserRefreshTokens {
		return ErrProxy
	}
	return b.Base.RevokeAllUserRefreshTokens(ctx, arg)
}

// ── RevokeFamilyRefreshTokens ────────────────────────────────────────────────

func (b *QuerierProxy) RevokeFamilyRefreshTokens(ctx context.Context, arg db.RevokeFamilyRefreshTokensParams) error {
	if b.FailRevokeFamilyRefreshTokens {
		return ErrProxy
	}
	return b.Base.RevokeFamilyRefreshTokens(ctx, arg)
}

// ── RevokePreVerificationTokens ──────────────────────────────────────────────

func (b *QuerierProxy) RevokePreVerificationTokens(ctx context.Context, userID pgtype.UUID) error {
	if b.FailRevokePreVerificationTokens {
		return ErrProxy
	}
	return b.Base.RevokePreVerificationTokens(ctx, userID)
}

// ── RevokeRefreshTokenByJTI ──────────────────────────────────────────────────

func (b *QuerierProxy) RevokeRefreshTokenByJTI(ctx context.Context, arg db.RevokeRefreshTokenByJTIParams) (pgconn.CommandTag, error) {
	if b.FailRevokeRefreshTokenByJTI {
		return pgconn.CommandTag{}, ErrProxy
	}
	return b.Base.RevokeRefreshTokenByJTI(ctx, arg)
}

// ── RevokeSessionRefreshTokens ───────────────────────────────────────────────

func (b *QuerierProxy) RevokeSessionRefreshTokens(ctx context.Context, sessionID pgtype.UUID) error {
	if b.FailRevokeSessionRefreshTokens {
		return ErrProxy
	}
	return b.Base.RevokeSessionRefreshTokens(ctx, sessionID)
}

// ── UpdateUserProfile ─────────────────────────────────────────────────────────

func (b *QuerierProxy) UpdateUserProfile(ctx context.Context, arg db.UpdateUserProfileParams) error {
	if b.FailUpdateUserProfile {
		return ErrProxy
	}
	return b.Base.UpdateUserProfile(ctx, arg)
}

// ── UnlockAccount ────────────────────────────────────────────────────────────

func (b *QuerierProxy) UnlockAccount(ctx context.Context, userID pgtype.UUID) error {
	if b.FailUnlockAccount {
		return ErrProxy
	}
	return b.Base.UnlockAccount(ctx, userID)
}

// ── UpdateLastLoginAt ────────────────────────────────────────────────────────

func (b *QuerierProxy) UpdateLastLoginAt(ctx context.Context, userID pgtype.UUID) error {
	if b.FailUpdateLastLoginAt {
		return ErrProxy
	}
	return b.Base.UpdateLastLoginAt(ctx, userID)
}

// ── UpdatePasswordHash ───────────────────────────────────────────────────────

func (b *QuerierProxy) UpdatePasswordHash(ctx context.Context, arg db.UpdatePasswordHashParams) error {
	if b.FailUpdatePasswordHash {
		return ErrProxy
	}
	return b.Base.UpdatePasswordHash(ctx, arg)
}

// ── UpdateSessionLastActive ──────────────────────────────────────────────────

func (b *QuerierProxy) IncrementChangePasswordFailures(ctx context.Context, userID pgtype.UUID) (int16, error) {
	if b.FailIncrementChangePasswordFailures {
		return 0, ErrProxy
	}
	return b.Base.IncrementChangePasswordFailures(ctx, userID)
}

// ── ResetChangePasswordFailures ──────────────────────────────────────────────

func (b *QuerierProxy) ResetChangePasswordFailures(ctx context.Context, userID pgtype.UUID) error {
	if b.FailResetChangePasswordFailures {
		return ErrProxy
	}
	return b.Base.ResetChangePasswordFailures(ctx, userID)
}

// ── UpdateSessionLastActive ──────────────────────────────────────────────────

func (b *QuerierProxy) UpdateSessionLastActive(ctx context.Context, id pgtype.UUID) error {
	if b.FailUpdateSessionLastActive {
		return ErrProxy
	}
	return b.Base.UpdateSessionLastActive(ctx, id)
}

// ── GetUserForSetPassword ──────────────────────────────────────────────────────

func (b *QuerierProxy) GetUserForSetPassword(ctx context.Context, userID pgtype.UUID) (db.GetUserForSetPasswordRow, error) {
	if b.FailGetUserForSetPassword {
		return db.GetUserForSetPasswordRow{}, ErrProxy
	}
	return b.Base.GetUserForSetPassword(ctx, userID)
}

// ── SetPasswordHash ───────────────────────────────────────────────────────────

func (b *QuerierProxy) SetPasswordHash(ctx context.Context, arg db.SetPasswordHashParams) (int64, error) {
	if b.FailSetPasswordHash {
		return 0, ErrProxy
	}
	return b.Base.SetPasswordHash(ctx, arg)
}
