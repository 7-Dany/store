// Package authsharedtest provides test-only helpers shared across all auth
// feature sub-packages. It must never be imported by production code.
package authsharedtest

import (
	"context"

	"github.com/7-Dany/store/backend/internal/domain/auth/login"
	"github.com/7-Dany/store/backend/internal/domain/auth/password"
	"github.com/7-Dany/store/backend/internal/domain/auth/register"
	"github.com/7-Dany/store/backend/internal/domain/auth/session"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/7-Dany/store/backend/internal/domain/auth/unlock"
	"github.com/7-Dany/store/backend/internal/domain/auth/verification"
	me "github.com/7-Dany/store/backend/internal/domain/profile/me"
	email "github.com/7-Dany/store/backend/internal/domain/profile/email"
	profilesession "github.com/7-Dany/store/backend/internal/domain/profile/session"
	setpassword "github.com/7-Dany/store/backend/internal/domain/profile/set-password"
	username "github.com/7-Dany/store/backend/internal/domain/profile/username"
)

// ─────────────────────────────────────────────────────────────────────────────
// LoginFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

// LoginFakeServicer is a hand-written implementation of login.Servicer for
// handler unit tests. Each method delegates to its Fn field if non-nil,
// otherwise returns the zero value and nil error.
type LoginFakeServicer struct {
	LoginFn func(ctx context.Context, in login.LoginInput) (login.LoggedInSession, error)
}

// compile-time interface check.
var _ login.Servicer = (*LoginFakeServicer)(nil)

func (f *LoginFakeServicer) Login(ctx context.Context, in login.LoginInput) (login.LoggedInSession, error) {
	if f.LoginFn != nil {
		return f.LoginFn(ctx, in)
	}
	return login.LoggedInSession{}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// PasswordFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

// PasswordFakeServicer is a hand-written implementation of password.Servicer
// for handler unit tests. Each method delegates to its Fn field if non-nil,
// otherwise returns the zero value and nil error.
type PasswordFakeServicer struct {
	RequestPasswordResetFn      func(ctx context.Context, in password.ForgotPasswordInput) (authshared.OTPIssuanceResult, error)
	VerifyResetCodeFn           func(ctx context.Context, in password.VerifyResetCodeInput) (string, error)
	ConsumePasswordResetTokenFn func(ctx context.Context, in password.ResetPasswordInput) ([16]byte, error)
	UpdatePasswordHashFn        func(ctx context.Context, in password.ChangePasswordInput) error
}

// compile-time interface check.
var _ password.Servicer = (*PasswordFakeServicer)(nil)

func (f *PasswordFakeServicer) RequestPasswordReset(ctx context.Context, in password.ForgotPasswordInput) (authshared.OTPIssuanceResult, error) {
	if f.RequestPasswordResetFn != nil {
		return f.RequestPasswordResetFn(ctx, in)
	}
	return authshared.OTPIssuanceResult{}, nil
}

// VerifyResetCode delegates to VerifyResetCodeFn if set.
func (f *PasswordFakeServicer) VerifyResetCode(ctx context.Context, in password.VerifyResetCodeInput) (string, error) {
	if f.VerifyResetCodeFn != nil {
		return f.VerifyResetCodeFn(ctx, in)
	}
	return "", nil
}

func (f *PasswordFakeServicer) ConsumePasswordResetToken(ctx context.Context, in password.ResetPasswordInput) ([16]byte, error) {
	if f.ConsumePasswordResetTokenFn != nil {
		return f.ConsumePasswordResetTokenFn(ctx, in)
	}
	return [16]byte{}, nil
}

// UpdatePasswordHash delegates to UpdatePasswordHashFn if set.
func (f *PasswordFakeServicer) UpdatePasswordHash(ctx context.Context, in password.ChangePasswordInput) error {
	if f.UpdatePasswordHashFn != nil {
		return f.UpdatePasswordHashFn(ctx, in)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// MeFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

// MeFakeServicer is a hand-written implementation of me.Servicer for handler
// unit tests. Each method delegates to its Fn field if non-nil, otherwise
// returns the zero value and nil error.
type MeFakeServicer struct {
	GetUserProfileFn func(ctx context.Context, userID string) (me.UserProfile, error)
	UpdateProfileFn  func(ctx context.Context, in me.UpdateProfileInput) error
}

// compile-time interface check.
var _ me.Servicer = (*MeFakeServicer)(nil)

// GetUserProfile delegates to GetUserProfileFn if set.
func (f *MeFakeServicer) GetUserProfile(ctx context.Context, userID string) (me.UserProfile, error) {
	if f.GetUserProfileFn != nil {
		return f.GetUserProfileFn(ctx, userID)
	}
	return me.UserProfile{}, nil
}

// UpdateProfile delegates to UpdateProfileFn if set.
func (f *MeFakeServicer) UpdateProfile(ctx context.Context, in me.UpdateProfileInput) error {
	if f.UpdateProfileFn != nil {
		return f.UpdateProfileFn(ctx, in)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// RegisterFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

// RegisterFakeServicer is a hand-written implementation of register.Servicer
// for handler unit tests. RegisterFn is called when non-nil; otherwise Register
// returns a canned success result so tests only configure the fields they care about.
type RegisterFakeServicer struct {
	RegisterFn func(ctx context.Context, in register.RegisterInput) (register.RegisterResult, error)
}

// compile-time interface check.
var _ register.Servicer = (*RegisterFakeServicer)(nil)

func (f *RegisterFakeServicer) Register(ctx context.Context, in register.RegisterInput) (register.RegisterResult, error) {
	if f.RegisterFn != nil {
		return f.RegisterFn(ctx, in)
	}
	return register.RegisterResult{
		UserID:  "00000000-0000-0000-0000-000000000001",
		Email:   in.Email,
		RawCode: "123456",
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// SessionFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

// SessionFakeServicer is a hand-written implementation of session.Servicer for
// handler unit tests. Each method delegates to its Fn field if non-nil,
// otherwise returns the zero value and nil error.
type SessionFakeServicer struct {
	RotateRefreshTokenFn func(ctx context.Context, jti [16]byte, ipAddress, userAgent string) (session.RotatedSession, error)
	LogoutFn             func(ctx context.Context, in session.LogoutTxInput) error
}

// compile-time interface check.
var _ session.Servicer = (*SessionFakeServicer)(nil)

func (f *SessionFakeServicer) RotateRefreshToken(ctx context.Context, jti [16]byte, ipAddress, userAgent string) (session.RotatedSession, error) {
	if f.RotateRefreshTokenFn != nil {
		return f.RotateRefreshTokenFn(ctx, jti, ipAddress, userAgent)
	}
	return session.RotatedSession{}, nil
}

func (f *SessionFakeServicer) Logout(ctx context.Context, in session.LogoutTxInput) error {
	if f.LogoutFn != nil {
		return f.LogoutFn(ctx, in)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// UnlockFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

// UnlockFakeServicer is a hand-written implementation of unlock.Servicer for
// handler unit tests. Each method delegates to its Fn field if non-nil,
// otherwise returns the zero value and nil error.
type UnlockFakeServicer struct {
	RequestUnlockFn      func(ctx context.Context, in unlock.RequestUnlockInput) (authshared.OTPIssuanceResult, error)
	ConsumeUnlockTokenFn func(ctx context.Context, in unlock.ConfirmUnlockInput) error
}

// compile-time interface check.
var _ unlock.Servicer = (*UnlockFakeServicer)(nil)

func (f *UnlockFakeServicer) RequestUnlock(ctx context.Context, in unlock.RequestUnlockInput) (authshared.OTPIssuanceResult, error) {
	if f.RequestUnlockFn != nil {
		return f.RequestUnlockFn(ctx, in)
	}
	return authshared.OTPIssuanceResult{}, nil
}

func (f *UnlockFakeServicer) ConsumeUnlockToken(ctx context.Context, in unlock.ConfirmUnlockInput) error {
	if f.ConsumeUnlockTokenFn != nil {
		return f.ConsumeUnlockTokenFn(ctx, in)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// VerificationFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

// VerificationFakeServicer is a hand-written implementation of verification.Servicer
// for handler unit tests. Each method delegates to its Fn field if non-nil,
// otherwise returns the zero value and nil error.
type VerificationFakeServicer struct {
	VerifyEmailFn        func(ctx context.Context, in verification.VerifyEmailInput) error
	ResendVerificationFn func(ctx context.Context, in verification.ResendInput) (authshared.OTPIssuanceResult, error)
}

// compile-time interface check.
var _ verification.Servicer = (*VerificationFakeServicer)(nil)

func (f *VerificationFakeServicer) VerifyEmail(ctx context.Context, in verification.VerifyEmailInput) error {
	if f.VerifyEmailFn != nil {
		return f.VerifyEmailFn(ctx, in)
	}
	return nil
}

func (f *VerificationFakeServicer) ResendVerification(ctx context.Context, in verification.ResendInput) (authshared.OTPIssuanceResult, error) {
	if f.ResendVerificationFn != nil {
		return f.ResendVerificationFn(ctx, in)
	}
	return authshared.OTPIssuanceResult{}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// ProfileSessionFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

// ProfileSessionFakeServicer is a hand-written implementation of
// profilesession.Servicer for handler unit tests. Each method delegates to its
// Fn field if non-nil, otherwise returns the zero value and nil error.
type ProfileSessionFakeServicer struct {
	GetActiveSessionsFn func(ctx context.Context, userID string) ([]profilesession.ActiveSession, error)
	RevokeSessionFn     func(ctx context.Context, userID, sessionID, ipAddress, userAgent string) error
}

// compile-time interface check.
var _ profilesession.Servicer = (*ProfileSessionFakeServicer)(nil)

func (f *ProfileSessionFakeServicer) GetActiveSessions(ctx context.Context, userID string) ([]profilesession.ActiveSession, error) {
	if f.GetActiveSessionsFn != nil {
		return f.GetActiveSessionsFn(ctx, userID)
	}
	return nil, nil
}

func (f *ProfileSessionFakeServicer) RevokeSession(ctx context.Context, userID, sessionID, ipAddress, userAgent string) error {
	if f.RevokeSessionFn != nil {
		return f.RevokeSessionFn(ctx, userID, sessionID, ipAddress, userAgent)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// SetPasswordFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

// SetPasswordFakeServicer is a hand-written implementation of
// setpassword.Servicer for handler unit tests. SetPasswordFn is called when
// non-nil; otherwise SetPassword returns nil so tests only configure the
// fields they care about.
type SetPasswordFakeServicer struct {
	SetPasswordFn func(ctx context.Context, in setpassword.SetPasswordInput) error
}

// compile-time interface check.
var _ setpassword.Servicer = (*SetPasswordFakeServicer)(nil)

// SetPassword delegates to SetPasswordFn if set.
func (f *SetPasswordFakeServicer) SetPassword(ctx context.Context, in setpassword.SetPasswordInput) error {
	if f.SetPasswordFn != nil {
		return f.SetPasswordFn(ctx, in)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// UsernameFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

// UsernameFakeServicer is a hand-written implementation of username.Servicer for
// handler unit tests. Each method delegates to its Fn field if non-nil,
// otherwise returns the zero value and nil error so tests only configure the
// fields they care about.
type UsernameFakeServicer struct {
	CheckUsernameAvailableFn func(ctx context.Context, uname string) (bool, error)
	UpdateUsernameFn         func(ctx context.Context, in username.UpdateUsernameInput) error
}

// compile-time interface check.
var _ username.Servicer = (*UsernameFakeServicer)(nil)

// CheckUsernameAvailable delegates to CheckUsernameAvailableFn if set.
// Default: runs the real NormaliseAndValidateUsername so that handler tests
// which pass invalid inputs receive the correct validation sentinel without
// needing to configure a Fn. Returns (true, nil) for any valid username.
func (f *UsernameFakeServicer) CheckUsernameAvailable(ctx context.Context, uname string) (bool, error) {
	if f.CheckUsernameAvailableFn != nil {
		return f.CheckUsernameAvailableFn(ctx, uname)
	}
	if _, err := username.NormaliseAndValidateUsername(uname); err != nil {
		return false, err
	}
	return true, nil
}

// UpdateUsername delegates to UpdateUsernameFn if set.
// Default: runs the real NormaliseAndValidateUsername so that handler tests
// which pass invalid inputs receive the correct validation sentinel without
// needing to configure a Fn. Returns nil for any valid username.
func (f *UsernameFakeServicer) UpdateUsername(ctx context.Context, in username.UpdateUsernameInput) error {
	if f.UpdateUsernameFn != nil {
		return f.UpdateUsernameFn(ctx, in)
	}
	if _, err := username.NormaliseAndValidateUsername(in.Username); err != nil {
		return err
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// EmailChangeFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

// EmailChangeFakeServicer is a hand-written implementation of email.Servicer for
// handler unit tests. Each method delegates to its Fn field if non-nil, otherwise
// returns the zero value and nil error so tests only configure the fields they need.
type EmailChangeFakeServicer struct {
	RequestEmailChangeFn func(ctx context.Context, in email.EmailChangeRequestInput) (email.EmailChangeRequestResult, error)
	VerifyCurrentEmailFn func(ctx context.Context, in email.EmailChangeVerifyCurrentInput) (email.EmailChangeVerifyCurrentResult, error)
	ConfirmEmailChangeFn func(ctx context.Context, in email.EmailChangeConfirmInput) (email.ConfirmEmailChangeResult, error)
}

// compile-time interface check.
var _ email.Servicer = (*EmailChangeFakeServicer)(nil)

func (f *EmailChangeFakeServicer) RequestEmailChange(ctx context.Context, in email.EmailChangeRequestInput) (email.EmailChangeRequestResult, error) {
	if f.RequestEmailChangeFn != nil {
		return f.RequestEmailChangeFn(ctx, in)
	}
	return email.EmailChangeRequestResult{}, nil
}

func (f *EmailChangeFakeServicer) VerifyCurrentEmail(ctx context.Context, in email.EmailChangeVerifyCurrentInput) (email.EmailChangeVerifyCurrentResult, error) {
	if f.VerifyCurrentEmailFn != nil {
		return f.VerifyCurrentEmailFn(ctx, in)
	}
	return email.EmailChangeVerifyCurrentResult{GrantToken: "fake-grant-token", ExpiresIn: 600}, nil
}

func (f *EmailChangeFakeServicer) ConfirmEmailChange(ctx context.Context, in email.EmailChangeConfirmInput) (email.ConfirmEmailChangeResult, error) {
	if f.ConfirmEmailChangeFn != nil {
		return f.ConfirmEmailChangeFn(ctx, in)
	}
	return email.ConfirmEmailChangeResult{}, nil
}
