package google_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/domain/oauth/google"
	oauthshared "github.com/7-Dany/store/backend/internal/domain/oauth/shared"
	oauthsharedtest "github.com/7-Dany/store/backend/internal/domain/oauth/shared/testutil"
)

// ─────────────────────────────────────────────────────────────────────────────
// Local test fakes
// ─────────────────────────────────────────────────────────────────────────────

type FakeProvider struct {
	ExchangeCodeFn  func(ctx context.Context, code, verifier string) (google.GoogleTokens, error)
	VerifyIDTokenFn func(ctx context.Context, raw string) (google.GoogleClaims, error)
}

func (f *FakeProvider) ExchangeCode(ctx context.Context, code, verifier string) (google.GoogleTokens, error) {
	if f.ExchangeCodeFn != nil {
		return f.ExchangeCodeFn(ctx, code, verifier)
	}
	return google.GoogleTokens{IDToken: "raw.id.token", AccessToken: "raw-access-token"}, nil
}

func (f *FakeProvider) VerifyIDToken(ctx context.Context, raw string) (google.GoogleClaims, error) {
	if f.VerifyIDTokenFn != nil {
		return f.VerifyIDTokenFn(ctx, raw)
	}
	return google.GoogleClaims{Sub: "sub123", Email: "user@example.com", Name: "Test User", Picture: "https://example.com/pic.jpg"}, nil
}

type FakeEncryptor struct {
	Result string
	Err    error
}

func (f *FakeEncryptor) Encrypt(plaintext string) (string, error) {
	return f.Result, f.Err
}

// ─────────────────────────────────────────────────────────────────────────────
// Constructor helper
// ─────────────────────────────────────────────────────────────────────────────

func newTestService(t *testing.T) (*google.Service, *oauthsharedtest.GoogleFakeStorer, *FakeProvider, *FakeEncryptor) {
	t.Helper()
	store := &oauthsharedtest.GoogleFakeStorer{}
	provider := &FakeProvider{}
	enc := &FakeEncryptor{Result: "enc:faketoken"}
	svc := google.NewService(store, provider, enc)
	return svc, store, provider, enc
}

func defaultCallbackInput() google.CallbackInput {
	return google.CallbackInput{
		Code:         "auth-code",
		CodeVerifier: "verifier",
		IPAddress:    "127.0.0.1",
		UserAgent:    "test-agent",
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HandleCallback — S-layer tests (T-12 to T-35)
// ─────────────────────────────────────────────────────────────────────────────

// T-12: ExchangeCode failure → ErrTokenExchangeFailed.
func TestHandleCallback_T12_ExchangeCodeFailure(t *testing.T) {
	svc, _, provider, _ := newTestService(t)
	provider.ExchangeCodeFn = func(_ context.Context, _, _ string) (google.GoogleTokens, error) {
		return google.GoogleTokens{}, errors.New("network error")
	}

	_, err := svc.HandleCallback(context.Background(), defaultCallbackInput())
	assert.ErrorIs(t, err, google.ErrTokenExchangeFailed)
}

// T-14: VerifyIDToken failure → ErrInvalidIDToken.
func TestHandleCallback_T14_VerifyIDTokenFailure(t *testing.T) {
	svc, _, provider, _ := newTestService(t)
	provider.VerifyIDTokenFn = func(_ context.Context, _ string) (google.GoogleClaims, error) {
		return google.GoogleClaims{}, errors.New("bad sig")
	}

	_, err := svc.HandleCallback(context.Background(), defaultCallbackInput())
	assert.ErrorIs(t, err, google.ErrInvalidIDToken)
}

// T-16: Encrypt failure → internal error (not ErrTokenExchangeFailed).
func TestHandleCallback_T16_EncryptFailure(t *testing.T) {
	svc, _, _, enc := newTestService(t)
	enc.Err = errors.New("kms unavailable")
	enc.Result = ""

	_, err := svc.HandleCallback(context.Background(), defaultCallbackInput())
	require.Error(t, err)
	assert.False(t, errors.Is(err, google.ErrTokenExchangeFailed))
	assert.False(t, errors.Is(err, google.ErrInvalidIDToken))
}

// T-18: Login — existing identity, active user → OAuthLoginTx called, NewUser==false.
func TestHandleCallback_T18_LoginExistingIdentityActiveUser(t *testing.T) {
	svc, store, _, _ := newTestService(t)

	existingUserID := [16]byte{1}
	store.GetIdentityByProviderUIDFn = func(_ context.Context, _ string) (google.ProviderIdentity, error) {
		return google.ProviderIdentity{UserID: existingUserID}, nil
	}
	store.GetUserForOAuthCallbackFn = func(_ context.Context, _ [16]byte) (google.OAuthUserRecord, error) {
		return google.OAuthUserRecord{IsActive: true}, nil
	}
	loginCalled := false
	store.OAuthLoginTxFn = func(_ context.Context, _ google.OAuthLoginTxInput) (oauthshared.LoggedInSession, error) {
		loginCalled = true
		return oauthshared.LoggedInSession{}, nil
	}

	result, err := svc.HandleCallback(context.Background(), defaultCallbackInput())
	require.NoError(t, err)
	assert.True(t, loginCalled)
	assert.False(t, result.NewUser)
	assert.False(t, result.Linked)
}

// T-19: Login — existing identity, user is_locked → ErrAccountLocked.
func TestHandleCallback_T19_LoginExistingIdentityUserLocked(t *testing.T) {
	svc, store, _, _ := newTestService(t)

	store.GetIdentityByProviderUIDFn = func(_ context.Context, _ string) (google.ProviderIdentity, error) {
		return google.ProviderIdentity{UserID: [16]byte{1}}, nil
	}
	store.GetUserForOAuthCallbackFn = func(_ context.Context, _ [16]byte) (google.OAuthUserRecord, error) {
		return google.OAuthUserRecord{IsLocked: true}, nil
	}

	_, err := svc.HandleCallback(context.Background(), defaultCallbackInput())
	assert.ErrorIs(t, err, oauthshared.ErrAccountLocked)
}

// T-20: Login — existing identity, user admin_locked → ErrAccountLocked.
func TestHandleCallback_T20_LoginExistingIdentityAdminLocked(t *testing.T) {
	svc, store, _, _ := newTestService(t)

	store.GetIdentityByProviderUIDFn = func(_ context.Context, _ string) (google.ProviderIdentity, error) {
		return google.ProviderIdentity{UserID: [16]byte{1}}, nil
	}
	store.GetUserForOAuthCallbackFn = func(_ context.Context, _ [16]byte) (google.OAuthUserRecord, error) {
		return google.OAuthUserRecord{AdminLocked: true}, nil
	}

	_, err := svc.HandleCallback(context.Background(), defaultCallbackInput())
	assert.ErrorIs(t, err, oauthshared.ErrAccountLocked)
}

// T-21: Login — no identity, email match, active → UpsertUserIdentity + OAuthLoginTx called on existing userID.
func TestHandleCallback_T21_LoginNoIdentityEmailMatchActive(t *testing.T) {
	svc, store, _, _ := newTestService(t)

	existingUserID := [16]byte{2}
	store.GetUserByEmailForOAuthFn = func(_ context.Context, _ string) (google.OAuthUserRecord, error) {
		return google.OAuthUserRecord{ID: existingUserID, IsActive: true}, nil
	}
	upsertCalled := false
	store.UpsertUserIdentityFn = func(_ context.Context, in google.UpsertIdentityInput) error {
		upsertCalled = true
		assert.Equal(t, existingUserID, in.UserID)
		return nil
	}
	loginCalled := false
	store.OAuthLoginTxFn = func(_ context.Context, in google.OAuthLoginTxInput) (oauthshared.LoggedInSession, error) {
		loginCalled = true
		assert.Equal(t, existingUserID, in.UserID)
		return oauthshared.LoggedInSession{}, nil
	}

	result, err := svc.HandleCallback(context.Background(), defaultCallbackInput())
	require.NoError(t, err)
	assert.True(t, upsertCalled)
	assert.True(t, loginCalled)
	assert.False(t, result.NewUser)
}

// T-22: Login — no identity, email match, user locked → ErrAccountLocked.
func TestHandleCallback_T22_LoginNoIdentityEmailMatchLocked(t *testing.T) {
	svc, store, _, _ := newTestService(t)

	store.GetUserByEmailForOAuthFn = func(_ context.Context, _ string) (google.OAuthUserRecord, error) {
		return google.OAuthUserRecord{IsLocked: true}, nil
	}

	_, err := svc.HandleCallback(context.Background(), defaultCallbackInput())
	assert.ErrorIs(t, err, oauthshared.ErrAccountLocked)
}

// T-23: Login — no identity, no email match → OAuthRegisterTx called, NewUser==true.
func TestHandleCallback_T23_LoginNewUser(t *testing.T) {
	svc, store, _, _ := newTestService(t)

	// Both stores default to ErrIdentityNotFound — brand-new user path.
	registerCalled := false
	store.OAuthRegisterTxFn = func(_ context.Context, _ google.OAuthRegisterTxInput) (oauthshared.LoggedInSession, error) {
		registerCalled = true
		return oauthshared.LoggedInSession{}, nil
	}

	result, err := svc.HandleCallback(context.Background(), defaultCallbackInput())
	require.NoError(t, err)
	assert.True(t, registerCalled)
	assert.True(t, result.NewUser)
}

// T-25: Link mode — happy path → UpsertUserIdentity + audit with EventOAuthLinked, Linked==true.
func TestHandleCallback_T25_LinkModeHappyPath(t *testing.T) {
	svc, store, _, _ := newTestService(t)

	in := defaultCallbackInput()
	in.LinkUserID = "00000000-0000-0000-0000-000000000001"

	upsertCalled := false
	store.UpsertUserIdentityFn = func(_ context.Context, _ google.UpsertIdentityInput) error {
		upsertCalled = true
		return nil
	}
	var auditEvent audit.EventType
	store.InsertAuditLogTxFn = func(_ context.Context, a google.OAuthAuditInput) error {
		auditEvent = a.Event
		return nil
	}

	result, err := svc.HandleCallback(context.Background(), in)
	require.NoError(t, err)
	assert.True(t, upsertCalled)
	assert.Equal(t, audit.EventOAuthLinked, auditEvent)
	assert.True(t, result.Linked)
}

// T-26: Link mode — provider already linked to different user → ErrProviderAlreadyLinked.
func TestHandleCallback_T26_LinkModeConflict(t *testing.T) {
	svc, store, _, _ := newTestService(t)

	differentUserID := [16]byte{99}
	in := defaultCallbackInput()
	in.LinkUserID = "00000000-0000-0000-0000-000000000001"

	store.GetIdentityByProviderUIDFn = func(_ context.Context, _ string) (google.ProviderIdentity, error) {
		return google.ProviderIdentity{UserID: differentUserID}, nil
	}

	_, err := svc.HandleCallback(context.Background(), in)
	assert.ErrorIs(t, err, oauthshared.ErrProviderAlreadyLinked)
}

// T-28: Link mode — target user locked → ErrAccountLocked.
func TestHandleCallback_T28_LinkModeTargetUserLocked(t *testing.T) {
	svc, store, _, _ := newTestService(t)

	in := defaultCallbackInput()
	in.LinkUserID = "00000000-0000-0000-0000-000000000001"

	store.GetUserForOAuthCallbackFn = func(_ context.Context, _ [16]byte) (google.OAuthUserRecord, error) {
		return google.OAuthUserRecord{IsLocked: true}, nil
	}

	_, err := svc.HandleCallback(context.Background(), in)
	assert.ErrorIs(t, err, oauthshared.ErrAccountLocked)
}

// T-29: Access token stored with "enc:" prefix in UpsertUserIdentity call.
func TestHandleCallback_T29_AccessTokenEncPrefix(t *testing.T) {
	svc, store, _, _ := newTestService(t)

	// Force existing-identity path so UpsertUserIdentity is guaranteed to be called.
	store.GetIdentityByProviderUIDFn = func(_ context.Context, _ string) (google.ProviderIdentity, error) {
		return google.ProviderIdentity{UserID: [16]byte{1}}, nil
	}
	var capturedToken string
	store.UpsertUserIdentityFn = func(_ context.Context, in google.UpsertIdentityInput) error {
		capturedToken = in.AccessToken
		return nil
	}

	_, err := svc.HandleCallback(context.Background(), defaultCallbackInput())
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(capturedToken, "enc:"), "expected enc: prefix, got %q", capturedToken)
}

// T-33: OAuthLoginTx receives a cancel-free context.
func TestHandleCallback_T33_LoginTxCtxCancelFree(t *testing.T) {
	svc, store, _, _ := newTestService(t)

	store.GetIdentityByProviderUIDFn = func(_ context.Context, _ string) (google.ProviderIdentity, error) {
		return google.ProviderIdentity{UserID: [16]byte{1}}, nil
	}

	parentCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var capturedCtx context.Context
	store.OAuthLoginTxFn = func(ctx context.Context, _ google.OAuthLoginTxInput) (oauthshared.LoggedInSession, error) {
		capturedCtx = ctx
		return oauthshared.LoggedInSession{}, nil
	}

	_, err := svc.HandleCallback(parentCtx, defaultCallbackInput())
	require.NoError(t, err)
	require.NotNil(t, capturedCtx)
	assert.Nil(t, capturedCtx.Done(), "OAuthLoginTx ctx should be cancel-free")
}

// T-34: Link mode InsertAuditLogTx receives a cancel-free context.
func TestHandleCallback_T34_LinkAuditCtxCancelFree(t *testing.T) {
	svc, store, _, _ := newTestService(t)

	in := defaultCallbackInput()
	in.LinkUserID = "00000000-0000-0000-0000-000000000001"

	parentCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var capturedCtx context.Context
	store.InsertAuditLogTxFn = func(ctx context.Context, _ google.OAuthAuditInput) error {
		capturedCtx = ctx
		return nil
	}

	_, err := svc.HandleCallback(parentCtx, in)
	require.NoError(t, err)
	require.NotNil(t, capturedCtx)
	assert.Nil(t, capturedCtx.Done(), "InsertAuditLogTx ctx should be cancel-free")
}

// T-35: OAuthRegisterTx error is wrapped with "google.HandleCallback:" prefix.
func TestHandleCallback_T35_RegisterTxErrorWraps(t *testing.T) {
	svc, store, _, _ := newTestService(t)

	rawErr := errors.New("db connection refused")
	store.OAuthRegisterTxFn = func(_ context.Context, _ google.OAuthRegisterTxInput) (oauthshared.LoggedInSession, error) {
		return oauthshared.LoggedInSession{}, rawErr
	}

	_, err := svc.HandleCallback(context.Background(), defaultCallbackInput())
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "google.HandleCallback:"), "expected wrap prefix, got: %s", err)
	assert.ErrorIs(t, err, rawErr)
}

// ─────────────────────────────────────────────────────────────────────────────
// UnlinkGoogle — S-layer tests (T-39 to T-49)
// ─────────────────────────────────────────────────────────────────────────────

func defaultUserID() [16]byte { return [16]byte{1} }

// T-39: Happy path — identity_count=2, has_password=false → DeleteUserIdentity called, nil error.
func TestUnlinkGoogle_T39_HappyPath(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	// GetUserAuthMethods default: {HasPassword: false, IdentityCount: 2} — safe to unlink.

	store.GetIdentityByUserAndProviderFn = func(_ context.Context, _ [16]byte) (google.ProviderIdentity, error) {
		return google.ProviderIdentity{}, nil
	}
	deleteCalled := false
	store.DeleteUserIdentityFn = func(_ context.Context, _ [16]byte) (int64, error) {
		deleteCalled = true
		return 1, nil
	}

	err := svc.UnlinkGoogle(context.Background(), defaultUserID(), "", "")
	require.NoError(t, err)
	assert.True(t, deleteCalled)
}

// T-40: Identity not found → ErrIdentityNotFound.
func TestUnlinkGoogle_T40_IdentityNotFound(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	// GetIdentityByUserAndProvider defaults to ErrIdentityNotFound.

	err := svc.UnlinkGoogle(context.Background(), defaultUserID(), "", "")
	assert.ErrorIs(t, err, oauthshared.ErrIdentityNotFound)
}

// T-42: Last method — no password, 1 identity → ErrLastAuthMethod.
func TestUnlinkGoogle_T42_LastMethodNoPassword(t *testing.T) {
	svc, store, _, _ := newTestService(t)

	store.GetUserAuthMethodsFn = func(_ context.Context, _ [16]byte) (google.UserAuthMethods, error) {
		return google.UserAuthMethods{HasPassword: false, IdentityCount: 1}, nil
	}
	store.GetIdentityByUserAndProviderFn = func(_ context.Context, _ [16]byte) (google.ProviderIdentity, error) {
		return google.ProviderIdentity{}, nil
	}

	err := svc.UnlinkGoogle(context.Background(), defaultUserID(), "", "")
	assert.ErrorIs(t, err, oauthshared.ErrLastAuthMethod)
}

// T-44: Has password + 1 identity → can unlink (total = 2).
func TestUnlinkGoogle_T44_HasPasswordOneIdentity(t *testing.T) {
	svc, store, _, _ := newTestService(t)

	store.GetUserAuthMethodsFn = func(_ context.Context, _ [16]byte) (google.UserAuthMethods, error) {
		return google.UserAuthMethods{HasPassword: true, IdentityCount: 1}, nil
	}
	store.GetIdentityByUserAndProviderFn = func(_ context.Context, _ [16]byte) (google.ProviderIdentity, error) {
		return google.ProviderIdentity{}, nil
	}

	err := svc.UnlinkGoogle(context.Background(), defaultUserID(), "", "")
	require.NoError(t, err)
}

// T-45: No password + 2 identities → can unlink.
func TestUnlinkGoogle_T45_NoPasswordTwoIdentities(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	// GetUserAuthMethods default: {HasPassword: false, IdentityCount: 2}.

	store.GetIdentityByUserAndProviderFn = func(_ context.Context, _ [16]byte) (google.ProviderIdentity, error) {
		return google.ProviderIdentity{}, nil
	}

	err := svc.UnlinkGoogle(context.Background(), defaultUserID(), "", "")
	require.NoError(t, err)
}

// T-46: DeleteUserIdentity returns 0 rows → ErrIdentityNotFound (lost race).
func TestUnlinkGoogle_T46_DeleteZeroRows(t *testing.T) {
	svc, store, _, _ := newTestService(t)

	store.GetIdentityByUserAndProviderFn = func(_ context.Context, _ [16]byte) (google.ProviderIdentity, error) {
		return google.ProviderIdentity{}, nil
	}
	store.DeleteUserIdentityFn = func(_ context.Context, _ [16]byte) (int64, error) {
		return 0, nil
	}

	err := svc.UnlinkGoogle(context.Background(), defaultUserID(), "", "")
	assert.ErrorIs(t, err, oauthshared.ErrIdentityNotFound)
}

// T-48: InsertAuditLogTx receives a cancel-free context.
func TestUnlinkGoogle_T48_AuditCtxCancelFree(t *testing.T) {
	svc, store, _, _ := newTestService(t)

	store.GetIdentityByUserAndProviderFn = func(_ context.Context, _ [16]byte) (google.ProviderIdentity, error) {
		return google.ProviderIdentity{}, nil
	}

	parentCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var capturedCtx context.Context
	store.InsertAuditLogTxFn = func(ctx context.Context, _ google.OAuthAuditInput) error {
		capturedCtx = ctx
		return nil
	}

	err := svc.UnlinkGoogle(parentCtx, defaultUserID(), "", "")
	require.NoError(t, err)
	require.NotNil(t, capturedCtx)
	assert.Nil(t, capturedCtx.Done(), "InsertAuditLogTx ctx should be cancel-free")
}

// T-49: Store error is wrapped with "google.UnlinkGoogle:" prefix.
func TestUnlinkGoogle_T49_StoreErrorWraps(t *testing.T) {
	svc, store, _, _ := newTestService(t)

	store.GetIdentityByUserAndProviderFn = func(_ context.Context, _ [16]byte) (google.ProviderIdentity, error) {
		return google.ProviderIdentity{}, nil
	}
	rawErr := errors.New("connection pool exhausted")
	store.DeleteUserIdentityFn = func(_ context.Context, _ [16]byte) (int64, error) {
		return 0, rawErr
	}

	err := svc.UnlinkGoogle(context.Background(), defaultUserID(), "", "")
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "google.UnlinkGoogle:"), "expected wrap prefix, got: %s", err)
	assert.ErrorIs(t, err, rawErr)
}
