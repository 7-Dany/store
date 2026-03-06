package login_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/7-Dany/store/backend/internal/domain/auth/login"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/stretchr/testify/require"
)

func newService(store login.Storer) *login.Service {
	return login.NewService(store)
}

func makeLoginUser(pw string, t *testing.T) login.LoginUser {
	t.Helper()
	return login.LoginUser{
		ID:            authsharedtest.RandomUUID(),
		Email:         "user@example.com",
		PasswordHash:  authsharedtest.MustHashPassword(t, pw),
		IsActive:      true,
		EmailVerified: true,
		IsLocked:      false,
	}
}

// ── TestService_Login ─────────────────────────────────────────────────────────

func TestService_Login(t *testing.T) {
	t.Parallel()

	t.Run("success returns session data and resets failure counter", func(t *testing.T) {
		t.Parallel()
		user := makeLoginUser("Correct!1", t)
		wantSession := login.LoggedInSession{
			SessionID:     authsharedtest.RandomUUID(),
			RefreshJTI:    authsharedtest.RandomUUID(),
			FamilyID:      authsharedtest.RandomUUID(),
			RefreshExpiry: time.Now().Add(7 * 24 * time.Hour),
		}
		var resetCalled bool
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
			LoginTxFn:        func(_ context.Context, _ login.LoginTxInput) (login.LoggedInSession, error) { return wantSession, nil },
			ResetLoginFailuresTxFn: func(_ context.Context, _ [16]byte) error {
				resetCalled = true
				return nil
			},
		}
		svc := newService(store)
		got, err := svc.Login(context.Background(), login.LoginInput{Identifier: "user@example.com", Password: "Correct!1"})
		require.NoError(t, err)
		require.Equal(t, wantSession.SessionID, got.SessionID)
		require.Equal(t, wantSession.RefreshJTI, got.RefreshJTI)
		require.True(t, resetCalled, "ResetLoginFailuresTx must be called after a successful login")
	})

	t.Run("user not found runs dummy hash and returns ErrInvalidCredentials", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) {
				return login.LoginUser{}, authshared.ErrUserNotFound
			},
		}
		svc := newService(store)
		_, err := svc.Login(context.Background(), login.LoginInput{Identifier: "ghost@example.com", Password: "Passw0rd!1"})
		require.ErrorIs(t, err, authshared.ErrInvalidCredentials)
	})

	t.Run("wrong password increments failures and returns ErrInvalidCredentials", func(t *testing.T) {
		t.Parallel()
		user := makeLoginUser("Correct!1", t)
		var incCalled bool
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
			IncrementLoginFailuresTxFn: func(_ context.Context, _ [16]byte, _, _ string) error {
				incCalled = true
				return nil
			},
		}
		svc := newService(store)
		_, err := svc.Login(context.Background(), login.LoginInput{Identifier: "user@example.com", Password: "Wrong!1"})
		require.ErrorIs(t, err, authshared.ErrInvalidCredentials)
		require.True(t, incCalled)
	})

	t.Run("time-locked account returns LoginLockedError", func(t *testing.T) {
		t.Parallel()
		user := makeLoginUser("Correct!1", t)
		future := time.Now().Add(10 * time.Minute)
		user.LoginLockedUntil = &future
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
		}
		svc := newService(store)
		_, err := svc.Login(context.Background(), login.LoginInput{Identifier: "user@example.com", Password: "Correct!1"})
		var lockedErr *authshared.LoginLockedError
		require.ErrorAs(t, err, &lockedErr)
		require.ErrorIs(t, err, authshared.ErrLoginLocked)
		require.Positive(t, lockedErr.RetryAfter)
	})

	t.Run("account locked writes audit and returns ErrAccountLocked", func(t *testing.T) {
		t.Parallel()
		user := makeLoginUser("Correct!1", t)
		user.IsLocked = true
		var auditReason string
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
			WriteLoginFailedAuditTxFn: func(_ context.Context, _ [16]byte, reason, _, _ string) error {
				auditReason = reason
				return nil
			},
		}
		svc := newService(store)
		_, err := svc.Login(context.Background(), login.LoginInput{Identifier: "user@example.com", Password: "Correct!1"})
		require.ErrorIs(t, err, authshared.ErrAccountLocked)
		require.Equal(t, "account_locked", auditReason)
	})

	t.Run("unverified email writes audit and returns ErrEmailNotVerified", func(t *testing.T) {
		t.Parallel()
		user := makeLoginUser("Correct!1", t)
		user.EmailVerified = false
		var auditReason string
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
			WriteLoginFailedAuditTxFn: func(_ context.Context, _ [16]byte, reason, _, _ string) error {
				auditReason = reason
				return nil
			},
		}
		svc := newService(store)
		_, err := svc.Login(context.Background(), login.LoginInput{Identifier: "user@example.com", Password: "Correct!1"})
		require.ErrorIs(t, err, authshared.ErrEmailNotVerified)
		require.Equal(t, "email_not_verified", auditReason)
	})

	t.Run("inactive account writes audit and returns ErrAccountInactive", func(t *testing.T) {
		t.Parallel()
		user := makeLoginUser("Correct!1", t)
		user.IsActive = false
		var auditReason string
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
			WriteLoginFailedAuditTxFn: func(_ context.Context, _ [16]byte, reason, _, _ string) error {
				auditReason = reason
				return nil
			},
		}
		svc := newService(store)
		_, err := svc.Login(context.Background(), login.LoginInput{Identifier: "user@example.com", Password: "Correct!1"})
		require.ErrorIs(t, err, authshared.ErrAccountInactive)
		require.Equal(t, "account_inactive", auditReason)
	})

	t.Run("store error on GetUserForLogin wraps and returns it", func(t *testing.T) {
		t.Parallel()
		dbErr := errors.New("db timeout")
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) {
				return login.LoginUser{}, dbErr
			},
		}
		svc := newService(store)
		_, err := svc.Login(context.Background(), login.LoginInput{Identifier: "user@example.com", Password: "Passw0rd!1"})
		require.Error(t, err)
		require.ErrorIs(t, err, dbErr)
		require.NotErrorIs(t, err, authshared.ErrInvalidCredentials)
	})

	t.Run("malformed password hash returns wrapped error", func(t *testing.T) {
		t.Parallel()
		user := login.LoginUser{
			ID:            authsharedtest.RandomUUID(),
			Email:         "user@example.com",
			PasswordHash:  "not-a-bcrypt-hash", // too short — bcrypt rejects with ErrHashTooShort
			IsActive:      true,
			EmailVerified: true,
			IsLocked:      false,
		}
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
		}
		svc := newService(store)
		_, err := svc.Login(context.Background(), login.LoginInput{Identifier: "user@example.com", Password: "Passw0rd!1"})
		require.Error(t, err)
		require.NotErrorIs(t, err, authshared.ErrInvalidCredentials,
			"malformed hash must not be mapped to ErrInvalidCredentials")
	})

	t.Run("IncrementLoginFailuresTx error does not propagate on wrong password", func(t *testing.T) {
		t.Parallel()
		user := makeLoginUser("Correct!1", t)
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
			IncrementLoginFailuresTxFn: func(_ context.Context, _ [16]byte, _, _ string) error {
				return errors.New("store err")
			},
		}
		svc := newService(store)
		_, err := svc.Login(context.Background(), login.LoginInput{Identifier: "user@example.com", Password: "Wrong!1"})
		// Security: the increment error must be swallowed — only ErrInvalidCredentials surfaces.
		require.ErrorIs(t, err, authshared.ErrInvalidCredentials)
	})

	t.Run("WriteLoginFailedAuditTx error is silenced for account_locked", func(t *testing.T) {
		t.Parallel()
		user := makeLoginUser("Correct!1", t)
		user.IsLocked = true
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
			WriteLoginFailedAuditTxFn: func(_ context.Context, _ [16]byte, _, _, _ string) error {
				return errors.New("audit write failed")
			},
		}
		svc := newService(store)
		_, err := svc.Login(context.Background(), login.LoginInput{Identifier: "user@example.com", Password: "Correct!1"})
		// Security: audit errors are silenced so an audit-store failure cannot mask the lock.
		require.ErrorIs(t, err, authshared.ErrAccountLocked)
	})

	t.Run("WriteLoginFailedAuditTx error is silenced for email_not_verified", func(t *testing.T) {
		t.Parallel()
		user := makeLoginUser("Correct!1", t)
		user.EmailVerified = false
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
			WriteLoginFailedAuditTxFn: func(_ context.Context, _ [16]byte, _, _, _ string) error {
				return errors.New("audit write failed")
			},
		}
		svc := newService(store)
		_, err := svc.Login(context.Background(), login.LoginInput{Identifier: "user@example.com", Password: "Correct!1"})
		require.ErrorIs(t, err, authshared.ErrEmailNotVerified)
	})

	t.Run("WriteLoginFailedAuditTx error is silenced for account_inactive", func(t *testing.T) {
		t.Parallel()
		user := makeLoginUser("Correct!1", t)
		user.IsActive = false
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
			WriteLoginFailedAuditTxFn: func(_ context.Context, _ [16]byte, _, _, _ string) error {
				return errors.New("audit write failed")
			},
		}
		svc := newService(store)
		_, err := svc.Login(context.Background(), login.LoginInput{Identifier: "user@example.com", Password: "Correct!1"})
		require.ErrorIs(t, err, authshared.ErrAccountInactive)
	})

	t.Run("LoginTx error is wrapped and returned", func(t *testing.T) {
		t.Parallel()
		user := makeLoginUser("Correct!1", t)
		txErr := errors.New("tx failed")
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
			LoginTxFn: func(_ context.Context, _ login.LoginTxInput) (login.LoggedInSession, error) {
				return login.LoggedInSession{}, txErr
			},
		}
		svc := newService(store)
		_, err := svc.Login(context.Background(), login.LoginInput{Identifier: "user@example.com", Password: "Correct!1"})
		require.ErrorIs(t, err, txErr)
	})

	t.Run("timing_invariant: dummy hash runs on no-rows path", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) {
				return login.LoginUser{}, authshared.ErrUserNotFound
			},
		}
		svc := newService(store)
		start := time.Now()
		_, err := svc.Login(context.Background(), login.LoginInput{Identifier: "ghost@example.com", Password: "Passw0rd!1"})
		elapsed := time.Since(start)
		require.ErrorIs(t, err, authshared.ErrInvalidCredentials)
		// 500µs is a conservative lower-bound: any real bcrypt call takes longer,
		// but avoids flakiness on fast hardware where MinCost (4 rounds) may
		// complete in ~1ms or slightly under.
		minExpected := 500 * time.Microsecond
		require.GreaterOrEqual(t, elapsed, minExpected, "expected bcrypt latency from dummy hash")
	})

	t.Run("GetUserForLogin forwards identifier unchanged", func(t *testing.T) {
		t.Parallel()
		const input = "alice@example.com"
		var gotIdentifier string
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, id string) (login.LoginUser, error) {
				gotIdentifier = id
				return login.LoginUser{}, authshared.ErrUserNotFound
			},
		}
		newService(store).Login(context.Background(), login.LoginInput{Identifier: input, Password: "pw"}) //nolint:errcheck // error not relevant; test captures store call arguments only
		require.Equal(t, input, gotIdentifier)
	})

	t.Run("IncrementLoginFailuresTx receives correct userID", func(t *testing.T) {
		t.Parallel()
		user := makeLoginUser("Correct!1", t)
		var gotUserID [16]byte
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
			IncrementLoginFailuresTxFn: func(_ context.Context, id [16]byte, _, _ string) error {
				gotUserID = id
				return nil
			},
		}
		newService(store).Login(context.Background(), login.LoginInput{Identifier: "user@example.com", Password: "Wrong!1"}) //nolint:errcheck // error not relevant; test captures store call arguments only
		require.Equal(t, user.ID, gotUserID)
	})

	t.Run("IncrementLoginFailuresTx receives correct IPAddress and UserAgent", func(t *testing.T) {
		t.Parallel()
		user := makeLoginUser("Correct!1", t)
		var gotIP, gotUA string
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
			IncrementLoginFailuresTxFn: func(_ context.Context, _ [16]byte, ip, ua string) error {
				gotIP = ip
				gotUA = ua
				return nil
			},
		}
		newService(store).Login(context.Background(), login.LoginInput{ //nolint:errcheck // error not relevant; test captures store call arguments only
			Identifier: "user@example.com", Password: "Wrong!1",
			IPAddress: "1.2.3.4", UserAgent: "TestBrowser/1.0",
		})
		require.Equal(t, "1.2.3.4", gotIP)
		require.Equal(t, "TestBrowser/1.0", gotUA)
	})

	t.Run("LoginLockedUntil exactly at time.Now does not fire lockout", func(t *testing.T) {
		t.Parallel()
		user := makeLoginUser("Correct!1", t)
		now := time.Now()
		user.LoginLockedUntil = &now
		var loginTxCalled bool
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
			LoginTxFn: func(_ context.Context, _ login.LoginTxInput) (login.LoggedInSession, error) {
				loginTxCalled = true
				return login.LoggedInSession{}, nil
			},
		}
		_, err := newService(store).Login(context.Background(), login.LoginInput{Identifier: "user@example.com", Password: "Correct!1"})
		require.NoError(t, err)
		require.True(t, loginTxCalled, "login should proceed when LoginLockedUntil is exactly now (After is false)")
	})

	t.Run("LoginLockedUntil one second in the past does not fire lockout", func(t *testing.T) {
		t.Parallel()
		user := makeLoginUser("Correct!1", t)
		past := time.Now().Add(-1 * time.Second)
		user.LoginLockedUntil = &past
		var loginTxCalled bool
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
			LoginTxFn: func(_ context.Context, _ login.LoginTxInput) (login.LoggedInSession, error) {
				loginTxCalled = true
				return login.LoggedInSession{}, nil
			},
		}
		_, err := newService(store).Login(context.Background(), login.LoginInput{Identifier: "user@example.com", Password: "Correct!1"})
		require.NoError(t, err)
		require.True(t, loginTxCalled, "login should proceed when LoginLockedUntil is in the past")
	})

	t.Run("LoginLockedUntil nil does not fire lockout", func(t *testing.T) {
		t.Parallel()
		user := makeLoginUser("Correct!1", t) // LoginLockedUntil is nil
		var loginTxCalled bool
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
			LoginTxFn: func(_ context.Context, _ login.LoginTxInput) (login.LoggedInSession, error) {
				loginTxCalled = true
				return login.LoggedInSession{}, nil
			},
		}
		_, err := newService(store).Login(context.Background(), login.LoginInput{Identifier: "user@example.com", Password: "Correct!1"})
		require.NoError(t, err)
		require.True(t, loginTxCalled)
	})

	t.Run("RetryAfter is approximately time.Until LoginLockedUntil", func(t *testing.T) {
		t.Parallel()
		user := makeLoginUser("Correct!1", t)
		future := time.Now().Add(5 * time.Minute)
		user.LoginLockedUntil = &future
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
		}
		_, err := newService(store).Login(context.Background(), login.LoginInput{Identifier: "user@example.com", Password: "Correct!1"})
		var lockedErr *authshared.LoginLockedError
		require.ErrorAs(t, err, &lockedErr)
		expected := time.Until(future)
		require.InDelta(t, expected.Seconds(), lockedErr.RetryAfter.Seconds(), 2)
	})

	t.Run("guard order: IsLocked beats EmailVerified=false and IsActive=false", func(t *testing.T) {
		t.Parallel()
		user := makeLoginUser("Correct!1", t)
		user.IsLocked = true
		user.EmailVerified = false
		user.IsActive = false
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
			WriteLoginFailedAuditTxFn: func(_ context.Context, _ [16]byte, _, _, _ string) error { return nil },
		}
		_, err := newService(store).Login(context.Background(), login.LoginInput{Identifier: "user@example.com", Password: "Correct!1"})
		require.ErrorIs(t, err, authshared.ErrAccountLocked, "IsLocked must take precedence")
	})

	t.Run("guard order: EmailVerified=false beats IsActive=false", func(t *testing.T) {
		t.Parallel()
		user := makeLoginUser("Correct!1", t)
		user.IsLocked = false
		user.EmailVerified = false
		user.IsActive = false
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
			WriteLoginFailedAuditTxFn: func(_ context.Context, _ [16]byte, _, _, _ string) error { return nil },
		}
		_, err := newService(store).Login(context.Background(), login.LoginInput{Identifier: "user@example.com", Password: "Correct!1"})
		require.ErrorIs(t, err, authshared.ErrEmailNotVerified, "EmailVerified=false must beat IsActive=false")
	})

	t.Run("wrong password beats account locked guard", func(t *testing.T) {
		t.Parallel()
		user := makeLoginUser("Correct!1", t)
		user.IsLocked = true
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
			IncrementLoginFailuresTxFn: func(_ context.Context, _ [16]byte, _, _ string) error { return nil },
		}
		_, err := newService(store).Login(context.Background(), login.LoginInput{Identifier: "user@example.com", Password: "Wrong!1"})
		require.ErrorIs(t, err, authshared.ErrInvalidCredentials, "password check must run before guard checks")
		require.NotErrorIs(t, err, authshared.ErrAccountLocked)
	})

	t.Run("WriteLoginFailedAuditTx receives correct args for all guards", func(t *testing.T) {
		t.Parallel()
		const ip = "10.0.0.1"
		const ua = "TestAgent/1.0"
		checks := []struct {
			name       string
			setupUser  func(*login.LoginUser)
			wantErr    error
			wantReason string
		}{
			{
				name:       "account_locked",
				setupUser:  func(u *login.LoginUser) { u.IsLocked = true },
				wantErr:    authshared.ErrAccountLocked,
				wantReason: "account_locked",
			},
			{
				name:       "email_not_verified",
				setupUser:  func(u *login.LoginUser) { u.EmailVerified = false },
				wantErr:    authshared.ErrEmailNotVerified,
				wantReason: "email_not_verified",
			},
			{
				name:      "account_inactive",
				setupUser: func(u *login.LoginUser) { u.IsActive = false },
				wantErr:    authshared.ErrAccountInactive,
				wantReason: "account_inactive",
			},
		}
		for _, tc := range checks {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				user := makeLoginUser("Correct!1", t)
				tc.setupUser(&user)
				var gotID [16]byte
				var gotReason, gotIP, gotUA string
				store := &authsharedtest.LoginFakeStorer{
					GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
					WriteLoginFailedAuditTxFn: func(_ context.Context, id [16]byte, reason, ipAddr, userAgent string) error {
						gotID = id
						gotReason = reason
						gotIP = ipAddr
						gotUA = userAgent
						return nil
					},
				}
				_, err := newService(store).Login(context.Background(), login.LoginInput{
					Identifier: "user@example.com", Password: "Correct!1",
					IPAddress: ip, UserAgent: ua,
				})
				require.ErrorIs(t, err, tc.wantErr)
				require.Equal(t, user.ID, gotID)
				require.Equal(t, tc.wantReason, gotReason)
				require.Equal(t, ip, gotIP)
				require.Equal(t, ua, gotUA)
			})
		}
	})

	t.Run("LoginTxInput carries correct UserID, IPAddress, and UserAgent", func(t *testing.T) {
		t.Parallel()
		user := makeLoginUser("Correct!1", t)
		const ip = "5.5.5.5"
		const ua = "MyBrowser/2.0"
		var gotInput login.LoginTxInput
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
			LoginTxFn: func(_ context.Context, in login.LoginTxInput) (login.LoggedInSession, error) {
				gotInput = in
				return login.LoggedInSession{}, nil
			},
		}
		newService(store).Login(context.Background(), login.LoginInput{ //nolint:errcheck // error not relevant; test captures store call arguments only
			Identifier: "user@example.com", Password: "Correct!1",
			IPAddress: ip, UserAgent: ua,
		})
		require.Equal(t, user.ID, gotInput.UserID)
		require.Equal(t, ip, gotInput.IPAddress)
		require.Equal(t, ua, gotInput.UserAgent)
	})

	t.Run("returned LoggedInSession equals exactly what LoginTxFn returned", func(t *testing.T) {
		t.Parallel()
		user := makeLoginUser("Correct!1", t)
		wantSession := login.LoggedInSession{
			UserID:        user.ID,
			SessionID:     authsharedtest.RandomUUID(),
			RefreshJTI:    authsharedtest.RandomUUID(),
			FamilyID:      authsharedtest.RandomUUID(),
			RefreshExpiry: time.Now().Add(7 * 24 * time.Hour),
		}
		store := &authsharedtest.LoginFakeStorer{
			GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
			LoginTxFn: func(_ context.Context, _ login.LoginTxInput) (login.LoggedInSession, error) { return wantSession, nil },
		}
		got, err := newService(store).Login(context.Background(), login.LoginInput{Identifier: "user@example.com", Password: "Correct!1"})
		require.NoError(t, err)
		require.Equal(t, wantSession, got)
	})
}

// ── TestService_Login_ResetFailureIsSilent ────────────────────────────────────

// TestService_Login_ResetFailureIsSilent verifies the §2.6 invariant: a failure
// in ResetLoginFailuresTx must not propagate to the caller. The login result
// must be returned as success even when the counter reset fails.
func TestService_Login_ResetFailureIsSilent(t *testing.T) {
	t.Parallel()
	user := makeLoginUser("Correct!1", t)
	wantSession := login.LoggedInSession{
		SessionID:     authsharedtest.RandomUUID(),
		RefreshJTI:    authsharedtest.RandomUUID(),
		FamilyID:      authsharedtest.RandomUUID(),
		RefreshExpiry: time.Now().Add(7 * 24 * time.Hour),
	}
	store := &authsharedtest.LoginFakeStorer{
		GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
		LoginTxFn: func(_ context.Context, _ login.LoginTxInput) (login.LoggedInSession, error) {
			return wantSession, nil
		},
		ResetLoginFailuresTxFn: func(_ context.Context, _ [16]byte) error {
			return errors.New("counter reset failed")
		},
	}
	svc := newService(store)
	got, err := svc.Login(context.Background(), login.LoginInput{Identifier: "user@example.com", Password: "Correct!1"})
	require.NoError(t, err, "ResetLoginFailuresTx failure must not propagate to the caller")
	require.Equal(t, wantSession.SessionID, got.SessionID)
	require.Equal(t, wantSession.RefreshJTI, got.RefreshJTI)
}

// TestService_Login_IncrementLoginFailuresTx_ReceivesWithoutCancelContext
// verifies the security invariant from §3.6: a pre-cancelled outer context
// must not cancel the context delivered to IncrementLoginFailuresTx.
func TestService_Login_IncrementLoginFailuresTx_ReceivesWithoutCancelContext(t *testing.T) {
	t.Parallel()
	user := makeLoginUser("Correct!1", t)
	var gotCtx context.Context
	store := &authsharedtest.LoginFakeStorer{
		GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
		IncrementLoginFailuresTxFn: func(ctx context.Context, _ [16]byte, _, _ string) error {
			gotCtx = ctx
			return nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the service call
	newService(store).Login(ctx, login.LoginInput{Identifier: "user@example.com", Password: "Wrong!1"}) //nolint:errcheck
	require.NotNil(t, gotCtx)
	require.NoError(t, gotCtx.Err(),
		"context delivered to IncrementLoginFailuresTx must not be cancelled (WithoutCancel invariant)")
}

// TestService_Login_WriteLoginFailedAuditTx_ReceivesWithoutCancelContext
// verifies that WriteLoginFailedAuditTx is called with a non-cancellable
// context even when the outer request context is already cancelled.
func TestService_Login_WriteLoginFailedAuditTx_ReceivesWithoutCancelContext(t *testing.T) {
	t.Parallel()
	user := makeLoginUser("Correct!1", t)
	user.IsLocked = true
	var gotCtx context.Context
	store := &authsharedtest.LoginFakeStorer{
		GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
		WriteLoginFailedAuditTxFn: func(ctx context.Context, _ [16]byte, _, _, _ string) error {
			gotCtx = ctx
			return nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	newService(store).Login(ctx, login.LoginInput{Identifier: "user@example.com", Password: "Correct!1"}) //nolint:errcheck
	require.NotNil(t, gotCtx)
	require.NoError(t, gotCtx.Err(),
		"context delivered to WriteLoginFailedAuditTx must not be cancelled (WithoutCancel invariant)")
}

// TestService_Login_ResetLoginFailuresTx_ReceivesWithoutCancelContext
// verifies that ResetLoginFailuresTx is called with a non-cancellable
// context even when the outer request context is already cancelled.
func TestService_Login_ResetLoginFailuresTx_ReceivesWithoutCancelContext(t *testing.T) {
	t.Parallel()
	user := makeLoginUser("Correct!1", t)
	var gotCtx context.Context
	store := &authsharedtest.LoginFakeStorer{
		GetUserForLoginFn: func(_ context.Context, _ string) (login.LoginUser, error) { return user, nil },
		LoginTxFn: func(_ context.Context, _ login.LoginTxInput) (login.LoggedInSession, error) {
			return login.LoggedInSession{}, nil
		},
		ResetLoginFailuresTxFn: func(ctx context.Context, _ [16]byte) error {
			gotCtx = ctx
			return nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	newService(store).Login(ctx, login.LoginInput{Identifier: "user@example.com", Password: "Correct!1"}) //nolint:errcheck
	require.NotNil(t, gotCtx)
	require.NoError(t, gotCtx.Err(),
		"context delivered to ResetLoginFailuresTx must not be cancelled (WithoutCancel invariant)")
}
