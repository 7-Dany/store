package setpassword

// setPasswordRequest is the JSON payload for POST /set-password.
type setPasswordRequest struct {
	NewPassword string `json:"new_password"`
}
