// Package audit defines typed string constants for every audit log event.
package audit

// EventType is the underlying type for all audit log event name constants.
type EventType string

// When adding a constant here, also add it to AllEvents() and to the cases
// table in TestEventConstants_ExactValues. All three must stay in sync.
const (
	// EventRegister is emitted when a new user account is created.
	EventRegister EventType = "register"

	// EventRegisterFailed is emitted when a registration attempt is rejected
	// due to a duplicate email address or input validation failure.
	EventRegisterFailed EventType = "register_failed"

	// EventEmailVerified is emitted when a user successfully verifies their
	// email address using an OTP code.
	EventEmailVerified EventType = "email_verified"

	// EventVerifyAttemptFailed is emitted when a user submits an incorrect OTP
	// code during email verification. This constant also covers the expired-token
	// path; no separate expiry event is emitted by design.
	EventVerifyAttemptFailed EventType = "verify_attempt_failed"

	// EventAccountLocked is emitted by IncrementAttemptsTx when the OTP attempt
	// threshold is reached across any OTP flow (email_verification,
	// account_unlock, or password_reset).
	EventAccountLocked EventType = "account_locked"

	// EventAccountUnlocked is emitted when UnlockAccountTx clears the account lock
	// state (is_locked, failed_login_attempts, login_locked_until).
	EventAccountUnlocked EventType = "account_unlocked"

	// EventResendVerification is emitted when a new email verification token is
	// issued because the user requested a resend.
	EventResendVerification EventType = "resend_verification"

	// EventLogin is emitted when a user successfully authenticates and a new
	// session is created.
	EventLogin EventType = "login"

	// EventLoginFailed is emitted when a login attempt fails (wrong password,
	// unverified email, inactive account, or locked account).
	EventLoginFailed EventType = "login_failed"

	// EventLoginLockout is emitted by IncrementLoginFailuresTx when the
	// failed-login threshold is reached and login_locked_until is set to a future
	// time; this is a time-limited lockout distinct from the permanent is_locked
	// flag set by LockAccount.
	EventLoginLockout EventType = "login_lockout"

	// EventLogout is emitted when a user explicitly ends a session.
	EventLogout EventType = "logout"

	// EventTokenRefreshed is emitted when a refresh token is successfully
	// rotated and a new access token is issued.
	EventTokenRefreshed EventType = "token_refreshed"

	// EventRefreshFailed is emitted when a token-refresh attempt is rejected
	// because the token was not found, has expired, or its family has been revoked.
	EventRefreshFailed EventType = "refresh_failed"

	// EventTokenFamilyRevoked is emitted when an entire refresh-token family is
	// invalidated after a reuse-detection event (ADR-011).
	EventTokenFamilyRevoked EventType = "token_family_revoked"

	// EventUnlockRequested is emitted when a user initiates the self-service
	// account-unlock flow and an unlock OTP is sent.
	EventUnlockRequested EventType = "unlock_requested"

	// EventUnlockConfirmed is emitted when ConsumeUnlockTokenTx marks the unlock
	// OTP token as used (code was correct, token row consumed).
	EventUnlockConfirmed EventType = "unlock_confirmed"

	// EventUnlockAttemptFailed is emitted when a user submits an incorrect OTP
	// code during the account-unlock flow. This constant also covers the
	// expired-token path; no separate expiry event is emitted by design.
	EventUnlockAttemptFailed EventType = "unlock_attempt_failed"

	// EventPasswordResetRequested is emitted when a user initiates a
	// password-reset flow and a reset OTP is sent.
	EventPasswordResetRequested EventType = "password_reset_requested"

	// EventPasswordResetConfirmed is emitted when a user successfully consumes
	// a password-reset OTP.
	EventPasswordResetConfirmed EventType = "password_reset_confirmed"

	// EventPasswordResetAttemptFailed is emitted when a user submits an incorrect
	// OTP code during the password-reset flow. This constant also covers the
	// expired-token path; no separate expiry event is emitted by design.
	EventPasswordResetAttemptFailed EventType = "password_reset_attempt_failed"

	// EventPasswordResetCodeVerified is emitted when a user successfully verifies
	// a password-reset OTP code via POST /verify-reset-code. The OTP is not
	// consumed at this point — it is consumed when POST /reset-password completes.
	EventPasswordResetCodeVerified EventType = "password_reset_code_verified"

	// EventPasswordChanged is emitted when a user's password hash is updated
	// and all active sessions are revoked.
	EventPasswordChanged EventType = "password_changed"

	// EventPasswordChangeFailed is emitted when a change-password request is
	// rejected because the supplied current password is wrong.
	EventPasswordChangeFailed EventType = "password_change_failed"

	// EventSessionRevoked is emitted when a specific user session is explicitly
	// terminated by the account owner.
	EventSessionRevoked EventType = "session_revoked"

	// EventAllSessionsRevoked is emitted when every active session for a user is
	// terminated in a single operation (sign-out-everywhere).
	EventAllSessionsRevoked EventType = "all_sessions_revoked"

	// EventProfileUpdated is emitted when a user successfully updates their
	// display_name and/or avatar_url via PATCH /me/profile.
	EventProfileUpdated EventType = "profile_updated"

	// EventPasswordSet is emitted when an OAuth-only user successfully adds a
	// password to their account via POST /set-password.
	EventPasswordSet EventType = "password_set"

	// EventUsernameChanged is emitted when a user successfully updates their username.
	EventUsernameChanged EventType = "username_changed"

	// EventEmailChangeRequested is emitted when a user initiates an email change
	// and an OTP is sent to their current email address.
	EventEmailChangeRequested EventType = "email_change_requested"

	// EventEmailChangeVerifyAttemptFailed is emitted when a user submits an
	// incorrect OTP code during step 2 of the email-change flow (verify current
	// email). Records the failed attempt for security audit trails.
	EventEmailChangeVerifyAttemptFailed EventType = "email_change_verify_attempt_failed"

	// EventEmailChangeConfirmAttemptFailed is emitted when a user submits an
	// incorrect OTP code during step 3 of the email-change flow (confirm new
	// email). Records the failed attempt for security audit trails.
	EventEmailChangeConfirmAttemptFailed EventType = "email_change_confirm_attempt_failed"

	// EventEmailChangeCurrentVerified is emitted when a user successfully verifies
	// their current email OTP and receives a grant token for step 3.
	EventEmailChangeCurrentVerified EventType = "email_change_current_verified"

	// EventEmailChanged is emitted when a user's email address is successfully updated.
	// The metadata field contains old_email and new_email.
	EventEmailChanged EventType = "email_changed"

	// EventOAuthLogin is emitted when a user successfully authenticates or registers
	// via an OAuth provider. The metadata field contains provider and new_user (bool).
	EventOAuthLogin EventType = "oauth_login"

	// EventOAuthLinked is emitted when an OAuth identity is linked to an existing
	// authenticated account (link mode). The metadata field contains provider.
	EventOAuthLinked EventType = "oauth_linked"

	// EventOAuthUnlinked is emitted when an OAuth identity is removed from a user
	// account via DELETE /oauth/{provider}/unlink.
	EventOAuthUnlinked EventType = "oauth_unlinked"

	// EventAccountDeletionRequested is emitted inside ScheduleDeletionTx after
	// deleted_at is stamped. Written with context.WithoutCancel so a client
	// disconnect cannot abort the write.
	EventAccountDeletionRequested EventType = "account_deletion_requested"

	// EventAccountDeletionOTPRequested is emitted inside SendDeletionOTPTx after
	// the account_deletion OTP token is created and before the email is dispatched.
	EventAccountDeletionOTPRequested EventType = "account_deletion_otp_requested"

	// EventAccountDeletionCancelled is emitted inside CancelDeletionTx after
	// deleted_at is cleared. Written with context.WithoutCancel.
	EventAccountDeletionCancelled EventType = "account_deletion_cancelled"

	// EventAccountDeletionOTPFailed is emitted by IncrementAttemptsTx when the
	// user submits an incorrect OTP code during the email-deletion flow (Path B-2).
	EventAccountDeletionOTPFailed EventType = "account_deletion_otp_failed"

	// EventOwnerAssigned is emitted by AssignOwnerTx when the first owner role
	// assignment is successfully committed via POST /owner/assign. This is an
	// irreversible privilege escalation and must always be present in the audit trail.
	EventOwnerAssigned EventType = "owner_assigned"

	// EventOwnerTransferInitiated is emitted when the current owner initiates an
	// ownership transfer to a target user. Written with context.WithoutCancel.
	EventOwnerTransferInitiated EventType = "owner_transfer_initiated"

	// EventOwnerTransferAccepted is emitted when the target user accepts ownership.
	// The metadata field contains previous_owner_id and new_owner_id.
	// Written with context.WithoutCancel inside AcceptTransferTx.
	EventOwnerTransferAccepted EventType = "owner_transfer_accepted"

	// EventOwnerTransferCancelled is emitted when the initiating owner cancels the
	// pending transfer. Written with context.WithoutCancel.
	EventOwnerTransferCancelled EventType = "owner_transfer_cancelled"

	// ── Bitcoin payment domain ───────────────────────────────────────────────────────────────────────

	// EventBitcoinAddressWatched: at least one new address successfully registered
	// (added_count > 0). Re-registration of existing addresses is silent.
	EventBitcoinAddressWatched EventType = "bitcoin_address_watched"

	// EventBitcoinTxDetected: watched address appeared in a new mempool or confirmed tx.
	EventBitcoinTxDetected EventType = "bitcoin_tx_detected"

	// EventBitcoinSSETokenIssued: POST /bitcoin/events/token successfully created a
	// one-time SSE token. Metadata: userID, sha256(jti), exp, sourceIP.
	EventBitcoinSSETokenIssued EventType = "bitcoin_sse_token_issued"

	// EventBitcoinSSETokenConsumeFailure: GET /bitcoin/events rejected a token at
	// the token-validation layer. Metadata: reason (already_used | ip_mismatch |
	// sid_mismatch | expired), partial sha256(jti), sourceIP.
	// IMPORTANT: capacity-limit rejections use EventBitcoinSSECapExceeded, NOT this
	// event. Mixing them corrupts security analytics.
	EventBitcoinSSETokenConsumeFailure EventType = "bitcoin_sse_token_consume_failure"

	// EventBitcoinSSECapExceeded: GET /bitcoin/events rejected because a capacity
	// ceiling was reached. Metadata: reason (user_cap | process_cap), userID, sourceIP.
	// Separate from EventBitcoinSSETokenConsumeFailure so capacity events do not
	// trigger replay-detection alerts.
	EventBitcoinSSECapExceeded EventType = "bitcoin_sse_cap_exceeded"

	// EventBitcoinSSEConnected: SSE stream successfully established.
	// Metadata: userID, sourceIP.
	EventBitcoinSSEConnected EventType = "bitcoin_sse_connected"

	// EventBitcoinSSEDisconnected: SSE stream closed for any reason (client disconnect,
	// write error, ping failure, ctx cancellation). Written via doCleanup() using
	// context.Background() — never the cancelled handler context.
	// Metadata: userID, sourceIP, durationMs.
	EventBitcoinSSEDisconnected EventType = "bitcoin_sse_disconnected"

	// EventBitcoinRedisFallback: a Bitcoin domain Redis operation failed and the
	// system entered degraded mode. Written to stdout JSON AND BTC_FALLBACK_AUDIT_LOG.
	// Metadata: operation, error summary.
	EventBitcoinRedisFallback EventType = "bitcoin_redis_fallback"

	// EventBitcoinInvoiceReorgAdminRequired: a blockchain reorg affected an invoice
	// whose funds have already been swept. Admin must verify whether the sweep tx was
	// also reversed. Metadata: invoice_id, previous_status.
	EventBitcoinInvoiceReorgAdminRequired EventType = "bitcoin_invoice_reorg_admin_required"

	// EventBitcoinWatchLimitExceeded: POST /watch rejected because the per-user address
	// cap or 7-day registration window was reached. Metadata: userID, sourceIP,
	// reason (count_cap | registration_window_expired).
	EventBitcoinWatchLimitExceeded EventType = "bitcoin_watch_limit_exceeded"

	// EventBitcoinWatchRateLimitHit: POST /watch rejected by IP rate limiter.
	// Metadata: sourceIP.
	EventBitcoinWatchRateLimitHit EventType = "bitcoin_watch_rate_limit_hit"

	// EventBitcoinWatchInvalidAddress: POST /watch rejected because an address failed
	// validateAndNormalise. Metadata: userID, sourceIP, address_count,
	// invalid_address_hmac (HMAC-SHA256(BTC_AUDIT_HMAC_KEY, invalidAddr) — cross-event
	// correlation without retaining raw address PII).
	// IMPORTANT: separate from EventBitcoinWatchLimitExceeded — do not conflate
	// format-validation failures with cap-limit hits in security analytics.
	EventBitcoinWatchInvalidAddress EventType = "bitcoin_watch_invalid_address"

	// EventBitcoinSSEAuditWriteFailure: written to fallback log only when audit.Write
	// fails inside doCleanup for EventBitcoinSSEDisconnected. Enables detection of
	// audit trail gaps via bitcoin_audit_write_failures_total metric and fallback log.
	EventBitcoinSSEAuditWriteFailure EventType = "bitcoin_sse_audit_write_failure"
)

// AllEvents returns a slice of every audit event constant defined in this package.
func AllEvents() []EventType {
	return []EventType{
		EventRegister,
		EventRegisterFailed,
		EventEmailVerified,
		EventVerifyAttemptFailed,
		EventAccountLocked,
		EventAccountUnlocked,
		EventResendVerification,
		EventLogin,
		EventLoginFailed,
		EventLoginLockout,
		EventLogout,
		EventTokenRefreshed,
		EventRefreshFailed,
		EventTokenFamilyRevoked,
		EventUnlockRequested,
		EventUnlockConfirmed,
		EventUnlockAttemptFailed,
		EventPasswordResetRequested,
		EventPasswordResetConfirmed,
		EventPasswordResetAttemptFailed,
		EventPasswordResetCodeVerified,
		EventPasswordChanged,
		EventPasswordChangeFailed,
		EventSessionRevoked,
		EventAllSessionsRevoked,
		EventProfileUpdated,
		EventPasswordSet,
		EventUsernameChanged,
		EventEmailChangeRequested,
		EventEmailChangeCurrentVerified,
		EventEmailChanged,
		EventEmailChangeVerifyAttemptFailed,
		EventEmailChangeConfirmAttemptFailed,
		EventOAuthLogin,
		EventOAuthLinked,
		EventOAuthUnlinked,
		EventAccountDeletionRequested,
		EventAccountDeletionOTPRequested,
		EventAccountDeletionCancelled,
		EventAccountDeletionOTPFailed,
		EventOwnerAssigned,
		EventOwnerTransferInitiated,
		EventOwnerTransferAccepted,
		EventOwnerTransferCancelled,
		EventBitcoinAddressWatched,
		EventBitcoinTxDetected,
		EventBitcoinSSETokenIssued,
		EventBitcoinSSETokenConsumeFailure,
		EventBitcoinSSECapExceeded,
		EventBitcoinSSEConnected,
		EventBitcoinSSEDisconnected,
		EventBitcoinRedisFallback,
		EventBitcoinInvoiceReorgAdminRequired,
		EventBitcoinWatchLimitExceeded,
		EventBitcoinWatchRateLimitHit,
		EventBitcoinWatchInvalidAddress,
		EventBitcoinSSEAuditWriteFailure,
	}
}
