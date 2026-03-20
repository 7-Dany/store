package authshared

// AuthRecorder is the observability interface for all auth domain events.
//
// *telemetry.Registry satisfies this interface structurally via the hook methods
// in internal/platform/telemetry/auth_hooks.go. Pass deps.Metrics directly
// from server.New — no factory method needed.
//
// All implementations must be safe for concurrent use.
//
// Compile-time structural assertion in server.New:
//
//	var _ authshared.AuthRecorder = (*telemetry.Registry)(nil)
type AuthRecorder interface {
	// ── Login / Session ───────────────────────────────────────────────────

	// OnLoginSuccess records a successful login for the given provider.
	OnLoginSuccess(provider string)
	// OnLoginFailed records a failed login attempt.
	// reason must be one of the [LoginFailureReason] constants.
	OnLoginFailed(provider string, reason string)
	// OnLogout records a logout event.
	OnLogout()
	// OnTokenRefreshed records a successful token refresh.
	// clientType should be one of: "web", "mobile", "api", "unknown".
	OnTokenRefreshed(clientType string)
	// OnTokenValidationFailed records a JWT validation failure.
	// reason should be one of: "expired", "invalid_signature", "malformed", "revoked", "unknown".
	OnTokenValidationFailed(reason string)
	// OnSessionRevoked records an explicit session revocation.
	OnSessionRevoked()

	// ── Registration ──────────────────────────────────────────────────────

	// OnRegistrationSuccess records a successful user registration.
	OnRegistrationSuccess()
	// OnRegistrationFailed records a failed registration attempt.
	// reason must be one of the [RegistrationFailureReason] constants.
	OnRegistrationFailed(reason string)

	// ── Email verification ────────────────────────────────────────────────

	// OnEmailVerified records a successful email verification.
	OnEmailVerified()
	// OnVerificationResent records a verification email resend request.
	OnVerificationResent()

	// ── Password ──────────────────────────────────────────────────────────

	// OnPasswordResetRequested records a password reset request.
	OnPasswordResetRequested()
	// OnPasswordResetDenied records a denied password reset.
	// reason should be one of: "account_not_found", "rate_limited".
	OnPasswordResetDenied(reason string)
	// OnPasswordResetCompleted records a completed password reset.
	OnPasswordResetCompleted()
	// OnPasswordChanged records a password change by an authenticated user.
	OnPasswordChanged()

	// ── Account unlock ────────────────────────────────────────────────────

	// OnUnlockRequested records an account unlock OTP request.
	OnUnlockRequested()
	// OnUnlockCompleted records a completed account unlock.
	OnUnlockCompleted()

	// ── OAuth ─────────────────────────────────────────────────────────────

	// OnOAuthSuccess records a successful OAuth flow.
	// provider must be normalised through the allowlist before the call.
	OnOAuthSuccess(provider string)
	// OnOAuthFailed records an OAuth flow failure.
	// provider must be normalised; reason must be one of the [OAuthFailureReason] constants.
	OnOAuthFailed(provider string, reason string)
	// OnOAuthLinked records a new OAuth provider link.
	OnOAuthLinked(provider string)
	// OnOAuthUnlinked records an OAuth provider unlink.
	// A spike triggers the OAuthUnlinkSpike security alert.
	OnOAuthUnlinked(provider string)

	// ── Profile ───────────────────────────────────────────────────────────

	OnEmailChangeRequested()
	OnEmailChangeCompleted()
	OnUsernameChanged()
	OnAccountDeletionRequested()
	OnAccountDeletionCompleted()

	// ── Admin security events ─────────────────────────────────────────────

	// OnUserLocked records an account lock event.
	// reason must be one of: "admin_action", "auto_lockout".
	OnUserLocked(reason string)
	// OnUserUnlocked records an account unlock completion.
	OnUserUnlocked()
}

// ── Bounded reason types ──────────────────────────────────────────────────────
// These are type aliases (= string) so the compiler accepts the constants at
// every call site without an explicit cast. Prometheus cardinality budgets
// assume the bounded sets below — extend here, never pass raw strings.

// LoginFailureReason is the bounded set of valid reason values for [AuthRecorder.OnLoginFailed].
type LoginFailureReason = string

const (
	LoginReasonInvalidCredentials LoginFailureReason = "invalid_credentials"
	LoginReasonAccountLocked      LoginFailureReason = "account_locked"
	LoginReasonEmailUnverified    LoginFailureReason = "email_unverified"
	LoginReasonAccountInactive    LoginFailureReason = "account_inactive"
	LoginReasonRateLimit          LoginFailureReason = "rate_limit"
)

// RegistrationFailureReason is the bounded set of valid reason values for
// [AuthRecorder.OnRegistrationFailed].
type RegistrationFailureReason = string

const (
	RegistrationReasonEmailTaken    RegistrationFailureReason = "email_taken"
	RegistrationReasonUsernameTaken RegistrationFailureReason = "username_taken"
	RegistrationReasonInvalidInput  RegistrationFailureReason = "invalid_input"
	RegistrationReasonRateLimit     RegistrationFailureReason = "rate_limit"
)

// OAuthFailureReason is the bounded set of valid reason values for
// [AuthRecorder.OnOAuthFailed].
type OAuthFailureReason = string

const (
	OAuthReasonStateMismatch     OAuthFailureReason = "state_mismatch"
	OAuthReasonMalformedCallback OAuthFailureReason = "malformed_callback"
	OAuthReasonTokenExchange     OAuthFailureReason = "token_exchange_failed"
	OAuthReasonProfileFetch      OAuthFailureReason = "profile_fetch_failed"
	OAuthReasonProviderError     OAuthFailureReason = "provider_error"
	OAuthReasonUnknown           OAuthFailureReason = "unknown"
)

// ── NoopAuthRecorder ─────────────────────────────────────────────────────────

// NoopAuthRecorder satisfies [AuthRecorder] with empty method bodies.
// Use in auth domain unit tests that do not need metric assertions.
type NoopAuthRecorder struct{}

func (NoopAuthRecorder) OnLoginSuccess(string)          {}
func (NoopAuthRecorder) OnLoginFailed(string, string)   {}
func (NoopAuthRecorder) OnLogout()                      {}
func (NoopAuthRecorder) OnTokenRefreshed(string)        {}
func (NoopAuthRecorder) OnTokenValidationFailed(string) {}
func (NoopAuthRecorder) OnSessionRevoked()              {}
func (NoopAuthRecorder) OnRegistrationSuccess()         {}
func (NoopAuthRecorder) OnRegistrationFailed(string)    {}
func (NoopAuthRecorder) OnEmailVerified()               {}
func (NoopAuthRecorder) OnVerificationResent()          {}
func (NoopAuthRecorder) OnPasswordResetRequested()      {}
func (NoopAuthRecorder) OnPasswordResetDenied(string)   {}
func (NoopAuthRecorder) OnPasswordResetCompleted()      {}
func (NoopAuthRecorder) OnPasswordChanged()             {}
func (NoopAuthRecorder) OnUnlockRequested()             {}
func (NoopAuthRecorder) OnUnlockCompleted()             {}
func (NoopAuthRecorder) OnOAuthSuccess(string)          {}
func (NoopAuthRecorder) OnOAuthFailed(string, string)   {}
func (NoopAuthRecorder) OnOAuthLinked(string)           {}
func (NoopAuthRecorder) OnOAuthUnlinked(string)         {}
func (NoopAuthRecorder) OnEmailChangeRequested()        {}
func (NoopAuthRecorder) OnEmailChangeCompleted()        {}
func (NoopAuthRecorder) OnUsernameChanged()             {}
func (NoopAuthRecorder) OnAccountDeletionRequested()    {}
func (NoopAuthRecorder) OnAccountDeletionCompleted()    {}
func (NoopAuthRecorder) OnUserLocked(string)            {}
func (NoopAuthRecorder) OnUserUnlocked()                {}

// Compile-time assertion: NoopAuthRecorder must satisfy AuthRecorder.
// NoopAuthRecorder is a struct, not a pointer, so use a zero value — not nil.
var _ AuthRecorder = NoopAuthRecorder{}
