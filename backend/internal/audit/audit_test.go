package audit_test

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/stretchr/testify/require"
)

// allEvents is populated from audit.AllEvents so that adding a constant to
// audit.go automatically covers it in all enumeration tests below.
var allEvents = func() []struct {
	name  string
	value audit.EventType
} {
	evs := audit.AllEvents()
	out := make([]struct {
		name  string
		value audit.EventType
	}, len(evs))
	for i, ev := range evs {
		out[i] = struct {
			name  string
			value audit.EventType
		}{name: string(ev), value: ev}
	}
	return out
}()

// TestEventConstants_NonEmpty verifies that every constant has a non-empty
// value and that no value is composed entirely of whitespace.
// A blank or whitespace-only event name would silently corrupt the audit trail.
func TestEventConstants_NonEmpty(t *testing.T) {
	t.Parallel()

	for _, ev := range allEvents {
		t.Run(ev.name, func(t *testing.T) {
			t.Parallel()
			require.NotEmpty(t, ev.value, "event constant %s must not be empty", ev.name)
			require.NotEqual(t, strings.TrimSpace(string(ev.value)), "", "event constant %s must not be whitespace-only", ev.name)
		})
	}
}

// TestEventConstants_LowerSnakeCase verifies that every constant value
// matches the lower_snake_case format required by RULES.md §3.5.
// A value with wrong casing, a space, or a hyphen would violate the rule
// silently at runtime.
func TestEventConstants_LowerSnakeCase(t *testing.T) {
	t.Parallel()

	pattern := regexp.MustCompile(`^[a-z][a-z_]*[a-z]$`)
	for _, ev := range allEvents {
		t.Run(ev.name, func(t *testing.T) {
			t.Parallel()
			require.Regexp(t, pattern, string(ev.value),
				"event constant %s value %q must be lower_snake_case", ev.name, ev.value)
		})
	}
}

// TestEventConstants_Unique verifies that no two event constants share the
// same underlying string value.
// Duplicate values would make it impossible to distinguish between events in
// the audit log without reading application source code.
func TestEventConstants_Unique(t *testing.T) {
	t.Parallel()

	seen := make(map[string]string, len(allEvents)) // value → first constant name
	for _, ev := range allEvents {
		if first, exists := seen[string(ev.value)]; exists {
			t.Errorf("duplicate event value %q: shared by %s and %s", ev.value, first, ev.name)
		}
		seen[string(ev.value)] = ev.name
	}
}

// TestEventConstants_ExactValues verifies the exact string value of every
// constant. A rename that silently changes a persisted audit-trail value must
// fail this test so the developer knows to also migrate historical data.
//
// The count assertion at the top of this test enforces that AllEvents() is
// exhaustive: if a constant is added to audit.go and to this table but omitted
// from AllEvents(), len(allEvents) < len(cases) and the test fails.
// Conversely, if it is added to AllEvents() but forgotten here,
// len(allEvents) > len(cases) and the test also fails.
func TestEventConstants_ExactValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		constant audit.EventType
		want     string
	}{
		{audit.EventRegister, "register"},
		{audit.EventRegisterFailed, "register_failed"},
		{audit.EventEmailVerified, "email_verified"},
		{audit.EventVerifyAttemptFailed, "verify_attempt_failed"},
		{audit.EventAccountLocked, "account_locked"},
		{audit.EventAccountUnlocked, "account_unlocked"},
		{audit.EventResendVerification, "resend_verification"},
		{audit.EventLogin, "login"},
		{audit.EventLoginFailed, "login_failed"},
		{audit.EventLoginLockout, "login_lockout"},
		{audit.EventLogout, "logout"},
		{audit.EventTokenRefreshed, "token_refreshed"},
		{audit.EventRefreshFailed, "refresh_failed"},
		{audit.EventTokenFamilyRevoked, "token_family_revoked"},
		{audit.EventUnlockRequested, "unlock_requested"},
		{audit.EventUnlockConfirmed, "unlock_confirmed"},
		{audit.EventPasswordResetRequested, "password_reset_requested"},
		{audit.EventPasswordResetConfirmed, "password_reset_confirmed"},
		{audit.EventPasswordChanged, "password_changed"},
		{audit.EventPasswordChangeFailed, "password_change_failed"},
		{audit.EventSessionRevoked, "session_revoked"},
		{audit.EventAllSessionsRevoked, "all_sessions_revoked"},
		{audit.EventUnlockAttemptFailed, "unlock_attempt_failed"},
		{audit.EventPasswordResetAttemptFailed, "password_reset_attempt_failed"},
		{audit.EventPasswordResetCodeVerified, "password_reset_code_verified"},
		{audit.EventProfileUpdated, "profile_updated"},
		{audit.EventPasswordSet, "password_set"},
		{audit.EventUsernameChanged, "username_changed"},
		{audit.EventEmailChangeRequested, "email_change_requested"},
		{audit.EventEmailChangeCurrentVerified, "email_change_current_verified"},
		{audit.EventEmailChanged, "email_changed"},
		{audit.EventEmailChangeVerifyAttemptFailed, "email_change_verify_attempt_failed"},
		{audit.EventEmailChangeConfirmAttemptFailed, "email_change_confirm_attempt_failed"},
		{audit.EventOAuthLogin,    "oauth_login"},
		{audit.EventOAuthLinked,   "oauth_linked"},
		{audit.EventOAuthUnlinked, "oauth_unlinked"},
		{audit.EventAccountDeletionRequested,    "account_deletion_requested"},
		{audit.EventAccountDeletionOTPRequested, "account_deletion_otp_requested"},
		{audit.EventAccountDeletionCancelled,    "account_deletion_cancelled"},
		{audit.EventAccountDeletionOTPFailed,    "account_deletion_otp_failed"},
		{audit.EventOwnerAssigned,           "owner_assigned"},
		{audit.EventOwnerTransferInitiated,  "owner_transfer_initiated"},
		{audit.EventOwnerTransferAccepted,   "owner_transfer_accepted"},
		{audit.EventOwnerTransferCancelled,  "owner_transfer_cancelled"},
		{audit.EventBitcoinAddressWatched,            "bitcoin_address_watched"},
		{audit.EventBitcoinTxDetected,                "bitcoin_tx_detected"},
		{audit.EventBitcoinSSETokenIssued,            "bitcoin_sse_token_issued"},
		{audit.EventBitcoinSSETokenConsumeFailure,    "bitcoin_sse_token_consume_failure"},
		{audit.EventBitcoinSSECapExceeded,            "bitcoin_sse_cap_exceeded"},
		{audit.EventBitcoinSSEConnected,              "bitcoin_sse_connected"},
		{audit.EventBitcoinSSEDisconnected,           "bitcoin_sse_disconnected"},
		{audit.EventBitcoinRedisFallback,             "bitcoin_redis_fallback"},
		{audit.EventBitcoinInvoiceReorgAdminRequired, "bitcoin_invoice_reorg_admin_required"},
		{audit.EventBitcoinWatchLimitExceeded,        "bitcoin_watch_limit_exceeded"},
		{audit.EventBitcoinWatchRateLimitHit,         "bitcoin_watch_rate_limit_hit"},
		{audit.EventBitcoinWatchInvalidAddress,       "bitcoin_watch_invalid_address"},
		{audit.EventBitcoinSSEAuditWriteFailure,      "bitcoin_sse_audit_write_failure"},
	}

	// Map-based exhaustiveness check: names the missing constant rather than
	// printing a count mismatch.
	caseMap := make(map[audit.EventType]string, len(cases))
	for _, c := range cases {
		caseMap[c.constant] = c.want
	}

	// Every constant in AllEvents() must have a test case.
	for _, ev := range audit.AllEvents() {
		expected, ok := caseMap[ev]
		require.True(t, ok,
			"audit constant %q is in AllEvents() but missing from test cases — add it to TestEventConstants_ExactValues", ev)
		require.Equal(t, string(expected), string(ev),
			"constant value changed — historical audit rows use the old value; add a data migration before changing it")
	}

	// Every test case must be in AllEvents().
	allSet := make(map[audit.EventType]struct{}, len(audit.AllEvents()))
	for _, ev := range audit.AllEvents() {
		allSet[ev] = struct{}{}
	}
	for _, c := range cases {
		_, ok := allSet[c.constant]
		require.True(t, ok,
			"test case for %q is not in AllEvents() — remove it or add to AllEvents()", c.constant)
	}
}

// TestEventType_IsNamedType verifies that EventType is a named string type,
// not a type alias. A type alias (type EventType = string) would allow any
// string to be assigned to an EventType field, defeating the compile-time
// protection described in ADR-008.
func TestEventType_IsNamedType(t *testing.T) {
	t.Parallel()

	et := reflect.TypeOf(audit.EventType(""))
	require.Equal(t, "EventType", et.Name(),
		"EventType must be a named type, not a type alias")
	require.Equal(t, reflect.String, et.Kind())
	require.NotEqual(t, reflect.TypeOf(""), et,
		"EventType must not be identical to the built-in string type")
}
