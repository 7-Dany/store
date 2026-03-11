package bootstrap_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/7-Dany/store/backend/internal/domain/rbac/bootstrap"
	rbacshared "github.com/7-Dany/store/backend/internal/domain/rbac/shared"
	rbacsharedtest "github.com/7-Dany/store/backend/internal/domain/rbac/shared/testutil"
	"github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// validUserID is a well-formed UUID string used across service tests.
const validUserID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

// makeInput returns a BootstrapInput with the parsed UUID from userIDStr.
// Panics if userIDStr is not a valid UUID.
func makeInput(userIDStr string) bootstrap.BootstrapInput {
	uid, err := uuid.Parse(userIDStr)
	if err != nil {
		panic("makeInput: invalid UUID: " + err.Error())
	}
	return bootstrap.BootstrapInput{UserID: [16]byte(uid)}
}

// ── TestService_Bootstrap ────────────────────────────────────────────────────

func TestService_Bootstrap(t *testing.T) {
	t.Parallel()

	t.Run("success returns result from BootstrapOwnerTx", func(t *testing.T) {
		t.Parallel()
		want := bootstrap.BootstrapResult{
			UserID:    validUserID,
			RoleName:  "owner",
			GrantedAt: time.Now(),
		}
		store := &rbacsharedtest.BootstrapFakeStorer{
			BootstrapOwnerTxFn: func(_ context.Context, _ bootstrap.BootstrapTxInput) (bootstrap.BootstrapResult, error) {
				return want, nil
			},
		}
		got, err := bootstrap.NewService(store).Bootstrap(context.Background(), makeInput(validUserID))
		require.NoError(t, err)
		require.Equal(t, want, got)
	})

	t.Run("CountActiveOwners error is wrapped and returned", func(t *testing.T) {
		t.Parallel()
		dbErr := errors.New("db timeout")
		store := &rbacsharedtest.BootstrapFakeStorer{
			CountActiveOwnersFn: func(_ context.Context) (int64, error) {
				return 0, dbErr
			},
		}
		_, err := bootstrap.NewService(store).Bootstrap(context.Background(), makeInput(validUserID))
		require.ErrorIs(t, err, dbErr)
	})

	t.Run("owner already exists returns ErrOwnerAlreadyExists", func(t *testing.T) {
		t.Parallel()
		store := &rbacsharedtest.BootstrapFakeStorer{
			CountActiveOwnersFn: func(_ context.Context) (int64, error) {
				return 1, nil
			},
		}
		_, err := bootstrap.NewService(store).Bootstrap(context.Background(), makeInput(validUserID))
		require.ErrorIs(t, err, rbac.ErrOwnerAlreadyExists)
	})

	t.Run("GetOwnerRoleID error is wrapped and returned", func(t *testing.T) {
		t.Parallel()
		dbErr := errors.New("role lookup failed")
		store := &rbacsharedtest.BootstrapFakeStorer{
			GetOwnerRoleIDFn: func(_ context.Context) ([16]byte, error) {
				return [16]byte{}, dbErr
			},
		}
		_, err := bootstrap.NewService(store).Bootstrap(context.Background(), makeInput(validUserID))
		require.ErrorIs(t, err, dbErr)
	})

	t.Run("user not found returns ErrUserNotFound", func(t *testing.T) {
		t.Parallel()
		store := &rbacsharedtest.BootstrapFakeStorer{
			GetActiveUserByIDFn: func(_ context.Context, _ [16]byte) (bootstrap.BootstrapUser, error) {
				return bootstrap.BootstrapUser{}, rbacshared.ErrUserNotFound
			},
		}
		_, err := bootstrap.NewService(store).Bootstrap(context.Background(), makeInput(validUserID))
		require.ErrorIs(t, err, rbacshared.ErrUserNotFound)
	})

	t.Run("GetActiveUserByID non-sentinel error is wrapped and returned", func(t *testing.T) {
		t.Parallel()
		dbErr := errors.New("connection reset")
		store := &rbacsharedtest.BootstrapFakeStorer{
			GetActiveUserByIDFn: func(_ context.Context, _ [16]byte) (bootstrap.BootstrapUser, error) {
				return bootstrap.BootstrapUser{}, dbErr
			},
		}
		_, err := bootstrap.NewService(store).Bootstrap(context.Background(), makeInput(validUserID))
		require.ErrorIs(t, err, dbErr)
		require.NotErrorIs(t, err, rbacshared.ErrUserNotFound)
	})

	t.Run("inactive user returns ErrUserNotActive", func(t *testing.T) {
		t.Parallel()
		store := &rbacsharedtest.BootstrapFakeStorer{
			GetActiveUserByIDFn: func(_ context.Context, _ [16]byte) (bootstrap.BootstrapUser, error) {
				return bootstrap.BootstrapUser{IsActive: false, EmailVerified: true}, nil
			},
		}
		_, err := bootstrap.NewService(store).Bootstrap(context.Background(), makeInput(validUserID))
		require.ErrorIs(t, err, bootstrap.ErrUserNotActive)
	})

	t.Run("email not verified returns ErrUserNotVerified", func(t *testing.T) {
		t.Parallel()
		store := &rbacsharedtest.BootstrapFakeStorer{
			GetActiveUserByIDFn: func(_ context.Context, _ [16]byte) (bootstrap.BootstrapUser, error) {
				return bootstrap.BootstrapUser{IsActive: true, EmailVerified: false}, nil
			},
		}
		_, err := bootstrap.NewService(store).Bootstrap(context.Background(), makeInput(validUserID))
		require.ErrorIs(t, err, bootstrap.ErrUserNotVerified)
	})

	t.Run("guard order: inactive beats email_not_verified", func(t *testing.T) {
		t.Parallel()
		store := &rbacsharedtest.BootstrapFakeStorer{
			GetActiveUserByIDFn: func(_ context.Context, _ [16]byte) (bootstrap.BootstrapUser, error) {
				return bootstrap.BootstrapUser{IsActive: false, EmailVerified: false}, nil
			},
		}
		_, err := bootstrap.NewService(store).Bootstrap(context.Background(), makeInput(validUserID))
		require.ErrorIs(t, err, bootstrap.ErrUserNotActive,
			"ErrUserNotActive must be returned before ErrUserNotVerified is checked")
	})

	t.Run("BootstrapOwnerTx error is wrapped and returned", func(t *testing.T) {
		t.Parallel()
		txErr := errors.New("tx failed")
		store := &rbacsharedtest.BootstrapFakeStorer{
			BootstrapOwnerTxFn: func(_ context.Context, _ bootstrap.BootstrapTxInput) (bootstrap.BootstrapResult, error) {
				return bootstrap.BootstrapResult{}, txErr
			},
		}
		_, err := bootstrap.NewService(store).Bootstrap(context.Background(), makeInput(validUserID))
		require.ErrorIs(t, err, txErr)
	})

	t.Run("BootstrapOwnerTx receives correct UserID and RoleID", func(t *testing.T) {
		t.Parallel()
		wantUID, _ := uuid.Parse(validUserID)
		wantRoleID := [16]byte(uuid.New())
		var gotInput bootstrap.BootstrapTxInput
		store := &rbacsharedtest.BootstrapFakeStorer{
			GetOwnerRoleIDFn: func(_ context.Context) ([16]byte, error) {
				return wantRoleID, nil
			},
			BootstrapOwnerTxFn: func(_ context.Context, in bootstrap.BootstrapTxInput) (bootstrap.BootstrapResult, error) {
				gotInput = in
				return bootstrap.BootstrapResult{}, nil
			},
		}
		bootstrap.NewService(store).Bootstrap(context.Background(), makeInput(validUserID)) //nolint:errcheck
		require.Equal(t, [16]byte(wantUID), gotInput.UserID)
		require.Equal(t, wantRoleID, gotInput.RoleID)
	})

	t.Run("BootstrapOwnerTx is not called when owner already exists", func(t *testing.T) {
		t.Parallel()
		var txCalled bool
		store := &rbacsharedtest.BootstrapFakeStorer{
			CountActiveOwnersFn: func(_ context.Context) (int64, error) { return 2, nil },
			BootstrapOwnerTxFn: func(_ context.Context, _ bootstrap.BootstrapTxInput) (bootstrap.BootstrapResult, error) {
				txCalled = true
				return bootstrap.BootstrapResult{}, nil
			},
		}
		bootstrap.NewService(store).Bootstrap(context.Background(), makeInput(validUserID)) //nolint:errcheck
		require.False(t, txCalled, "BootstrapOwnerTx must not be called when an owner already exists")
	})

	t.Run("BootstrapOwnerTx not called when GetOwnerRoleID returns error", func(t *testing.T) {
		t.Parallel()
		var txCalled bool
		store := &rbacsharedtest.BootstrapFakeStorer{
			GetOwnerRoleIDFn: func(_ context.Context) ([16]byte, error) {
				return [16]byte{}, errors.New("role error")
			},
			BootstrapOwnerTxFn: func(_ context.Context, _ bootstrap.BootstrapTxInput) (bootstrap.BootstrapResult, error) {
				txCalled = true
				return bootstrap.BootstrapResult{}, nil
			},
		}
		bootstrap.NewService(store).Bootstrap(context.Background(), makeInput(validUserID)) //nolint:errcheck
		require.False(t, txCalled)
	})

	t.Run("BootstrapOwnerTx not called when GetActiveUserByID returns ErrUserNotFound", func(t *testing.T) {
		t.Parallel()
		var txCalled bool
		store := &rbacsharedtest.BootstrapFakeStorer{
			GetActiveUserByIDFn: func(_ context.Context, _ [16]byte) (bootstrap.BootstrapUser, error) {
				return bootstrap.BootstrapUser{}, rbacshared.ErrUserNotFound
			},
			BootstrapOwnerTxFn: func(_ context.Context, _ bootstrap.BootstrapTxInput) (bootstrap.BootstrapResult, error) {
				txCalled = true
				return bootstrap.BootstrapResult{}, nil
			},
		}
		bootstrap.NewService(store).Bootstrap(context.Background(), makeInput(validUserID)) //nolint:errcheck
		require.False(t, txCalled)
	})
}
