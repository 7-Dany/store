package email_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/domain/profile/email"
	profileshared "github.com/7-Dany/store/backend/internal/domain/profile/shared"
	"github.com/7-Dany/store/backend/internal/platform/mailer"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// ── test deps ─────────────────────────────────────────────────────────────────

// testDeps returns a minimal *app.Deps suitable for handler unit tests.
// Mail delivery is fire-and-forget: the queue is not started so no actual
// SMTP connections are ever made. MailDeliveryTimeout > 0 ensures that the
// context.WithTimeout call inside enqueueEmail produces a valid deadline.
func testDeps(t *testing.T) *app.Deps {
	t.Helper()
	m, err := mailer.NewWithAuth(mailer.Config{
		Host:    "localhost",
		Port:    25,
		From:    "noreply@example.com",
		AppName: "TestApp",
	}, nil /* no auth */)
	require.NoError(t, err)

	q := mailer.NewQueue()
	// Not calling q.Start() — jobs buffer without being delivered.
	t.Cleanup(func() { q.Shutdown() })

	return &app.Deps{
		Mailer:              m,
		MailQueue:           q,
		MailDeliveryTimeout: 5 * time.Second,
	}
}

// newTestHandler returns a Handler wired to svc and testDeps(t).
func newTestHandler(t *testing.T, svc email.Servicer) *email.Handler {
	t.Helper()
	return email.NewHandler(svc, testDeps(t))
}

// ── request helpers ───────────────────────────────────────────────────────────

func postWithUserID(h http.HandlerFunc, userID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := token.InjectUserIDForTest(req.Context(), userID)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

func postWithUserIDAndJTI(h http.HandlerFunc, userID, jti, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := token.InjectUserIDForTest(req.Context(), userID)
	ctx = token.InjectJTIForTest(ctx, jti)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

func postNoUserID(h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

const (
	handlerTestUID        = "00000000-0000-0000-0000-000000000001"
	handlerTestJTI        = "test-jti-abc123"
	validRequestChangeBody = `{"new_email":"new@example.com"}`
	validVerifyBody        = `{"code":"123456"}`
	validConfirmBody       = `{"grant_token":"550e8400-e29b-41d4-a716-446655440000","code":"123456"}`
)

// ── TestHandler_RequestChange ─────────────────────────────────────────────────

func TestHandler_RequestChange(t *testing.T) {
	t.Parallel()

	// ── Happy path → 200 ──────────────────────────────────────────────────────

	t.Run("success returns 200 with message", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			RequestEmailChangeFn: func(_ context.Context, _ email.EmailChangeRequestInput) (email.EmailChangeRequestResult, error) {
				return email.EmailChangeRequestResult{CurrentEmail: "current@example.com", RawCode: "123456"}, nil
			},
		}
		w := postWithUserID(newTestHandler(t, svc).RequestChange, handlerTestUID, validRequestChangeBody)
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), "verification code sent")
	})

	// ── ErrInvalidEmailFormat → 422 validation_error ──────────────────────────

	t.Run("ErrInvalidEmailFormat returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			RequestEmailChangeFn: func(_ context.Context, _ email.EmailChangeRequestInput) (email.EmailChangeRequestResult, error) {
				return email.EmailChangeRequestResult{}, email.ErrInvalidEmailFormat
			},
		}
		w := postWithUserID(newTestHandler(t, svc).RequestChange, handlerTestUID, validRequestChangeBody)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── ErrEmailTooLong → 422 validation_error ────────────────────────────────

	t.Run("ErrEmailTooLong returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			RequestEmailChangeFn: func(_ context.Context, _ email.EmailChangeRequestInput) (email.EmailChangeRequestResult, error) {
				return email.EmailChangeRequestResult{}, email.ErrEmailTooLong
			},
		}
		w := postWithUserID(newTestHandler(t, svc).RequestChange, handlerTestUID, validRequestChangeBody)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── ErrSameEmail → 422 same_email ────────────────────────────────────────

	t.Run("ErrSameEmail returns 422 same_email", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			RequestEmailChangeFn: func(_ context.Context, _ email.EmailChangeRequestInput) (email.EmailChangeRequestResult, error) {
				return email.EmailChangeRequestResult{}, email.ErrSameEmail
			},
		}
		w := postWithUserID(newTestHandler(t, svc).RequestChange, handlerTestUID, validRequestChangeBody)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "same_email")
	})

	// ── ErrEmailTaken → 409 email_taken ──────────────────────────────────────

	t.Run("ErrEmailTaken returns 409 email_taken", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			RequestEmailChangeFn: func(_ context.Context, _ email.EmailChangeRequestInput) (email.EmailChangeRequestResult, error) {
				return email.EmailChangeRequestResult{}, email.ErrEmailTaken
			},
		}
		w := postWithUserID(newTestHandler(t, svc).RequestChange, handlerTestUID, validRequestChangeBody)
		require.Equal(t, http.StatusConflict, w.Code)
		require.Contains(t, w.Body.String(), "email_taken")
	})

	// ── ErrCooldownActive → 429 cooldown_active ───────────────────────────────

	t.Run("ErrCooldownActive returns 429 cooldown_active", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			RequestEmailChangeFn: func(_ context.Context, _ email.EmailChangeRequestInput) (email.EmailChangeRequestResult, error) {
				return email.EmailChangeRequestResult{}, email.ErrCooldownActive
			},
		}
		w := postWithUserID(newTestHandler(t, svc).RequestChange, handlerTestUID, validRequestChangeBody)
		require.Equal(t, http.StatusTooManyRequests, w.Code)
		require.Contains(t, w.Body.String(), "cooldown_active")
	})

	// ── ErrUserNotFound → 500 internal_error ──────────────────────────────────

	t.Run("ErrUserNotFound returns 500 internal_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			RequestEmailChangeFn: func(_ context.Context, _ email.EmailChangeRequestInput) (email.EmailChangeRequestResult, error) {
				return email.EmailChangeRequestResult{}, profileshared.ErrUserNotFound
			},
		}
		w := postWithUserID(newTestHandler(t, svc).RequestChange, handlerTestUID, validRequestChangeBody)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "internal_error")
	})

	// ── Unexpected service error → 500 ───────────────────────────────────────

	t.Run("unexpected service error returns 500 internal_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			RequestEmailChangeFn: func(_ context.Context, _ email.EmailChangeRequestInput) (email.EmailChangeRequestResult, error) {
				return email.EmailChangeRequestResult{}, errors.New("db: connection refused")
			},
		}
		w := postWithUserID(newTestHandler(t, svc).RequestChange, handlerTestUID, validRequestChangeBody)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "internal_error")
	})

	// ── Missing JWT → 401 ─────────────────────────────────────────────────────

	t.Run("missing JWT returns 401 unauthorized", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{}
		w := postNoUserID(newTestHandler(t, svc).RequestChange, validRequestChangeBody)
		require.Equal(t, http.StatusUnauthorized, w.Code)
		require.Contains(t, w.Body.String(), "unauthorized")
	})

	// ── Malformed JSON → 422 ──────────────────────────────────────────────────

	t.Run("malformed JSON returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{}
		w := postWithUserID(newTestHandler(t, svc).RequestChange, handlerTestUID, `{"new_email":`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	// ── Body > 1 MiB → 413 ───────────────────────────────────────────────────

	t.Run("body exceeds MaxBodyBytes returns 413", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{}
		bigBody := strings.Repeat("x", 2<<20)
		w := postWithUserID(newTestHandler(t, svc).RequestChange, handlerTestUID, bigBody)
		require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	})

	// ── Service receives correct input ────────────────────────────────────────

	t.Run("service receives parsed UserID and NewEmail from body", func(t *testing.T) {
		t.Parallel()
		var captured email.EmailChangeRequestInput
		svc := &authsharedtest.EmailChangeFakeServicer{
			RequestEmailChangeFn: func(_ context.Context, in email.EmailChangeRequestInput) (email.EmailChangeRequestResult, error) {
				captured = in
				return email.EmailChangeRequestResult{CurrentEmail: "current@example.com", RawCode: "123456"}, nil
			},
		}
		uid := uuid.New().String()
		w := postWithUserID(newTestHandler(t, svc).RequestChange, uid, `{"new_email":"new@example.com"}`)
		require.Equal(t, http.StatusOK, w.Code)
		parsedUID, _ := uuid.Parse(uid)
		require.Equal(t, [16]byte(parsedUID), captured.UserID)
		require.Equal(t, "new@example.com", captured.NewEmail)
	})
}

// ── TestHandler_VerifyCurrent ─────────────────────────────────────────────────

func TestHandler_VerifyCurrent(t *testing.T) {
	t.Parallel()

	// ── Happy path → 200 with grant_token ─────────────────────────────────────

	t.Run("success returns 200 with grant_token and expires_in", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			VerifyCurrentEmailFn: func(_ context.Context, _ email.EmailChangeVerifyCurrentInput) (email.EmailChangeVerifyCurrentResult, error) {
				return email.EmailChangeVerifyCurrentResult{
					GrantToken:      "test-grant-token",
					ExpiresIn:       600,
					NewEmail:        "new@example.com",
					NewEmailRawCode: "654321",
				}, nil
			},
		}
		w := postWithUserID(newTestHandler(t, svc).VerifyCurrent, handlerTestUID, validVerifyBody)
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), "grant_token")
		require.Contains(t, w.Body.String(), "expires_in")
	})

	// ── ErrInvalidCodeFormat → 422 validation_error ───────────────────────────

	t.Run("ErrInvalidCodeFormat returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			VerifyCurrentEmailFn: func(_ context.Context, _ email.EmailChangeVerifyCurrentInput) (email.EmailChangeVerifyCurrentResult, error) {
				return email.EmailChangeVerifyCurrentResult{}, email.ErrInvalidCodeFormat
			},
		}
		w := postWithUserID(newTestHandler(t, svc).VerifyCurrent, handlerTestUID, validVerifyBody)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── ErrTokenNotFound → 422 validation_error ───────────────────────────────

	t.Run("ErrTokenNotFound returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			VerifyCurrentEmailFn: func(_ context.Context, _ email.EmailChangeVerifyCurrentInput) (email.EmailChangeVerifyCurrentResult, error) {
				return email.EmailChangeVerifyCurrentResult{}, authshared.ErrTokenNotFound
			},
		}
		w := postWithUserID(newTestHandler(t, svc).VerifyCurrent, handlerTestUID, validVerifyBody)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── ErrTokenExpired → 422 validation_error ────────────────────────────────

	t.Run("ErrTokenExpired returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			VerifyCurrentEmailFn: func(_ context.Context, _ email.EmailChangeVerifyCurrentInput) (email.EmailChangeVerifyCurrentResult, error) {
				return email.EmailChangeVerifyCurrentResult{}, authshared.ErrTokenExpired
			},
		}
		w := postWithUserID(newTestHandler(t, svc).VerifyCurrent, handlerTestUID, validVerifyBody)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── ErrInvalidCode → 422 validation_error ────────────────────────────────

	t.Run("ErrInvalidCode returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			VerifyCurrentEmailFn: func(_ context.Context, _ email.EmailChangeVerifyCurrentInput) (email.EmailChangeVerifyCurrentResult, error) {
				return email.EmailChangeVerifyCurrentResult{}, authshared.ErrInvalidCode
			},
		}
		w := postWithUserID(newTestHandler(t, svc).VerifyCurrent, handlerTestUID, validVerifyBody)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── ErrTooManyAttempts → 429 too_many_attempts ────────────────────────────

	t.Run("ErrTooManyAttempts returns 429 too_many_attempts", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			VerifyCurrentEmailFn: func(_ context.Context, _ email.EmailChangeVerifyCurrentInput) (email.EmailChangeVerifyCurrentResult, error) {
				return email.EmailChangeVerifyCurrentResult{}, authshared.ErrTooManyAttempts
			},
		}
		w := postWithUserID(newTestHandler(t, svc).VerifyCurrent, handlerTestUID, validVerifyBody)
		require.Equal(t, http.StatusTooManyRequests, w.Code)
		require.Contains(t, w.Body.String(), "too_many_attempts")
	})

	// ── ErrUserNotFound → 500 internal_error ──────────────────────────────────

	t.Run("ErrUserNotFound returns 500 internal_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			VerifyCurrentEmailFn: func(_ context.Context, _ email.EmailChangeVerifyCurrentInput) (email.EmailChangeVerifyCurrentResult, error) {
				return email.EmailChangeVerifyCurrentResult{}, profileshared.ErrUserNotFound
			},
		}
		w := postWithUserID(newTestHandler(t, svc).VerifyCurrent, handlerTestUID, validVerifyBody)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "internal_error")
	})

	// ── Unexpected service error → 500 ───────────────────────────────────────

	t.Run("unexpected service error returns 500 internal_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			VerifyCurrentEmailFn: func(_ context.Context, _ email.EmailChangeVerifyCurrentInput) (email.EmailChangeVerifyCurrentResult, error) {
				return email.EmailChangeVerifyCurrentResult{}, errors.New("unexpected")
			},
		}
		w := postWithUserID(newTestHandler(t, svc).VerifyCurrent, handlerTestUID, validVerifyBody)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "internal_error")
	})

	// ── Missing JWT → 401 ─────────────────────────────────────────────────────

	t.Run("missing JWT returns 401 unauthorized", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{}
		w := postNoUserID(newTestHandler(t, svc).VerifyCurrent, validVerifyBody)
		require.Equal(t, http.StatusUnauthorized, w.Code)
		require.Contains(t, w.Body.String(), "unauthorized")
	})

	// ── Malformed JSON → 422 ──────────────────────────────────────────────────

	t.Run("malformed JSON returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{}
		w := postWithUserID(newTestHandler(t, svc).VerifyCurrent, handlerTestUID, `{"code":`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	// ── Body > 1 MiB → 413 ───────────────────────────────────────────────────

	t.Run("body exceeds MaxBodyBytes returns 413", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{}
		bigBody := strings.Repeat("x", 2<<20)
		w := postWithUserID(newTestHandler(t, svc).VerifyCurrent, handlerTestUID, bigBody)
		require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	})

	// ── Service receives correct input ────────────────────────────────────────

	t.Run("service receives parsed UserID and code from body", func(t *testing.T) {
		t.Parallel()
		var captured email.EmailChangeVerifyCurrentInput
		svc := &authsharedtest.EmailChangeFakeServicer{
			VerifyCurrentEmailFn: func(_ context.Context, in email.EmailChangeVerifyCurrentInput) (email.EmailChangeVerifyCurrentResult, error) {
				captured = in
				return email.EmailChangeVerifyCurrentResult{
					GrantToken: "token", ExpiresIn: 600,
					NewEmail: "new@example.com", NewEmailRawCode: "654321",
				}, nil
			},
		}
		uid := uuid.New().String()
		w := postWithUserID(newTestHandler(t, svc).VerifyCurrent, uid, `{"code":"123456"}`)
		require.Equal(t, http.StatusOK, w.Code)
		parsedUID, _ := uuid.Parse(uid)
		require.Equal(t, [16]byte(parsedUID), captured.UserID)
		require.Equal(t, "123456", captured.Code)
	})
}

// ── TestHandler_ConfirmChange ─────────────────────────────────────────────────

func TestHandler_ConfirmChange(t *testing.T) {
	t.Parallel()

	// ── Happy path → 200 ──────────────────────────────────────────────────────

	t.Run("success returns 200 with message", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			ConfirmEmailChangeFn: func(_ context.Context, _ email.EmailChangeConfirmInput) (email.ConfirmEmailChangeResult, error) {
				return email.ConfirmEmailChangeResult{OldEmail: "old@example.com"}, nil
			},
		}
		w := postWithUserIDAndJTI(newTestHandler(t, svc).ConfirmChange, handlerTestUID, handlerTestJTI, validConfirmBody)
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), "email address updated successfully")
	})

	// ── ErrGrantTokenEmpty → 422 validation_error ─────────────────────────────

	t.Run("ErrGrantTokenEmpty returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			ConfirmEmailChangeFn: func(_ context.Context, _ email.EmailChangeConfirmInput) (email.ConfirmEmailChangeResult, error) {
				return email.ConfirmEmailChangeResult{}, email.ErrGrantTokenEmpty
			},
		}
		w := postWithUserIDAndJTI(newTestHandler(t, svc).ConfirmChange, handlerTestUID, handlerTestJTI, validConfirmBody)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── ErrInvalidCodeFormat → 422 validation_error ───────────────────────────

	t.Run("ErrInvalidCodeFormat returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			ConfirmEmailChangeFn: func(_ context.Context, _ email.EmailChangeConfirmInput) (email.ConfirmEmailChangeResult, error) {
				return email.ConfirmEmailChangeResult{}, email.ErrInvalidCodeFormat
			},
		}
		w := postWithUserIDAndJTI(newTestHandler(t, svc).ConfirmChange, handlerTestUID, handlerTestJTI, validConfirmBody)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── ErrGrantTokenInvalid → 422 invalid_grant_token ────────────────────────

	t.Run("ErrGrantTokenInvalid returns 422 invalid_grant_token", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			ConfirmEmailChangeFn: func(_ context.Context, _ email.EmailChangeConfirmInput) (email.ConfirmEmailChangeResult, error) {
				return email.ConfirmEmailChangeResult{}, email.ErrGrantTokenInvalid
			},
		}
		w := postWithUserIDAndJTI(newTestHandler(t, svc).ConfirmChange, handlerTestUID, handlerTestJTI, validConfirmBody)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "invalid_grant_token")
	})

	// ── ErrTokenNotFound → 422 validation_error ───────────────────────────────

	t.Run("ErrTokenNotFound returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			ConfirmEmailChangeFn: func(_ context.Context, _ email.EmailChangeConfirmInput) (email.ConfirmEmailChangeResult, error) {
				return email.ConfirmEmailChangeResult{}, authshared.ErrTokenNotFound
			},
		}
		w := postWithUserIDAndJTI(newTestHandler(t, svc).ConfirmChange, handlerTestUID, handlerTestJTI, validConfirmBody)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── ErrTokenExpired → 422 validation_error ────────────────────────────────

	t.Run("ErrTokenExpired returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			ConfirmEmailChangeFn: func(_ context.Context, _ email.EmailChangeConfirmInput) (email.ConfirmEmailChangeResult, error) {
				return email.ConfirmEmailChangeResult{}, authshared.ErrTokenExpired
			},
		}
		w := postWithUserIDAndJTI(newTestHandler(t, svc).ConfirmChange, handlerTestUID, handlerTestJTI, validConfirmBody)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── ErrInvalidCode → 422 validation_error ────────────────────────────────

	t.Run("ErrInvalidCode returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			ConfirmEmailChangeFn: func(_ context.Context, _ email.EmailChangeConfirmInput) (email.ConfirmEmailChangeResult, error) {
				return email.ConfirmEmailChangeResult{}, authshared.ErrInvalidCode
			},
		}
		w := postWithUserIDAndJTI(newTestHandler(t, svc).ConfirmChange, handlerTestUID, handlerTestJTI, validConfirmBody)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── ErrTooManyAttempts → 429 too_many_attempts ────────────────────────────

	t.Run("ErrTooManyAttempts returns 429 too_many_attempts", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			ConfirmEmailChangeFn: func(_ context.Context, _ email.EmailChangeConfirmInput) (email.ConfirmEmailChangeResult, error) {
				return email.ConfirmEmailChangeResult{}, authshared.ErrTooManyAttempts
			},
		}
		w := postWithUserIDAndJTI(newTestHandler(t, svc).ConfirmChange, handlerTestUID, handlerTestJTI, validConfirmBody)
		require.Equal(t, http.StatusTooManyRequests, w.Code)
		require.Contains(t, w.Body.String(), "too_many_attempts")
	})

	// ── ErrEmailTaken → 409 email_taken ──────────────────────────────────────

	t.Run("ErrEmailTaken returns 409 email_taken", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			ConfirmEmailChangeFn: func(_ context.Context, _ email.EmailChangeConfirmInput) (email.ConfirmEmailChangeResult, error) {
				return email.ConfirmEmailChangeResult{}, email.ErrEmailTaken
			},
		}
		w := postWithUserIDAndJTI(newTestHandler(t, svc).ConfirmChange, handlerTestUID, handlerTestJTI, validConfirmBody)
		require.Equal(t, http.StatusConflict, w.Code)
		require.Contains(t, w.Body.String(), "email_taken")
	})

	// ── ErrUserNotFound → 500 internal_error ──────────────────────────────────

	t.Run("ErrUserNotFound returns 500 internal_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			ConfirmEmailChangeFn: func(_ context.Context, _ email.EmailChangeConfirmInput) (email.ConfirmEmailChangeResult, error) {
				return email.ConfirmEmailChangeResult{}, profileshared.ErrUserNotFound
			},
		}
		w := postWithUserIDAndJTI(newTestHandler(t, svc).ConfirmChange, handlerTestUID, handlerTestJTI, validConfirmBody)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "internal_error")
	})

	// ── Unexpected service error → 500 ───────────────────────────────────────

	t.Run("unexpected service error returns 500 internal_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{
			ConfirmEmailChangeFn: func(_ context.Context, _ email.EmailChangeConfirmInput) (email.ConfirmEmailChangeResult, error) {
				return email.ConfirmEmailChangeResult{}, errors.New("db: tx aborted")
			},
		}
		w := postWithUserIDAndJTI(newTestHandler(t, svc).ConfirmChange, handlerTestUID, handlerTestJTI, validConfirmBody)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "internal_error")
	})

	// ── Missing JWT (user ID) → 401 ──────────────────────────────────────────

	t.Run("missing JWT user ID returns 401 unauthorized", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{}
		w := postNoUserID(newTestHandler(t, svc).ConfirmChange, validConfirmBody)
		require.Equal(t, http.StatusUnauthorized, w.Code)
		require.Contains(t, w.Body.String(), "unauthorized")
	})

	// ── Missing JTI → 401 ────────────────────────────────────────────────────

	t.Run("missing JTI returns 401 unauthorized", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{}
		// userID present but no JTI in context.
		w := postWithUserID(newTestHandler(t, svc).ConfirmChange, handlerTestUID, validConfirmBody)
		require.Equal(t, http.StatusUnauthorized, w.Code)
		require.Contains(t, w.Body.String(), "unauthorized")
	})

	// ── Malformed JSON → 422 ──────────────────────────────────────────────────

	t.Run("malformed JSON returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{}
		w := postWithUserIDAndJTI(newTestHandler(t, svc).ConfirmChange, handlerTestUID, handlerTestJTI, `{"grant_token":`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	// ── Body > 1 MiB → 413 ───────────────────────────────────────────────────

	t.Run("body exceeds MaxBodyBytes returns 413", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.EmailChangeFakeServicer{}
		bigBody := strings.Repeat("x", 2<<20)
		w := postWithUserIDAndJTI(newTestHandler(t, svc).ConfirmChange, handlerTestUID, handlerTestJTI, bigBody)
		require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	})

	// ── Service receives correct input ────────────────────────────────────────

	t.Run("service receives parsed UserID, GrantToken, Code and AccessJTI", func(t *testing.T) {
		t.Parallel()
		var captured email.EmailChangeConfirmInput
		svc := &authsharedtest.EmailChangeFakeServicer{
			ConfirmEmailChangeFn: func(_ context.Context, in email.EmailChangeConfirmInput) (email.ConfirmEmailChangeResult, error) {
				captured = in
				return email.ConfirmEmailChangeResult{OldEmail: "old@example.com"}, nil
			},
		}
		uid := uuid.New().String()
		const jti = "my-jti-value"
		const grantToken = "550e8400-e29b-41d4-a716-446655440001"
		body := `{"grant_token":"` + grantToken + `","code":"123456"}`
		w := postWithUserIDAndJTI(newTestHandler(t, svc).ConfirmChange, uid, jti, body)
		require.Equal(t, http.StatusOK, w.Code)
		parsedUID, _ := uuid.Parse(uid)
		require.Equal(t, [16]byte(parsedUID), captured.UserID)
		require.Equal(t, grantToken, captured.GrantToken)
		require.Equal(t, "123456", captured.Code)
		require.Equal(t, jti, captured.AccessJTI)
	})
}
