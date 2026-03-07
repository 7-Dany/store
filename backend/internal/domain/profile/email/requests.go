package email

// requestChangeRequest is the decoded JSON body for POST /email/request-change.
type requestChangeRequest struct {
	NewEmail string `json:"new_email"`
}

// verifyCurrentRequest is the decoded JSON body for POST /email/verify-current.
type verifyCurrentRequest struct {
	Code string `json:"code"`
}

// verifyCurrentResponse is the JSON body returned on a successful step 2.
type verifyCurrentResponse struct {
	GrantToken string `json:"grant_token"`
	ExpiresIn  int    `json:"expires_in"`
}

// confirmChangeRequest is the decoded JSON body for POST /email/confirm-change.
type confirmChangeRequest struct {
	GrantToken string `json:"grant_token"`
	Code       string `json:"code"`
}

// messageResponse is the shared success body for steps 1 and 3.
type messageResponse struct {
	Message string `json:"message"`
}
