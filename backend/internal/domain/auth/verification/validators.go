package verification

import (
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
)

// validateVerifyEmailRequest validates the verify-email request body.
func validateVerifyEmailRequest(req *verifyEmailRequest) error {
	var err error
	req.Email, err = authshared.NormaliseEmail(req.Email)
	if err != nil {
		return err
	}
	if err := authshared.ValidateOTPCode(req.Code); err != nil {
		return err
	}
	return nil
}

// validateResendRequest validates the resend-verification request body.
func validateResendRequest(req *resendVerificationRequest) error {
	var err error
	req.Email, err = authshared.NormaliseEmail(req.Email)
	if err != nil {
		return err
	}
	return nil
}
