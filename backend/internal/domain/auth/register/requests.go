package register

// registerRequest is the JSON payload for POST /auth/register.
type registerRequest struct {
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Password    string `json:"password"`
}
