package register_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/7-Dany/store/backend/internal/domain/auth/register"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/platform/mailer"
	mailertest "github.com/7-Dany/store/backend/internal/platform/mailer/testutil"
	"github.com/stretchr/testify/require"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newRegisterRequest(t *testing.T, body map[string]any) *http.Request {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func newHandler(t *testing.T, svc register.Servicer, mailerErr error) *register.Handler {
	t.Helper()
	var base mailer.OTPHandlerBase
	if mailerErr != nil {
		base = mailertest.ErrorBase(mailerErr)
	} else {
		base = mailertest.NoopBase()
	}
	base.Timeout = 5 * time.Second
	return register.NewHandler(svc, base, authshared.NoopAuthRecorder{})
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestHandler_Register_Success(t *testing.T) {
	t.Parallel()
	h := newHandler(t, &authsharedtest.RegisterFakeServicer{}, nil)
	w := httptest.NewRecorder()
	h.Register(w, newRegisterRequest(t, map[string]any{
		"display_name": "Alice",
		"email":        "alice@example.com",
		"password":     "P@ssw0rd!1",
	}))
	require.Equal(t, http.StatusCreated, w.Code)

	var body map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	require.NotEmpty(t, body["message"], "201 response must contain a non-empty message field")
}

func TestHandler_Register_InvalidJSON(t *testing.T) {
	t.Parallel()
	h := newHandler(t, &authsharedtest.RegisterFakeServicer{}, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/register", bytes.NewBufferString("{bad json"))
	r.Header.Set("Content-Type", "application/json")
	h.Register(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_Register_ValidationError(t *testing.T) {
	t.Parallel()
	h := newHandler(t, &authsharedtest.RegisterFakeServicer{}, nil)
	w := httptest.NewRecorder()
	// Missing password → validation error
	h.Register(w, newRegisterRequest(t, map[string]any{
		"display_name": "Alice",
		"email":        "alice@example.com",
		"password":     "",
	}))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestHandler_Register_MissingDisplayName(t *testing.T) {
	t.Parallel()
	h := newHandler(t, &authsharedtest.RegisterFakeServicer{}, nil)
	w := httptest.NewRecorder()
	h.Register(w, newRegisterRequest(t, map[string]any{
		"display_name": "",
		"email":        "alice@example.com",
		"password":     "P@ssw0rd!1",
	}))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestHandler_Register_InvalidEmail(t *testing.T) {
	t.Parallel()
	h := newHandler(t, &authsharedtest.RegisterFakeServicer{}, nil)
	w := httptest.NewRecorder()
	h.Register(w, newRegisterRequest(t, map[string]any{
		"display_name": "Alice",
		"email":        "not-an-email",
		"password":     "P@ssw0rd!1",
	}))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestHandler_Register_EmailTaken(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.RegisterFakeServicer{
		RegisterFn: func(_ context.Context, _ register.RegisterInput) (register.RegisterResult, error) {
			return register.RegisterResult{}, authshared.ErrEmailTaken
		},
	}
	h := newHandler(t, svc, nil)
	w := httptest.NewRecorder()
	h.Register(w, newRegisterRequest(t, map[string]any{
		"display_name": "Alice",
		"email":        "alice@example.com",
		"password":     "P@ssw0rd!1",
	}))
	require.Equal(t, http.StatusConflict, w.Code)

	var body map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	require.Equal(t, "email_taken", body["code"],
		"response body code must be email_taken")
}

func TestHandler_Register_ServiceError(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.RegisterFakeServicer{
		RegisterFn: func(_ context.Context, _ register.RegisterInput) (register.RegisterResult, error) {
			return register.RegisterResult{}, errors.New("unexpected db error")
		},
	}
	h := newHandler(t, svc, nil)
	w := httptest.NewRecorder()
	h.Register(w, newRegisterRequest(t, map[string]any{
		"display_name": "Alice",
		"email":        "alice@example.com",
		"password":     "P@ssw0rd!1",
	}))
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandler_Register_MailDeliveryFailed_503(t *testing.T) {
	t.Parallel()
	h := newHandler(t, &authsharedtest.RegisterFakeServicer{}, errors.New("smtp timeout"))
	w := httptest.NewRecorder()
	h.Register(w, newRegisterRequest(t, map[string]any{
		"display_name": "Alice",
		"email":        "alice@example.com",
		"password":     "P@ssw0rd!1",
	}))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)

	var body map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	require.Contains(t, body["message"], "resend endpoint",
		"503 body must direct the client to the resend endpoint")
}

func TestHandler_Register_MailDeliveryFailed_503_CodeField(t *testing.T) {
	t.Parallel()
	h := newHandler(t, &authsharedtest.RegisterFakeServicer{}, errors.New("smtp timeout"))
	w := httptest.NewRecorder()
	h.Register(w, newRegisterRequest(t, map[string]any{
		"display_name": "Alice",
		"email":        "alice@example.com",
		"password":     "P@ssw0rd!1",
	}))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)

	var body map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	require.Equal(t, "mail_delivery_failed", body["code"],
		"503 response must carry the machine-readable code mail_delivery_failed")
}

func TestHandler_Register_BodyTooLarge(t *testing.T) {
	t.Parallel()
	h := newHandler(t, &authsharedtest.RegisterFakeServicer{}, nil)
	w := httptest.NewRecorder()
	// Build a body larger than 1 MiB.
	huge := make([]byte, 1<<20+1)
	for i := range huge {
		huge[i] = 'x'
	}
	payload := append([]byte(`{"old_password":"`), huge...)
	payload = append(payload, '"', '}')
	r := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(payload))
	r.Header.Set("Content-Type", "application/json")
	h.Register(w, r)
	// http.MaxBytesReader causes json.Decoder to return *http.MaxBytesError;
	// respond.DecodeJSON maps that to 413 Request Entity Too Large.
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

func TestHandler_Register_WithUsername_Success(t *testing.T) {
	t.Parallel()
	h := newHandler(t, &authsharedtest.RegisterFakeServicer{}, nil)
	w := httptest.NewRecorder()
	h.Register(w, newRegisterRequest(t, map[string]any{
		"display_name": "Alice",
		"email":        "alice@example.com",
		"password":     "P@ssw0rd!1",
		"username":     "alice123",
	}))
	require.Equal(t, http.StatusCreated, w.Code,
		"valid username must be accepted and result in 201")
}

func TestHandler_Register_UsernameTaken(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.RegisterFakeServicer{
		RegisterFn: func(_ context.Context, _ register.RegisterInput) (register.RegisterResult, error) {
			return register.RegisterResult{}, authshared.ErrUsernameTaken
		},
	}
	h := newHandler(t, svc, nil)
	w := httptest.NewRecorder()
	h.Register(w, newRegisterRequest(t, map[string]any{
		"display_name": "Alice",
		"email":        "alice@example.com",
		"password":     "P@ssw0rd!1",
		"username":     "alice123",
	}))
	require.Equal(t, http.StatusConflict, w.Code)

	var body map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	require.Equal(t, "username_taken", body["code"],
		"response body code must be username_taken")
}

func TestHandler_Register_UsernameInvalid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		username string
	}{
		{"too short", "ab"},
		{"invalid chars symbol", "alice!"},
		{"invalid chars hyphen", "alice-bob"},
		{"leading underscore", "_alice"},
		{"trailing underscore", "alice_"},
		{"consecutive underscores", "alice__bob"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHandler(t, &authsharedtest.RegisterFakeServicer{}, nil)
			w := httptest.NewRecorder()
			h.Register(w, newRegisterRequest(t, map[string]any{
				"display_name": "Alice",
				"email":        "alice@example.com",
				"password":     "P@ssw0rd!1",
				"username":     tc.username,
			}))
			require.Equal(t, http.StatusUnprocessableEntity, w.Code,
				"invalid username %q must be rejected with 422", tc.username)

			var body map[string]string
			require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
			require.Equal(t, "validation_error", body["code"])
		})
	}
}

func TestHandler_Register_UsernameNormalisedBeforeService(t *testing.T) {
	t.Parallel()
	// Verify the handler passes the normalised (lowercased) username to the service.
	var capturedInput register.RegisterInput
	svc := &authsharedtest.RegisterFakeServicer{
		RegisterFn: func(_ context.Context, in register.RegisterInput) (register.RegisterResult, error) {
			capturedInput = in
			return register.RegisterResult{
				UserID:  "00000000-0000-0000-0000-000000000001",
				Email:   in.Email,
				RawCode: "123456",
			}, nil
		},
	}
	h := newHandler(t, svc, nil)
	w := httptest.NewRecorder()
	h.Register(w, newRegisterRequest(t, map[string]any{
		"display_name": "Alice",
		"email":        "alice@example.com",
		"password":     "P@ssw0rd!1",
		"username":     "ALICE123",
	}))
	require.Equal(t, http.StatusCreated, w.Code)
	require.Equal(t, "alice123", capturedInput.Username,
		"username must be lowercased by validateAndNormalise before reaching the service")
}

func TestHandler_Register_WeakPassword(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		password string
	}{
		{"too short", "Ab1!"},
		{"no uppercase", "p@ssw0rd!1"},
		{"no digit", "P@ssword!!"},
		{"no symbol", "Passw0rd11"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHandler(t, &authsharedtest.RegisterFakeServicer{}, nil)
			w := httptest.NewRecorder()
			h.Register(w, newRegisterRequest(t, map[string]any{
				"display_name": "Alice",
				"email":        "alice@example.com",
				"password":     tc.password,
			}))
			require.Equal(t, http.StatusUnprocessableEntity, w.Code,
				"weak password %q must be rejected with 422", tc.password)
		})
	}
}
