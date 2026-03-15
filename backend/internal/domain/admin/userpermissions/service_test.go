package userpermissions_test

import (
	"context"
	"errors"
	"testing"
	"time"

	adminsharedtest "github.com/7-Dany/store/backend/internal/domain/admin/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/admin/userpermissions"
	"github.com/stretchr/testify/require"
)

const (
	svcTargetID     = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	svcActorID      = "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
	svcPermissionID = "cccccccc-dddd-eeee-ffff-aaaaaaaaaaaa"
	svcGrantID      = "dddddddd-eeee-ffff-aaaa-bbbbbbbbbbbb"
)

func futureTime() time.Time { return time.Now().Add(24 * time.Hour) }

func TestService_GrantPermission_InvalidTargetUUID(t *testing.T) {
	t.Parallel()
	svc := userpermissions.NewService(&adminsharedtest.UserPermissionsFakeStorer{})
	_, err := svc.GrantPermission(context.Background(), "not-a-uuid", svcActorID,
		userpermissions.GrantPermissionInput{PermissionID: svcPermissionID, GrantedReason: "test", ExpiresAt: futureTime()})
	require.ErrorIs(t, err, userpermissions.ErrGrantNotFound)
}

func TestService_GrantPermission_PermissionIDEmpty(t *testing.T) {
	t.Parallel()
	svc := userpermissions.NewService(&adminsharedtest.UserPermissionsFakeStorer{})
	_, err := svc.GrantPermission(context.Background(), svcTargetID, svcActorID,
		userpermissions.GrantPermissionInput{PermissionID: "", GrantedReason: "test", ExpiresAt: futureTime()})
	require.ErrorIs(t, err, userpermissions.ErrPermissionIDEmpty)
}

func TestService_GrantPermission_ExpiresAtRequired(t *testing.T) {
	t.Parallel()
	svc := userpermissions.NewService(&adminsharedtest.UserPermissionsFakeStorer{})
	_, err := svc.GrantPermission(context.Background(), svcTargetID, svcActorID,
		userpermissions.GrantPermissionInput{PermissionID: svcPermissionID, GrantedReason: "test"})
	require.ErrorIs(t, err, userpermissions.ErrExpiresAtRequired)
}

func TestService_GrantPermission_DefaultsScopeOwn(t *testing.T) {
	t.Parallel()
	var capturedInput userpermissions.GrantPermissionTxInput
	store := &adminsharedtest.UserPermissionsFakeStorer{
		GrantPermissionTxFn: func(_ context.Context, in userpermissions.GrantPermissionTxInput) (userpermissions.UserPermission, error) {
			capturedInput = in
			return userpermissions.UserPermission{Scope: in.Scope}, nil
		},
	}
	svc := userpermissions.NewService(store)
	got, err := svc.GrantPermission(context.Background(), svcTargetID, svcActorID,
		userpermissions.GrantPermissionInput{PermissionID: svcPermissionID, GrantedReason: "test", ExpiresAt: futureTime(), Scope: ""})
	require.NoError(t, err)
	require.Equal(t, "own", capturedInput.Scope)
	require.Equal(t, "own", got.Scope)
}

func TestService_GrantPermission_PropagatesStoreError(t *testing.T) {
	t.Parallel()
	storeErr := errors.New("db down")
	store := &adminsharedtest.UserPermissionsFakeStorer{
		GrantPermissionTxFn: func(_ context.Context, _ userpermissions.GrantPermissionTxInput) (userpermissions.UserPermission, error) {
			return userpermissions.UserPermission{}, storeErr
		},
	}
	svc := userpermissions.NewService(store)
	_, err := svc.GrantPermission(context.Background(), svcTargetID, svcActorID,
		userpermissions.GrantPermissionInput{PermissionID: svcPermissionID, GrantedReason: "test", ExpiresAt: futureTime()})
	require.ErrorIs(t, err, storeErr)
}

func TestService_RevokePermission_InvalidTargetUUID(t *testing.T) {
	t.Parallel()
	svc := userpermissions.NewService(&adminsharedtest.UserPermissionsFakeStorer{})
	err := svc.RevokePermission(context.Background(), "not-a-uuid", svcGrantID, svcActorID)
	require.ErrorIs(t, err, userpermissions.ErrGrantNotFound)
}

func TestService_RevokePermission_InvalidGrantUUID(t *testing.T) {
	t.Parallel()
	svc := userpermissions.NewService(&adminsharedtest.UserPermissionsFakeStorer{})
	err := svc.RevokePermission(context.Background(), svcTargetID, "not-a-uuid", svcActorID)
	require.ErrorIs(t, err, userpermissions.ErrGrantNotFound)
}

func TestService_RevokePermission_PropagatesStoreErrGrantNotFound(t *testing.T) {
	t.Parallel()
	store := &adminsharedtest.UserPermissionsFakeStorer{
		RevokePermissionFn: func(_ context.Context, _, _ [16]byte, _ string) error { return userpermissions.ErrGrantNotFound },
	}
	svc := userpermissions.NewService(store)
	err := svc.RevokePermission(context.Background(), svcTargetID, svcGrantID, svcActorID)
	require.ErrorIs(t, err, userpermissions.ErrGrantNotFound)
}

func TestService_ListPermissions_InvalidUUID(t *testing.T) {
	t.Parallel()
	svc := userpermissions.NewService(&adminsharedtest.UserPermissionsFakeStorer{})
	_, err := svc.ListPermissions(context.Background(), "not-a-uuid")
	require.ErrorIs(t, err, userpermissions.ErrGrantNotFound)
}

func TestService_ListPermissions_Success(t *testing.T) {
	t.Parallel()
	expected := []userpermissions.UserPermission{{ID: "some-id", Name: "read"}}
	store := &adminsharedtest.UserPermissionsFakeStorer{
		GetUserPermissionsFn: func(_ context.Context, _ [16]byte) ([]userpermissions.UserPermission, error) { return expected, nil },
	}
	svc := userpermissions.NewService(store)
	got, err := svc.ListPermissions(context.Background(), svcTargetID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "read", got[0].Name)
}

func TestService_GrantPermission_GrantedReasonEmpty(t *testing.T) {
	t.Parallel()
	svc := userpermissions.NewService(&adminsharedtest.UserPermissionsFakeStorer{})
	_, err := svc.GrantPermission(context.Background(), svcTargetID, svcActorID,
		userpermissions.GrantPermissionInput{PermissionID: svcPermissionID, ExpiresAt: futureTime()})
	require.ErrorIs(t, err, userpermissions.ErrGrantedReasonEmpty)
}

func TestService_GrantPermission_InvalidPermissionUUID(t *testing.T) {
	t.Parallel()
	svc := userpermissions.NewService(&adminsharedtest.UserPermissionsFakeStorer{})
	_, err := svc.GrantPermission(context.Background(), svcTargetID, svcActorID,
		userpermissions.GrantPermissionInput{PermissionID: "not-a-uuid", GrantedReason: "test", ExpiresAt: futureTime()})
	require.ErrorIs(t, err, userpermissions.ErrPermissionNotFound)
}

func TestService_GrantPermission_ExpiresAtInPast(t *testing.T) {
	t.Parallel()
	svc := userpermissions.NewService(&adminsharedtest.UserPermissionsFakeStorer{})
	_, err := svc.GrantPermission(context.Background(), svcTargetID, svcActorID,
		userpermissions.GrantPermissionInput{PermissionID: svcPermissionID, GrantedReason: "test", ExpiresAt: time.Now().Add(-time.Second)})
	require.ErrorIs(t, err, userpermissions.ErrExpiresAtInPast)
}

func TestService_GrantPermission_ScopeAllPassedThrough(t *testing.T) {
	t.Parallel()
	var capturedInput userpermissions.GrantPermissionTxInput
	store := &adminsharedtest.UserPermissionsFakeStorer{
		GrantPermissionTxFn: func(_ context.Context, in userpermissions.GrantPermissionTxInput) (userpermissions.UserPermission, error) {
			capturedInput = in
			return userpermissions.UserPermission{Scope: in.Scope}, nil
		},
	}
	svc := userpermissions.NewService(store)
	_, err := svc.GrantPermission(context.Background(), svcTargetID, svcActorID,
		userpermissions.GrantPermissionInput{PermissionID: svcPermissionID, GrantedReason: "test", ExpiresAt: futureTime(), Scope: "all"})
	require.NoError(t, err)
	require.Equal(t, "all", capturedInput.Scope)
}

func TestService_GrantPermission_AlreadyGrantedPropagates(t *testing.T) {
	t.Parallel()
	store := &adminsharedtest.UserPermissionsFakeStorer{
		GrantPermissionTxFn: func(_ context.Context, _ userpermissions.GrantPermissionTxInput) (userpermissions.UserPermission, error) {
			return userpermissions.UserPermission{}, userpermissions.ErrPermissionAlreadyGranted
		},
	}
	svc := userpermissions.NewService(store)
	_, err := svc.GrantPermission(context.Background(), svcTargetID, svcActorID,
		userpermissions.GrantPermissionInput{PermissionID: svcPermissionID, GrantedReason: "test", ExpiresAt: futureTime()})
	require.ErrorIs(t, err, userpermissions.ErrPermissionAlreadyGranted)
}

func TestService_GrantPermission_PrivilegeEscalationPropagates(t *testing.T) {
	t.Parallel()
	store := &adminsharedtest.UserPermissionsFakeStorer{
		GrantPermissionTxFn: func(_ context.Context, _ userpermissions.GrantPermissionTxInput) (userpermissions.UserPermission, error) {
			return userpermissions.UserPermission{}, userpermissions.ErrPrivilegeEscalation
		},
	}
	svc := userpermissions.NewService(store)
	_, err := svc.GrantPermission(context.Background(), svcTargetID, svcActorID,
		userpermissions.GrantPermissionInput{PermissionID: svcPermissionID, GrantedReason: "test", ExpiresAt: futureTime()})
	require.ErrorIs(t, err, userpermissions.ErrPrivilegeEscalation)
}

func TestService_ListPermissions_StoreError(t *testing.T) {
	t.Parallel()
	storeErr := errors.New("db down")
	store := &adminsharedtest.UserPermissionsFakeStorer{
		GetUserPermissionsFn: func(_ context.Context, _ [16]byte) ([]userpermissions.UserPermission, error) { return nil, storeErr },
	}
	svc := userpermissions.NewService(store)
	_, err := svc.ListPermissions(context.Background(), svcTargetID)
	require.ErrorIs(t, err, storeErr)
}

func TestService_RevokePermission_InvalidActingUUID(t *testing.T) {
	t.Parallel()
	svc := userpermissions.NewService(&adminsharedtest.UserPermissionsFakeStorer{})
	err := svc.RevokePermission(context.Background(), svcTargetID, svcGrantID, "not-a-uuid")
	require.NotNil(t, err)
	require.False(t, errors.Is(err, userpermissions.ErrGrantNotFound))
}

func TestService_RevokePermission_StoreError(t *testing.T) {
	t.Parallel()
	storeErr := errors.New("db down")
	store := &adminsharedtest.UserPermissionsFakeStorer{
		RevokePermissionFn: func(_ context.Context, _, _ [16]byte, _ string) error { return storeErr },
	}
	svc := userpermissions.NewService(store)
	err := svc.RevokePermission(context.Background(), svcTargetID, svcGrantID, svcActorID)
	require.ErrorIs(t, err, storeErr)
}

func TestService_GrantPermission_InvalidActingUUID(t *testing.T) {
	t.Parallel()
	svc := userpermissions.NewService(&adminsharedtest.UserPermissionsFakeStorer{})
	_, err := svc.GrantPermission(context.Background(), svcTargetID, "not-a-uuid",
		userpermissions.GrantPermissionInput{PermissionID: svcPermissionID, GrantedReason: "test", ExpiresAt: futureTime()})
	require.NotNil(t, err)
	require.False(t, errors.Is(err, userpermissions.ErrGrantNotFound))
	require.False(t, errors.Is(err, userpermissions.ErrPermissionNotFound))
}
