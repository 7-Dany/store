// Package register provides the HTTP handler, service, and store for the
// user registration flow: creating an account, issuing the first email
// verification token, and delivering the OTP via the mailer.
package register

import "time"

// ── Register ──────────────────────────────────────────────────────────────────

// RegisterInput holds the validated fields required to create a new account.
// All string values are trimmed and normalised before reaching the service.
type RegisterInput struct {
	DisplayName string
	Email       string
	Password    string
	IPAddress   string
	UserAgent   string
}

// RegisterResult is returned by Service.Register on success.
type RegisterResult struct {
	UserID  string
	Email   string
	RawCode string
}

// CreateUserInput carries every field Store.CreateUserTx needs to create a
// user row, issue a verification token, and write the audit log.
// CodeHash is the bcrypt hash of the OTP raw code produced by
// authshared.GenerateCodeHash before the transaction begins.
// TTL is the OTP token lifetime; sourced from config.Config.OTPValidMinutes.
type CreateUserInput struct {
	DisplayName  string
	Email        string
	PasswordHash string
	CodeHash     string
	TTL          time.Duration
	IPAddress    string
	UserAgent    string
}

// CreatedUser is returned by Store.CreateUserTx on success.
// UserID is the canonical string form of the UUID ("xxxxxxxx-xxxx-…").
type CreatedUser struct {
	UserID string
	Email  string
}
