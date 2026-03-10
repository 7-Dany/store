package deleteaccount_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	deleteaccount "github.com/7-Dany/store/backend/internal/domain/profile/delete-account"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

const testUserID = "00000000-0000-0000-0000-000000000001"

// newFakeHandler builds a Handler wired with the provided fake servicer.
// mailer/mailQueue are nil; enqueueEmail no-ops so Path B-1 tests can still
// assert on the 202 response body without a real mail backend.
// otpTTL is set to 15 minutes — matching the production default — so
// tests that check the Path B-1 202 body can assert on expires_in:900.
func newFakeHandler(svc deleteaccount.Servicer) *deleteaccount.Handler {
	return deleteaccount.NewHandler(svc, nil, nil, 0, 15*time.Minute)
}

// deleteJSONWithUserID fires a DELETE with a fake user ID injected into the
// request context.
func deleteJSONWithUserID(h http.HandlerFunc, userID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, "/me", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := token.InjectUserIDForTest(req.Context(), userID)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

// deleteJSON fires a DELETE without a user ID (no JWT).
func deleteJSON(h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, "/me", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

// postJSONWithUserID fires a POST with a fake user ID injected into the context.
func postJSONWithUserID(h http.HandlerFunc, userID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/me/cancel-deletion", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := token.InjectUserIDForTest(req.Context(), userID)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

// getWithUserID fires a GET with a fake user ID injected into the context.
func getWithUserID(h http.HandlerFunc, userID, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	ctx := token.InjectUserIDForTest(req.Context(), userID)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

// getNoUser fires a GET without a user ID (no JWT).
func getNoUser(h http.HandlerFunc, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

// postJSON fires a POST without a user ID (no JWT).
func postJSON(h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/me/cancel-deletion", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

// ── TestHandler_Delete ────────────────────────────────────────────────────────

func TestHandler_Delete(t *testing.T) {
	t.Parallel()

	// ── T-17: No JWT → 401 ───────────────────────────────────────────────────

	t.Run("T-17: no JWT returns 401 unauthorized", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{}
		w := deleteJSON(newFakeHandler(svc).Delete, `{}`)
		require.Equal(t, http.StatusUnauthorized, w.Code)
		require.Contains(t, w.Body.String(), "unauthorized")
	})

	// ── T-08: ErrInvalidCredentials → 401 ──────────────────────────────────────

	t.Run("T-08: wrong password returns 401 invalid_credentials", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{
			DeleteWithPasswordFn: func(_ context.Context, _ deleteaccount.DeleteWithPasswordInput) (deleteaccount.DeletionScheduled, error) {
				return deleteaccount.DeletionScheduled{}, authshared.ErrInvalidCredentials
			},
		}
		w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{"password":"wrongpass"}`)
		require.Equal(t, http.StatusUnauthorized, w.Code)
		require.Contains(t, w.Body.String(), "invalid_credentials")
	})

	// ── T-06: ErrAlreadyPendingDeletion → 409 ────────────────────────────────

	t.Run("T-06: already pending deletion returns 409 already_pending_deletion", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{
			DeleteWithPasswordFn: func(_ context.Context, _ deleteaccount.DeleteWithPasswordInput) (deleteaccount.DeletionScheduled, error) {
				return deleteaccount.DeletionScheduled{}, deleteaccount.ErrAlreadyPendingDeletion
			},
		}
		w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{"password":"Str0ng!Pass"}`)
		require.Equal(t, http.StatusConflict, w.Code)
		require.Contains(t, w.Body.String(), "already_pending_deletion")
	})

	// ── Empty-body: ResolveUserForDeletion → ErrAlreadyPendingDeletion → 409 ───

	t.Run("empty body with pending account returns 409 already_pending_deletion", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{
			ResolveUserForDeletionFn: func(_ context.Context, _ string) (deleteaccount.DeletionUser, deleteaccount.UserAuthMethods, error) {
				return deleteaccount.DeletionUser{}, deleteaccount.UserAuthMethods{}, deleteaccount.ErrAlreadyPendingDeletion
			},
		}
		w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{}`)
		require.Equal(t, http.StatusConflict, w.Code)
		require.Contains(t, w.Body.String(), "already_pending_deletion")
	})

	// ── T-20: Success includes scheduled_deletion_at ──────────────────────────

	t.Run("T-20: success response includes scheduled_deletion_at", func(t *testing.T) {
		t.Parallel()
		scheduledAt := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
		svc := &authsharedtest.DeleteAccountFakeServicer{
			DeleteWithPasswordFn: func(_ context.Context, _ deleteaccount.DeleteWithPasswordInput) (deleteaccount.DeletionScheduled, error) {
				return deleteaccount.DeletionScheduled{ScheduledDeletionAt: scheduledAt}, nil
			},
		}
		w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{"password":"Str0ng!Pass"}`)
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), "scheduled_deletion_at")
		require.Contains(t, w.Body.String(), "2026-04-08")
	})

	// ── T-07: Password account, no password field → 400 ──────────────────────

	t.Run("T-07: password account with no password field returns 400 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{
			ResolveUserForDeletionFn: func(_ context.Context, _ string) (deleteaccount.DeletionUser, deleteaccount.UserAuthMethods, error) {
				return deleteaccount.DeletionUser{},
					deleteaccount.UserAuthMethods{HasPassword: true},
					nil
			},
		}
		w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{}`)
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── T-04: Telegram-only user step 1 → 202 with auth_method:telegram ──────

	t.Run("T-04: Telegram-only user step 1 returns 202 with auth_method telegram", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{
			ResolveUserForDeletionFn: func(_ context.Context, _ string) (deleteaccount.DeletionUser, deleteaccount.UserAuthMethods, error) {
				return deleteaccount.DeletionUser{Email: nil},
					deleteaccount.UserAuthMethods{HasPassword: false, IdentityCount: 1},
					nil
			},
		}
		w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{}`)
		require.Equal(t, http.StatusAccepted, w.Code)
		require.Contains(t, w.Body.String(), `"auth_method":"telegram"`)
	})

	// ── T-09: OTP wrong format → 422 ─────────────────────────────────────────

	t.Run("T-09: OTP code with invalid format returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{
			ConfirmEmailDeletionFn: func(_ context.Context, _ deleteaccount.ConfirmOTPDeletionInput) (deleteaccount.DeletionScheduled, error) {
				return deleteaccount.DeletionScheduled{}, authshared.ErrCodeInvalidFormat
			},
		}
		w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{"code":"abc"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── T-14: Telegram HMAC fails → 401 ──────────────────────────────────────

	t.Run("T-14: Telegram HMAC failure returns 401 invalid_telegram_auth", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{
			ConfirmTelegramDeletionFn: func(_ context.Context, _ deleteaccount.ConfirmTelegramDeletionInput) (deleteaccount.DeletionScheduled, error) {
				return deleteaccount.DeletionScheduled{}, deleteaccount.ErrInvalidTelegramAuth
			},
		}
		body := `{"telegram_auth":{"id":12345,"first_name":"Test","auth_date":1234567890,"hash":"badhash"}}`
		w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, body)
		require.Equal(t, http.StatusUnauthorized, w.Code)
		require.Contains(t, w.Body.String(), "invalid_telegram_auth")
	})

	// ── T-15: auth_date too old → 401 ────────────────────────────────────────

	t.Run("T-15: auth_date too old returns 401 invalid_telegram_auth", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{
			ConfirmTelegramDeletionFn: func(_ context.Context, _ deleteaccount.ConfirmTelegramDeletionInput) (deleteaccount.DeletionScheduled, error) {
				return deleteaccount.DeletionScheduled{}, deleteaccount.ErrInvalidTelegramAuth
			},
		}
		body := `{"telegram_auth":{"id":12345,"first_name":"Test","auth_date":1000000000,"hash":"anyhash"}}`
		w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, body)
		require.Equal(t, http.StatusUnauthorized, w.Code)
		require.Contains(t, w.Body.String(), "invalid_telegram_auth")
	})

	// ── Unexpected service error → 500 ────────────────────────────────────────

	t.Run("unexpected service error returns 500 internal_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{
			DeleteWithPasswordFn: func(_ context.Context, _ deleteaccount.DeleteWithPasswordInput) (deleteaccount.DeletionScheduled, error) {
				return deleteaccount.DeletionScheduled{}, errors.New("db: connection refused")
			},
		}
		w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{"password":"Str0ng!Pass"}`)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "internal_error")
	})

	// ── Malformed JSON → 400 ─────────────────────────────────────────────────

	t.Run("malformed JSON returns 400", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{}
		w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{"password":`)
		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	// ── Body over MaxBodyBytes → 413 ──────────────────────────────────────────

	t.Run("body over MaxBodyBytes returns 413", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{}
		oversized := strings.Repeat("a", int(respond.MaxBodyBytes)+1)
		w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, oversized)
		require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	})

	// ── T-10: ErrTokenNotFound → 422 ──────────────────────────────────────────

	t.Run("T-10: token not found returns 422 token_not_found", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{
			ConfirmEmailDeletionFn: func(_ context.Context, _ deleteaccount.ConfirmOTPDeletionInput) (deleteaccount.DeletionScheduled, error) {
				return deleteaccount.DeletionScheduled{}, authshared.ErrTokenNotFound
			},
		}
		w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{"code":"123456"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "token_not_found")
	})

	// ── T-11: ErrTokenAlreadyUsed → 422 ───────────────────────────────────────

	t.Run("T-11: token already used returns 422 token_already_used", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{
			ConfirmEmailDeletionFn: func(_ context.Context, _ deleteaccount.ConfirmOTPDeletionInput) (deleteaccount.DeletionScheduled, error) {
				return deleteaccount.DeletionScheduled{}, authshared.ErrTokenAlreadyUsed
			},
		}
		w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{"code":"123456"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "token_already_used")
	})

	// ── T-12: ErrInvalidCode → 422 ────────────────────────────────────────────

	t.Run("T-12: invalid OTP code returns 422 invalid_code", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{
			ConfirmEmailDeletionFn: func(_ context.Context, _ deleteaccount.ConfirmOTPDeletionInput) (deleteaccount.DeletionScheduled, error) {
				return deleteaccount.DeletionScheduled{}, authshared.ErrInvalidCode
			},
		}
		w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{"code":"123456"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "invalid_code")
	})

	// ── T-13: ErrTooManyAttempts → 429 ────────────────────────────────────────

	t.Run("T-13: too many OTP attempts returns 429 too_many_attempts", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{
			ConfirmEmailDeletionFn: func(_ context.Context, _ deleteaccount.ConfirmOTPDeletionInput) (deleteaccount.DeletionScheduled, error) {
				return deleteaccount.DeletionScheduled{}, authshared.ErrTooManyAttempts
			},
		}
		w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{"code":"123456"}`)
		require.Equal(t, http.StatusTooManyRequests, w.Code)
		require.Contains(t, w.Body.String(), "too_many_attempts")
	})

	// ── T-16: ErrTelegramIdentityMismatch → 401 ───────────────────────────────

	t.Run("T-16: Telegram identity mismatch returns 401 telegram_identity_mismatch", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{
			ConfirmTelegramDeletionFn: func(_ context.Context, _ deleteaccount.ConfirmTelegramDeletionInput) (deleteaccount.DeletionScheduled, error) {
				return deleteaccount.DeletionScheduled{}, deleteaccount.ErrTelegramIdentityMismatch
			},
		}
		body := `{"telegram_auth":{"id":99999,"first_name":"Evil","auth_date":9999999999,"hash":"anyhash"}}`
		w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, body)
		require.Equal(t, http.StatusUnauthorized, w.Code)
		require.Contains(t, w.Body.String(), "telegram_identity_mismatch")
	})

	// ── ResolveUserForDeletion error → 500 ────────────────────────────────────

	t.Run("ResolveUserForDeletion service error returns 500 internal_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{
			ResolveUserForDeletionFn: func(_ context.Context, _ string) (deleteaccount.DeletionUser, deleteaccount.UserAuthMethods, error) {
				return deleteaccount.DeletionUser{}, deleteaccount.UserAuthMethods{}, errors.New("db: timeout")
			},
		}
		w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{}`)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "internal_error")
	})

	// ── T-02: Path B-1 happy path → 202 ──────────────────────────────────────

	t.Run("T-02: email user step 1 returns 202 with auth_method email_otp", func(t *testing.T) {
		t.Parallel()
		userEmail := "user@example.com"
		svc := &authsharedtest.DeleteAccountFakeServicer{
			ResolveUserForDeletionFn: func(_ context.Context, _ string) (deleteaccount.DeletionUser, deleteaccount.UserAuthMethods, error) {
				return deleteaccount.DeletionUser{Email: &userEmail},
					deleteaccount.UserAuthMethods{HasPassword: false},
					nil
			},
			InitiateEmailDeletionFn: func(_ context.Context, _ deleteaccount.ScheduleDeletionInput) (authshared.OTPIssuanceResult, error) {
				return authshared.OTPIssuanceResult{UserID: testUserID, Email: userEmail, RawCode: "123456"}, nil
			},
		}
		w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{}`)
		require.Equal(t, http.StatusAccepted, w.Code)
		require.Contains(t, w.Body.String(), "verification code sent")
		require.Contains(t, w.Body.String(), `"auth_method":"email_otp"`,
			"Path B-1 202 must include auth_method:email_otp so the client knows which UI to render")
		require.Contains(t, w.Body.String(), `"expires_in":900`,
			"Path B-1 202 must include expires_in so the client can render a countdown timer")
	})

	// T-B-multiauth: multi-linked user (email + Telegram, no password) routes to email_otp

	t.Run("T-B-multiauth: email+Telegram user with no password routes to email_otp not telegram", func(t *testing.T) {
		t.Parallel()
		userEmail := "multi@example.com"
		svc := &authsharedtest.DeleteAccountFakeServicer{
			ResolveUserForDeletionFn: func(_ context.Context, _ string) (deleteaccount.DeletionUser, deleteaccount.UserAuthMethods, error) {
				// IdentityCount=2: both OAuth email and Telegram linked, no password.
				return deleteaccount.DeletionUser{Email: &userEmail},
					deleteaccount.UserAuthMethods{HasPassword: false, IdentityCount: 2},
					nil
			},
			InitiateEmailDeletionFn: func(_ context.Context, _ deleteaccount.ScheduleDeletionInput) (authshared.OTPIssuanceResult, error) {
				return authshared.OTPIssuanceResult{UserID: testUserID, Email: userEmail, RawCode: "654321"}, nil
			},
		}
		w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{}`)
		require.Equal(t, http.StatusAccepted, w.Code)
		// email takes priority over Telegram when both are linked (D-11 priority rule).
		require.Contains(t, w.Body.String(), `"auth_method":"email_otp"`)
		require.NotContains(t, w.Body.String(), `"auth_method":"telegram"`)
	})

	// ── Path B-1 InitiateEmailDeletion error → 500 ────────────────────────────

	t.Run("InitiateEmailDeletion error returns 500 internal_error", func(t *testing.T) {
		t.Parallel()
		userEmail := "user@example.com"
		svc := &authsharedtest.DeleteAccountFakeServicer{
			ResolveUserForDeletionFn: func(_ context.Context, _ string) (deleteaccount.DeletionUser, deleteaccount.UserAuthMethods, error) {
				return deleteaccount.DeletionUser{Email: &userEmail},
					deleteaccount.UserAuthMethods{HasPassword: false},
					nil
			},
			InitiateEmailDeletionFn: func(_ context.Context, _ deleteaccount.ScheduleDeletionInput) (authshared.OTPIssuanceResult, error) {
				return authshared.OTPIssuanceResult{}, errors.New("store: insert failed")
			},
		}
		w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{}`)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "internal_error")
	})

	// ── T-03: Path B-2 happy path → 200 ──────────────────────────────────────

	t.Run("T-03: OTP confirm happy path returns 200 with scheduled_deletion_at", func(t *testing.T) {
		t.Parallel()
		scheduledAt := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
		svc := &authsharedtest.DeleteAccountFakeServicer{
			ConfirmEmailDeletionFn: func(_ context.Context, _ deleteaccount.ConfirmOTPDeletionInput) (deleteaccount.DeletionScheduled, error) {
				return deleteaccount.DeletionScheduled{ScheduledDeletionAt: scheduledAt}, nil
			},
		}
		w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{"code":"123456"}`)
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), "scheduled_deletion_at")
		require.Contains(t, w.Body.String(), "2026-05-01")
	})

	// ── T-05: Path C-2 happy path → 200 ──────────────────────────────────────

	t.Run("T-05: Telegram confirm happy path returns 200 with scheduled_deletion_at", func(t *testing.T) {
		t.Parallel()
		scheduledAt := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
		svc := &authsharedtest.DeleteAccountFakeServicer{
			ConfirmTelegramDeletionFn: func(_ context.Context, _ deleteaccount.ConfirmTelegramDeletionInput) (deleteaccount.DeletionScheduled, error) {
				return deleteaccount.DeletionScheduled{ScheduledDeletionAt: scheduledAt}, nil
			},
		}
		body := `{"telegram_auth":{"id":12345,"first_name":"Test","auth_date":9999999999,"hash":"validhash"}}`
		w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, body)
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), "scheduled_deletion_at")
	})
}

// ── TestHandler_CancelDeletion ────────────────────────────────────────────────

func TestHandler_CancelDeletion(t *testing.T) {
	t.Parallel()

	// ── T-27: Happy path → 200 ────────────────────────────────────────────────

	t.Run("T-27: cancel happy path returns 200", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{} // CancelDeletionFn nil → nil
		w := postJSONWithUserID(newFakeHandler(svc).CancelDeletion, testUserID, `{}`)
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), "account deletion cancelled")
	})

	// ── T-28: Not pending → 409 ───────────────────────────────────────────────

	t.Run("T-28: not pending deletion returns 409 not_pending_deletion", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{
			CancelDeletionFn: func(_ context.Context, _ deleteaccount.CancelDeletionInput) error {
				return deleteaccount.ErrNotPendingDeletion
			},
		}
		w := postJSONWithUserID(newFakeHandler(svc).CancelDeletion, testUserID, `{}`)
		require.Equal(t, http.StatusConflict, w.Code)
		require.Contains(t, w.Body.String(), "not_pending_deletion")
	})

	// ── T-29: No JWT → 401 ────────────────────────────────────────────────────

	t.Run("T-29: no JWT on cancel returns 401 unauthorized", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{}
		w := postJSON(newFakeHandler(svc).CancelDeletion, `{}`)
		require.Equal(t, http.StatusUnauthorized, w.Code)
		require.Contains(t, w.Body.String(), "unauthorized")
	})

	// ── Cancel body over MaxBodyBytes → 413 ────────────────────────────────────

	t.Run("cancel body over MaxBodyBytes returns 413", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{}
		oversized := strings.Repeat("a", int(respond.MaxBodyBytes)+1)
		w := postJSONWithUserID(newFakeHandler(svc).CancelDeletion, testUserID, oversized)
		require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	})

	// ── Cancel malformed JSON → 400 ────────────────────────────────────────────

	t.Run("cancel malformed JSON returns 400", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{}
		w := postJSONWithUserID(newFakeHandler(svc).CancelDeletion, testUserID, `{bad}`)
		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	// ── Unexpected service error → 500 ────────────────────────────────────────

	t.Run("unexpected cancel service error returns 500 internal_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{
			CancelDeletionFn: func(_ context.Context, _ deleteaccount.CancelDeletionInput) error {
				return errors.New("db: pool exhausted")
			},
		}
		w := postJSONWithUserID(newFakeHandler(svc).CancelDeletion, testUserID, `{}`)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "internal_error")
	})
}

// ── TestHandler_DeletionMethod ────────────────────────────────────────────

func TestHandler_DeletionMethod(t *testing.T) {
	t.Parallel()

	// ── Happy path: password user → 200 deletion_method:password ───────────────

	t.Run("password user returns 200 deletion_method password", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{
			GetDeletionMethodFn: func(_ context.Context, _ string) (deleteaccount.DeletionMethodResult, error) {
				return deleteaccount.DeletionMethodResult{Method: "password"}, nil
			},
		}
		w := getWithUserID(newFakeHandler(svc).DeletionMethod, testUserID, "/me/deletion-method")
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), `"deletion_method":"password"`)
	})

	// ── Happy path: email-OTP user → 200 deletion_method:email_otp ────────────

	t.Run("email-OTP user returns 200 deletion_method email_otp", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{
			GetDeletionMethodFn: func(_ context.Context, _ string) (deleteaccount.DeletionMethodResult, error) {
				return deleteaccount.DeletionMethodResult{Method: "email_otp"}, nil
			},
		}
		w := getWithUserID(newFakeHandler(svc).DeletionMethod, testUserID, "/me/deletion-method")
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), `"deletion_method":"email_otp"`)
	})

	// ── Happy path: Telegram-only user → 200 deletion_method:telegram ───────

	t.Run("Telegram-only user returns 200 deletion_method telegram", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{
			GetDeletionMethodFn: func(_ context.Context, _ string) (deleteaccount.DeletionMethodResult, error) {
				return deleteaccount.DeletionMethodResult{Method: "telegram"}, nil
			},
		}
		w := getWithUserID(newFakeHandler(svc).DeletionMethod, testUserID, "/me/deletion-method")
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), `"deletion_method":"telegram"`)
	})

	// ── No JWT → 401 ──────────────────────────────────────────────────

	t.Run("no JWT returns 401 unauthorized", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{}
		w := getNoUser(newFakeHandler(svc).DeletionMethod, "/me/deletion-method")
		require.Equal(t, http.StatusUnauthorized, w.Code)
		require.Contains(t, w.Body.String(), "unauthorized")
	})

	// ── ErrAlreadyPendingDeletion → 409 ───────────────────────────────

	t.Run("already pending deletion returns 409 already_pending_deletion", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{
			GetDeletionMethodFn: func(_ context.Context, _ string) (deleteaccount.DeletionMethodResult, error) {
				return deleteaccount.DeletionMethodResult{}, deleteaccount.ErrAlreadyPendingDeletion
			},
		}
		w := getWithUserID(newFakeHandler(svc).DeletionMethod, testUserID, "/me/deletion-method")
		require.Equal(t, http.StatusConflict, w.Code)
		require.Contains(t, w.Body.String(), "already_pending_deletion")
	})

	// ── Unexpected service error → 500 ───────────────────────────────

	t.Run("service error returns 500 internal_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.DeleteAccountFakeServicer{
			GetDeletionMethodFn: func(_ context.Context, _ string) (deleteaccount.DeletionMethodResult, error) {
				return deleteaccount.DeletionMethodResult{}, errors.New("db: connection refused")
			},
		}
		w := getWithUserID(newFakeHandler(svc).DeletionMethod, testUserID, "/me/deletion-method")
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "internal_error")
	})
}
