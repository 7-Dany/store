// Package setpassword provides the HTTP handler, service, and store for
// POST /api/v1/auth/set-password — adding a password to an OAuth-only account.
package setpassword

// SetPasswordInput holds the caller-supplied data for service.SetPassword.
type SetPasswordInput struct {
	UserID      string
	NewPassword string
	IPAddress   string
	UserAgent   string
}

// SetPasswordUser is the minimal user view returned by store.GetUserForSetPassword.
type SetPasswordUser struct {
	// HasNoPassword is true when password_hash IS NULL in the users table,
	// meaning the account was created exclusively via OAuth.
	HasNoPassword bool
}
