package verification_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/auth/verification"
	"github.com/7-Dany/store/backend/internal/platform/mailer"
	mailertest "github.com/7-Dany/store/backend/internal/platform/mailer/testutil"
	"github.com/stretchr/testify/require"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newVerifyRequest(t *testing.T, body map[string]any) *http.Request {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodPost, "/verify-email", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func newResendRequest(t *testing.T, body map[string]any) *http.Request {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodPost, "/resend-verification", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func newVerificationHandler(t *testing.T, svc verification.Servicer, mailerErr error) *verification.Handler {
	t.Helper()
	var base mailer.OTPHandlerBase
	if mailerErr != nil {
		base = mailertest.ErrorBase(mailerErr)
	} else {
		base = mailertest.NoopBase()
	}
	base.Timeout = 5 * time.Second
	return verification.NewHandler(svc, &authsharedtest.NopBackoffChecker{}, base)
}


// ── VerifyEmail tests ─────────────────────────────────────────────────────────

func TestHandler_VerifyEmail_Success(t *testing.T) {
	t.Parallel()
	h := newVerificationHandler(t, &authsharedtest.VerificationFakeServicer{}, nil)
	w := httptest.NewRecorder()
	h.VerifyEmail(w, newVerifyRequest(t, map[string]any{
		"email": "alice@example.com",
		"code":  "123456",
	}))
	require.Equal(t, http.StatusOK, w.Code)
}

func TestHandler_VerifyEmail_InvalidJSON(t *testing.T) {
	t.Parallel()
	h := newVerificationHandler(t, &authsharedtest.VerificationFakeServicer{}, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/verify-email", bytes.NewBufferString("{bad json"))
	r.Header.Set("Content-Type", "application/json")
	h.VerifyEmail(w, r)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestHandler_VerifyEmail_ValidationError_EmptyCode(t *testing.T) {
	t.Parallel()
	h := newVerificationHandler(t, &authsharedtest.VerificationFakeServicer{}, nil)
	w := httptest.NewRecorder()
	h.VerifyEmail(w, newVerifyRequest(t, map[string]any{
		"email": "alice@example.com",
		"code":  "",
	}))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestHandler_VerifyEmail_ErrInvalidCode_Returns422(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.VerificationFakeServicer{
		VerifyEmailFn: func(_ context.Context, _ verification.VerifyEmailInput) error {
			return authshared.ErrInvalidCode
		},
	}
	h := newVerificationHandler(t, svc, nil)
	w := httptest.NewRecorder()
	h.VerifyEmail(w, newVerifyRequest(t, map[string]any{
		"email": "alice@example.com",
		"code":  "999999",
	}))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestHandler_VerifyEmail_ErrTokenNotFound_Returns422(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.VerificationFakeServicer{
		VerifyEmailFn: func(_ context.Context, _ verification.VerifyEmailInput) error {
			return authshared.ErrTokenNotFound
		},
	}
	h := newVerificationHandler(t, svc, nil)
	w := httptest.NewRecorder()
	h.VerifyEmail(w, newVerifyRequest(t, map[string]any{
		"email": "alice@example.com",
		"code":  "123456",
	}))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestHandler_VerifyEmail_ErrTooManyAttempts_Returns429(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.VerificationFakeServicer{
		VerifyEmailFn: func(_ context.Context, _ verification.VerifyEmailInput) error {
			return authshared.ErrTooManyAttempts
		},
	}
	h := newVerificationHandler(t, svc, nil)
	w := httptest.NewRecorder()
	h.VerifyEmail(w, newVerifyRequest(t, map[string]any{
		"email": "alice@example.com",
		"code":  "123456",
	}))
	require.Equal(t, http.StatusTooManyRequests, w.Code)
}

func TestHandler_VerifyEmail_ErrAccountLocked_Returns423(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.VerificationFakeServicer{
		VerifyEmailFn: func(_ context.Context, _ verification.VerifyEmailInput) error {
			return authshared.ErrAccountLocked
		},
	}
	h := newVerificationHandler(t, svc, nil)
	w := httptest.NewRecorder()
	h.VerifyEmail(w, newVerifyRequest(t, map[string]any{
		"email": "alice@example.com",
		"code":  "123456",
	}))
	require.Equal(t, http.StatusLocked, w.Code)
}

func TestHandler_VerifyEmail_ErrAlreadyVerified_Returns200(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.VerificationFakeServicer{
		VerifyEmailFn: func(_ context.Context, _ verification.VerifyEmailInput) error {
			return authshared.ErrAlreadyVerified
		},
	}
	h := newVerificationHandler(t, svc, nil)
	w := httptest.NewRecorder()
	h.VerifyEmail(w, newVerifyRequest(t, map[string]any{
		"email": "alice@example.com",
		"code":  "123456",
	}))
	// Idempotent success
	require.Equal(t, http.StatusOK, w.Code)
}

func TestHandler_VerifyEmail_ServiceError_Returns500(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.VerificationFakeServicer{
		VerifyEmailFn: func(_ context.Context, _ verification.VerifyEmailInput) error {
			return errors.New("unexpected db error")
		},
	}
	h := newVerificationHandler(t, svc, nil)
	w := httptest.NewRecorder()
	h.VerifyEmail(w, newVerifyRequest(t, map[string]any{
		"email": "alice@example.com",
		"code":  "123456",
	}))
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandler_VerifyEmail_BodyTooLarge(t *testing.T) {
	t.Parallel()
	h := newVerificationHandler(t, &authsharedtest.VerificationFakeServicer{}, nil)
	w := httptest.NewRecorder()
	large := make([]byte, 1<<20+1) // raw non-JSON bytes exceed 1 MiB
	for i := range large {
		large[i] = 'a'
	}
	r := httptest.NewRequest(http.MethodPost, "/verify-email", bytes.NewReader(large))
	r.Header.Set("Content-Type", "application/json")
	h.VerifyEmail(w, r)
	// respond.DecodeJSON drains the body on syntax error so MaxBytesReader
	// fires → 413 Request Entity Too Large (Sync S-3, RULES.md §3.14).
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

func TestHandler_VerifyEmail_ErrTokenExpired_Returns422(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.VerificationFakeServicer{
		VerifyEmailFn: func(_ context.Context, _ verification.VerifyEmailInput) error {
			return authshared.ErrTokenExpired
		},
	}
	h := newVerificationHandler(t, svc, nil)
	w := httptest.NewRecorder()
	h.VerifyEmail(w, newVerifyRequest(t, map[string]any{
		"email": "alice@example.com",
		"code":  "123456",
	}))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestHandler_VerifyEmail_ErrTokenAlreadyUsed_Returns422(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.VerificationFakeServicer{
		VerifyEmailFn: func(_ context.Context, _ verification.VerifyEmailInput) error {
			return authshared.ErrTokenAlreadyUsed
		},
	}
	h := newVerificationHandler(t, svc, nil)
	w := httptest.NewRecorder()
	h.VerifyEmail(w, newVerifyRequest(t, map[string]any{
		"email": "alice@example.com",
		"code":  "123456",
	}))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

// fakeBackoffChecker implements verification.BackoffChecker for handler tests.
type fakeBackoffChecker struct {
	// allowResult controls whether Allow returns ok=true or ok=false.
	allowResult bool
	// remaining is the duration returned by Allow when allowResult is false.
	remaining            time.Duration
	RecordFailureCalled bool
	ResetCalled         bool
}

func (f *fakeBackoffChecker) Allow(_ context.Context, _ string) (bool, time.Duration) {
	return f.allowResult, f.remaining
}
func (f *fakeBackoffChecker) RecordFailure(_ context.Context, _ string) time.Duration {
	f.RecordFailureCalled = true
	return 0
}
func (f *fakeBackoffChecker) Reset(_ context.Context, _ string) {
	f.ResetCalled = true
}

func TestHandler_VerifyEmail_BackoffGate_Returns429(t *testing.T) {
	t.Parallel()
	fake := &fakeBackoffChecker{allowResult: false, remaining: 15 * time.Second}
	h := verification.NewHandler(
		&authsharedtest.VerificationFakeServicer{},
		fake,
		mailertest.NoopBase(),
	)
	w := httptest.NewRecorder()
	h.VerifyEmail(w, newVerifyRequest(t, map[string]any{
		"email": "alice@example.com",
		"code":  "123456",
	}))
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.NotEmpty(t, w.Header().Get("Retry-After"))
}

func TestHandler_VerifyEmail_ValidationError_EmptyEmail(t *testing.T) {
	t.Parallel()
	h := newVerificationHandler(t, &authsharedtest.VerificationFakeServicer{}, nil)
	w := httptest.NewRecorder()
	h.VerifyEmail(w, newVerifyRequest(t, map[string]any{
		"email": "",
		"code":  "123456",
	}))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestHandler_VerifyEmail_ErrInvalidCode_RecordsBackoffFailure(t *testing.T) {
	t.Parallel()
	fake := &fakeBackoffChecker{allowResult: true}
	svc := &authsharedtest.VerificationFakeServicer{
		VerifyEmailFn: func(_ context.Context, _ verification.VerifyEmailInput) error {
			return authshared.ErrInvalidCode
		},
	}
	h := verification.NewHandler(svc, fake, mailertest.NoopBase())
	w := httptest.NewRecorder()
	h.VerifyEmail(w, newVerifyRequest(t, map[string]any{
		"email": "alice@example.com",
		"code":  "999999",
	}))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	require.True(t, fake.RecordFailureCalled)
}

func TestHandler_VerifyEmail_ErrTooManyAttempts_RecordsBackoffFailure(t *testing.T) {
	t.Parallel()
	fake := &fakeBackoffChecker{allowResult: true}
	svc := &authsharedtest.VerificationFakeServicer{
		VerifyEmailFn: func(_ context.Context, _ verification.VerifyEmailInput) error {
			return authshared.ErrTooManyAttempts
		},
	}
	h := verification.NewHandler(svc, fake, mailertest.NoopBase())
	w := httptest.NewRecorder()
	h.VerifyEmail(w, newVerifyRequest(t, map[string]any{
		"email": "alice@example.com",
		"code":  "123456",
	}))
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.True(t, fake.RecordFailureCalled)
}

func TestHandler_VerifyEmail_Success_ResetsBackoff(t *testing.T) {
	t.Parallel()
	fake := &fakeBackoffChecker{allowResult: true}
	h := verification.NewHandler(
		&authsharedtest.VerificationFakeServicer{},
		fake,
		mailertest.NoopBase(),
	)
	w := httptest.NewRecorder()
	h.VerifyEmail(w, newVerifyRequest(t, map[string]any{
		"email": "alice@example.com",
		"code":  "123456",
	}))
	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, fake.ResetCalled)
}

func TestHandler_VerifyEmail_ErrAlreadyVerified_ResetsBackoff(t *testing.T) {
	t.Parallel()
	fake := &fakeBackoffChecker{allowResult: true}
	svc := &authsharedtest.VerificationFakeServicer{
		VerifyEmailFn: func(_ context.Context, _ verification.VerifyEmailInput) error {
			return authshared.ErrAlreadyVerified
		},
	}
	h := verification.NewHandler(svc, fake, mailertest.NoopBase())
	w := httptest.NewRecorder()
	h.VerifyEmail(w, newVerifyRequest(t, map[string]any{
		"email": "alice@example.com",
		"code":  "123456",
	}))
	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, fake.ResetCalled)
}

func TestHandler_ResendVerification_BodyTooLarge_Returns413(t *testing.T) {
	t.Parallel()
	h := newVerificationHandler(t, &authsharedtest.VerificationFakeServicer{}, nil)
	w := httptest.NewRecorder()
	large := make([]byte, 1<<20+1) // raw non-JSON bytes exceed 1 MiB
	for i := range large {
		large[i] = 'a'
	}
	r := httptest.NewRequest(http.MethodPost, "/resend-verification", bytes.NewReader(large))
	r.Header.Set("Content-Type", "application/json")
	h.ResendVerification(w, r)
	// respond.DecodeJSON drains the body on syntax error so MaxBytesReader
	// fires → 413 Request Entity Too Large (Sync S-3, RULES.md §3.14).
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

// ── ResendVerification tests ──────────────────────────────────────────────────

func TestHandler_ResendVerification_AlwaysReturns202(t *testing.T) {
	t.Parallel()
	// Even with no-op service, always returns 202.
	h := newVerificationHandler(t, &authsharedtest.VerificationFakeServicer{}, nil)
	w := httptest.NewRecorder()
	h.ResendVerification(w, newResendRequest(t, map[string]any{
		"email": "alice@example.com",
	}))
	require.Equal(t, http.StatusAccepted, w.Code)
}

func TestHandler_ResendVerification_ValidEmail_RawCodePresent_SendsMail(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.VerificationFakeServicer{
		ResendVerificationFn: func(_ context.Context, _ verification.ResendInput) (authshared.OTPIssuanceResult, error) {
			return authshared.OTPIssuanceResult{
				UserID:  "user-1",
				Email:   "alice@example.com",
				RawCode: "654321",
			}, nil
		},
	}
	h := newVerificationHandler(t, svc, nil)
	w := httptest.NewRecorder()
	h.ResendVerification(w, newResendRequest(t, map[string]any{
		"email": "alice@example.com",
	}))
	require.Equal(t, http.StatusAccepted, w.Code)
}

func TestHandler_ResendVerification_MailDeliveryFailed_Returns503(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.VerificationFakeServicer{
		ResendVerificationFn: func(_ context.Context, _ verification.ResendInput) (authshared.OTPIssuanceResult, error) {
			return authshared.OTPIssuanceResult{
				UserID:  "user-1",
				Email:   "alice@example.com",
				RawCode: "654321",
			}, nil
		},
	}
	h := newVerificationHandler(t, svc, errors.New("smtp timeout"))
	w := httptest.NewRecorder()
	h.ResendVerification(w, newResendRequest(t, map[string]any{
		"email": "alice@example.com",
	}))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandler_ResendVerification_InvalidJSON(t *testing.T) {
	t.Parallel()
	h := newVerificationHandler(t, &authsharedtest.VerificationFakeServicer{}, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/resend-verification", bytes.NewBufferString("{bad json"))
	r.Header.Set("Content-Type", "application/json")
	h.ResendVerification(w, r)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestHandler_ResendVerification_EmptyEmail_Returns422(t *testing.T) {
	t.Parallel()
	h := newVerificationHandler(t, &authsharedtest.VerificationFakeServicer{}, nil)
	w := httptest.NewRecorder()
	h.ResendVerification(w, newResendRequest(t, map[string]any{
		"email": "",
	}))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestHandler_ResendVerification_ServiceError_Returns500(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.VerificationFakeServicer{
		ResendVerificationFn: func(_ context.Context, _ verification.ResendInput) (authshared.OTPIssuanceResult, error) {
			return authshared.OTPIssuanceResult{}, errors.New("db error")
		},
	}
	h := newVerificationHandler(t, svc, nil)
	w := httptest.NewRecorder()
	h.ResendVerification(w, newResendRequest(t, map[string]any{
		"email": "alice@example.com",
	}))
	require.Equal(t, http.StatusInternalServerError, w.Code)
}
