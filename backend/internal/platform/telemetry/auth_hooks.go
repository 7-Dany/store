package telemetry

// Auth hook methods implement the authshared.AuthRecorder interface structurally.
// The interface is defined in domain/auth/shared/recorder.go; *Registry satisfies
// it without importing that package (no import cycle).
//
// All methods are nil-safe: calling them on a nil *Registry is a no-op.
// Prometheus label values are always primitive constants — never derived from
// raw user input.

// OnLoginSuccess records a successful login for the given provider.
func (r *Registry) OnLoginSuccess(provider string) {
	if r == nil {
		return
	}
	r.authLogins.WithLabelValues(provider, "success").Inc()
}

// OnLoginFailed records a failed login attempt.
// reason must be one of the LoginFailureReason constants defined in authshared.
func (r *Registry) OnLoginFailed(provider, reason string) {
	if r == nil {
		return
	}
	r.authLogins.WithLabelValues(provider, "failure").Inc()
	r.authLoginFailures.WithLabelValues(provider, reason).Inc()
}

// OnLogout records a logout event.
func (r *Registry) OnLogout() {
	if r == nil {
		return
	}
	r.authLogouts.Inc()
}

// OnTokenRefreshed records a successful token refresh.
// clientType should be one of: "web", "mobile", "api", "unknown".
func (r *Registry) OnTokenRefreshed(clientType string) {
	if r == nil {
		return
	}
	r.authTokenRefreshes.WithLabelValues(clientType).Inc()
}

// OnTokenValidationFailed records a JWT validation failure.
// reason should be one of: "expired", "invalid_signature", "malformed", "revoked", "unknown".
func (r *Registry) OnTokenValidationFailed(reason string) {
	if r == nil {
		return
	}
	r.authTokenValidationFailures.WithLabelValues(reason).Inc()
}

// OnSessionRevoked records an explicit session revocation.
func (r *Registry) OnSessionRevoked() {
	if r == nil {
		return
	}
	r.authSessionRevocations.Inc()
}

// OnRegistrationSuccess records a successful user registration.
func (r *Registry) OnRegistrationSuccess() {
	if r == nil {
		return
	}
	r.authRegistrations.WithLabelValues("success").Inc()
}

// OnRegistrationFailed records a failed registration attempt.
// reason must be one of the RegistrationFailureReason constants defined in authshared.
func (r *Registry) OnRegistrationFailed(reason string) {
	if r == nil {
		return
	}
	r.authRegistrations.WithLabelValues("failure").Inc()
	r.authRegistrationFailures.WithLabelValues(reason).Inc()
}

// OnEmailVerified records a successful email verification.
func (r *Registry) OnEmailVerified() {
	if r == nil {
		return
	}
	r.authEmailVerifications.Inc()
}

// OnVerificationResent records a verification email resend request.
func (r *Registry) OnVerificationResent() {
	if r == nil {
		return
	}
	r.authVerificationResends.Inc()
}

// OnPasswordResetRequested records a password reset request.
func (r *Registry) OnPasswordResetRequested() {
	if r == nil {
		return
	}
	r.authPasswordResets.WithLabelValues("requested").Inc()
}

// OnPasswordResetDenied records a denied password reset request.
// reason should be one of: "account_not_found", "rate_limited".
func (r *Registry) OnPasswordResetDenied(reason string) {
	if r == nil {
		return
	}
	r.authPasswordResetsDenied.WithLabelValues(reason).Inc()
}

// OnPasswordResetCompleted records a completed password reset.
func (r *Registry) OnPasswordResetCompleted() {
	if r == nil {
		return
	}
	r.authPasswordResets.WithLabelValues("completed").Inc()
}

// OnPasswordChanged records a password change by an authenticated user.
func (r *Registry) OnPasswordChanged() {
	if r == nil {
		return
	}
	r.authPasswordChanges.Inc()
}

// OnUnlockRequested records an account unlock OTP request.
func (r *Registry) OnUnlockRequested() {
	if r == nil {
		return
	}
	r.authAccountUnlocks.WithLabelValues("requested").Inc()
}

// OnUnlockCompleted records a completed account unlock.
func (r *Registry) OnUnlockCompleted() {
	if r == nil {
		return
	}
	r.authAccountUnlocks.WithLabelValues("completed").Inc()
}

// OnOAuthSuccess records a successful OAuth flow completion.
// provider must be normalised through the allowlist in the call site before
// being passed here. Never pass a raw URL path segment.
func (r *Registry) OnOAuthSuccess(provider string) {
	if r == nil {
		return
	}
	r.authOAuth.WithLabelValues(provider, "success").Inc()
}

// OnOAuthFailed records an OAuth flow failure.
// provider must be normalised; reason must be one of the OAuthFailureReason constants.
func (r *Registry) OnOAuthFailed(provider, reason string) {
	if r == nil {
		return
	}
	r.authOAuth.WithLabelValues(provider, "failure").Inc()
	r.authOAuthFailures.WithLabelValues(provider, reason).Inc()
}

// OnOAuthLinked records a new OAuth provider link event.
func (r *Registry) OnOAuthLinked(provider string) {
	if r == nil {
		return
	}
	r.authOAuthLinks.WithLabelValues(provider).Inc()
}

// OnOAuthUnlinked records an OAuth provider unlink event.
// A spike triggers OAuthUnlinkSpike alert (possible account takeover campaign).
func (r *Registry) OnOAuthUnlinked(provider string) {
	if r == nil {
		return
	}
	r.authOAuthUnlinks.WithLabelValues(provider).Inc()
}

// OnEmailChangeRequested records an email change request.
func (r *Registry) OnEmailChangeRequested() {
	if r == nil {
		return
	}
	r.authEmailChanges.WithLabelValues("requested").Inc()
}

// OnEmailChangeCompleted records a completed email change.
func (r *Registry) OnEmailChangeCompleted() {
	if r == nil {
		return
	}
	r.authEmailChanges.WithLabelValues("completed").Inc()
}

// OnUsernameChanged records a username change event.
func (r *Registry) OnUsernameChanged() {
	if r == nil {
		return
	}
	r.authUsernameChanges.Inc()
}

// OnAccountDeletionRequested records an account deletion request.
func (r *Registry) OnAccountDeletionRequested() {
	if r == nil {
		return
	}
	r.authAccountDeletions.WithLabelValues("requested").Inc()
}

// OnAccountDeletionCompleted records a completed account deletion.
func (r *Registry) OnAccountDeletionCompleted() {
	if r == nil {
		return
	}
	r.authAccountDeletions.WithLabelValues("completed").Inc()
}

// OnUserLocked records an account lock event.
// reason must be one of: "admin_action", "auto_lockout".
func (r *Registry) OnUserLocked(reason string) {
	if r == nil {
		return
	}
	r.authUserLocks.WithLabelValues(reason).Inc()
}

// OnUserUnlocked records an account unlock completion.
func (r *Registry) OnUserUnlocked() {
	if r == nil {
		return
	}
	r.authUserUnlocks.Inc()
}
