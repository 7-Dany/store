package userlock_test

import (
	"context"
	"errors"
	"testing"

	rbacsharedtest "github.com/7-Dany/store/backend/internal/domain/rbac/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/rbac/userlock"
	platformrbac "github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/stretchr/testify/require"
)

const (
	svcTargetID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	svcActorID  = "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
)

// ── LockUser ──────────────────────────────────────────────────────────────────

// T-R45s: invalid targetUserID UUID → ErrUserNotFound
func TestService_LockUser_InvalidTargetUUID(t *testing.T) {
	t.Parallel()
	svc := userlock.NewService(&rbacsharedtest.UserLockFakeStorer{}, nil)
	err := svc.LockUser(context.Background(), "not-a-uuid", svcActorID, userlock.LockUserInput{Reason: "test"})
	require.ErrorIs(t, err, userlock.ErrUserNotFound)
}

// T-R45t: target == actor (self-lock) → ErrCannotLockSelf
func TestService_LockUser_SelfLock(t *testing.T) {
	t.Parallel()
	svc := userlock.NewService(&rbacsharedtest.UserLockFakeStorer{}, nil)
	err := svc.LockUser(context.Background(), svcActorID, svcActorID, userlock.LockUserInput{Reason: "test"})
	require.ErrorIs(t, err, platformrbac.ErrCannotLockSelf)
}

// T-R45v: IsOwnerUser returns true → ErrCannotLockOwner
func TestService_LockUser_CannotLockOwner(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.UserLockFakeStorer{
		IsOwnerUserFn: func(_ context.Context, _ [16]byte) (bool, error) {
			return true, nil
		},
	}
	svc := userlock.NewService(store, nil)
	err := svc.LockUser(context.Background(), svcTargetID, svcActorID, userlock.LockUserInput{Reason: "test"})
	require.ErrorIs(t, err, platformrbac.ErrCannotLockOwner)
}

// T-R45w: GetLockStatus returns ErrUserNotFound → ErrUserNotFound
func TestService_LockUser_UserNotFound(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.UserLockFakeStorer{
		GetLockStatusFn: func(_ context.Context, _ [16]byte) (userlock.UserLockStatus, error) {
			return userlock.UserLockStatus{}, userlock.ErrUserNotFound
		},
	}
	svc := userlock.NewService(store, nil)
	err := svc.LockUser(context.Background(), svcTargetID, svcActorID, userlock.LockUserInput{Reason: "test"})
	require.ErrorIs(t, err, userlock.ErrUserNotFound)
}

// T-R45x: store.LockUserTx propagates error
func TestService_LockUser_LockTxError(t *testing.T) {
	t.Parallel()
	storeErr := errors.New("db down")
	store := &rbacsharedtest.UserLockFakeStorer{
		LockUserTxFn: func(_ context.Context, _ userlock.LockUserTxInput) error {
			return storeErr
		},
	}
	svc := userlock.NewService(store, nil)
	err := svc.LockUser(context.Background(), svcTargetID, svcActorID, userlock.LockUserInput{Reason: "test"})
	require.ErrorIs(t, err, storeErr)
}

// T-R45y: success path — LockUserTx receives correct input
func TestService_LockUser_Success(t *testing.T) {
	t.Parallel()
	var capturedInput userlock.LockUserTxInput
	store := &rbacsharedtest.UserLockFakeStorer{
		LockUserTxFn: func(_ context.Context, in userlock.LockUserTxInput) error {
			capturedInput = in
			return nil
		},
	}
	svc := userlock.NewService(store, nil)
	err := svc.LockUser(context.Background(), svcTargetID, svcActorID, userlock.LockUserInput{Reason: "spam"})
	require.NoError(t, err)
	require.Equal(t, "spam", capturedInput.Reason)
}

// T-R45z: invalid actingUserID UUID → non-nil wrapped error (not ErrUserNotFound)
func TestService_LockUser_InvalidActingUUID(t *testing.T) {
	t.Parallel()
	svc := userlock.NewService(&rbacsharedtest.UserLockFakeStorer{}, nil)
	err := svc.LockUser(context.Background(), svcTargetID, "not-a-uuid", userlock.LockUserInput{Reason: "test"})
	require.NotNil(t, err)
	require.False(t, errors.Is(err, userlock.ErrUserNotFound))
}

// ── UnlockUser ────────────────────────────────────────────────────────────────

// T-R46s: invalid targetUserID UUID → ErrUserNotFound
func TestService_UnlockUser_InvalidTargetUUID(t *testing.T) {
	t.Parallel()
	svc := userlock.NewService(&rbacsharedtest.UserLockFakeStorer{}, nil)
	err := svc.UnlockUser(context.Background(), "not-a-uuid", svcActorID)
	require.ErrorIs(t, err, userlock.ErrUserNotFound)
}

// T-R46t: GetLockStatus returns ErrUserNotFound → ErrUserNotFound
func TestService_UnlockUser_UserNotFound(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.UserLockFakeStorer{
		GetLockStatusFn: func(_ context.Context, _ [16]byte) (userlock.UserLockStatus, error) {
			return userlock.UserLockStatus{}, userlock.ErrUserNotFound
		},
	}
	svc := userlock.NewService(store, nil)
	err := svc.UnlockUser(context.Background(), svcTargetID, svcActorID)
	require.ErrorIs(t, err, userlock.ErrUserNotFound)
}

// T-R46u: store.UnlockUser propagates error
func TestService_UnlockUser_StoreError(t *testing.T) {
	t.Parallel()
	storeErr := errors.New("db down")
	store := &rbacsharedtest.UserLockFakeStorer{
		UnlockUserTxFn: func(_ context.Context, _ [16]byte, _ string) error {
			return storeErr
		},
	}
	svc := userlock.NewService(store, nil)
	err := svc.UnlockUser(context.Background(), svcTargetID, svcActorID)
	require.ErrorIs(t, err, storeErr)
}

// T-R46v: success path
func TestService_UnlockUser_Success(t *testing.T) {
	t.Parallel()
	svc := userlock.NewService(&rbacsharedtest.UserLockFakeStorer{}, nil)
	err := svc.UnlockUser(context.Background(), svcTargetID, svcActorID)
	require.NoError(t, err)
}

// ── GetLockStatus ─────────────────────────────────────────────────────────────

// T-R47s: invalid targetUserID UUID → ErrUserNotFound
func TestService_GetLockStatus_InvalidTargetUUID(t *testing.T) {
	t.Parallel()
	svc := userlock.NewService(&rbacsharedtest.UserLockFakeStorer{}, nil)
	_, err := svc.GetLockStatus(context.Background(), "not-a-uuid")
	require.ErrorIs(t, err, userlock.ErrUserNotFound)
}

// T-R47t: store returns ErrUserNotFound → ErrUserNotFound
func TestService_GetLockStatus_UserNotFound(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.UserLockFakeStorer{
		GetLockStatusFn: func(_ context.Context, _ [16]byte) (userlock.UserLockStatus, error) {
			return userlock.UserLockStatus{}, userlock.ErrUserNotFound
		},
	}
	svc := userlock.NewService(store, nil)
	_, err := svc.GetLockStatus(context.Background(), svcTargetID)
	require.ErrorIs(t, err, userlock.ErrUserNotFound)
}

// T-R47u: success — returns status
func TestService_GetLockStatus_Success(t *testing.T) {
	t.Parallel()
	expected := userlock.UserLockStatus{UserID: svcTargetID, AdminLocked: true}
	store := &rbacsharedtest.UserLockFakeStorer{
		GetLockStatusFn: func(_ context.Context, _ [16]byte) (userlock.UserLockStatus, error) {
			return expected, nil
		},
	}
	svc := userlock.NewService(store, nil)
	got, err := svc.GetLockStatus(context.Background(), svcTargetID)
	require.NoError(t, err)
	require.True(t, got.AdminLocked)
}
