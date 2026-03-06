package verification

// verifyEmailRequest is the HTTP request body for POST /verify-email.
type verifyEmailRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

// resendVerificationRequest is the HTTP request body for POST /resend-verification.
type resendVerificationRequest struct {
	Email string `json:"email"`
}
