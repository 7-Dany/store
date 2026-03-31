package watch

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/audit"
	bitcoinshared "github.com/7-Dany/store/backend/internal/domain/bitcoin/shared"
)

type fakeStorer struct {
	createWatchFn               func(ctx context.Context, in watchWriteInput) (Watch, error)
	getWatchFn                  func(ctx context.Context, in GetWatchInput) (Watch, error)
	listWatchesFn               func(ctx context.Context, in ListWatchesInput) ([]Watch, error)
	updateWatchFn               func(ctx context.Context, in watchUpdateInput) (Watch, error)
	deleteWatchFn               func(ctx context.Context, in DeleteWatchInput) error
	countActiveAddressWatchesFn func(ctx context.Context, userID, network string) (int, error)
	writeAuditLogFn             func(ctx context.Context, event audit.EventType, userID, sourceIP string, metadata map[string]string) error
}

var _ Storer = (*fakeStorer)(nil)

func (f *fakeStorer) CreateWatch(ctx context.Context, in watchWriteInput) (Watch, error) {
	if f.createWatchFn != nil {
		return f.createWatchFn(ctx, in)
	}
	return Watch{}, nil
}

func (f *fakeStorer) GetWatch(ctx context.Context, in GetWatchInput) (Watch, error) {
	if f.getWatchFn != nil {
		return f.getWatchFn(ctx, in)
	}
	return Watch{}, nil
}

func (f *fakeStorer) ListWatches(ctx context.Context, in ListWatchesInput) ([]Watch, error) {
	if f.listWatchesFn != nil {
		return f.listWatchesFn(ctx, in)
	}
	return nil, nil
}

func (f *fakeStorer) UpdateWatch(ctx context.Context, in watchUpdateInput) (Watch, error) {
	if f.updateWatchFn != nil {
		return f.updateWatchFn(ctx, in)
	}
	return Watch{}, nil
}

func (f *fakeStorer) DeleteWatch(ctx context.Context, in DeleteWatchInput) error {
	if f.deleteWatchFn != nil {
		return f.deleteWatchFn(ctx, in)
	}
	return nil
}

func (f *fakeStorer) CountActiveAddressWatches(ctx context.Context, userID, network string) (int, error) {
	if f.countActiveAddressWatchesFn != nil {
		return f.countActiveAddressWatchesFn(ctx, userID, network)
	}
	return 0, nil
}

func (f *fakeStorer) WriteAuditLog(ctx context.Context, event audit.EventType, userID, sourceIP string, metadata map[string]string) error {
	if f.writeAuditLogFn != nil {
		return f.writeAuditLogFn(ctx, event, userID, sourceIP, metadata)
	}
	return nil
}

func TestCreateWatch_AddressSuccess(t *testing.T) {
	t.Parallel()

	userID := uuid.New().String()
	address := "tb1qexampleaddress0000000000000000000000000"
	var events []audit.EventType

	svc := NewService(&fakeStorer{
		createWatchFn: func(_ context.Context, in watchWriteInput) (Watch, error) {
			return Watch{
				ID:        7,
				Network:   in.Network,
				WatchType: in.WatchType,
				Address:   in.Address,
				Status:    WatchStatusActive,
			}, nil
		},
		writeAuditLogFn: func(_ context.Context, event audit.EventType, _, _ string, metadata map[string]string) error {
			events = append(events, event)
			assert.Equal(t, "create", metadata["action"])
			return nil
		},
	}, bitcoinshared.NoopBitcoinRecorder{}, 5)

	row, err := svc.CreateWatch(context.Background(), CreateWatchInput{
		UserID:    userID,
		Network:   "testnet4",
		WatchType: WatchTypeAddress,
		Address:   &address,
		SourceIP:  "127.0.0.1",
	})

	require.NoError(t, err)
	assert.Equal(t, WatchTypeAddress, row.WatchType)
	require.Len(t, events, 1)
	assert.Equal(t, audit.EventBitcoinAddressWatched, events[0])
}

func TestCreateWatch_AddressLimitExceeded(t *testing.T) {
	t.Parallel()

	address := "tb1qlimitaddress000000000000000000000000000"
	svc := NewService(&fakeStorer{
		countActiveAddressWatchesFn: func(_ context.Context, _, _ string) (int, error) {
			return 2, nil
		},
	}, bitcoinshared.NoopBitcoinRecorder{}, 2)

	_, err := svc.CreateWatch(context.Background(), CreateWatchInput{
		UserID:    uuid.New().String(),
		Network:   "testnet4",
		WatchType: WatchTypeAddress,
		Address:   &address,
	})

	require.Error(t, err)
	assert.True(t, errors.Is(err, bitcoinshared.ErrWatchLimitExceeded))
}

func TestUpdateWatch_TransactionToAddressChecksCapacity(t *testing.T) {
	t.Parallel()

	address := "tb1qupdateaddress00000000000000000000000000"
	svc := NewService(&fakeStorer{
		getWatchFn: func(_ context.Context, _ GetWatchInput) (Watch, error) {
			txid := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			return Watch{ID: 9, Network: "testnet4", WatchType: WatchTypeTransaction, TxID: &txid}, nil
		},
		countActiveAddressWatchesFn: func(_ context.Context, _, _ string) (int, error) {
			return 1, nil
		},
		updateWatchFn: func(_ context.Context, in watchUpdateInput) (Watch, error) {
			return Watch{ID: in.ID, Network: in.Network, WatchType: in.WatchType, Address: in.Address}, nil
		},
	}, bitcoinshared.NoopBitcoinRecorder{}, 2)

	row, err := svc.UpdateWatch(context.Background(), UpdateWatchInput{
		ID:        9,
		UserID:    uuid.New().String(),
		WatchType: WatchTypeAddress,
		Address:   &address,
	})

	require.NoError(t, err)
	assert.Equal(t, WatchTypeAddress, row.WatchType)
	require.NotNil(t, row.Address)
}

func TestDeleteWatch_DelegatesToStore(t *testing.T) {
	t.Parallel()

	deleted := false
	svc := NewService(&fakeStorer{
		getWatchFn: func(_ context.Context, _ GetWatchInput) (Watch, error) {
			address := "tb1qdeleteaddress00000000000000000000000000"
			return Watch{ID: 1, Network: "testnet4", WatchType: WatchTypeAddress, Address: &address}, nil
		},
		deleteWatchFn: func(_ context.Context, in DeleteWatchInput) error {
			deleted = in.ID == 1
			return nil
		},
	}, bitcoinshared.NoopBitcoinRecorder{}, 2)

	err := svc.DeleteWatch(context.Background(), DeleteWatchInput{ID: 1, UserID: uuid.New().String()})
	require.NoError(t, err)
	assert.True(t, deleted)
}
