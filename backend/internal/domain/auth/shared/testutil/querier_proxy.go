// Package authsharedtest provides test-only helpers shared across all auth
// feature sub-packages. It must never be imported by production code.
package authsharedtest

import (
	"context"
	"errors"
	"time"

	"github.com/7-Dany/store/backend/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrProxy is the sentinel error returned by any QuerierProxy when a Fail* flag is set.
var ErrProxy = errors.New("querier_proxy: injected error")

// compile-time check that *QuerierProxy satisfies db.Querier.
var _ db.Querier = (*QuerierProxy)(nil)

// QuerierProxy wraps db.Querier with per-method failure injection.
//
// How it works: db.Querier is embedded directly, so every method that is NOT
// explicitly overridden here is automatically forwarded to the underlying
// implementation (set via NewQuerierProxy). Only methods that have a
// corresponding Fail* flag need an explicit override.
//
// Adding a new query to db.Querier (e.g. via `make sqlc`) requires NO change
// here — the new method is forwarded automatically. Fail* flags are only added
// when a specific test needs to inject a failure for that query.
type QuerierProxy struct {
	db.Querier // embedded — auto-forwards any method not explicitly overridden below

	// ── InsertAuditLog ───────────────────────────────────────────────────────
	FailInsertAuditLog       bool
	// InsertAuditLogFailOnCall, when non-zero, causes InsertAuditLog to fail only
	// on the Nth call (1-based). When zero and FailInsertAuditLog is true, every
	// call fails.
	InsertAuditLogFailOnCall int
	// InsertAuditLogCallCount counts how many times InsertAuditLog has been called.
	// Updated regardless of FailInsertAuditLog.
	InsertAuditLogCallCount int

	// ── Shared flags ─────────────────────────────────────────────────────────
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
	ConsumePasswordResetTokenZero            bool
	FailGetUserPasswordHash                  bool
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
	FailGetUserForUnlock       bool
	FailGetUnlockToken         bool
	FailHasConsumedUnlockToken bool
	FailConsumeUnlockToken     bool
	ConsumeUnlockTokenZero     bool
	FailCreateUnlockToken      bool
	FailUnlockAccount          bool

	// ── set-password ─────────────────────────────────────────────────────────
	FailGetUserForSetPassword bool
	FailSetPasswordHash       bool

	// ── username ─────────────────────────────────────────────────────────────
	FailCheckUsernameAvailable   bool
	FailGetUserForUsernameUpdate bool
	FailSetUsername              bool

	// ── email change ─────────────────────────────────────────────────────────
	FailCheckEmailAvailableForChange             bool
	FailGetLatestEmailChangeVerifyTokenCreatedAt bool
	FailInvalidateUserEmailChangeVerifyTokens    bool
	FailCreateEmailChangeVerifyToken             bool
	FailGetEmailChangeVerifyToken                bool
	FailConsumeEmailChangeToken                  bool
	ConsumeEmailChangeTokenZero                  bool
	FailInvalidateUserEmailChangeConfirmTokens   bool
	FailCreateEmailChangeConfirmToken            bool
	FailGetEmailChangeConfirmToken               bool
	FailGetUserForEmailChangeTx                  bool
	FailSetUserEmail                             bool
	SetUserEmailZero                             bool

	// ── verification ─────────────────────────────────────────────────────────
	FailGetEmailVerificationToken     bool
	FailConsumeEmailVerificationToken bool
	// ConsumeEmailVerificationTokenZero causes ConsumeEmailVerificationToken to
	// return (0, nil), simulating a concurrent consume (ErrTokenAlreadyUsed path).
	// Checked before FailConsumeEmailVerificationToken.
	ConsumeEmailVerificationTokenZero bool
	FailRevokePreVerificationTokens   bool
	FailMarkEmailVerified             bool
	// MarkEmailVerifiedZero causes MarkEmailVerified to return (0, nil), simulating
	// a row already verified/locked so VerifyEmailTx enters its GetUserVerifiedAndLocked
	// fallback. Checked before FailMarkEmailVerified.
	MarkEmailVerifiedZero                   bool
	FailGetUserVerifiedAndLocked            bool
	FailGetUserForResend                    bool
	FailGetLatestVerificationTokenCreatedAt bool
	FailInvalidateAllUserTokens             bool

	// ── delete account ────────────────────────────────────────────────────────
	FailGetUserForDeletion                bool
	FailGetUserAuthMethods                bool
	FailGetIdentityByUserAndProvider      bool
	FailScheduleUserDeletion              bool
	ScheduleUserDeletionZero         bool // returns (nil, pgx.ErrNoRows) simulating not-found / already-pending race
	FailCancelUserDeletion           bool
	CancelUserDeletionZero           bool // returns (0, nil) simulating not-pending
	FailInvalidateUserDeletionTokens bool
	FailCreateAccountDeletionToken   bool
	FailGetAccountDeletionToken      bool
	FailConsumeAccountDeletionToken  bool
	ConsumeAccountDeletionTokenZero  bool // returns (0, nil) simulating already-used
}

// NewQuerierProxy constructs a QuerierProxy backed by base.
func NewQuerierProxy(base db.Querier) *QuerierProxy {
	return &QuerierProxy{Querier: base}
}

// ── ConsumeEmailVerificationToken ────────────────────────────────────────────

func (b *QuerierProxy) ConsumeEmailVerificationToken(ctx context.Context, id pgtype.UUID) (int64, error) {
	if b.ConsumeEmailVerificationTokenZero {
		return 0, nil
	}
	if b.FailConsumeEmailVerificationToken {
		return 0, ErrProxy
	}
	return b.Querier.ConsumeEmailVerificationToken(ctx, id)
}

// ── ConsumePasswordResetToken ────────────────────────────────────────────────

func (b *QuerierProxy) ConsumePasswordResetToken(ctx context.Context, id pgtype.UUID) (int64, error) {
	if b.ConsumePasswordResetTokenZero {
		return 0, nil
	}
	if b.FailConsumePasswordResetToken {
		return 0, ErrProxy
	}
	return b.Querier.ConsumePasswordResetToken(ctx, id)
}

// ── ConsumeUnlockToken ───────────────────────────────────────────────────────

func (b *QuerierProxy) ConsumeUnlockToken(ctx context.Context, id pgtype.UUID) (int64, error) {
	if b.ConsumeUnlockTokenZero {
		return 0, nil
	}
	if b.FailConsumeUnlockToken {
		return 0, ErrProxy
	}
	return b.Querier.ConsumeUnlockToken(ctx, id)
}

// ── CreateEmailVerificationToken ─────────────────────────────────────────────

func (b *QuerierProxy) CreateEmailVerificationToken(ctx context.Context, arg db.CreateEmailVerificationTokenParams) (db.CreateEmailVerificationTokenRow, error) {
	if b.FailCreateEmailVerificationToken {
		return db.CreateEmailVerificationTokenRow{}, ErrProxy
	}
	return b.Querier.CreateEmailVerificationToken(ctx, arg)
}

// ── CreatePasswordResetToken ─────────────────────────────────────────────────

func (b *QuerierProxy) CreatePasswordResetToken(ctx context.Context, arg db.CreatePasswordResetTokenParams) (db.CreatePasswordResetTokenRow, error) {
	if b.FailCreatePasswordResetToken {
		return db.CreatePasswordResetTokenRow{}, ErrProxy
	}
	return b.Querier.CreatePasswordResetToken(ctx, arg)
}

// ── CreateRefreshToken ───────────────────────────────────────────────────────

func (b *QuerierProxy) CreateRefreshToken(ctx context.Context, arg db.CreateRefreshTokenParams) (db.CreateRefreshTokenRow, error) {
	if b.FailCreateRefreshToken {
		return db.CreateRefreshTokenRow{}, ErrProxy
	}
	return b.Querier.CreateRefreshToken(ctx, arg)
}

// ── CreateRotatedRefreshToken ────────────────────────────────────────────────

func (b *QuerierProxy) CreateRotatedRefreshToken(ctx context.Context, arg db.CreateRotatedRefreshTokenParams) (db.CreateRotatedRefreshTokenRow, error) {
	if b.FailCreateRotatedRefreshToken {
		return db.CreateRotatedRefreshTokenRow{}, ErrProxy
	}
	return b.Querier.CreateRotatedRefreshToken(ctx, arg)
}

// ── CreateUnlockToken ────────────────────────────────────────────────────────

func (b *QuerierProxy) CreateUnlockToken(ctx context.Context, arg db.CreateUnlockTokenParams) (db.CreateUnlockTokenRow, error) {
	if b.FailCreateUnlockToken {
		return db.CreateUnlockTokenRow{}, ErrProxy
	}
	return b.Querier.CreateUnlockToken(ctx, arg)
}

// ── CreateUser ───────────────────────────────────────────────────────────────

func (b *QuerierProxy) CreateUser(ctx context.Context, arg db.CreateUserParams) (db.CreateUserRow, error) {
	if b.FailCreateUser {
		return db.CreateUserRow{}, ErrProxy
	}
	return b.Querier.CreateUser(ctx, arg)
}

// ── CreateUserSession ────────────────────────────────────────────────────────

func (b *QuerierProxy) CreateUserSession(ctx context.Context, arg db.CreateUserSessionParams) (db.CreateUserSessionRow, error) {
	if b.FailCreateUserSession {
		return db.CreateUserSessionRow{}, ErrProxy
	}
	return b.Querier.CreateUserSession(ctx, arg)
}

// ── EndAllUserSessions ───────────────────────────────────────────────────────

func (b *QuerierProxy) EndAllUserSessions(ctx context.Context, userID pgtype.UUID) error {
	if b.FailEndAllUserSessions {
		return ErrProxy
	}
	return b.Querier.EndAllUserSessions(ctx, userID)
}

// ── EndUserSession ───────────────────────────────────────────────────────────

func (b *QuerierProxy) EndUserSession(ctx context.Context, id pgtype.UUID) error {
	if b.FailEndUserSession {
		return ErrProxy
	}
	return b.Querier.EndUserSession(ctx, id)
}

// ── GetActiveSessions ────────────────────────────────────────────────────────

func (b *QuerierProxy) GetActiveSessions(ctx context.Context, userID pgtype.UUID) ([]db.GetActiveSessionsRow, error) {
	if b.FailGetActiveSessions {
		return nil, ErrProxy
	}
	return b.Querier.GetActiveSessions(ctx, userID)
}

// ── GetEmailVerificationToken ────────────────────────────────────────────────

func (b *QuerierProxy) GetEmailVerificationToken(ctx context.Context, email string) (db.GetEmailVerificationTokenRow, error) {
	if b.FailGetEmailVerificationToken {
		return db.GetEmailVerificationTokenRow{}, ErrProxy
	}
	return b.Querier.GetEmailVerificationToken(ctx, email)
}

// ── GetLatestVerificationTokenCreatedAt ──────────────────────────────────────

func (b *QuerierProxy) GetLatestVerificationTokenCreatedAt(ctx context.Context, userID pgtype.UUID) (time.Time, error) {
	if b.FailGetLatestVerificationTokenCreatedAt {
		return time.Time{}, ErrProxy
	}
	return b.Querier.GetLatestVerificationTokenCreatedAt(ctx, userID)
}

// ── GetPasswordResetTokenCreatedAt ─────────────────────────────────────────

func (b *QuerierProxy) GetPasswordResetTokenCreatedAt(ctx context.Context, email string) (time.Time, error) {
	if b.FailGetPasswordResetTokenCreatedAt {
		return time.Time{}, ErrProxy
	}
	return b.Querier.GetPasswordResetTokenCreatedAt(ctx, email)
}

// ── GetPasswordResetToken ────────────────────────────────────────────────────

func (b *QuerierProxy) GetPasswordResetToken(ctx context.Context, email string) (db.GetPasswordResetTokenRow, error) {
	if b.FailGetPasswordResetToken {
		return db.GetPasswordResetTokenRow{}, ErrProxy
	}
	return b.Querier.GetPasswordResetToken(ctx, email)
}

// ── GetPasswordResetTokenForVerify ───────────────────────────────────────────

func (b *QuerierProxy) GetPasswordResetTokenForVerify(ctx context.Context, email string) (db.GetPasswordResetTokenForVerifyRow, error) {
	if b.FailGetPasswordResetTokenForVerify {
		return db.GetPasswordResetTokenForVerifyRow{}, ErrProxy
	}
	return b.Querier.GetPasswordResetTokenForVerify(ctx, email)
}

// ── GetRefreshTokenByJTI ─────────────────────────────────────────────────────

func (b *QuerierProxy) GetRefreshTokenByJTI(ctx context.Context, jti pgtype.UUID) (db.GetRefreshTokenByJTIRow, error) {
	if b.FailGetRefreshTokenByJTI {
		return db.GetRefreshTokenByJTIRow{}, ErrProxy
	}
	return b.Querier.GetRefreshTokenByJTI(ctx, jti)
}

// ── GetSessionByID ───────────────────────────────────────────────────────────

func (b *QuerierProxy) GetSessionByID(ctx context.Context, id pgtype.UUID) (db.GetSessionByIDRow, error) {
	if b.FailGetSessionByID {
		return db.GetSessionByIDRow{}, ErrProxy
	}
	return b.Querier.GetSessionByID(ctx, id)
}

// ── GetUnlockToken ───────────────────────────────────────────────────────────

func (b *QuerierProxy) GetUnlockToken(ctx context.Context, email string) (db.GetUnlockTokenRow, error) {
	if b.FailGetUnlockToken {
		return db.GetUnlockTokenRow{}, ErrProxy
	}
	return b.Querier.GetUnlockToken(ctx, email)
}

// ── HasConsumedUnlockToken ───────────────────────────────────────────────────

func (b *QuerierProxy) HasConsumedUnlockToken(ctx context.Context, email string) (bool, error) {
	if b.FailHasConsumedUnlockToken {
		return false, ErrProxy
	}
	return b.Querier.HasConsumedUnlockToken(ctx, email)
}

// ── GetUserForLogin ──────────────────────────────────────────────────────────

func (b *QuerierProxy) GetUserForLogin(ctx context.Context, id pgtype.Text) (db.GetUserForLoginRow, error) {
	if b.FailGetUserForLogin {
		return db.GetUserForLoginRow{}, ErrProxy
	}
	return b.Querier.GetUserForLogin(ctx, id)
}

// ── GetUserForPasswordReset ──────────────────────────────────────────────────

func (b *QuerierProxy) GetUserForPasswordReset(ctx context.Context, email pgtype.Text) (db.GetUserForPasswordResetRow, error) {
	if b.FailGetUserForPasswordReset {
		return db.GetUserForPasswordResetRow{}, ErrProxy
	}
	return b.Querier.GetUserForPasswordReset(ctx, email)
}

// ── GetUserForResend ─────────────────────────────────────────────────────────

func (b *QuerierProxy) GetUserForResend(ctx context.Context, email pgtype.Text) (db.GetUserForResendRow, error) {
	if b.FailGetUserForResend {
		return db.GetUserForResendRow{}, ErrProxy
	}
	return b.Querier.GetUserForResend(ctx, email)
}

// ── GetUserForUnlock ─────────────────────────────────────────────────────────

func (b *QuerierProxy) GetUserForUnlock(ctx context.Context, email pgtype.Text) (db.GetUserForUnlockRow, error) {
	if b.FailGetUserForUnlock {
		return db.GetUserForUnlockRow{}, ErrProxy
	}
	return b.Querier.GetUserForUnlock(ctx, email)
}

// ── GetUserPasswordHash ──────────────────────────────────────────────────────

func (b *QuerierProxy) GetUserPasswordHash(ctx context.Context, userID pgtype.UUID) (db.GetUserPasswordHashRow, error) {
	if b.FailGetUserPasswordHash {
		return db.GetUserPasswordHashRow{}, ErrProxy
	}
	return b.Querier.GetUserPasswordHash(ctx, userID)
}

// ── GetUserProfile ───────────────────────────────────────────────────────────

func (b *QuerierProxy) GetUserProfile(ctx context.Context, userID pgtype.UUID) (db.GetUserProfileRow, error) {
	if b.FailGetUserProfile {
		return db.GetUserProfileRow{}, ErrProxy
	}
	return b.Querier.GetUserProfile(ctx, userID)
}

// ── GetUserVerifiedAndLocked ─────────────────────────────────────────────────

func (b *QuerierProxy) GetUserVerifiedAndLocked(ctx context.Context, userID pgtype.UUID) (db.GetUserVerifiedAndLockedRow, error) {
	if b.FailGetUserVerifiedAndLocked {
		return db.GetUserVerifiedAndLockedRow{}, ErrProxy
	}
	return b.Querier.GetUserVerifiedAndLocked(ctx, userID)
}

// ── IncrementLoginFailures ───────────────────────────────────────────────────

func (b *QuerierProxy) IncrementLoginFailures(ctx context.Context, userID pgtype.UUID) (db.IncrementLoginFailuresRow, error) {
	if b.FailIncrementLoginFailures {
		return db.IncrementLoginFailuresRow{}, ErrProxy
	}
	return b.Querier.IncrementLoginFailures(ctx, userID)
}

// ── IncrementVerificationAttempts ───────────────────────────────────────────

func (b *QuerierProxy) IncrementVerificationAttempts(ctx context.Context, id pgtype.UUID) (int16, error) {
	if b.FailIncrementVerificationAttempts {
		return 0, ErrProxy
	}
	return b.Querier.IncrementVerificationAttempts(ctx, id)
}

// ── InsertAuditLog ───────────────────────────────────────────────────────────

// InsertAuditLog delegates to the underlying Querier unless FailInsertAuditLog
// is true. When InsertAuditLogFailOnCall is non-zero, failure is injected only
// on the matching call number; otherwise every call fails.
func (b *QuerierProxy) InsertAuditLog(ctx context.Context, arg db.InsertAuditLogParams) error {
	b.InsertAuditLogCallCount++
	if b.FailInsertAuditLog {
		if b.InsertAuditLogFailOnCall == 0 || b.InsertAuditLogCallCount == b.InsertAuditLogFailOnCall {
			return ErrProxy
		}
	}
	return b.Querier.InsertAuditLog(ctx, arg)
}

// ── InvalidateAllUserPasswordResetTokens ─────────────────────────────────────

func (b *QuerierProxy) InvalidateAllUserPasswordResetTokens(ctx context.Context, userID pgtype.UUID) error {
	if b.FailInvalidateAllUserPasswordResetTokens {
		return ErrProxy
	}
	return b.Querier.InvalidateAllUserPasswordResetTokens(ctx, userID)
}

// ── InvalidateAllUserTokens ──────────────────────────────────────────────────

func (b *QuerierProxy) InvalidateAllUserTokens(ctx context.Context, userID pgtype.UUID) error {
	if b.FailInvalidateAllUserTokens {
		return ErrProxy
	}
	return b.Querier.InvalidateAllUserTokens(ctx, userID)
}

// ── LockAccount ──────────────────────────────────────────────────────────────

func (b *QuerierProxy) LockAccount(ctx context.Context, userID pgtype.UUID) (int64, error) {
	if b.FailLockAccount {
		return 0, ErrProxy
	}
	return b.Querier.LockAccount(ctx, userID)
}

// ── MarkEmailVerified ────────────────────────────────────────────────────────

func (b *QuerierProxy) MarkEmailVerified(ctx context.Context, userID pgtype.UUID) (int64, error) {
	if b.MarkEmailVerifiedZero {
		return 0, nil
	}
	if b.FailMarkEmailVerified {
		return 0, ErrProxy
	}
	return b.Querier.MarkEmailVerified(ctx, userID)
}

// ── ResetLoginFailures ───────────────────────────────────────────────────────

func (b *QuerierProxy) ResetLoginFailures(ctx context.Context, userID pgtype.UUID) error {
	if b.FailResetLoginFailures {
		return ErrProxy
	}
	return b.Querier.ResetLoginFailures(ctx, userID)
}

// ── RevokeAllUserRefreshTokens ───────────────────────────────────────────────

func (b *QuerierProxy) RevokeAllUserRefreshTokens(ctx context.Context, arg db.RevokeAllUserRefreshTokensParams) error {
	if b.FailRevokeAllUserRefreshTokens {
		return ErrProxy
	}
	return b.Querier.RevokeAllUserRefreshTokens(ctx, arg)
}

// ── RevokeFamilyRefreshTokens ────────────────────────────────────────────────

func (b *QuerierProxy) RevokeFamilyRefreshTokens(ctx context.Context, arg db.RevokeFamilyRefreshTokensParams) error {
	if b.FailRevokeFamilyRefreshTokens {
		return ErrProxy
	}
	return b.Querier.RevokeFamilyRefreshTokens(ctx, arg)
}

// ── RevokePreVerificationTokens ──────────────────────────────────────────────

func (b *QuerierProxy) RevokePreVerificationTokens(ctx context.Context, userID pgtype.UUID) error {
	if b.FailRevokePreVerificationTokens {
		return ErrProxy
	}
	return b.Querier.RevokePreVerificationTokens(ctx, userID)
}

// ── RevokeRefreshTokenByJTI ──────────────────────────────────────────────────

func (b *QuerierProxy) RevokeRefreshTokenByJTI(ctx context.Context, arg db.RevokeRefreshTokenByJTIParams) (pgconn.CommandTag, error) {
	if b.FailRevokeRefreshTokenByJTI {
		return pgconn.CommandTag{}, ErrProxy
	}
	return b.Querier.RevokeRefreshTokenByJTI(ctx, arg)
}

// ── RevokeSessionRefreshTokens ───────────────────────────────────────────────

func (b *QuerierProxy) RevokeSessionRefreshTokens(ctx context.Context, sessionID pgtype.UUID) error {
	if b.FailRevokeSessionRefreshTokens {
		return ErrProxy
	}
	return b.Querier.RevokeSessionRefreshTokens(ctx, sessionID)
}

// ── UpdateUserProfile ─────────────────────────────────────────────────────────

func (b *QuerierProxy) UpdateUserProfile(ctx context.Context, arg db.UpdateUserProfileParams) error {
	if b.FailUpdateUserProfile {
		return ErrProxy
	}
	return b.Querier.UpdateUserProfile(ctx, arg)
}

// ── UnlockAccount ────────────────────────────────────────────────────────────

func (b *QuerierProxy) UnlockAccount(ctx context.Context, userID pgtype.UUID) error {
	if b.FailUnlockAccount {
		return ErrProxy
	}
	return b.Querier.UnlockAccount(ctx, userID)
}

// ── UpdateLastLoginAt ────────────────────────────────────────────────────────

func (b *QuerierProxy) UpdateLastLoginAt(ctx context.Context, userID pgtype.UUID) error {
	if b.FailUpdateLastLoginAt {
		return ErrProxy
	}
	return b.Querier.UpdateLastLoginAt(ctx, userID)
}

// ── UpdatePasswordHash ───────────────────────────────────────────────────────

func (b *QuerierProxy) UpdatePasswordHash(ctx context.Context, arg db.UpdatePasswordHashParams) error {
	if b.FailUpdatePasswordHash {
		return ErrProxy
	}
	return b.Querier.UpdatePasswordHash(ctx, arg)
}

// ── IncrementChangePasswordFailures ──────────────────────────────────────────

func (b *QuerierProxy) IncrementChangePasswordFailures(ctx context.Context, userID pgtype.UUID) (int16, error) {
	if b.FailIncrementChangePasswordFailures {
		return 0, ErrProxy
	}
	return b.Querier.IncrementChangePasswordFailures(ctx, userID)
}

// ── ResetChangePasswordFailures ──────────────────────────────────────────────

func (b *QuerierProxy) ResetChangePasswordFailures(ctx context.Context, userID pgtype.UUID) error {
	if b.FailResetChangePasswordFailures {
		return ErrProxy
	}
	return b.Querier.ResetChangePasswordFailures(ctx, userID)
}

// ── UpdateSessionLastActive ──────────────────────────────────────────────────

func (b *QuerierProxy) UpdateSessionLastActive(ctx context.Context, id pgtype.UUID) error {
	if b.FailUpdateSessionLastActive {
		return ErrProxy
	}
	return b.Querier.UpdateSessionLastActive(ctx, id)
}

// ── GetUserForSetPassword ──────────────────────────────────────────────────────

func (b *QuerierProxy) GetUserForSetPassword(ctx context.Context, userID pgtype.UUID) (db.GetUserForSetPasswordRow, error) {
	if b.FailGetUserForSetPassword {
		return db.GetUserForSetPasswordRow{}, ErrProxy
	}
	return b.Querier.GetUserForSetPassword(ctx, userID)
}

// ── SetPasswordHash ───────────────────────────────────────────────────────────

func (b *QuerierProxy) SetPasswordHash(ctx context.Context, arg db.SetPasswordHashParams) (int64, error) {
	if b.FailSetPasswordHash {
		return 0, ErrProxy
	}
	return b.Querier.SetPasswordHash(ctx, arg)
}

// ── CheckUsernameAvailable ────────────────────────────────────────────────────

func (b *QuerierProxy) CheckUsernameAvailable(ctx context.Context, username pgtype.Text) (bool, error) {
	if b.FailCheckUsernameAvailable {
		return false, ErrProxy
	}
	return b.Querier.CheckUsernameAvailable(ctx, username)
}

// ── GetUserForUsernameUpdate ──────────────────────────────────────────────────

func (b *QuerierProxy) GetUserForUsernameUpdate(ctx context.Context, userID pgtype.UUID) (db.GetUserForUsernameUpdateRow, error) {
	if b.FailGetUserForUsernameUpdate {
		return db.GetUserForUsernameUpdateRow{}, ErrProxy
	}
	return b.Querier.GetUserForUsernameUpdate(ctx, userID)
}

// ── SetUsername ───────────────────────────────────────────────────────────────

func (b *QuerierProxy) SetUsername(ctx context.Context, arg db.SetUsernameParams) (int64, error) {
	if b.FailSetUsername {
		return 0, ErrProxy
	}
	return b.Querier.SetUsername(ctx, arg)
}

// ── Email change ─────────────────────────────────────────────────────────────

func (b *QuerierProxy) CheckEmailAvailableForChange(ctx context.Context, arg db.CheckEmailAvailableForChangeParams) (bool, error) {
	if b.FailCheckEmailAvailableForChange {
		return false, ErrProxy
	}
	return b.Querier.CheckEmailAvailableForChange(ctx, arg)
}

func (b *QuerierProxy) GetLatestEmailChangeVerifyTokenCreatedAt(ctx context.Context, userID pgtype.UUID) (time.Time, error) {
	if b.FailGetLatestEmailChangeVerifyTokenCreatedAt {
		return time.Time{}, ErrProxy
	}
	return b.Querier.GetLatestEmailChangeVerifyTokenCreatedAt(ctx, userID)
}

func (b *QuerierProxy) InvalidateUserEmailChangeVerifyTokens(ctx context.Context, userID pgtype.UUID) error {
	if b.FailInvalidateUserEmailChangeVerifyTokens {
		return ErrProxy
	}
	return b.Querier.InvalidateUserEmailChangeVerifyTokens(ctx, userID)
}

func (b *QuerierProxy) CreateEmailChangeVerifyToken(ctx context.Context, arg db.CreateEmailChangeVerifyTokenParams) (db.CreateEmailChangeVerifyTokenRow, error) {
	if b.FailCreateEmailChangeVerifyToken {
		return db.CreateEmailChangeVerifyTokenRow{}, ErrProxy
	}
	return b.Querier.CreateEmailChangeVerifyToken(ctx, arg)
}

func (b *QuerierProxy) GetEmailChangeVerifyToken(ctx context.Context, userID pgtype.UUID) (db.GetEmailChangeVerifyTokenRow, error) {
	if b.FailGetEmailChangeVerifyToken {
		return db.GetEmailChangeVerifyTokenRow{}, ErrProxy
	}
	return b.Querier.GetEmailChangeVerifyToken(ctx, userID)
}

func (b *QuerierProxy) ConsumeEmailChangeToken(ctx context.Context, id pgtype.UUID) (int64, error) {
	if b.ConsumeEmailChangeTokenZero {
		return 0, nil
	}
	if b.FailConsumeEmailChangeToken {
		return 0, ErrProxy
	}
	return b.Querier.ConsumeEmailChangeToken(ctx, id)
}

func (b *QuerierProxy) InvalidateUserEmailChangeConfirmTokens(ctx context.Context, userID pgtype.UUID) error {
	if b.FailInvalidateUserEmailChangeConfirmTokens {
		return ErrProxy
	}
	return b.Querier.InvalidateUserEmailChangeConfirmTokens(ctx, userID)
}

func (b *QuerierProxy) CreateEmailChangeConfirmToken(ctx context.Context, arg db.CreateEmailChangeConfirmTokenParams) (db.CreateEmailChangeConfirmTokenRow, error) {
	if b.FailCreateEmailChangeConfirmToken {
		return db.CreateEmailChangeConfirmTokenRow{}, ErrProxy
	}
	return b.Querier.CreateEmailChangeConfirmToken(ctx, arg)
}

func (b *QuerierProxy) GetEmailChangeConfirmToken(ctx context.Context, userID pgtype.UUID) (db.GetEmailChangeConfirmTokenRow, error) {
	if b.FailGetEmailChangeConfirmToken {
		return db.GetEmailChangeConfirmTokenRow{}, ErrProxy
	}
	return b.Querier.GetEmailChangeConfirmToken(ctx, userID)
}

func (b *QuerierProxy) GetUserForEmailChangeTx(ctx context.Context, userID pgtype.UUID) (db.GetUserForEmailChangeTxRow, error) {
	if b.FailGetUserForEmailChangeTx {
		return db.GetUserForEmailChangeTxRow{}, ErrProxy
	}
	return b.Querier.GetUserForEmailChangeTx(ctx, userID)
}

func (b *QuerierProxy) SetUserEmail(ctx context.Context, arg db.SetUserEmailParams) (int64, error) {
	if b.SetUserEmailZero {
		return 0, nil
	}
	if b.FailSetUserEmail {
		return 0, ErrProxy
	}
	return b.Querier.SetUserEmail(ctx, arg)
}

// ── delete account ────────────────────────────────────────────────────────────

func (b *QuerierProxy) GetUserForDeletion(ctx context.Context, userID pgtype.UUID) (db.GetUserForDeletionRow, error) {
	if b.FailGetUserForDeletion {
		return db.GetUserForDeletionRow{}, ErrProxy
	}
	return b.Querier.GetUserForDeletion(ctx, userID)
}

func (b *QuerierProxy) GetUserAuthMethods(ctx context.Context, userID pgtype.UUID) (db.GetUserAuthMethodsRow, error) {
	if b.FailGetUserAuthMethods {
		return db.GetUserAuthMethodsRow{}, ErrProxy
	}
	return b.Querier.GetUserAuthMethods(ctx, userID)
}

func (b *QuerierProxy) GetIdentityByUserAndProvider(ctx context.Context, arg db.GetIdentityByUserAndProviderParams) (db.GetIdentityByUserAndProviderRow, error) {
	if b.FailGetIdentityByUserAndProvider {
		return db.GetIdentityByUserAndProviderRow{}, ErrProxy
	}
	return b.Querier.GetIdentityByUserAndProvider(ctx, arg)
}

func (b *QuerierProxy) ScheduleUserDeletion(ctx context.Context, userID pgtype.UUID) (*time.Time, error) {
	if b.ScheduleUserDeletionZero {
		return nil, pgx.ErrNoRows
	}
	if b.FailScheduleUserDeletion {
		return nil, ErrProxy
	}
	return b.Querier.ScheduleUserDeletion(ctx, userID)
}

func (b *QuerierProxy) CancelUserDeletion(ctx context.Context, userID pgtype.UUID) (int64, error) {
	if b.CancelUserDeletionZero {
		return 0, nil
	}
	if b.FailCancelUserDeletion {
		return 0, ErrProxy
	}
	return b.Querier.CancelUserDeletion(ctx, userID)
}

func (b *QuerierProxy) InvalidateUserDeletionTokens(ctx context.Context, userID pgtype.UUID) error {
	if b.FailInvalidateUserDeletionTokens {
		return ErrProxy
	}
	return b.Querier.InvalidateUserDeletionTokens(ctx, userID)
}

func (b *QuerierProxy) CreateAccountDeletionToken(ctx context.Context, arg db.CreateAccountDeletionTokenParams) (db.CreateAccountDeletionTokenRow, error) {
	if b.FailCreateAccountDeletionToken {
		return db.CreateAccountDeletionTokenRow{}, ErrProxy
	}
	return b.Querier.CreateAccountDeletionToken(ctx, arg)
}

func (b *QuerierProxy) GetAccountDeletionToken(ctx context.Context, userID pgtype.UUID) (db.GetAccountDeletionTokenRow, error) {
	if b.FailGetAccountDeletionToken {
		return db.GetAccountDeletionTokenRow{}, ErrProxy
	}
	return b.Querier.GetAccountDeletionToken(ctx, userID)
}

func (b *QuerierProxy) ConsumeAccountDeletionToken(ctx context.Context, id pgtype.UUID) (int64, error) {
	if b.ConsumeAccountDeletionTokenZero {
		return 0, nil
	}
	if b.FailConsumeAccountDeletionToken {
		return 0, ErrProxy
	}
	return b.Querier.ConsumeAccountDeletionToken(ctx, id)
}
