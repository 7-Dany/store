package password

// changePasswordRequest is the JSON payload for POST /change-password.
type changePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// forgotPasswordRequest is the JSON payload for POST /auth/forgot-password.
type forgotPasswordRequest struct {
	Email string `json:"email"`
}

// verifyResetCodeRequest is the JSON payload for POST /verify-reset-code.
type verifyResetCodeRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

// verifyResetCodeResponse is the JSON response for POST /verify-reset-code.
type verifyResetCodeResponse struct {
	ResetToken string `json:"reset_token"`
	ExpiresIn  int    `json:"expires_in"` // seconds
}

// resetPasswordRequest is the JSON payload for POST /reset-password.
// reset_token is the grant token returned by POST /verify-reset-code.
type resetPasswordRequest struct {
	ResetToken  string `json:"reset_token"`
	NewPassword string `json:"new_password"`
}
