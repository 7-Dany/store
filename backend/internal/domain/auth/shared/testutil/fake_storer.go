// Package authsharedtest provides test-only helpers shared across all auth
// feature sub-packages. It must never be imported by production code.
package authsharedtest

import (
	"context"
	"time"

	"github.com/7-Dany/store/backend/internal/domain/auth/login"
	"github.com/7-Dany/store/backend/internal/domain/auth/password"
	"github.com/7-Dany/store/backend/internal/domain/auth/register"
	"github.com/7-Dany/store/backend/internal/domain/auth/session"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/7-Dany/store/backend/internal/domain/auth/unlock"
	"github.com/7-Dany/store/backend/internal/domain/auth/verification"
	me "github.com/7-Dany/store/backend/internal/domain/profile/me"
	profilesession "github.com/7-Dany/store/backend/internal/domain/profile/session"
	setpassword "github.com/7-Dany/store/backend/internal/domain/profile/set-password"
	username "github.com/7-Dany/store/backend/internal/domain/profile/username"
)

// ─────────────────────────────────────────────────────────────────────────────
// LoginFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// LoginFakeStorer is a hand-written implementation of login.Storer for service
// unit tests. Each method delegates to its Fn field if non-nil, otherwise
// returns the zero value and nil error so tests only need to configure the
// fields they care about.
type LoginFakeStorer struct {
	GetUserForLoginFn          func(ctx context.Context, identifier string) (login.LoginUser, error)
	LoginTxFn                  func(ctx context.Context, in login.LoginTxInput) (login.LoggedInSession, error)
	IncrementLoginFailuresTxFn func(ctx context.Context, userID [16]byte, ipAddress, userAgent string) error
	ResetLoginFailuresTxFn     func(ctx context.Context, userID [16]byte) error
	WriteLoginFailedAuditTxFn  func(ctx context.Context, userID [16]byte, reason, ipAddress, userAgent string) error
}

// compile-time interface check.
var _ login.Storer = (*LoginFakeStorer)(nil)

func (f *LoginFakeStorer) GetUserForLogin(ctx context.Context, identifier string) (login.LoginUser, error) {
	if f.GetUserForLoginFn != nil {
		return f.GetUserForLoginFn(ctx, identifier)
	}
	return login.LoginUser{}, nil
}

func (f *LoginFakeStorer) LoginTx(ctx context.Context, in login.LoginTxInput) (login.LoggedInSession, error) {
	if f.LoginTxFn != nil {
		return f.LoginTxFn(ctx, in)
	}
	return login.LoggedInSession{}, nil
}

func (f *LoginFakeStorer) IncrementLoginFailuresTx(ctx context.Context, userID [16]byte, ipAddress, userAgent string) error {
	if f.IncrementLoginFailuresTxFn != nil {
		return f.IncrementLoginFailuresTxFn(ctx, userID, ipAddress, userAgent)
	}
	return nil
}

func (f *LoginFakeStorer) ResetLoginFailuresTx(ctx context.Context, userID [16]byte) error {
	if f.ResetLoginFailuresTxFn != nil {
		return f.ResetLoginFailuresTxFn(ctx, userID)
	}
	return nil
}

func (f *LoginFakeStorer) WriteLoginFailedAuditTx(ctx context.Context, userID [16]byte, reason, ipAddress, userAgent string) error {
	if f.WriteLoginFailedAuditTxFn != nil {
		return f.WriteLoginFailedAuditTxFn(ctx, userID, reason, ipAddress, userAgent)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// PasswordFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// PasswordFakeStorer is a hand-written implementation of password.Storer for
// service unit tests. Each method delegates to its Fn field if non-nil;
// otherwise it returns the zero value and nil error so tests only configure the
// fields they need.
type PasswordFakeStorer struct {
	GetUserForPasswordResetFn           func(ctx context.Context, email string) (password.GetUserForPasswordResetResult, error)
	GetPasswordResetTokenForVerifyFn    func(ctx context.Context, email string) (authshared.VerificationToken, error)
	GetPasswordResetTokenCreatedAtFn    func(ctx context.Context, email string) (time.Time, error)
	RequestPasswordResetTxFn            func(ctx context.Context, in password.RequestPasswordResetStoreInput) error
	ConsumeAndUpdatePasswordTxFn        func(ctx context.Context, in password.ConsumeAndUpdateInput, checkFn func(authshared.VerificationToken) error) ([16]byte, error)
	IncrementAttemptsTxFn               func(ctx context.Context, in authshared.IncrementInput) error
	GetUserPasswordHashFn               func(ctx context.Context, userID [16]byte) (password.CurrentCredentials, error)
	UpdatePasswordHashTxFn              func(ctx context.Context, userID [16]byte, newHash, ipAddress, userAgent string) error
	IncrementChangePasswordFailuresTxFn func(ctx context.Context, userID [16]byte, ipAddress, userAgent string) (int16, error)
	ResetChangePasswordFailuresTxFn     func(ctx context.Context, userID [16]byte) error
	WritePasswordChangeFailedAuditTxFn  func(ctx context.Context, userID [16]byte, ipAddress, userAgent string) error
}

// compile-time interface check.
var _ password.Storer = (*PasswordFakeStorer)(nil)

// GetPasswordResetTokenForVerify delegates to GetPasswordResetTokenForVerifyFn if set.
func (f *PasswordFakeStorer) GetPasswordResetTokenForVerify(ctx context.Context, email string) (authshared.VerificationToken, error) {
	if f.GetPasswordResetTokenForVerifyFn != nil {
		return f.GetPasswordResetTokenForVerifyFn(ctx, email)
	}
	return authshared.VerificationToken{}, authshared.ErrTokenNotFound
}

func (f *PasswordFakeStorer) GetUserForPasswordReset(ctx context.Context, email string) (password.GetUserForPasswordResetResult, error) {
	if f.GetUserForPasswordResetFn != nil {
		return f.GetUserForPasswordResetFn(ctx, email)
	}
	return password.GetUserForPasswordResetResult{}, nil
}

func (f *PasswordFakeStorer) GetPasswordResetTokenCreatedAt(ctx context.Context, email string) (time.Time, error) {
	if f.GetPasswordResetTokenCreatedAtFn != nil {
		return f.GetPasswordResetTokenCreatedAtFn(ctx, email)
	}
	return time.Time{}, authshared.ErrTokenNotFound
}

func (f *PasswordFakeStorer) RequestPasswordResetTx(ctx context.Context, in password.RequestPasswordResetStoreInput) error {
	if f.RequestPasswordResetTxFn != nil {
		return f.RequestPasswordResetTxFn(ctx, in)
	}
	return nil
}

func (f *PasswordFakeStorer) ConsumeAndUpdatePasswordTx(ctx context.Context, in password.ConsumeAndUpdateInput, checkFn func(authshared.VerificationToken) error) ([16]byte, error) {
	if f.ConsumeAndUpdatePasswordTxFn != nil {
		return f.ConsumeAndUpdatePasswordTxFn(ctx, in, checkFn)
	}
	return [16]byte{}, nil
}

func (f *PasswordFakeStorer) IncrementAttemptsTx(ctx context.Context, in authshared.IncrementInput) error {
	if f.IncrementAttemptsTxFn != nil {
		return f.IncrementAttemptsTxFn(ctx, in)
	}
	return nil
}

// GetUserPasswordHash delegates to GetUserPasswordHashFn if set.
func (f *PasswordFakeStorer) GetUserPasswordHash(ctx context.Context, userID [16]byte) (password.CurrentCredentials, error) {
	if f.GetUserPasswordHashFn != nil {
		return f.GetUserPasswordHashFn(ctx, userID)
	}
	return password.CurrentCredentials{}, nil
}

// UpdatePasswordHashTx delegates to UpdatePasswordHashTxFn if set.
func (f *PasswordFakeStorer) UpdatePasswordHashTx(ctx context.Context, userID [16]byte, newHash, ipAddress, userAgent string) error {
	if f.UpdatePasswordHashTxFn != nil {
		return f.UpdatePasswordHashTxFn(ctx, userID, newHash, ipAddress, userAgent)
	}
	return nil
}

// IncrementChangePasswordFailuresTx delegates to IncrementChangePasswordFailuresTxFn if set.
// Default: returns (0, nil) so tests that don't configure it never trip the
// ErrTooManyAttempts threshold (changePasswordMaxAttempts == 5).
func (f *PasswordFakeStorer) IncrementChangePasswordFailuresTx(ctx context.Context, userID [16]byte, ipAddress, userAgent string) (int16, error) {
	if f.IncrementChangePasswordFailuresTxFn != nil {
		return f.IncrementChangePasswordFailuresTxFn(ctx, userID, ipAddress, userAgent)
	}
	return 0, nil
}

// ResetChangePasswordFailuresTx delegates to ResetChangePasswordFailuresTxFn if set.
func (f *PasswordFakeStorer) ResetChangePasswordFailuresTx(ctx context.Context, userID [16]byte) error {
	if f.ResetChangePasswordFailuresTxFn != nil {
		return f.ResetChangePasswordFailuresTxFn(ctx, userID)
	}
	return nil
}

// WritePasswordChangeFailedAuditTx delegates to WritePasswordChangeFailedAuditTxFn if set.
func (f *PasswordFakeStorer) WritePasswordChangeFailedAuditTx(ctx context.Context, userID [16]byte, ipAddress, userAgent string) error {
	if f.WritePasswordChangeFailedAuditTxFn != nil {
		return f.WritePasswordChangeFailedAuditTxFn(ctx, userID, ipAddress, userAgent)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// MeFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// MeFakeStorer is a hand-written implementation of me.Storer for service unit
// tests. Each method delegates to its Fn field if non-nil, otherwise returns
// the zero value and nil error so tests only need to configure the fields they
// care about.
type MeFakeStorer struct {
	GetUserProfileFn  func(ctx context.Context, userID [16]byte) (me.UserProfile, error)
	UpdateProfileTxFn func(ctx context.Context, in me.UpdateProfileInput) error
}

// compile-time interface check.
var _ me.Storer = (*MeFakeStorer)(nil)

// GetUserProfile delegates to GetUserProfileFn if set.
func (f *MeFakeStorer) GetUserProfile(ctx context.Context, userID [16]byte) (me.UserProfile, error) {
	if f.GetUserProfileFn != nil {
		return f.GetUserProfileFn(ctx, userID)
	}
	return me.UserProfile{}, nil
}

// UpdateProfileTx delegates to UpdateProfileTxFn if set.
func (f *MeFakeStorer) UpdateProfileTx(ctx context.Context, in me.UpdateProfileInput) error {
	if f.UpdateProfileTxFn != nil {
		return f.UpdateProfileTxFn(ctx, in)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// RegisterFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// RegisterFakeStorer is a hand-written implementation of register.Storer for
// service unit tests. Each method delegates to its Fn field if non-nil;
// otherwise it returns the zero value and nil error so tests only configure the
// fields they need.
type RegisterFakeStorer struct {
	CreateUserTxFn               func(ctx context.Context, in register.CreateUserInput) (register.CreatedUser, error)
	WriteRegisterFailedAuditTxFn func(ctx context.Context, userID [16]byte, ipAddress, userAgent string) error
}

// compile-time interface check.
var _ register.Storer = (*RegisterFakeStorer)(nil)

func (f *RegisterFakeStorer) CreateUserTx(ctx context.Context, in register.CreateUserInput) (register.CreatedUser, error) {
	if f.CreateUserTxFn != nil {
		return f.CreateUserTxFn(ctx, in)
	}
	return register.CreatedUser{}, nil
}

func (f *RegisterFakeStorer) WriteRegisterFailedAuditTx(ctx context.Context, userID [16]byte, ipAddress, userAgent string) error {
	if f.WriteRegisterFailedAuditTxFn != nil {
		return f.WriteRegisterFailedAuditTxFn(ctx, userID, ipAddress, userAgent)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// SessionFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// SessionFakeStorer is a hand-written implementation of session.Storer for
// service unit tests. Each method delegates to its Fn field if non-nil;
// otherwise it returns the zero value and nil error so tests only configure the
// fields they need.
type SessionFakeStorer struct {
	GetRefreshTokenByJTIFn      func(ctx context.Context, jti [16]byte) (session.StoredRefreshToken, error)
	GetUserVerifiedAndLockedFn  func(ctx context.Context, userID [16]byte) (session.UserStatusResult, error)
	RotateRefreshTokenTxFn      func(ctx context.Context, in session.RotateTxInput) (session.RotatedSession, error)
	RevokeFamilyTokensTxFn      func(ctx context.Context, userID, familyID [16]byte, reason string) error
	RevokeAllUserTokensTxFn     func(ctx context.Context, userID [16]byte, reason, ipAddress, userAgent string) error
	LogoutTxFn                  func(ctx context.Context, in session.LogoutTxInput) error
	WriteRefreshFailedAuditTxFn func(ctx context.Context, ipAddress, userAgent string) error
}

// compile-time interface check.
var _ session.Storer = (*SessionFakeStorer)(nil)

func (f *SessionFakeStorer) GetRefreshTokenByJTI(ctx context.Context, jti [16]byte) (session.StoredRefreshToken, error) {
	if f.GetRefreshTokenByJTIFn != nil {
		return f.GetRefreshTokenByJTIFn(ctx, jti)
	}
	return session.StoredRefreshToken{}, nil
}

func (f *SessionFakeStorer) GetUserVerifiedAndLocked(ctx context.Context, userID [16]byte) (session.UserStatusResult, error) {
	if f.GetUserVerifiedAndLockedFn != nil {
		return f.GetUserVerifiedAndLockedFn(ctx, userID)
	}
	return session.UserStatusResult{EmailVerified: true, IsActive: true}, nil
}

func (f *SessionFakeStorer) RotateRefreshTokenTx(ctx context.Context, in session.RotateTxInput) (session.RotatedSession, error) {
	if f.RotateRefreshTokenTxFn != nil {
		return f.RotateRefreshTokenTxFn(ctx, in)
	}
	return session.RotatedSession{}, nil
}

func (f *SessionFakeStorer) RevokeFamilyTokensTx(ctx context.Context, userID, familyID [16]byte, reason string) error {
	if f.RevokeFamilyTokensTxFn != nil {
		return f.RevokeFamilyTokensTxFn(ctx, userID, familyID, reason)
	}
	return nil
}

func (f *SessionFakeStorer) RevokeAllUserTokensTx(ctx context.Context, userID [16]byte, reason, ipAddress, userAgent string) error {
	if f.RevokeAllUserTokensTxFn != nil {
		return f.RevokeAllUserTokensTxFn(ctx, userID, reason, ipAddress, userAgent)
	}
	return nil
}

func (f *SessionFakeStorer) LogoutTx(ctx context.Context, in session.LogoutTxInput) error {
	if f.LogoutTxFn != nil {
		return f.LogoutTxFn(ctx, in)
	}
	return nil
}

func (f *SessionFakeStorer) WriteRefreshFailedAuditTx(ctx context.Context, ipAddress, userAgent string) error {
	if f.WriteRefreshFailedAuditTxFn != nil {
		return f.WriteRefreshFailedAuditTxFn(ctx, ipAddress, userAgent)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// UnlockFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// UnlockFakeStorer is a hand-written implementation of unlock.Storer for
// service unit tests. Each method delegates to its Fn field if non-nil;
// otherwise it returns the zero value and nil error so tests only configure the
// fields they need.
type UnlockFakeStorer struct {
	GetUserForUnlockFn     func(ctx context.Context, email string) (unlock.UnlockUser, error)
	GetUnlockTokenFn       func(ctx context.Context, email string) (authshared.VerificationToken, error)
	RequestUnlockTxFn      func(ctx context.Context, in unlock.RequestUnlockStoreInput) error
	ConsumeUnlockTokenTxFn func(ctx context.Context, email string, checkFn func(authshared.VerificationToken) error) error
	UnlockAccountTxFn      func(ctx context.Context, userID [16]byte, ip, ua string) error
	IncrementAttemptsTxFn  func(ctx context.Context, in authshared.IncrementInput) error
}

// compile-time interface check.
var _ unlock.Storer = (*UnlockFakeStorer)(nil)

func (f *UnlockFakeStorer) GetUserForUnlock(ctx context.Context, email string) (unlock.UnlockUser, error) {
	if f.GetUserForUnlockFn != nil {
		return f.GetUserForUnlockFn(ctx, email)
	}
	return unlock.UnlockUser{}, nil
}

func (f *UnlockFakeStorer) GetUnlockToken(ctx context.Context, email string) (authshared.VerificationToken, error) {
	if f.GetUnlockTokenFn != nil {
		return f.GetUnlockTokenFn(ctx, email)
	}
	return authshared.VerificationToken{}, authshared.ErrTokenNotFound
}

func (f *UnlockFakeStorer) RequestUnlockTx(ctx context.Context, in unlock.RequestUnlockStoreInput) error {
	if f.RequestUnlockTxFn != nil {
		return f.RequestUnlockTxFn(ctx, in)
	}
	return nil
}

// ConsumeUnlockTokenTx delegates to ConsumeUnlockTokenTxFn if set.
func (f *UnlockFakeStorer) ConsumeUnlockTokenTx(ctx context.Context, email string, checkFn func(authshared.VerificationToken) error) error {
	if f.ConsumeUnlockTokenTxFn != nil {
		return f.ConsumeUnlockTokenTxFn(ctx, email, checkFn)
	}
	return nil
}

func (f *UnlockFakeStorer) UnlockAccountTx(ctx context.Context, userID [16]byte, ip, ua string) error {
	if f.UnlockAccountTxFn != nil {
		return f.UnlockAccountTxFn(ctx, userID, ip, ua)
	}
	return nil
}

func (f *UnlockFakeStorer) IncrementAttemptsTx(ctx context.Context, in authshared.IncrementInput) error {
	if f.IncrementAttemptsTxFn != nil {
		return f.IncrementAttemptsTxFn(ctx, in)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// VerificationFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// VerificationFakeStorer is a hand-written implementation of verification.Storer
// for service unit tests. Each method delegates to its Fn field if non-nil;
// otherwise it returns the zero value and nil error so tests only configure the
// fields they need.
type VerificationFakeStorer struct {
	GetLatestTokenCreatedAtFn func(ctx context.Context, userID [16]byte) (time.Time, error)
	GetUserForResendFn        func(ctx context.Context, email string) (verification.ResendUser, error)
	IncrementAttemptsTxFn     func(ctx context.Context, in authshared.IncrementInput) error
	ResendVerificationTxFn    func(ctx context.Context, in verification.ResendStoreInput, codeHash string) error
	VerifyEmailTxFn           func(ctx context.Context, email, ipAddress, userAgent string, checkFn func(authshared.VerificationToken) error) error
}

// compile-time interface check.
var _ verification.Storer = (*VerificationFakeStorer)(nil)

func (f *VerificationFakeStorer) GetLatestTokenCreatedAt(ctx context.Context, userID [16]byte) (time.Time, error) {
	if f.GetLatestTokenCreatedAtFn != nil {
		return f.GetLatestTokenCreatedAtFn(ctx, userID)
	}
	return time.Time{}, nil
}

func (f *VerificationFakeStorer) GetUserForResend(ctx context.Context, email string) (verification.ResendUser, error) {
	if f.GetUserForResendFn != nil {
		return f.GetUserForResendFn(ctx, email)
	}
	return verification.ResendUser{}, nil
}

func (f *VerificationFakeStorer) IncrementAttemptsTx(ctx context.Context, in authshared.IncrementInput) error {
	if f.IncrementAttemptsTxFn != nil {
		return f.IncrementAttemptsTxFn(ctx, in)
	}
	return nil
}

func (f *VerificationFakeStorer) ResendVerificationTx(ctx context.Context, in verification.ResendStoreInput, codeHash string) error {
	if f.ResendVerificationTxFn != nil {
		return f.ResendVerificationTxFn(ctx, in, codeHash)
	}
	return nil
}

func (f *VerificationFakeStorer) VerifyEmailTx(ctx context.Context, email, ipAddress, userAgent string, checkFn func(authshared.VerificationToken) error) error {
	if f.VerifyEmailTxFn != nil {
		return f.VerifyEmailTxFn(ctx, email, ipAddress, userAgent, checkFn)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// ProfileSessionFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// ProfileSessionFakeStorer is a hand-written implementation of
// profilesession.Storer for service unit tests. Each method delegates to its
// Fn field if non-nil, otherwise returns the zero value and nil error so tests
// only need to configure the fields they care about.
type ProfileSessionFakeStorer struct {
	GetActiveSessionsFn func(ctx context.Context, userID [16]byte) ([]profilesession.ActiveSession, error)
	RevokeSessionTxFn   func(ctx context.Context, sessionID, ownerUserID [16]byte, ipAddress, userAgent string) error
}

// compile-time interface check.
var _ profilesession.Storer = (*ProfileSessionFakeStorer)(nil)

func (f *ProfileSessionFakeStorer) GetActiveSessions(ctx context.Context, userID [16]byte) ([]profilesession.ActiveSession, error) {
	if f.GetActiveSessionsFn != nil {
		return f.GetActiveSessionsFn(ctx, userID)
	}
	return nil, nil
}

func (f *ProfileSessionFakeStorer) RevokeSessionTx(ctx context.Context, sessionID, ownerUserID [16]byte, ipAddress, userAgent string) error {
	if f.RevokeSessionTxFn != nil {
		return f.RevokeSessionTxFn(ctx, sessionID, ownerUserID, ipAddress, userAgent)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// SetPasswordFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// SetPasswordFakeStorer is a hand-written implementation of setpassword.Storer
// for service unit tests. Each method delegates to its Fn field if non-nil,
// otherwise returns the zero value and nil error so tests only need to
// configure the fields they care about.
type SetPasswordFakeStorer struct {
	GetUserForSetPasswordFn func(ctx context.Context, userID [16]byte) (setpassword.SetPasswordUser, error)
	SetPasswordHashTxFn     func(ctx context.Context, in setpassword.SetPasswordInput, newHash string) error
}

// compile-time interface check.
var _ setpassword.Storer = (*SetPasswordFakeStorer)(nil)

// GetUserForSetPassword delegates to GetUserForSetPasswordFn if set.
func (f *SetPasswordFakeStorer) GetUserForSetPassword(ctx context.Context, userID [16]byte) (setpassword.SetPasswordUser, error) {
	if f.GetUserForSetPasswordFn != nil {
		return f.GetUserForSetPasswordFn(ctx, userID)
	}
	return setpassword.SetPasswordUser{}, nil
}

// SetPasswordHashTx delegates to SetPasswordHashTxFn if set.
func (f *SetPasswordFakeStorer) SetPasswordHashTx(ctx context.Context, in setpassword.SetPasswordInput, newHash string) error {
	if f.SetPasswordHashTxFn != nil {
		return f.SetPasswordHashTxFn(ctx, in, newHash)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// UsernameFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// UsernameFakeStorer is a hand-written implementation of username.Storer for
// service unit tests. Each method delegates to its Fn field if non-nil,
// otherwise returns the zero value and nil error so tests only need to
// configure the fields they care about.
type UsernameFakeStorer struct {
	CheckUsernameAvailableFn func(ctx context.Context, uname string) (bool, error)
	UpdateUsernameTxFn       func(ctx context.Context, in username.UpdateUsernameInput) error
}

// compile-time interface check.
var _ username.Storer = (*UsernameFakeStorer)(nil)

// CheckUsernameAvailable delegates to CheckUsernameAvailableFn if set.
// Default: returns (true, nil) — username is available — so tests that do not
// configure it never cause a spurious "taken" result.
func (f *UsernameFakeStorer) CheckUsernameAvailable(ctx context.Context, uname string) (bool, error) {
	if f.CheckUsernameAvailableFn != nil {
		return f.CheckUsernameAvailableFn(ctx, uname)
	}
	return true, nil
}

// UpdateUsernameTx delegates to UpdateUsernameTxFn if set.
func (f *UsernameFakeStorer) UpdateUsernameTx(ctx context.Context, in username.UpdateUsernameInput) error {
	if f.UpdateUsernameTxFn != nil {
		return f.UpdateUsernameTxFn(ctx, in)
	}
	return nil
}
