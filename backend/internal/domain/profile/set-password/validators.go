package setpassword

import (
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
)

// validateSetPasswordRequest validates the decoded request body before the
// service is called. It catches empty and structurally weak passwords at the
// HTTP layer so the service never receives invalid input.
func validateSetPasswordRequest(req *setPasswordRequest) error {
	return authshared.ValidatePassword(req.NewPassword)
}
