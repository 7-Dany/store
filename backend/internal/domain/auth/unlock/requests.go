package unlock

// requestUnlockRequest is the JSON payload for POST /request-unlock.
type requestUnlockRequest struct {
	Email string `json:"email"`
}

// confirmUnlockRequest is the JSON payload for POST /confirm-unlock.
type confirmUnlockRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}
