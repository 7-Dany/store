package login

// loginRequest is the JSON payload for POST /auth/login.
// Identifier accepts either an email address or a username.
type loginRequest struct {
	Identifier string `json:"identifier"`
	Password   string `json:"password"`
}
