package unlock

import (
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
)

// validateUnlockRequest validates and normalises a requestUnlockRequest.
// Returns an authshared.ErrXxx sentinel on failure.
func validateUnlockRequest(req *requestUnlockRequest) error {
	var err error
	req.Email, err = authshared.NormaliseEmail(req.Email)
	if err != nil {
		return err
	}
	return nil
}

// validateConfirmUnlockRequest validates and normalises a confirmUnlockRequest.
// Returns an authshared.ErrXxx sentinel on failure.
func validateConfirmUnlockRequest(req *confirmUnlockRequest) error {
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
