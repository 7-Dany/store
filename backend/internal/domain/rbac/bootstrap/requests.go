package bootstrap

import "time"

// bootstrapRequest is the decoded JSON request body.
// user_id is intentionally absent — it is derived from the authenticated
// caller's JWT so a logged-in user can only make themselves the owner.
type bootstrapRequest struct {
	BootstrapSecret string `json:"bootstrap_secret"`
}

// BootstrapResult is returned on success and written as the JSON response body.
type BootstrapResult struct {
	UserID    string    `json:"user_id"`
	RoleName  string    `json:"role_name"`
	GrantedAt time.Time `json:"granted_at"`
}
