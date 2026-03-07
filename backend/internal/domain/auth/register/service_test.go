package register_test

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/7-Dany/store/backend/internal/domain/auth/register"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/stretchr/testify/require"
)

func TestRegister_Success(t *testing.T) {
	t.Parallel()

	var capturedInput register.CreateUserInput
	store := &authsharedtest.RegisterFakeStorer{
		CreateUserTxFn: func(_ context.Context, in register.CreateUserInput) (register.CreatedUser, error) {
			capturedInput = in
			return register.CreatedUser{UserID: "user-1", Email: in.Email}, nil
		},
	}

	svc := register.NewService(store)
	result, err := svc.Register(context.Background(), register.RegisterInput{
		DisplayName: "Alice",
		Email:       "alice@example.com",
		Password:    "P@ssw0rd!1",
		IPAddress:   "127.0.0.1",
		UserAgent:   "test-agent",
	})

	require.NoError(t, err)
	require.Equal(t, "user-1", result.UserID)
	require.Equal(t, "alice@example.com", result.Email)
	require.NotEmpty(t, result.RawCode, "RawCode must be non-empty on success")

	// Password stored as hash, never plaintext.
	require.NotEqual(t, "P@ssw0rd!1", capturedInput.PasswordHash,
		"PasswordHash must be bcrypt-hashed, not plaintext")
	require.NotEmpty(t, capturedInput.CodeHash, "CodeHash must be non-empty")
}

func TestRegister_RawCodeHashPairing(t *testing.T) {
	t.Parallel()

	var capturedHash string
	store := &authsharedtest.RegisterFakeStorer{
		CreateUserTxFn: func(_ context.Context, in register.CreateUserInput) (register.CreatedUser, error) {
			capturedHash = in.CodeHash
			return register.CreatedUser{UserID: "user-1", Email: in.Email}, nil
		},
	}

	svc := register.NewService(store)
	result, err := svc.Register(context.Background(), register.RegisterInput{
		DisplayName: "Alice",
		Email:       "alice@example.com",
		Password:    "P@ssw0rd!1",
		IPAddress:   "127.0.0.1",
		UserAgent:   "test-agent",
	})

	require.NoError(t, err)
	require.NotEmpty(t, result.RawCode, "RawCode must be non-empty")
	require.NotEmpty(t, capturedHash, "CodeHash must be non-empty")
	require.NoError(t,
		bcrypt.CompareHashAndPassword([]byte(capturedHash), []byte(result.RawCode)),
		"CodeHash must be the bcrypt hash of RawCode — a swapped pair breaks OTP verification",
	)
}

func TestRegister_HashPasswordFailure(t *testing.T) {
	t.Parallel()
	// An empty password causes HashPassword to fail.
	svc := register.NewService(&authsharedtest.RegisterFakeStorer{})
	_, err := svc.Register(context.Background(), register.RegisterInput{
		DisplayName: "Alice",
		Email:       "alice@example.com",
		Password:    "", // triggers HashPassword error
		IPAddress:   "127.0.0.1",
		UserAgent:   "test-agent",
	})
	require.Error(t, err)
}

func TestRegister_CreateUserTxError_Generic(t *testing.T) {
	t.Parallel()

	dbErr := errors.New("db connection refused")
	store := &authsharedtest.RegisterFakeStorer{
		CreateUserTxFn: func(_ context.Context, _ register.CreateUserInput) (register.CreatedUser, error) {
			return register.CreatedUser{}, dbErr
		},
	}

	svc := register.NewService(store)
	_, err := svc.Register(context.Background(), register.RegisterInput{
		DisplayName: "Alice",
		Email:       "alice@example.com",
		Password:    "P@ssw0rd!1",
		IPAddress:   "127.0.0.1",
		UserAgent:   "test-agent",
	})

	require.ErrorIs(t, err, dbErr)
}

func TestRegister_ErrEmailTaken_PropagatedUnchanged(t *testing.T) {
	t.Parallel()

	var (
		auditCalled bool
		auditCtx    context.Context
	)
	store := &authsharedtest.RegisterFakeStorer{
		CreateUserTxFn: func(_ context.Context, _ register.CreateUserInput) (register.CreatedUser, error) {
			return register.CreatedUser{}, authshared.ErrEmailTaken
		},
		WriteRegisterFailedAuditTxFn: func(ctx context.Context, _ [16]byte, _, _ string) error {
			auditCalled = true
			auditCtx = ctx
			return nil
		},
	}

	svc := register.NewService(store)
	_, err := svc.Register(context.Background(), register.RegisterInput{
		DisplayName: "Alice",
		Email:       "alice@example.com",
		Password:    "P@ssw0rd!1",
		IPAddress:   "127.0.0.1",
		UserAgent:   "test-agent",
	})

	require.ErrorIs(t, err, authshared.ErrEmailTaken,
		"ErrEmailTaken must propagate unchanged so the handler can map it to 409")
	// ADR-008: a register_failed audit row must be written on every ErrEmailTaken path.
	require.True(t, auditCalled,
		"WriteRegisterFailedAuditTx must be called when ErrEmailTaken is returned")
	// ADR-004: the context passed to WriteRegisterFailedAuditTx must be detached
	// from the request context (context.WithoutCancel) so a client disconnect
	// cannot abort the audit write.
	require.Nil(t, auditCtx.Done(),
		"WriteRegisterFailedAuditTx must receive a WithoutCancel context (ctx.Done() == nil)")
}

// TestRegister_GenerateCodeHashFailure asserts that when generateCodeHash returns
// an error the service propagates it and never calls the store.
func TestRegister_GenerateCodeHashFailure(t *testing.T) {
	t.Parallel()

	injectedErr := errors.New("rng failure")
	store := &authsharedtest.RegisterFakeStorer{
		CreateUserTxFn: func(_ context.Context, _ register.CreateUserInput) (register.CreatedUser, error) {
			t.Fatal("CreateUserTx must not be called when generateCodeHash fails")
			return register.CreatedUser{}, nil
		},
	}

	svc := register.NewService(store)
	svc.SetGenerateCodeHashForTest(func() (string, string, error) {
		return "", "", injectedErr
	})

	_, err := svc.Register(context.Background(), register.RegisterInput{
		DisplayName: "Alice",
		Email:       "alice@example.com",
		Password:    "P@ssw0rd!1",
		IPAddress:   "127.0.0.1",
		UserAgent:   "test-agent",
	})

	require.ErrorIs(t, err, injectedErr,
		"generateCodeHash error must be propagated unchanged")
}

// TestRegister_ErrEmailTaken_AuditWriteFailure asserts that when
// WriteRegisterFailedAuditTx itself returns an error the service logs it and
// still propagates ErrEmailTaken unchanged. The audit error must not replace
// or mask the original sentinel.
func TestRegister_ErrEmailTaken_AuditWriteFailure(t *testing.T) {
	t.Parallel()

	store := &authsharedtest.RegisterFakeStorer{
		CreateUserTxFn: func(_ context.Context, _ register.CreateUserInput) (register.CreatedUser, error) {
			return register.CreatedUser{}, authshared.ErrEmailTaken
		},
		WriteRegisterFailedAuditTxFn: func(_ context.Context, _ [16]byte, _, _ string) error {
			// Simulate a DB timeout during the audit write.
			return errors.New("db timeout")
		},
	}

	svc := register.NewService(store)
	_, err := svc.Register(context.Background(), register.RegisterInput{
		DisplayName: "Alice",
		Email:       "alice@example.com",
		Password:    "P@ssw0rd!1",
		IPAddress:   "127.0.0.1",
		UserAgent:   "test-agent",
	})

	// The audit write failure must not mask ErrEmailTaken.
	require.ErrorIs(t, err, authshared.ErrEmailTaken,
		"audit write failure must not mask ErrEmailTaken")
}

// TestRegister_UsernamePassedThrough asserts that a non-empty username in
// RegisterInput is forwarded verbatim to CreateUserInput.
func TestRegister_UsernamePassedThrough(t *testing.T) {
	t.Parallel()

	var capturedInput register.CreateUserInput
	store := &authsharedtest.RegisterFakeStorer{
		CreateUserTxFn: func(_ context.Context, in register.CreateUserInput) (register.CreatedUser, error) {
			capturedInput = in
			return register.CreatedUser{UserID: "user-1", Email: in.Email}, nil
		},
	}

	svc := register.NewService(store)
	_, err := svc.Register(context.Background(), register.RegisterInput{
		DisplayName: "Alice",
		Email:       "alice@example.com",
		Password:    "P@ssw0rd!1",
		Username:    "alice123",
		IPAddress:   "127.0.0.1",
		UserAgent:   "test-agent",
	})

	require.NoError(t, err)
	require.Equal(t, "alice123", capturedInput.Username,
		"Username must be forwarded from RegisterInput to CreateUserInput unchanged")
}

// TestRegister_EmptyUsername_PassedThrough asserts that an empty username in
// RegisterInput (the optional-omitted case) is forwarded as an empty string to
// CreateUserInput, which the store will persist as NULL.
func TestRegister_EmptyUsername_PassedThrough(t *testing.T) {
	t.Parallel()

	var capturedInput register.CreateUserInput
	store := &authsharedtest.RegisterFakeStorer{
		CreateUserTxFn: func(_ context.Context, in register.CreateUserInput) (register.CreatedUser, error) {
			capturedInput = in
			return register.CreatedUser{UserID: "user-1", Email: in.Email}, nil
		},
	}

	svc := register.NewService(store)
	_, err := svc.Register(context.Background(), register.RegisterInput{
		DisplayName: "Alice",
		Email:       "alice@example.com",
		Password:    "P@ssw0rd!1",
		Username:    "", // omitted
		IPAddress:   "127.0.0.1",
		UserAgent:   "test-agent",
	})

	require.NoError(t, err)
	require.Empty(t, capturedInput.Username,
		"empty Username must be forwarded as empty string so the store can persist NULL")
}

// TestRegister_ErrUsernameTaken_PropagatedUnchanged asserts that ErrUsernameTaken
// from CreateUserTx is propagated unchanged to the caller without triggering an
// audit write (audit is only written on ErrEmailTaken).
func TestRegister_ErrUsernameTaken_PropagatedUnchanged(t *testing.T) {
	t.Parallel()

	var auditCalled bool
	store := &authsharedtest.RegisterFakeStorer{
		CreateUserTxFn: func(_ context.Context, _ register.CreateUserInput) (register.CreatedUser, error) {
			return register.CreatedUser{}, authshared.ErrUsernameTaken
		},
		WriteRegisterFailedAuditTxFn: func(_ context.Context, _ [16]byte, _, _ string) error {
			auditCalled = true
			return nil
		},
	}

	svc := register.NewService(store)
	_, err := svc.Register(context.Background(), register.RegisterInput{
		DisplayName: "Alice",
		Email:       "alice@example.com",
		Password:    "P@ssw0rd!1",
		Username:    "alice123",
		IPAddress:   "127.0.0.1",
		UserAgent:   "test-agent",
	})

	require.ErrorIs(t, err, authshared.ErrUsernameTaken,
		"ErrUsernameTaken must propagate unchanged so the handler can map it to 409")
	require.False(t, auditCalled,
		"WriteRegisterFailedAuditTx must NOT be called for ErrUsernameTaken — audit is only written for ErrEmailTaken")
}

// TestRegister_TimingInvariant asserts that HashPassword is called before
// CreateUserTx on both the success path and the duplicate-email path.
// The spy records call order; CreateUserTx executes after HashPassword iff
// the PasswordHash field is already set when CreateUserTx is entered.
func TestRegister_TimingInvariant(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		storeErr error
	}{
		{"success path", nil},
		{"duplicate-email path", authshared.ErrEmailTaken},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var hashWasSetBeforeStore bool
			store := &authsharedtest.RegisterFakeStorer{
				CreateUserTxFn: func(_ context.Context, in register.CreateUserInput) (register.CreatedUser, error) {
					// If PasswordHash is non-empty here, HashPassword ran first.
					hashWasSetBeforeStore = in.PasswordHash != ""
					if tc.storeErr != nil {
						return register.CreatedUser{}, tc.storeErr
					}
					return register.CreatedUser{UserID: "u1", Email: in.Email}, nil
				},
			}

			svc := register.NewService(store)
			_, _ = svc.Register(context.Background(), register.RegisterInput{
				DisplayName: "Alice",
				Email:       "alice@example.com",
				Password:    "P@ssw0rd!1",
				IPAddress:   "127.0.0.1",
				UserAgent:   "test-agent",
			})

			require.True(t, hashWasSetBeforeStore,
				"HashPassword must be called before CreateUserTx (%s)", tc.name)
		})
	}
}
