package password

import (
	"errors"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
)

// validateForgotPasswordRequest normalises and fully validates the email
// format via authshared.NormaliseEmail. Validation failures return 422.
// The 202 anti-enumeration response applies only to service-layer paths
// where the account does not exist, is unverified, is locked, or is inactive.
func validateForgotPasswordRequest(req *forgotPasswordRequest) error {
	var err error
	req.Email, err = authshared.NormaliseEmail(req.Email)
	if err != nil {
		return err
	}
	return nil
}

// validateChangePasswordRequest checks that both password fields are non-empty.
// Full strength validation is delegated to the service layer.
func validateChangePasswordRequest(req *changePasswordRequest) error {
	if req.OldPassword == "" {
		return errors.New("old_password is required")
	}
	if req.NewPassword == "" {
		return errors.New("new_password is required")
	}
	return nil
}

// validateVerifyResetCodeRequest normalises the email and validates the OTP code format.
func validateVerifyResetCodeRequest(req *verifyResetCodeRequest) error {
	var err error
	req.Email, err = authshared.NormaliseEmail(req.Email)
	if err != nil {
		return err
	}
	return authshared.ValidateOTPCode(req.Code)
}

// validateResetPasswordRequest checks that both fields are non-empty and that
// the new password meets strength requirements.
// The grant token is validated by the KV lookup in the handler — not here.
func validateResetPasswordRequest(req *resetPasswordRequest) error {
	if req.ResetToken == "" {
		return errors.New("reset_token is required")
	}
	return authshared.ValidatePassword(req.NewPassword)
}
