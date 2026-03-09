package telegram_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/7-Dany/store/backend/internal/domain/oauth/shared"
	oauthsharedtest "github.com/7-Dany/store/backend/internal/domain/oauth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/oauth/telegram"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

var (
	testUserID    = [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	otherUserID   = [16]byte{16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}
	testIPAddress = "127.0.0.1"
	testUserAgent = "TestAgent/1.0"
)

func testUser() telegram.TelegramUser {
	return telegram.TelegramUser{
		ID:        123456789,
		FirstName: "Test",
		LastName:  "User",
	}
}

func callbackInput() telegram.CallbackInput {
	return telegram.CallbackInput{
		User:      testUser(),
		IPAddress: testIPAddress,
		UserAgent: testUserAgent,
	}
}

func linkInput() telegram.LinkInput {
	return telegram.LinkInput{
		UserID:    testUserID,
		User:      testUser(),
		IPAddress: testIPAddress,
		UserAgent: testUserAgent,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestService_HandleCallback
// ─────────────────────────────────────────────────────────────────────────────

func TestService_HandleCallback(t *testing.T) {
	t.Parallel()

	// S-01: Happy path — new user (register).
	t.Run("S-01_new_user", func(t *testing.T) {
		t.Parallel()

		wantSession := oauthshared.LoggedInSession{UserID: testUserID}
		store := &oauthsharedtest.TelegramFakeStorer{
			// GetIdentityByProviderUID default → ErrIdentityNotFound (new user)
			OAuthRegisterTxFn: func(_ context.Context, _ telegram.OAuthRegisterTxInput) (oauthshared.LoggedInSession, error) {
				return wantSession, nil
			},
		}
		svc := telegram.NewService(store)

		result, err := svc.HandleCallback(context.Background(), callbackInput())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.NewUser {
			t.Error("expected NewUser=true")
		}
		if result.Session != wantSession {
			t.Errorf("session mismatch: got %v want %v", result.Session, wantSession)
		}
	})

	// S-02: Happy path — returning user (login).
	t.Run("S-02_returning_user", func(t *testing.T) {
		t.Parallel()

		wantSession := oauthshared.LoggedInSession{UserID: testUserID}
		loginTxCalled := 0
		store := &oauthsharedtest.TelegramFakeStorer{
			GetIdentityByProviderUIDFn: func(_ context.Context, _ string) (telegram.ProviderIdentity, error) {
				return telegram.ProviderIdentity{UserID: testUserID}, nil
			},
			GetUserForOAuthCallbackFn: func(_ context.Context, _ [16]byte) (telegram.OAuthUserRecord, error) {
				return telegram.OAuthUserRecord{IsActive: true}, nil
			},
			OAuthLoginTxFn: func(_ context.Context, _ telegram.OAuthLoginTxInput) (oauthshared.LoggedInSession, error) {
				loginTxCalled++
				return wantSession, nil
			},
		}
		svc := telegram.NewService(store)

		result, err := svc.HandleCallback(context.Background(), callbackInput())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.NewUser {
			t.Error("expected NewUser=false")
		}
		if result.Session != wantSession {
			t.Errorf("session mismatch: got %v want %v", result.Session, wantSession)
		}
		if loginTxCalled != 1 {
			t.Errorf("OAuthLoginTx called %d times, want 1", loginTxCalled)
		}
	})

	// S-03: Account locked — returning user.
	t.Run("S-03_account_locked", func(t *testing.T) {
		t.Parallel()

		store := &oauthsharedtest.TelegramFakeStorer{
			GetIdentityByProviderUIDFn: func(_ context.Context, _ string) (telegram.ProviderIdentity, error) {
				return telegram.ProviderIdentity{UserID: testUserID}, nil
			},
			GetUserForOAuthCallbackFn: func(_ context.Context, _ [16]byte) (telegram.OAuthUserRecord, error) {
				return telegram.OAuthUserRecord{IsLocked: true}, nil
			},
		}
		svc := telegram.NewService(store)

		_, err := svc.HandleCallback(context.Background(), callbackInput())
		if !errors.Is(err, oauthshared.ErrAccountLocked) {
			t.Errorf("expected ErrAccountLocked, got %v", err)
		}
	})

	// S-04: GetIdentityByProviderUID returns unexpected error.
	t.Run("S-04_get_identity_unexpected_error", func(t *testing.T) {
		t.Parallel()

		store := &oauthsharedtest.TelegramFakeStorer{
			GetIdentityByProviderUIDFn: func(_ context.Context, _ string) (telegram.ProviderIdentity, error) {
				return telegram.ProviderIdentity{}, errors.New("db connection lost")
			},
		}
		svc := telegram.NewService(store)

		_, err := svc.HandleCallback(context.Background(), callbackInput())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !containsPrefix(err.Error(), "telegram.HandleCallback:") {
			t.Errorf("expected error prefix 'telegram.HandleCallback:', got %q", err.Error())
		}
	})

	// S-05: OAuthLoginTx failure.
	t.Run("S-05_login_tx_failure", func(t *testing.T) {
		t.Parallel()

		store := &oauthsharedtest.TelegramFakeStorer{
			GetIdentityByProviderUIDFn: func(_ context.Context, _ string) (telegram.ProviderIdentity, error) {
				return telegram.ProviderIdentity{UserID: testUserID}, nil
			},
			GetUserForOAuthCallbackFn: func(_ context.Context, _ [16]byte) (telegram.OAuthUserRecord, error) {
				return telegram.OAuthUserRecord{IsActive: true}, nil
			},
			OAuthLoginTxFn: func(_ context.Context, _ telegram.OAuthLoginTxInput) (oauthshared.LoggedInSession, error) {
				return oauthshared.LoggedInSession{}, errors.New("tx failed")
			},
		}
		svc := telegram.NewService(store)

		_, err := svc.HandleCallback(context.Background(), callbackInput())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !containsPrefix(err.Error(), "telegram.HandleCallback:") {
			t.Errorf("expected error prefix 'telegram.HandleCallback:', got %q", err.Error())
		}
	})

	// S-06: OAuthRegisterTx failure.
	t.Run("S-06_register_tx_failure", func(t *testing.T) {
		t.Parallel()

		store := &oauthsharedtest.TelegramFakeStorer{
			// default GetIdentityByProviderUID → ErrIdentityNotFound
			OAuthRegisterTxFn: func(_ context.Context, _ telegram.OAuthRegisterTxInput) (oauthshared.LoggedInSession, error) {
				return oauthshared.LoggedInSession{}, errors.New("register failed")
			},
		}
		svc := telegram.NewService(store)

		_, err := svc.HandleCallback(context.Background(), callbackInput())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !containsPrefix(err.Error(), "telegram.HandleCallback:") {
			t.Errorf("expected error prefix 'telegram.HandleCallback:', got %q", err.Error())
		}
	})

	// S-07: OAuthLoginTx is called with context.WithoutCancel ctx.
	t.Run("S-07_login_tx_without_cancel_ctx", func(t *testing.T) {
		t.Parallel()

		cancelCtx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var capturedCtx context.Context
		store := &oauthsharedtest.TelegramFakeStorer{
			GetIdentityByProviderUIDFn: func(_ context.Context, _ string) (telegram.ProviderIdentity, error) {
				return telegram.ProviderIdentity{UserID: testUserID}, nil
			},
			GetUserForOAuthCallbackFn: func(_ context.Context, _ [16]byte) (telegram.OAuthUserRecord, error) {
				return telegram.OAuthUserRecord{IsActive: true}, nil
			},
			OAuthLoginTxFn: func(ctx context.Context, _ telegram.OAuthLoginTxInput) (oauthshared.LoggedInSession, error) {
				capturedCtx = ctx
				return oauthshared.LoggedInSession{}, nil
			},
		}
		svc := telegram.NewService(store)

		_, err := svc.HandleCallback(cancelCtx, callbackInput())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedCtx == nil {
			t.Fatal("OAuthLoginTx was not called")
		}
		if capturedCtx.Done() != nil {
			t.Error("expected context.WithoutCancel ctx (Done() should be nil)")
		}
	})

	// S-08: OAuthRegisterTx is called with context.WithoutCancel ctx.
	t.Run("S-08_register_tx_without_cancel_ctx", func(t *testing.T) {
		t.Parallel()

		cancelCtx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var capturedCtx context.Context
		store := &oauthsharedtest.TelegramFakeStorer{
			// default GetIdentityByProviderUID → ErrIdentityNotFound
			OAuthRegisterTxFn: func(ctx context.Context, _ telegram.OAuthRegisterTxInput) (oauthshared.LoggedInSession, error) {
				capturedCtx = ctx
				return oauthshared.LoggedInSession{}, nil
			},
		}
		svc := telegram.NewService(store)

		_, err := svc.HandleCallback(cancelCtx, callbackInput())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedCtx == nil {
			t.Fatal("OAuthRegisterTx was not called")
		}
		if capturedCtx.Done() != nil {
			t.Error("expected context.WithoutCancel ctx (Done() should be nil)")
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// TestService_LinkTelegram
// ─────────────────────────────────────────────────────────────────────────────

func TestService_LinkTelegram(t *testing.T) {
	t.Parallel()

	// S-09: Happy path.
	t.Run("S-09_happy_path", func(t *testing.T) {
		t.Parallel()

		insertCalled := 0
		store := &oauthsharedtest.TelegramFakeStorer{
			// GetUserForOAuthCallback default → active user
			// GetIdentityByUserAndProvider default → ErrIdentityNotFound
			// GetIdentityByProviderUID default → ErrIdentityNotFound
			InsertUserIdentityFn: func(_ context.Context, _ telegram.InsertIdentityInput) error {
				insertCalled++
				return nil
			},
			// InsertAuditLogTx default → nil
		}
		svc := telegram.NewService(store)

		err := svc.LinkTelegram(context.Background(), linkInput())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if insertCalled != 1 {
			t.Errorf("InsertUserIdentity called %d times, want 1", insertCalled)
		}
	})

	// S-10: User already linked (ErrProviderAlreadyLinked).
	t.Run("S-10_already_linked", func(t *testing.T) {
		t.Parallel()

		store := &oauthsharedtest.TelegramFakeStorer{
			GetIdentityByUserAndProviderFn: func(_ context.Context, _ [16]byte) (telegram.ProviderIdentity, error) {
				return telegram.ProviderIdentity{UserID: testUserID}, nil
			},
		}
		svc := telegram.NewService(store)

		err := svc.LinkTelegram(context.Background(), linkInput())
		if !errors.Is(err, telegram.ErrProviderAlreadyLinked) {
			t.Errorf("expected ErrProviderAlreadyLinked, got %v", err)
		}
	})

	// S-11: Telegram UID taken by another user (ErrProviderUIDTaken).
	t.Run("S-11_uid_taken_by_other_user", func(t *testing.T) {
		t.Parallel()

		store := &oauthsharedtest.TelegramFakeStorer{
			// GetIdentityByUserAndProvider default → ErrIdentityNotFound
			GetIdentityByProviderUIDFn: func(_ context.Context, _ string) (telegram.ProviderIdentity, error) {
				return telegram.ProviderIdentity{UserID: otherUserID}, nil
			},
		}
		svc := telegram.NewService(store)

		err := svc.LinkTelegram(context.Background(), linkInput())
		if !errors.Is(err, telegram.ErrProviderUIDTaken) {
			t.Errorf("expected ErrProviderUIDTaken, got %v", err)
		}
	})

	// S-12: Idempotent — same user already has this identity.
	t.Run("S-12_idempotent_same_user", func(t *testing.T) {
		t.Parallel()

		insertCalled := 0
		store := &oauthsharedtest.TelegramFakeStorer{
			// GetIdentityByUserAndProvider default → ErrIdentityNotFound
			GetIdentityByProviderUIDFn: func(_ context.Context, _ string) (telegram.ProviderIdentity, error) {
				// Same user — idempotent fall-through.
				return telegram.ProviderIdentity{UserID: testUserID}, nil
			},
			InsertUserIdentityFn: func(_ context.Context, _ telegram.InsertIdentityInput) error {
				insertCalled++
				return nil
			},
		}
		svc := telegram.NewService(store)

		err := svc.LinkTelegram(context.Background(), linkInput())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if insertCalled != 1 {
			t.Errorf("InsertUserIdentity called %d times, want 1", insertCalled)
		}
	})

	// S-13: Account locked.
	t.Run("S-13_account_locked", func(t *testing.T) {
		t.Parallel()

		store := &oauthsharedtest.TelegramFakeStorer{
			GetUserForOAuthCallbackFn: func(_ context.Context, _ [16]byte) (telegram.OAuthUserRecord, error) {
				return telegram.OAuthUserRecord{IsLocked: true}, nil
			},
		}
		svc := telegram.NewService(store)

		err := svc.LinkTelegram(context.Background(), linkInput())
		if !errors.Is(err, oauthshared.ErrAccountLocked) {
			t.Errorf("expected ErrAccountLocked, got %v", err)
		}
	})

	// S-14: InsertAuditLogTx uses context.WithoutCancel ctx.
	t.Run("S-14_audit_without_cancel_ctx", func(t *testing.T) {
		t.Parallel()

		cancelCtx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var capturedCtx context.Context
		store := &oauthsharedtest.TelegramFakeStorer{
			InsertAuditLogTxFn: func(ctx context.Context, _ telegram.OAuthAuditInput) error {
				capturedCtx = ctx
				return nil
			},
		}
		svc := telegram.NewService(store)

		err := svc.LinkTelegram(cancelCtx, linkInput())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedCtx == nil {
			t.Fatal("InsertAuditLogTx was not called")
		}
		if capturedCtx.Done() != nil {
			t.Error("expected context.WithoutCancel ctx (Done() should be nil)")
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// TestService_UnlinkTelegram
// ─────────────────────────────────────────────────────────────────────────────

func TestService_UnlinkTelegram(t *testing.T) {
	t.Parallel()

	// S-15: Happy path — user has password.
	t.Run("S-15_happy_path_has_password", func(t *testing.T) {
		t.Parallel()

		store := &oauthsharedtest.TelegramFakeStorer{
			GetUserAuthMethodsFn: func(_ context.Context, _ [16]byte) (telegram.UserAuthMethods, error) {
				return telegram.UserAuthMethods{HasPassword: true, IdentityCount: 1}, nil
			},
			GetIdentityByUserAndProviderFn: func(_ context.Context, _ [16]byte) (telegram.ProviderIdentity, error) {
				return telegram.ProviderIdentity{UserID: testUserID}, nil
			},
			// DeleteUserIdentity default → (1, nil)
		}
		svc := telegram.NewService(store)

		err := svc.UnlinkTelegram(context.Background(), testUserID, testIPAddress, testUserAgent)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// S-16: Happy path — user has another OAuth identity.
	t.Run("S-16_happy_path_other_identity", func(t *testing.T) {
		t.Parallel()

		store := &oauthsharedtest.TelegramFakeStorer{
			GetUserAuthMethodsFn: func(_ context.Context, _ [16]byte) (telegram.UserAuthMethods, error) {
				return telegram.UserAuthMethods{HasPassword: false, IdentityCount: 2}, nil
			},
			GetIdentityByUserAndProviderFn: func(_ context.Context, _ [16]byte) (telegram.ProviderIdentity, error) {
				return telegram.ProviderIdentity{UserID: testUserID}, nil
			},
			// DeleteUserIdentity default → (1, nil)
		}
		svc := telegram.NewService(store)

		err := svc.UnlinkTelegram(context.Background(), testUserID, testIPAddress, testUserAgent)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// S-17: Provider not linked.
	t.Run("S-17_provider_not_linked", func(t *testing.T) {
		t.Parallel()

		store := &oauthsharedtest.TelegramFakeStorer{
			// GetUserAuthMethods default → HasPassword=false, IdentityCount=2
			// GetIdentityByUserAndProvider default → ErrIdentityNotFound
		}
		svc := telegram.NewService(store)

		err := svc.UnlinkTelegram(context.Background(), testUserID, testIPAddress, testUserAgent)
		if !errors.Is(err, telegram.ErrProviderNotLinked) {
			t.Errorf("expected ErrProviderNotLinked, got %v", err)
		}
	})

	// S-18: Last auth method.
	t.Run("S-18_last_auth_method", func(t *testing.T) {
		t.Parallel()

		store := &oauthsharedtest.TelegramFakeStorer{
			GetUserAuthMethodsFn: func(_ context.Context, _ [16]byte) (telegram.UserAuthMethods, error) {
				return telegram.UserAuthMethods{HasPassword: false, IdentityCount: 1}, nil
			},
			GetIdentityByUserAndProviderFn: func(_ context.Context, _ [16]byte) (telegram.ProviderIdentity, error) {
				return telegram.ProviderIdentity{UserID: testUserID}, nil
			},
		}
		svc := telegram.NewService(store)

		err := svc.UnlinkTelegram(context.Background(), testUserID, testIPAddress, testUserAgent)
		if !errors.Is(err, oauthshared.ErrLastAuthMethod) {
			t.Errorf("expected ErrLastAuthMethod, got %v", err)
		}
	})

	// S-19: Delete returns 0 rows (lost race).
	t.Run("S-19_delete_zero_rows_lost_race", func(t *testing.T) {
		t.Parallel()

		store := &oauthsharedtest.TelegramFakeStorer{
			GetUserAuthMethodsFn: func(_ context.Context, _ [16]byte) (telegram.UserAuthMethods, error) {
				return telegram.UserAuthMethods{HasPassword: true, IdentityCount: 1}, nil
			},
			GetIdentityByUserAndProviderFn: func(_ context.Context, _ [16]byte) (telegram.ProviderIdentity, error) {
				return telegram.ProviderIdentity{UserID: testUserID}, nil
			},
			DeleteUserIdentityFn: func(_ context.Context, _ [16]byte) (int64, error) {
				return 0, nil
			},
		}
		svc := telegram.NewService(store)

		err := svc.UnlinkTelegram(context.Background(), testUserID, testIPAddress, testUserAgent)
		if !errors.Is(err, telegram.ErrProviderNotLinked) {
			t.Errorf("expected ErrProviderNotLinked (lost race), got %v", err)
		}
	})

	// S-20: InsertAuditLogTx uses context.WithoutCancel ctx.
	t.Run("S-20_audit_without_cancel_ctx", func(t *testing.T) {
		t.Parallel()

		cancelCtx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var capturedCtx context.Context
		store := &oauthsharedtest.TelegramFakeStorer{
			GetUserAuthMethodsFn: func(_ context.Context, _ [16]byte) (telegram.UserAuthMethods, error) {
				return telegram.UserAuthMethods{HasPassword: true, IdentityCount: 1}, nil
			},
			GetIdentityByUserAndProviderFn: func(_ context.Context, _ [16]byte) (telegram.ProviderIdentity, error) {
				return telegram.ProviderIdentity{UserID: testUserID}, nil
			},
			InsertAuditLogTxFn: func(ctx context.Context, _ telegram.OAuthAuditInput) error {
				capturedCtx = ctx
				return nil
			},
		}
		svc := telegram.NewService(store)

		err := svc.UnlinkTelegram(cancelCtx, testUserID, testIPAddress, testUserAgent)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedCtx == nil {
			t.Fatal("InsertAuditLogTx was not called")
		}
		if capturedCtx.Done() != nil {
			t.Error("expected context.WithoutCancel ctx (Done() should be nil)")
		}
	})

	// S-21: GetUserAuthMethods returns an error.
	t.Run("S-21_get_auth_methods_error", func(t *testing.T) {
		t.Parallel()

		store := &oauthsharedtest.TelegramFakeStorer{
			GetUserAuthMethodsFn: func(_ context.Context, _ [16]byte) (telegram.UserAuthMethods, error) {
				return telegram.UserAuthMethods{}, errors.New("db error")
			},
		}
		svc := telegram.NewService(store)

		err := svc.UnlinkTelegram(context.Background(), testUserID, testIPAddress, testUserAgent)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !containsPrefix(err.Error(), "telegram.UnlinkTelegram:") {
			t.Errorf("expected error prefix 'telegram.UnlinkTelegram:', got %q", err.Error())
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// containsPrefix reports whether s starts with prefix.
func containsPrefix(s, prefix string) bool {
	return strings.HasPrefix(s, prefix)
}
