// Package username provides the HTTP handler, service, and store for the
// username availability check (GET /username/available) and username mutation
// (PATCH /me/username) endpoints.
package username

// CheckUsernameAvailableInput is the service-layer input for the availability check.
type CheckUsernameAvailableInput struct {
	Username string
}

// UpdateUsernameInput is the service-layer input for the username mutation.
type UpdateUsernameInput struct {
	UserID    [16]byte
	Username  string
	IPAddress string
	UserAgent string
}
