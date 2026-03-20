package google_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/domain/oauth/google"
	oauthshared "github.com/7-Dany/store/backend/internal/domain/oauth/shared"
	oauthsharedtest "github.com/7-Dany/store/backend/internal/domain/oauth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// ─────────────────────────────────────────────────────────────────────────────
// Test doubles
// ─────────────────────────────────────────────────────────────────────────────

// fakeKV is a minimal in-memory KV implementation for handler unit tests.
type fakeKV struct {
	data  map[string]string
	GetFn func(ctx context.Context, key string) (string, error)
	SetFn func(ctx context.Context, key, value string, ttl time.Duration) error
	DelFn func(ctx context.Context, key string) error
}

func newFakeKV() *fakeKV {
	f := &fakeKV{data: make(map[string]string)}
	f.GetFn = func(_ context.Context, key string) (string, error) {
		v, ok := f.data[key]
		if !ok {
			return "", kvstore.ErrNotFound
		}
		return v, nil
	}
	f.SetFn = func(_ context.Context, key, value string, _ time.Duration) error {
		f.data[key] = value
		return nil
	}
	f.DelFn = func(_ context.Context, key string) error {
		delete(f.data, key)
		return nil
	}
	return f
}

func (f *fakeKV) Get(ctx context.Context, key string) (string, error) {
	return f.GetFn(ctx, key)
}
func (f *fakeKV) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return f.SetFn(ctx, key, value, ttl)
}
func (f *fakeKV) Delete(ctx context.Context, key string) error {
	return f.DelFn(ctx, key)
}
func (f *fakeKV) Exists(ctx context.Context, key string) (bool, error) {
	_, ok := f.data[key]
	return ok, nil
}
func (f *fakeKV) Keys(_ context.Context, prefix string) ([]string, error) {
	var out []string
	for k := range f.data {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out, nil
}
func (f *fakeKV) StartCleanup(_ context.Context) {}
func (f *fakeKV) Close() error                   { return nil }

// compile-time check.
var _ kvstore.Store = (*fakeKV)(nil)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

const (
	testAccessSecret  = "test-access-secret-must-be-32bytes!!"
	testRefreshSecret = "test-refresh-secret-must-be-32bytes!"
	testClientID      = "test-client-id"
	testRedirectURI   = "http://localhost:8080/callback"
	testSuccessURL    = "http://localhost:3000/dashboard"
	testErrorURL      = "http://localhost:3000/login"
)

func testJWTConfig() token.JWTConfig {
	return token.JWTConfig{
		JWTAccessSecret:  testAccessSecret,
		JWTRefreshSecret: testRefreshSecret,
		AccessTTL:        15 * time.Minute,
		SecureCookies:    false,
	}
}

func newHandler(svc google.Servicer, kv kvstore.Store) *google.Handler {
	return google.NewHandler(
		svc, kv, testJWTConfig(),
		testClientID, testRedirectURI, testSuccessURL, testErrorURL,
		false, // secureCookies=false for tests
		oauthsharedtest.NoopOAuthRecorder{},
	)
}

func newInitiateRequest() *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	return r
}

func newCallbackRequest(state, code, errParam string) *http.Request {
	url := "/callback"
	parts := []string{}
	if state != "" {
		parts = append(parts, "state="+state)
	}
	if code != "" {
		parts = append(parts, "code="+code)
	}
	if errParam != "" {
		parts = append(parts, "error="+errParam)
	}
	if len(parts) > 0 {
		url += "?" + strings.Join(parts, "&")
	}
	return httptest.NewRequest(http.MethodGet, url, nil)
}

// happyCallbackResult returns a minimal CallbackResult for a login path.
func happyCallbackResult() google.CallbackResult {
	return google.CallbackResult{
		Session: oauthshared.LoggedInSession{
			UserID:        [16]byte(uuid.New()),
			SessionID:     [16]byte(uuid.New()),
			RefreshJTI:    [16]byte(uuid.New()),
			FamilyID:      [16]byte(uuid.New()),
			RefreshExpiry: time.Now().Add(7 * 24 * time.Hour),
		},
		Linked: false,
	}
}

// seedState stores a fake OAuthState in fakeKV and returns the state UUID.
func seedState(kv *fakeKV, codeVerifier, linkUserID string) string {
	state := uuid.New().String()
	payload, _ := json.Marshal(google.OAuthState{
		CodeVerifier: codeVerifier,
		LinkUserID:   linkUserID,
	})
	kv.data["goauth:state:"+state] = string(payload)
	return state
}

// ─────────────────────────────────────────────────────────────────────────────
// HandleInitiate tests
// ─────────────────────────────────────────────────────────────────────────────

// T-01: Happy path — no Authorization header → 302 to accounts.google.com.
func TestHandleInitiate_HappyPath_NoAuthHeader(t *testing.T) {
	kv := newFakeKV()
	h := newHandler(&oauthsharedtest.GoogleFakeServicer{}, kv)

	w := httptest.NewRecorder()
	h.HandleInitiate(w, newInitiateRequest())

	assert.Equal(t, http.StatusFound, w.Code)
	loc := w.Header().Get("Location")
	assert.Contains(t, loc, "accounts.google.com")
}

// T-02: Valid Bearer → link_user_id stored in KV equals parsed subject.
func TestHandleInitiate_ValidBearer_LinkUserIDStored(t *testing.T) {
	kv := newFakeKV()
	h := newHandler(&oauthsharedtest.GoogleFakeServicer{}, kv)

	userID := uuid.New()
	accessToken, err := token.GenerateAccessToken(userID.String(), uuid.NewString(), 15*time.Minute, testAccessSecret)
	require.NoError(t, err)

	var capturedKey, capturedVal string
	kv.SetFn = func(_ context.Context, key, value string, _ time.Duration) error {
		capturedKey = key
		capturedVal = value
		return nil
	}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer "+accessToken)
	w := httptest.NewRecorder()
	h.HandleInitiate(w, r)

	require.NotEmpty(t, capturedKey)
	var state google.OAuthState
	require.NoError(t, json.Unmarshal([]byte(capturedVal), &state))
	assert.Equal(t, userID.String(), state.LinkUserID)
}

// T-03: Invalid Bearer → treated as unauthenticated (link_user_id == "").
func TestHandleInitiate_InvalidBearer_TreatedAsUnauthenticated(t *testing.T) {
	kv := newFakeKV()
	h := newHandler(&oauthsharedtest.GoogleFakeServicer{}, kv)

	var capturedVal string
	kv.SetFn = func(_ context.Context, key, value string, _ time.Duration) error {
		capturedVal = value
		return nil
	}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer not-a-valid-jwt")
	w := httptest.NewRecorder()
	h.HandleInitiate(w, r)

	assert.Equal(t, http.StatusFound, w.Code)
	var state google.OAuthState
	require.NoError(t, json.Unmarshal([]byte(capturedVal), &state))
	assert.Equal(t, "", state.LinkUserID)
}

// T-04: KV set failure → 500 internal_error.
func TestHandleInitiate_KVSetFailure_Returns500(t *testing.T) {
	kv := newFakeKV()
	kv.SetFn = func(_ context.Context, _, _ string, _ time.Duration) error {
		return errors.New("kv unavailable")
	}
	h := newHandler(&oauthsharedtest.GoogleFakeServicer{}, kv)

	w := httptest.NewRecorder()
	h.HandleInitiate(w, newInitiateRequest())

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertJSONCode(t, w, "internal_error")
}

// T-05: Two requests produce distinct state KV keys.
func TestHandleInitiate_TwoRequests_DistinctStateKeys(t *testing.T) {
	var keys []string
	kv := newFakeKV()
	kv.SetFn = func(_ context.Context, key, value string, _ time.Duration) error {
		keys = append(keys, key)
		kv.data[key] = value
		return nil
	}
	h := newHandler(&oauthsharedtest.GoogleFakeServicer{}, kv)

	h.HandleInitiate(httptest.NewRecorder(), newInitiateRequest())
	h.HandleInitiate(httptest.NewRecorder(), newInitiateRequest())

	require.Len(t, keys, 2)
	assert.NotEqual(t, keys[0], keys[1])
}

// T-06: Redirect URL contains PKCE params.
func TestHandleInitiate_RedirectContainsPKCEParams(t *testing.T) {
	kv := newFakeKV()
	h := newHandler(&oauthsharedtest.GoogleFakeServicer{}, kv)

	w := httptest.NewRecorder()
	h.HandleInitiate(w, newInitiateRequest())

	loc := w.Header().Get("Location")
	assert.Contains(t, loc, "code_challenge=")
	assert.Contains(t, loc, "code_challenge_method=S256")
	assert.Contains(t, loc, "state=")
}

// ─────────────────────────────────────────────────────────────────────────────
// HandleCallback pre-service guard tests
// ─────────────────────────────────────────────────────────────────────────────

// T-07: error query param → redirect oauth_cancelled.
func TestHandleCallback_ErrorParam_RedirectsCancelled(t *testing.T) {
	kv := newFakeKV()
	h := newHandler(&oauthsharedtest.GoogleFakeServicer{}, kv)

	w := httptest.NewRecorder()
	h.HandleCallback(w, newCallbackRequest("", "", "access_denied"))

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "error=oauth_cancelled")
}

// T-08: Missing state param → redirect invalid_state.
func TestHandleCallback_MissingState_RedirectsInvalidState(t *testing.T) {
	kv := newFakeKV()
	h := newHandler(&oauthsharedtest.GoogleFakeServicer{}, kv)

	w := httptest.NewRecorder()
	h.HandleCallback(w, newCallbackRequest("", "code123", ""))

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "error=invalid_state")
}

// T-09: State not in KV → redirect invalid_state.
func TestHandleCallback_StateNotInKV_RedirectsInvalidState(t *testing.T) {
	kv := newFakeKV()
	kv.GetFn = func(_ context.Context, _ string) (string, error) {
		return "", kvstore.ErrNotFound
	}
	h := newHandler(&oauthsharedtest.GoogleFakeServicer{}, kv)

	w := httptest.NewRecorder()
	h.HandleCallback(w, newCallbackRequest("unknown-state", "code123", ""))

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "error=invalid_state")
}

// T-10: Missing code after valid state → redirect invalid_state.
func TestHandleCallback_MissingCode_RedirectsInvalidState(t *testing.T) {
	kv := newFakeKV()
	state := seedState(kv, "verifier", "")
	h := newHandler(&oauthsharedtest.GoogleFakeServicer{}, kv)

	r := newCallbackRequest(state, "", "")
	w := httptest.NewRecorder()
	h.HandleCallback(w, r)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "error=invalid_state")
}

// T-11: KV del failure is non-fatal — flow continues to service.
func TestHandleCallback_KVDelFailure_NonFatal(t *testing.T) {
	kv := newFakeKV()
	state := seedState(kv, "verifier", "")
	kv.DelFn = func(_ context.Context, _ string) error {
		return errors.New("del failed")
	}

	svc := &oauthsharedtest.GoogleFakeServicer{
		HandleCallbackFn: func(_ context.Context, _ google.CallbackInput) (google.CallbackResult, error) {
			return happyCallbackResult(), nil
		},
	}
	h := newHandler(svc, kv)

	r := newCallbackRequest(state, "code123", "")
	w := httptest.NewRecorder()
	h.HandleCallback(w, r)

	// Flow continues: expect redirect to success URL (not error URL).
	assert.Equal(t, http.StatusFound, w.Code)
	assert.Contains(t, w.Header().Get("Location"), testSuccessURL)
}

// ─────────────────────────────────────────────────────────────────────────────
// HandleCallback error mapping tests
// ─────────────────────────────────────────────────────────────────────────────

func runCallbackErrorTest(t *testing.T, svcErr error, wantErrorCode string) {
	t.Helper()
	kv := newFakeKV()
	state := seedState(kv, "verifier", "")

	svc := &oauthsharedtest.GoogleFakeServicer{
		HandleCallbackFn: func(_ context.Context, _ google.CallbackInput) (google.CallbackResult, error) {
			return google.CallbackResult{}, svcErr
		},
	}
	h := newHandler(svc, kv)

	r := newCallbackRequest(state, "code123", "")
	w := httptest.NewRecorder()
	h.HandleCallback(w, r)

	assert.Equal(t, http.StatusFound, w.Code)
	loc := w.Header().Get("Location")
	assert.Contains(t, loc, "error="+wantErrorCode)
}

// T-13: ErrTokenExchangeFailed → redirect token_exchange_failed.
func TestHandleCallback_ErrTokenExchangeFailed(t *testing.T) {
	runCallbackErrorTest(t, google.ErrTokenExchangeFailed, "token_exchange_failed")
}

// T-15: ErrInvalidIDToken → redirect invalid_id_token.
func TestHandleCallback_ErrInvalidIDToken(t *testing.T) {
	runCallbackErrorTest(t, google.ErrInvalidIDToken, "invalid_id_token")
}

// T-17: Internal error → redirect server_error.
func TestHandleCallback_InternalError_RedirectsServerError(t *testing.T) {
	runCallbackErrorTest(t, errors.New("db down"), "server_error")
}

// T-24: ErrAccountLocked → redirect account_locked.
func TestHandleCallback_ErrAccountLocked(t *testing.T) {
	runCallbackErrorTest(t, oauthshared.ErrAccountLocked, "account_locked")
}

// T-27: ErrProviderAlreadyLinked → redirect provider_already_linked.
func TestHandleCallback_ErrProviderAlreadyLinked(t *testing.T) {
	runCallbackErrorTest(t, oauthshared.ErrProviderAlreadyLinked, "provider_already_linked")
}

// ─────────────────────────────────────────────────────────────────────────────
// HandleCallback cookie / redirect tests
// ─────────────────────────────────────────────────────────────────────────────

// T-30: Login mode sets oauth_access_token cookie (MaxAge=30, non-HttpOnly).
func TestHandleCallback_LoginMode_SetsOAuthAccessTokenCookie(t *testing.T) {
	kv := newFakeKV()
	state := seedState(kv, "verifier", "")

	svc := &oauthsharedtest.GoogleFakeServicer{
		HandleCallbackFn: func(_ context.Context, _ google.CallbackInput) (google.CallbackResult, error) {
			return happyCallbackResult(), nil
		},
	}
	h := newHandler(svc, kv)

	r := newCallbackRequest(state, "code123", "")
	w := httptest.NewRecorder()
	h.HandleCallback(w, r)

	cookies := w.Result().Cookies()
	var oauthCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "oauth_access_token" {
			oauthCookie = c
			break
		}
	}
	require.NotNil(t, oauthCookie, "oauth_access_token cookie not set")
	assert.Equal(t, 30, oauthCookie.MaxAge)
	assert.False(t, oauthCookie.HttpOnly)
}

// T-31: Login mode sets refresh_token cookie (HttpOnly, Path=/api/v1/auth).
func TestHandleCallback_LoginMode_SetsRefreshTokenCookie(t *testing.T) {
	kv := newFakeKV()
	state := seedState(kv, "verifier", "")

	svc := &oauthsharedtest.GoogleFakeServicer{
		HandleCallbackFn: func(_ context.Context, _ google.CallbackInput) (google.CallbackResult, error) {
			return happyCallbackResult(), nil
		},
	}
	h := newHandler(svc, kv)

	r := newCallbackRequest(state, "code123", "")
	w := httptest.NewRecorder()
	h.HandleCallback(w, r)

	cookies := w.Result().Cookies()
	var refreshCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "refresh_token" {
			refreshCookie = c
			break
		}
	}
	require.NotNil(t, refreshCookie, "refresh_token cookie not set")
	assert.True(t, refreshCookie.HttpOnly)
	assert.Equal(t, "/api/v1/auth", refreshCookie.Path)
}

// T-32: Link mode sets no session cookies.
func TestHandleCallback_LinkMode_NoSessionCookies(t *testing.T) {
	kv := newFakeKV()
	state := seedState(kv, "verifier", "")

	svc := &oauthsharedtest.GoogleFakeServicer{
		HandleCallbackFn: func(_ context.Context, _ google.CallbackInput) (google.CallbackResult, error) {
			return google.CallbackResult{Linked: true}, nil
		},
	}
	h := newHandler(svc, kv)

	r := newCallbackRequest(state, "code123", "")
	w := httptest.NewRecorder()
	h.HandleCallback(w, r)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "action=linked")

	cookies := w.Result().Cookies()
	for _, c := range cookies {
		assert.NotEqual(t, "refresh_token", c.Name, "refresh_token cookie must not be set in link mode")
		assert.NotEqual(t, "oauth_access_token", c.Name, "oauth_access_token cookie must not be set in link mode")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HandleUnlink tests
// ─────────────────────────────────────────────────────────────────────────────

func newUnlinkRequest(userID string) *http.Request {
	r := httptest.NewRequest(http.MethodDelete, "/unlink", nil)
	if userID != "" {
		r = r.WithContext(token.InjectUserIDForTest(r.Context(), userID))
	}
	return r
}

// T-41: ErrIdentityNotFound → 404 not_found.
func TestHandleUnlink_ErrIdentityNotFound_Returns404(t *testing.T) {
	svc := &oauthsharedtest.GoogleFakeServicer{
		UnlinkGoogleFn: func(_ context.Context, _ [16]byte, _, _ string) error {
			return oauthshared.ErrIdentityNotFound
		},
	}
	h := newHandler(svc, newFakeKV())

	w := httptest.NewRecorder()
	h.HandleUnlink(w, newUnlinkRequest(uuid.New().String()))

	assert.Equal(t, http.StatusNotFound, w.Code)
	assertJSONCode(t, w, "not_found")
}

// T-43: ErrLastAuthMethod → 422 last_auth_method.
func TestHandleUnlink_ErrLastAuthMethod_Returns422(t *testing.T) {
	svc := &oauthsharedtest.GoogleFakeServicer{
		UnlinkGoogleFn: func(_ context.Context, _ [16]byte, _, _ string) error {
			return oauthshared.ErrLastAuthMethod
		},
	}
	h := newHandler(svc, newFakeKV())

	w := httptest.NewRecorder()
	h.HandleUnlink(w, newUnlinkRequest(uuid.New().String()))

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assertJSONCode(t, w, "last_auth_method")
}

// T-47: Missing JWT → 401 unauthorized.
func TestHandleUnlink_MissingJWT_Returns401(t *testing.T) {
	h := newHandler(&oauthsharedtest.GoogleFakeServicer{}, newFakeKV())

	// No user ID injected into context.
	r := httptest.NewRequest(http.MethodDelete, "/unlink", nil)
	w := httptest.NewRecorder()
	h.HandleUnlink(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assertJSONCode(t, w, "unauthorized")
}

// HandleUnlink: uuid.Parse failure on malformed sub → 401 unauthorized.
func TestHandleUnlink_MalformedSub_Returns401(t *testing.T) {
	h := newHandler(&oauthsharedtest.GoogleFakeServicer{}, newFakeKV())

	r := httptest.NewRequest(http.MethodDelete, "/unlink", nil)
	r = r.WithContext(token.InjectUserIDForTest(r.Context(), "not-a-uuid"))
	w := httptest.NewRecorder()
	h.HandleUnlink(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assertJSONCode(t, w, "unauthorized")
}

// HandleUnlink: unexpected service error → 500 internal_error.
func TestHandleUnlink_InternalError_Returns500(t *testing.T) {
	svc := &oauthsharedtest.GoogleFakeServicer{
		UnlinkGoogleFn: func(_ context.Context, _ [16]byte, _, _ string) error {
			return errors.New("db down")
		},
	}
	h := newHandler(svc, newFakeKV())

	w := httptest.NewRecorder()
	h.HandleUnlink(w, newUnlinkRequest(uuid.New().String()))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertJSONCode(t, w, "internal_error")
}

// HandleUnlink: success → 200 with message.
func TestHandleUnlink_Success_Returns200(t *testing.T) {
	h := newHandler(&oauthsharedtest.GoogleFakeServicer{}, newFakeKV())

	w := httptest.NewRecorder()
	h.HandleUnlink(w, newUnlinkRequest(uuid.New().String()))

	assert.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, "google account unlinked successfully", body.Message)
}

// HandleCallback: corrupt KV value → redirect invalid_state.
func TestHandleCallback_CorruptKVValue_RedirectsInvalidState(t *testing.T) {
	kv := newFakeKV()
	state := uuid.New().String()
	kv.data["goauth:state:"+state] = "{not valid json"
	h := newHandler(&oauthsharedtest.GoogleFakeServicer{}, kv)

	w := httptest.NewRecorder()
	h.HandleCallback(w, newCallbackRequest(state, "code123", ""))

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "error=invalid_state")
}

// HandleCallback: MintTokens failure → redirect server_error.
func TestHandleCallback_MintTokensFailure_RedirectsServerError(t *testing.T) {
	kv := newFakeKV()
	state := seedState(kv, "verifier", "")

	// Return a session with a zero-value RefreshExpiry so MintTokens fails.
	svc := &oauthsharedtest.GoogleFakeServicer{
		HandleCallbackFn: func(_ context.Context, _ google.CallbackInput) (google.CallbackResult, error) {
			return google.CallbackResult{
				Session: oauthshared.LoggedInSession{
					UserID:    [16]byte(uuid.New()),
					SessionID: [16]byte(uuid.New()),
					RefreshJTI: [16]byte(uuid.New()),
					FamilyID:  [16]byte(uuid.New()),
					// RefreshExpiry zero → negative TTL → GenerateRefreshToken errors.
				},
			}, nil
		},
	}
	h := newHandler(svc, kv)

	w := httptest.NewRecorder()
	h.HandleCallback(w, newCallbackRequest(state, "code123", ""))

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "error=server_error")
}

// HandleCallback: login mode redirect URL has provider=google, no action=linked.
func TestHandleCallback_LoginMode_RedirectURLHasProviderGoogle(t *testing.T) {
	kv := newFakeKV()
	state := seedState(kv, "verifier", "")

	svc := &oauthsharedtest.GoogleFakeServicer{
		HandleCallbackFn: func(_ context.Context, _ google.CallbackInput) (google.CallbackResult, error) {
			return happyCallbackResult(), nil
		},
	}
	h := newHandler(svc, kv)

	w := httptest.NewRecorder()
	h.HandleCallback(w, newCallbackRequest(state, "code123", ""))

	loc := w.Header().Get("Location")
	assert.Contains(t, loc, "provider=google")
	assert.NotContains(t, loc, "action=linked")
}

// T-32 (extended): link mode redirect URL has both provider=google and action=linked.
func TestHandleCallback_LinkMode_RedirectURLHasProviderAndAction(t *testing.T) {
	kv := newFakeKV()
	state := seedState(kv, "verifier", "")

	svc := &oauthsharedtest.GoogleFakeServicer{
		HandleCallbackFn: func(_ context.Context, _ google.CallbackInput) (google.CallbackResult, error) {
			return google.CallbackResult{Linked: true}, nil
		},
	}
	h := newHandler(svc, kv)

	w := httptest.NewRecorder()
	h.HandleCallback(w, newCallbackRequest(state, "code123", ""))

	loc := w.Header().Get("Location")
	assert.Contains(t, loc, "provider=google")
	assert.Contains(t, loc, "action=linked")
}

// ─────────────────────────────────────────────────────────────────────────────
// Assertion helpers
// ─────────────────────────────────────────────────────────────────────────────

// assertJSONCode decodes the response body and asserts the "code" field.
func assertJSONCode(t *testing.T, w *httptest.ResponseRecorder, wantCode string) {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, wantCode, body.Code)
}
