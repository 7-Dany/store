package telegram_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/domain/oauth/telegram"
	oauthshared "github.com/7-Dany/store/backend/internal/domain/oauth/shared"
	oauthsharedtest "github.com/7-Dany/store/backend/internal/domain/oauth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// ─────────────────────────────────────────────────────────────────────────────
// Test infrastructure
// ─────────────────────────────────────────────────────────────────────────────

const testBotToken = "test-bot-token"

// jwtCfg is the JWTConfig used by all handler tests.
// Secrets are ≥32 bytes and distinct as required by token package validation.
var jwtCfg = token.JWTConfig{
	JWTAccessSecret:  "test-access-secret-must-be-32bytes!!",
	JWTRefreshSecret: "test-refresh-secret-must-be-32bytes!",
	AccessTTL:        15 * time.Minute,
	SecureCookies:    false,
}

// newTestHandler constructs a Handler wired with the given Servicer and shared
// test JWTConfig. secureCookies is always false in tests.
func newTestHandler(svc telegram.Servicer) *telegram.Handler {
	return telegram.NewHandler(svc, testBotToken, jwtCfg, false)
}

// computeHash reproduces the Telegram HMAC-SHA256 algorithm for building
// payloads that pass VerifyHMAC in the handler.
//
// Algorithm (Telegram spec):
//  1. secret_key = SHA256(botToken)
//  2. data_check_string = sorted "key=value" pairs (excluding hash), joined by "\n"
//  3. hash = hex(HMAC_SHA256(secret_key, data_check_string))
func computeHash(id int64, authDate int64, botToken string, extra map[string]string) string {
	secretKey := sha256.Sum256([]byte(botToken))

	pairs := []string{
		fmt.Sprintf("auth_date=%d", authDate),
		fmt.Sprintf("id=%d", id),
	}
	for k, v := range extra {
		if v != "" {
			pairs = append(pairs, k+"="+v)
		}
	}
	sort.Strings(pairs)

	dataCheck := strings.Join(pairs, "\n")
	mac := hmac.New(sha256.New, secretKey[:])
	mac.Write([]byte(dataCheck))
	return hex.EncodeToString(mac.Sum(nil))
}

// validCallbackBody returns a JSON body with a correct HMAC for testBotToken
// and the given id / authDate.
func validCallbackBody(id int64, authDate int64) string {
	hash := computeHash(id, authDate, testBotToken, nil)
	return fmt.Sprintf(`{"id":%d,"auth_date":%d,"hash":%q}`, id, authDate, hash)
}

// freshCallbackBody returns a valid body using time.Now() as auth_date.
func freshCallbackBody() string {
	return validCallbackBody(12345678, time.Now().Unix())
}

// newCallbackRequest builds a POST /telegram/callback request with the given body.
func newCallbackRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/telegram/callback", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

// newLinkRequest builds a POST /telegram/link request with the given body and
// an injected user ID in the context.
func newLinkRequest(body string, userIDStr string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/telegram/link", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(token.InjectUserIDForTest(r.Context(), userIDStr))
	return r
}

// newUnlinkRequest builds a DELETE /telegram/unlink request with an injected
// user ID and no body.
func newUnlinkRequest(userIDStr string) *http.Request {
	r := httptest.NewRequest(http.MethodDelete, "/telegram/unlink", nil)
	r = r.WithContext(token.InjectUserIDForTest(r.Context(), userIDStr))
	return r
}

// assertJSONCode decodes the {"code":"..."} envelope and asserts the code
// matches wantCode.
func assertJSONCode(t *testing.T, w *httptest.ResponseRecorder, wantCode string) {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), "response body: %s", w.Body.String())
	assert.Equal(t, wantCode, body.Code)
}

// assertAccessTokenBody asserts that the response body contains a non-empty
// access_token, token_type="Bearer", and expires_in > 0.
func assertAccessTokenBody(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	var body struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), "response body: %s", w.Body.String())
	assert.NotEmpty(t, body.AccessToken)
	assert.Equal(t, "Bearer", body.TokenType)
	assert.Greater(t, body.ExpiresIn, 0)
}

// findCookie returns the named Set-Cookie from the recorder, or nil.
func findCookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// validSession returns a LoggedInSession with RefreshExpiry in the future,
// suitable for triggering a successful token.MintTokens call.
func validSession() oauthshared.LoggedInSession {
	return oauthshared.LoggedInSession{
		UserID:        [16]byte(uuid.New()),
		SessionID:     [16]byte(uuid.New()),
		RefreshJTI:    [16]byte(uuid.New()),
		FamilyID:      [16]byte(uuid.New()),
		RefreshExpiry: time.Now().Add(7 * 24 * time.Hour),
	}
}

// validUserID returns a new random UUID string for use in link/unlink tests.
func validUserID() string {
	return uuid.New().String()
}

// ─────────────────────────────────────────────────────────────────────────────
// HandleCallback tests
// ─────────────────────────────────────────────────────────────────────────────

// T-01: new user → 201, access_token in body, refresh_token cookie HttpOnly.
func TestHandleCallback_NewUser_Returns201(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{
		HandleCallbackFn: func(_ context.Context, _ telegram.CallbackInput) (telegram.CallbackResult, error) {
			return telegram.CallbackResult{NewUser: true, Session: validSession()}, nil
		},
	})
	w := httptest.NewRecorder()
	h.HandleCallback(w, newCallbackRequest(freshCallbackBody()))

	assert.Equal(t, http.StatusCreated, w.Code)
	assertAccessTokenBody(t, w)

	cookie := findCookie(w, "refresh_token")
	require.NotNil(t, cookie, "expected refresh_token cookie to be set")
	assert.True(t, cookie.HttpOnly)
}

// T-02: returning user → 200, access_token in body, refresh_token cookie set.
func TestHandleCallback_ReturningUser_Returns200(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{
		HandleCallbackFn: func(_ context.Context, _ telegram.CallbackInput) (telegram.CallbackResult, error) {
			return telegram.CallbackResult{NewUser: false, Session: validSession()}, nil
		},
	})
	w := httptest.NewRecorder()
	h.HandleCallback(w, newCallbackRequest(freshCallbackBody()))

	assert.Equal(t, http.StatusOK, w.Code)
	assertAccessTokenBody(t, w)
	assert.NotNil(t, findCookie(w, "refresh_token"), "expected refresh_token cookie")
}

// T-03: wrong HMAC → 401 invalid_signature.
func TestHandleCallback_InvalidHMAC_Returns401(t *testing.T) {
	t.Parallel()

	body := fmt.Sprintf(`{"id":12345678,"auth_date":%d,"hash":"deadbeef"}`, time.Now().Unix())
	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{})
	w := httptest.NewRecorder()
	h.HandleCallback(w, newCallbackRequest(body))

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assertJSONCode(t, w, "invalid_signature")
}

// T-04: auth_date > 86400s old → 401 auth_date_expired.
func TestHandleCallback_AuthDateTooOld_Returns401(t *testing.T) {
	t.Parallel()

	oldDate := time.Now().Unix() - 90000 // 90000 > 86400
	body := validCallbackBody(12345678, oldDate)
	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{})
	w := httptest.NewRecorder()
	h.HandleCallback(w, newCallbackRequest(body))

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assertJSONCode(t, w, "auth_date_expired")
}

// T-05: auth_date > 60s in future → 401 auth_date_expired.
func TestHandleCallback_AuthDateInFuture_Returns401(t *testing.T) {
	t.Parallel()

	futureDate := time.Now().Unix() + 120 // 120 > 60
	body := validCallbackBody(12345678, futureDate)
	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{})
	w := httptest.NewRecorder()
	h.HandleCallback(w, newCallbackRequest(body))

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assertJSONCode(t, w, "auth_date_expired")
}

// T-06: id=0 → 422 validation_error (id check precedes HMAC).
func TestHandleCallback_MissingID_Returns422(t *testing.T) {
	t.Parallel()

	authDate := time.Now().Unix()
	hash := computeHash(0, authDate, testBotToken, nil)
	body := fmt.Sprintf(`{"id":0,"auth_date":%d,"hash":%q}`, authDate, hash)

	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{})
	w := httptest.NewRecorder()
	h.HandleCallback(w, newCallbackRequest(body))

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assertJSONCode(t, w, "validation_error")
}

// T-07: service returns ErrAccountLocked → 423 account_locked.
func TestHandleCallback_AccountLocked_Returns423(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{
		HandleCallbackFn: func(_ context.Context, _ telegram.CallbackInput) (telegram.CallbackResult, error) {
			return telegram.CallbackResult{}, oauthshared.ErrAccountLocked
		},
	})
	w := httptest.NewRecorder()
	h.HandleCallback(w, newCallbackRequest(freshCallbackBody()))

	assert.Equal(t, http.StatusLocked, w.Code)
	assertJSONCode(t, w, "account_locked")
}

// T-08: service returns ErrAccountInactive → 403 account_inactive.
func TestHandleCallback_AccountInactive_Returns403(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{
		HandleCallbackFn: func(_ context.Context, _ telegram.CallbackInput) (telegram.CallbackResult, error) {
			return telegram.CallbackResult{}, oauthshared.ErrAccountInactive
		},
	})
	w := httptest.NewRecorder()
	h.HandleCallback(w, newCallbackRequest(freshCallbackBody()))

	assert.Equal(t, http.StatusForbidden, w.Code)
	assertJSONCode(t, w, "account_inactive")
}

// T-09: malformed JSON → 400 bad_request (respond.DecodeJSON returns 400 for syntax errors).
func TestHandleCallback_MalformedJSON_Returns400(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{})
	w := httptest.NewRecorder()
	h.HandleCallback(w, newCallbackRequest("{not valid json"))

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// T-10: service returns ErrProviderUIDTaken → 409 provider_uid_taken.
func TestHandleCallback_ProviderUIDTaken_Returns409(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{
		HandleCallbackFn: func(_ context.Context, _ telegram.CallbackInput) (telegram.CallbackResult, error) {
			return telegram.CallbackResult{}, telegram.ErrProviderUIDTaken
		},
	})
	w := httptest.NewRecorder()
	h.HandleCallback(w, newCallbackRequest(freshCallbackBody()))

	assert.Equal(t, http.StatusConflict, w.Code)
	assertJSONCode(t, w, "provider_uid_taken")
}

// T-11: service returns unexpected error → 500 internal_error.
func TestHandleCallback_InternalError_Returns500(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{
		HandleCallbackFn: func(_ context.Context, _ telegram.CallbackInput) (telegram.CallbackResult, error) {
			return telegram.CallbackResult{}, errors.New("db down")
		},
	})
	w := httptest.NewRecorder()
	h.HandleCallback(w, newCallbackRequest(freshCallbackBody()))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertJSONCode(t, w, "internal_error")
}

// ─────────────────────────────────────────────────────────────────────────────
// Body-size limit tests (413)
// ─────────────────────────────────────────────────────────────────────────────

// Callback: body exceeds MaxBodyBytes → 413.
func TestHandleCallback_BodyTooLarge_Returns413(t *testing.T) {
	t.Parallel()

	// Build a body just over the limit.
	oversized := strings.Repeat("x", int(respond.MaxBodyBytes)+1)
	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{})
	w := httptest.NewRecorder()
	h.HandleCallback(w, newCallbackRequest(oversized))

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

// Link: body exceeds MaxBodyBytes → 413.
func TestHandleLink_BodyTooLarge_Returns413(t *testing.T) {
	t.Parallel()

	oversized := strings.Repeat("x", int(respond.MaxBodyBytes)+1)
	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{})
	w := httptest.NewRecorder()
	h.HandleLink(w, newLinkRequest(oversized, validUserID()))

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

// MintTokens failure: Session.RefreshExpiry is zero (past) → GenerateRefreshToken
// errors → 500 internal_error.
func TestHandleCallback_MintTokensFailure_Returns500(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{
		HandleCallbackFn: func(_ context.Context, _ telegram.CallbackInput) (telegram.CallbackResult, error) {
			return telegram.CallbackResult{
				NewUser: false,
				Session: oauthshared.LoggedInSession{
					UserID:        [16]byte(uuid.New()),
					SessionID:     [16]byte(uuid.New()),
					RefreshJTI:    [16]byte(uuid.New()),
					FamilyID:      [16]byte(uuid.New()),
					RefreshExpiry: time.Time{}, // zero → past → GenerateRefreshToken error
				},
			}, nil
		},
	})
	w := httptest.NewRecorder()
	h.HandleCallback(w, newCallbackRequest(freshCallbackBody()))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertJSONCode(t, w, "internal_error")
}

// Verify refresh_token cookie is HttpOnly and Path=/api/v1/auth on new-user path.
func TestHandleCallback_NewUser_SetsRefreshTokenCookieHttpOnly(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{
		HandleCallbackFn: func(_ context.Context, _ telegram.CallbackInput) (telegram.CallbackResult, error) {
			return telegram.CallbackResult{NewUser: true, Session: validSession()}, nil
		},
	})
	w := httptest.NewRecorder()
	h.HandleCallback(w, newCallbackRequest(freshCallbackBody()))

	require.Equal(t, http.StatusCreated, w.Code)
	cookie := findCookie(w, "refresh_token")
	require.NotNil(t, cookie, "expected refresh_token cookie")
	assert.True(t, cookie.HttpOnly)
	assert.Equal(t, "/api/v1/auth", cookie.Path)
}

// ─────────────────────────────────────────────────────────────────────────────
// HandleLink tests
// ─────────────────────────────────────────────────────────────────────────────

// T-12: happy path → 204.
func TestHandleLink_HappyPath_Returns204(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{})
	w := httptest.NewRecorder()
	h.HandleLink(w, newLinkRequest(freshCallbackBody(), validUserID()))

	assert.Equal(t, http.StatusNoContent, w.Code)
}

// T-13: service returns ErrProviderAlreadyLinked → 409 provider_already_linked.
func TestHandleLink_AlreadyLinked_Returns409(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{
		LinkTelegramFn: func(_ context.Context, _ telegram.LinkInput) error {
			return telegram.ErrProviderAlreadyLinked
		},
	})
	w := httptest.NewRecorder()
	h.HandleLink(w, newLinkRequest(freshCallbackBody(), validUserID()))

	assert.Equal(t, http.StatusConflict, w.Code)
	assertJSONCode(t, w, "provider_already_linked")
}

// T-14: service returns ErrProviderUIDTaken → 409 provider_uid_taken.
func TestHandleLink_ProviderUIDTaken_Returns409(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{
		LinkTelegramFn: func(_ context.Context, _ telegram.LinkInput) error {
			return telegram.ErrProviderUIDTaken
		},
	})
	w := httptest.NewRecorder()
	h.HandleLink(w, newLinkRequest(freshCallbackBody(), validUserID()))

	assert.Equal(t, http.StatusConflict, w.Code)
	assertJSONCode(t, w, "provider_uid_taken")
}

// T-15: wrong HMAC → 401 invalid_signature.
func TestHandleLink_InvalidHMAC_Returns401(t *testing.T) {
	t.Parallel()

	body := fmt.Sprintf(`{"id":12345678,"auth_date":%d,"hash":"deadbeef"}`, time.Now().Unix())
	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{})
	w := httptest.NewRecorder()
	h.HandleLink(w, newLinkRequest(body, validUserID()))

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assertJSONCode(t, w, "invalid_signature")
}

// T-16: auth_date too old → 401 auth_date_expired.
func TestHandleLink_AuthDateExpired_Returns401(t *testing.T) {
	t.Parallel()

	oldDate := time.Now().Unix() - 90000
	body := validCallbackBody(12345678, oldDate)
	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{})
	w := httptest.NewRecorder()
	h.HandleLink(w, newLinkRequest(body, validUserID()))

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assertJSONCode(t, w, "auth_date_expired")
}

// No user ID in context → 401 unauthorized.
func TestHandleLink_MissingJWT_Returns401(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{})
	r := httptest.NewRequest(http.MethodPost, "/telegram/link", strings.NewReader(freshCallbackBody()))
	r.Header.Set("Content-Type", "application/json")
	// No InjectUserIDForTest → no user ID in context.
	w := httptest.NewRecorder()
	h.HandleLink(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assertJSONCode(t, w, "unauthorized")
}

// Malformed user ID in context → 401 unauthorized.
func TestHandleLink_MalformedUserID_Returns401(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{})
	w := httptest.NewRecorder()
	h.HandleLink(w, newLinkRequest(freshCallbackBody(), "not-a-uuid"))

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assertJSONCode(t, w, "unauthorized")
}

// id=0 in body → 422 validation_error.
func TestHandleLink_MissingID_Returns422(t *testing.T) {
	t.Parallel()

	authDate := time.Now().Unix()
	hash := computeHash(0, authDate, testBotToken, nil)
	body := fmt.Sprintf(`{"id":0,"auth_date":%d,"hash":%q}`, authDate, hash)

	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{})
	w := httptest.NewRecorder()
	h.HandleLink(w, newLinkRequest(body, validUserID()))

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assertJSONCode(t, w, "validation_error")
}

// Service returns unexpected error → 500 internal_error.
func TestHandleLink_InternalError_Returns500(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{
		LinkTelegramFn: func(_ context.Context, _ telegram.LinkInput) error {
			return errors.New("db down")
		},
	})
	w := httptest.NewRecorder()
	h.HandleLink(w, newLinkRequest(freshCallbackBody(), validUserID()))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertJSONCode(t, w, "internal_error")
}

// ─────────────────────────────────────────────────────────────────────────────
// HandleUnlink tests
// ─────────────────────────────────────────────────────────────────────────────

// T-19: happy path → 204.
func TestHandleUnlink_HappyPath_Returns204(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{})
	w := httptest.NewRecorder()
	h.HandleUnlink(w, newUnlinkRequest(validUserID()))

	assert.Equal(t, http.StatusNoContent, w.Code)
}

// T-21: service returns ErrProviderNotLinked → 404 provider_not_linked.
func TestHandleUnlink_ProviderNotLinked_Returns404(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{
		UnlinkTelegramFn: func(_ context.Context, _ [16]byte, _, _ string) error {
			return telegram.ErrProviderNotLinked
		},
	})
	w := httptest.NewRecorder()
	h.HandleUnlink(w, newUnlinkRequest(validUserID()))

	assert.Equal(t, http.StatusNotFound, w.Code)
	assertJSONCode(t, w, "provider_not_linked")
}

// T-22: service returns ErrLastAuthMethod → 409 last_auth_method.
func TestHandleUnlink_LastAuthMethod_Returns409(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{
		UnlinkTelegramFn: func(_ context.Context, _ [16]byte, _, _ string) error {
			return oauthshared.ErrLastAuthMethod
		},
	})
	w := httptest.NewRecorder()
	h.HandleUnlink(w, newUnlinkRequest(validUserID()))

	assert.Equal(t, http.StatusConflict, w.Code)
	assertJSONCode(t, w, "last_auth_method")
}

// T-23: no user ID in context → 401 unauthorized.
func TestHandleUnlink_MissingJWT_Returns401(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{})
	r := httptest.NewRequest(http.MethodDelete, "/telegram/unlink", nil)
	// No InjectUserIDForTest → no user ID in context.
	w := httptest.NewRecorder()
	h.HandleUnlink(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assertJSONCode(t, w, "unauthorized")
}

// Malformed user ID in context → 401 unauthorized.
func TestHandleUnlink_MalformedUserID_Returns401(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{})
	w := httptest.NewRecorder()
	h.HandleUnlink(w, newUnlinkRequest("not-a-uuid"))

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assertJSONCode(t, w, "unauthorized")
}

// Service returns unexpected error → 500 internal_error.
func TestHandleUnlink_InternalError_Returns500(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&oauthsharedtest.TelegramFakeServicer{
		UnlinkTelegramFn: func(_ context.Context, _ [16]byte, _, _ string) error {
			return errors.New("db down")
		},
	})
	w := httptest.NewRecorder()
	h.HandleUnlink(w, newUnlinkRequest(validUserID()))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertJSONCode(t, w, "internal_error")
}
