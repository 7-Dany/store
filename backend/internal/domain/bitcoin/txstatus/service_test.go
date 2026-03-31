package txstatus

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/platform/bitcoin/rpc"
)

// ── fakeRPC ───────────────────────────────────────────────────────────────────

// fakeRPC is a minimal implementation of TxQuerier for service unit tests.
// GetTransaction delegates to fn. GetMempoolEntry delegates to mempoolFn if set;
// otherwise it panics so accidental calls in tests that don't expect it fail loudly.
type fakeRPC struct {
	fn             func(ctx context.Context, txid string, verbose bool) (rpc.WalletTx, error)
	mempoolFn      func(ctx context.Context, txid string) (rpc.MempoolEntry, error)
	blockHeaderFn  func(ctx context.Context, hash string) (rpc.BlockHeader, error)
	blockVerboseFn func(ctx context.Context, hash string) (rpc.VerboseBlock, error)
}

// fakeStore is a hand-written Storer used by service tests.
type fakeStore struct {
	createFn func(ctx context.Context, in trackedTxStatusWriteInput) (TrackedTxStatus, error)
	getFn    func(ctx context.Context, in GetTrackedTxStatusInput) (TrackedTxStatus, error)
	listFn   func(ctx context.Context, in ListTrackedTxStatusesInput) ([]TrackedTxStatus, error)
	updateFn func(ctx context.Context, in trackedTxStatusUpdateInput) (TrackedTxStatus, error)
	deleteFn func(ctx context.Context, in DeleteTrackedTxStatusInput) error
}

// fakeCRUDRPC is a narrow TxQuerier for CRUD-focused service tests.
type fakeCRUDRPC struct {
	getTransactionFn  func(ctx context.Context, txid string, verbose bool) (rpc.WalletTx, error)
	getMempoolFn      func(ctx context.Context, txid string) (rpc.MempoolEntry, error)
	getBlockHeaderFn  func(ctx context.Context, hash string) (rpc.BlockHeader, error)
	getBlockVerboseFn func(ctx context.Context, hash string) (rpc.VerboseBlock, error)
}

// compile-time checks that the fakes satisfy the package contracts.
var (
	_ TxQuerier = (*fakeRPC)(nil)
	_ Storer    = (*fakeStore)(nil)
	_ TxQuerier = (*fakeCRUDRPC)(nil)
)

func (f *fakeRPC) GetTransaction(ctx context.Context, txid string, verbose bool) (rpc.WalletTx, error) {
	return f.fn(ctx, txid, verbose)
}

func (f *fakeRPC) GetMempoolEntry(ctx context.Context, txid string) (rpc.MempoolEntry, error) {
	if f.mempoolFn != nil {
		return f.mempoolFn(ctx, txid)
	}
	panic("fakeRPC.GetMempoolEntry: mempoolFn not set — configure it for this test")
}

func (f *fakeRPC) GetBlockHeader(ctx context.Context, hash string) (rpc.BlockHeader, error) {
	if f.blockHeaderFn != nil {
		return f.blockHeaderFn(ctx, hash)
	}
	panic("fakeRPC.GetBlockHeader: blockHeaderFn not set — configure it for this test")
}

func (f *fakeRPC) GetBlockVerbose(ctx context.Context, hash string) (rpc.VerboseBlock, error) {
	if f.blockVerboseFn != nil {
		return f.blockVerboseFn(ctx, hash)
	}
	panic("fakeRPC.GetBlockVerbose: blockVerboseFn not set — configure it for this test")
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (f *fakeStore) CreateTrackedTxStatus(ctx context.Context, in trackedTxStatusWriteInput) (TrackedTxStatus, error) {
	if f.createFn != nil {
		return f.createFn(ctx, in)
	}
	panic("fakeStore.CreateTrackedTxStatus: createFn not set")
}

func (f *fakeStore) GetTrackedTxStatus(ctx context.Context, in GetTrackedTxStatusInput) (TrackedTxStatus, error) {
	if f.getFn != nil {
		return f.getFn(ctx, in)
	}
	panic("fakeStore.GetTrackedTxStatus: getFn not set")
}

func (f *fakeStore) ListTrackedTxStatuses(ctx context.Context, in ListTrackedTxStatusesInput) ([]TrackedTxStatus, error) {
	if f.listFn != nil {
		return f.listFn(ctx, in)
	}
	panic("fakeStore.ListTrackedTxStatuses: listFn not set")
}

func (f *fakeStore) UpdateTrackedTxStatus(ctx context.Context, in trackedTxStatusUpdateInput) (TrackedTxStatus, error) {
	if f.updateFn != nil {
		return f.updateFn(ctx, in)
	}
	panic("fakeStore.UpdateTrackedTxStatus: updateFn not set")
}

func (f *fakeStore) DeleteTrackedTxStatus(ctx context.Context, in DeleteTrackedTxStatusInput) error {
	if f.deleteFn != nil {
		return f.deleteFn(ctx, in)
	}
	panic("fakeStore.DeleteTrackedTxStatus: deleteFn not set")
}

func (f *fakeCRUDRPC) GetTransaction(ctx context.Context, txid string, verbose bool) (rpc.WalletTx, error) {
	if f.getTransactionFn != nil {
		return f.getTransactionFn(ctx, txid, verbose)
	}
	panic("fakeCRUDRPC.GetTransaction: getTransactionFn not set")
}

func (f *fakeCRUDRPC) GetMempoolEntry(ctx context.Context, txid string) (rpc.MempoolEntry, error) {
	if f.getMempoolFn != nil {
		return f.getMempoolFn(ctx, txid)
	}
	return rpc.MempoolEntry{}, &rpc.RPCError{Code: -5, Message: "not in mempool"}
}

func (f *fakeCRUDRPC) GetBlockHeader(ctx context.Context, hash string) (rpc.BlockHeader, error) {
	if f.getBlockHeaderFn != nil {
		return f.getBlockHeaderFn(ctx, hash)
	}
	return rpc.BlockHeader{}, &rpc.RPCError{Code: -5, Message: "Block not found"}
}

func (f *fakeCRUDRPC) GetBlockVerbose(ctx context.Context, hash string) (rpc.VerboseBlock, error) {
	if f.getBlockVerboseFn != nil {
		return f.getBlockVerboseFn(ctx, hash)
	}
	return rpc.VerboseBlock{}, &rpc.RPCError{Code: -5, Message: "Block not found"}
}

// newServiceUnderTest constructs a Service for live lookup tests.
func newServiceUnderTest(rpcClient TxQuerier) *Service {
	return NewService(rpcClient, nil, nil, "testnet4")
}

// newCRUDService constructs a Service with a writable Storer for CRUD tests.
func newCRUDService(rpcClient TxQuerier, store Storer) *Service {
	return NewService(rpcClient, store, nil, "testnet4")
}

// ── single-transaction tests ──────────────────────────────────────────────────

// TestGetTxStatus_Confirmed verifies that a transaction with positive
// Confirmations resolves to status "confirmed" with the correct numeric fields.
func TestGetTxStatus_Confirmed(t *testing.T) {
	t.Parallel()

	fake := &fakeRPC{fn: func(_ context.Context, _ string, _ bool) (rpc.WalletTx, error) {
		return rpc.WalletTx{Confirmations: 3, BlockHeight: 126378}, nil
	}}
	svc := newServiceUnderTest(fake)

	result, err := svc.GetTxStatus(context.Background(), GetTxStatusInput{UserID: uuid.NewString(), TxID: "abc"})

	require.NoError(t, err)
	assert.Equal(t, TxStatusConfirmed, result.Status)
	assert.Equal(t, 3, result.Confirmations)
	assert.Equal(t, 126378, result.BlockHeight)
}

// TestGetTxStatus_Mempool verifies that Confirmations==0 maps to "mempool".
func TestGetTxStatus_Mempool(t *testing.T) {
	t.Parallel()

	fake := &fakeRPC{fn: func(_ context.Context, _ string, _ bool) (rpc.WalletTx, error) {
		return rpc.WalletTx{Confirmations: 0}, nil
	}}
	svc := newServiceUnderTest(fake)

	result, err := svc.GetTxStatus(context.Background(), GetTxStatusInput{UserID: uuid.NewString(), TxID: "abc"})

	require.NoError(t, err)
	assert.Equal(t, TxStatusMempool, result.Status)
}

// TestGetTxStatus_NotFound verifies that RPC -5 from both GetTransaction and
// GetMempoolEntry together produce status "not_found".
func TestGetTxStatus_NotFound(t *testing.T) {
	t.Parallel()

	fake := &fakeRPC{
		fn: func(_ context.Context, _ string, _ bool) (rpc.WalletTx, error) {
			return rpc.WalletTx{}, &rpc.RPCError{Code: -5, Message: "No such wallet transaction"}
		},
		mempoolFn: func(_ context.Context, _ string) (rpc.MempoolEntry, error) {
			return rpc.MempoolEntry{}, &rpc.RPCError{Code: -5, Message: "Transaction not in mempool"}
		},
	}
	svc := newServiceUnderTest(fake)

	result, err := svc.GetTxStatus(context.Background(), GetTxStatusInput{UserID: uuid.NewString(), TxID: "abc"})

	require.NoError(t, err)
	assert.Equal(t, TxStatusNotFound, result.Status)
}

// TestGetTxStatus_WalletUnknownButInMempool verifies the mempool fallback: when
// GetTransaction returns -5 (not in wallet) but GetMempoolEntry succeeds, the
// status is "mempool" rather than "not_found".
func TestGetTxStatus_WalletUnknownButInMempool(t *testing.T) {
	t.Parallel()

	fake := &fakeRPC{
		fn: func(_ context.Context, _ string, _ bool) (rpc.WalletTx, error) {
			return rpc.WalletTx{}, &rpc.RPCError{Code: -5, Message: "No such wallet transaction"}
		},
		mempoolFn: func(_ context.Context, _ string) (rpc.MempoolEntry, error) {
			return rpc.MempoolEntry{}, nil // tx IS in the mempool
		},
	}
	svc := newServiceUnderTest(fake)

	result, err := svc.GetTxStatus(context.Background(), GetTxStatusInput{UserID: uuid.NewString(), TxID: "abc"})

	require.NoError(t, err)
	assert.Equal(t, TxStatusMempool, result.Status)
}

// TestGetTxStatus_MempoolEntryRPCError verifies that a non-not-found error from
// GetMempoolEntry propagates as ErrRPCUnavailable.
func TestGetTxStatus_MempoolEntryRPCError(t *testing.T) {
	t.Parallel()

	fake := &fakeRPC{
		fn: func(_ context.Context, _ string, _ bool) (rpc.WalletTx, error) {
			return rpc.WalletTx{}, &rpc.RPCError{Code: -5, Message: "No such wallet transaction"}
		},
		mempoolFn: func(_ context.Context, _ string) (rpc.MempoolEntry, error) {
			return rpc.MempoolEntry{}, &rpc.RPCError{Code: -8, Message: "node error"}
		},
	}
	svc := newServiceUnderTest(fake)

	_, err := svc.GetTxStatus(context.Background(), GetTxStatusInput{UserID: uuid.NewString(), TxID: "abc"})

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRPCUnavailable))
}

// TestGetTxStatus_Conflicting verifies that Confirmations==-1 maps to "conflicting".
func TestGetTxStatus_Conflicting(t *testing.T) {
	t.Parallel()

	fake := &fakeRPC{fn: func(_ context.Context, _ string, _ bool) (rpc.WalletTx, error) {
		return rpc.WalletTx{Confirmations: -1}, nil
	}}
	svc := newServiceUnderTest(fake)

	result, err := svc.GetTxStatus(context.Background(), GetTxStatusInput{UserID: uuid.NewString(), TxID: "abc"})

	require.NoError(t, err)
	assert.Equal(t, TxStatusConflicting, result.Status)
}

// TestGetTxStatus_RPCDown verifies that a non-404 RPC error is wrapped as
// ErrRPCUnavailable and propagated to the caller.
func TestGetTxStatus_RPCDown(t *testing.T) {
	t.Parallel()

	fake := &fakeRPC{fn: func(_ context.Context, _ string, _ bool) (rpc.WalletTx, error) {
		return rpc.WalletTx{}, &rpc.RPCError{Code: -8, Message: "other error"}
	}}
	svc := newServiceUnderTest(fake)

	_, err := svc.GetTxStatus(context.Background(), GetTxStatusInput{UserID: uuid.NewString(), TxID: "abc"})

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRPCUnavailable))
}

// TestGetTxStatus_Abandoned verifies Confirmations == -2 maps to "abandoned",
// not "conflicting". These are semantically distinct Bitcoin Core states:
// -1 = replaced by a confirmed block; -2 = wallet explicitly abandoned the tx.
func TestGetTxStatus_Abandoned(t *testing.T) {
	t.Parallel()

	fake := &fakeRPC{fn: func(_ context.Context, _ string, _ bool) (rpc.WalletTx, error) {
		return rpc.WalletTx{Confirmations: -2}, nil
	}}
	svc := newServiceUnderTest(fake)

	result, err := svc.GetTxStatus(context.Background(), GetTxStatusInput{UserID: uuid.NewString(), TxID: "abc"})

	require.NoError(t, err)
	assert.Equal(t, TxStatusAbandoned, result.Status)
}

// ── batch tests ───────────────────────────────────────────────────────────────

// TestGetTxStatusBatch_MixedStatuses verifies that a batch with three txids
// resolves all three independently and returns the correct statuses.
func TestGetTxStatusBatch_MixedStatuses(t *testing.T) {
	t.Parallel()

	const (
		txConfirmed = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		txNotFound  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		txMempool   = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	)

	fake := &fakeRPC{
		fn: func(_ context.Context, txid string, _ bool) (rpc.WalletTx, error) {
			switch txid {
			case txConfirmed:
				return rpc.WalletTx{Confirmations: 3, BlockHeight: 100}, nil
			case txNotFound:
				return rpc.WalletTx{}, &rpc.RPCError{Code: -5, Message: "No such wallet transaction"}
			case txMempool:
				return rpc.WalletTx{Confirmations: 0}, nil
			default:
				return rpc.WalletTx{}, &rpc.RPCError{Code: -8, Message: "unexpected txid: " + txid}
			}
		},
		mempoolFn: func(_ context.Context, txid string) (rpc.MempoolEntry, error) {
			// Only txNotFound reaches the mempool fallback.
			if txid == txNotFound {
				return rpc.MempoolEntry{}, &rpc.RPCError{Code: -5, Message: "Transaction not in mempool"}
			}
			panic("fakeRPC.GetMempoolEntry: unexpected txid " + txid)
		},
	}
	svc := newServiceUnderTest(fake)

	results, err := svc.GetTxStatusBatch(context.Background(), GetTxStatusBatchInput{
		UserID: uuid.NewString(),
		TxIDs:  []string{txConfirmed, txNotFound, txMempool},
	})

	require.NoError(t, err)
	require.Len(t, results, 3)
	assert.Equal(t, TxStatusConfirmed, results[txConfirmed].Status)
	assert.Equal(t, TxStatusNotFound, results[txNotFound].Status)
	assert.Equal(t, TxStatusMempool, results[txMempool].Status)
}

// TestGetTxStatusBatch_RPCDown_AbortsEntireBatch verifies that a non-404 RPC
// error on the first call causes the entire batch to abort and return a nil map.
func TestGetTxStatusBatch_RPCDown_AbortsEntireBatch(t *testing.T) {
	t.Parallel()

	fake := &fakeRPC{fn: func(_ context.Context, _ string, _ bool) (rpc.WalletTx, error) {
		return rpc.WalletTx{}, &rpc.RPCError{Code: -8, Message: "node error"}
	}}
	svc := newServiceUnderTest(fake)

	results, err := svc.GetTxStatusBatch(context.Background(), GetTxStatusBatchInput{
		UserID: uuid.NewString(),
		TxIDs: []string{
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		},
	})

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRPCUnavailable))
	assert.Nil(t, results)
}

// TestGetTxStatusBatch_EmptyInput verifies that an empty TxIDs slice returns
// an empty map and no error without calling any RPC method.
func TestGetTxStatusBatch_EmptyInput(t *testing.T) {
	t.Parallel()

	fake := &fakeRPC{
		fn: func(_ context.Context, _ string, _ bool) (rpc.WalletTx, error) {
			panic("fakeRPC.GetTransaction: must not be called for empty input")
		},
	}
	svc := newServiceUnderTest(fake)

	results, err := svc.GetTxStatusBatch(context.Background(), GetTxStatusBatchInput{
		UserID: uuid.NewString(),
		TxIDs:  []string{},
	})

	require.NoError(t, err)
	assert.Empty(t, results)
}

// TestGetTxStatusBatch_CancelledContext verifies that a pre-cancelled context
// propagates through the concurrent fan-out and returns an error.
func TestGetTxStatusBatch_CancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before any call

	fake := &fakeRPC{
		fn: func(ctx context.Context, _ string, _ bool) (rpc.WalletTx, error) {
			// Propagate context cancellation as an RPC error.
			return rpc.WalletTx{}, ctx.Err()
		},
	}
	svc := newServiceUnderTest(fake)

	_, err := svc.GetTxStatusBatch(ctx, GetTxStatusBatchInput{
		UserID: uuid.NewString(),
		TxIDs:  []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	})

	require.Error(t, err)
}

func TestGetTxStatus_UsesTrackedFallbackWhenRPCCannotResolve(t *testing.T) {
	t.Parallel()

	userID := uuid.NewString()
	blockHash := strings.Repeat("f", 64)
	height := int64(777001)
	svc := newCRUDService(&fakeCRUDRPC{
		getTransactionFn: func(_ context.Context, _ string, _ bool) (rpc.WalletTx, error) {
			return rpc.WalletTx{}, &rpc.RPCError{Code: -5, Message: "No such wallet transaction"}
		},
		getMempoolFn: func(_ context.Context, _ string) (rpc.MempoolEntry, error) {
			return rpc.MempoolEntry{}, &rpc.RPCError{Code: -5, Message: "Transaction not in mempool"}
		},
		getBlockVerboseFn: func(_ context.Context, gotHash string) (rpc.VerboseBlock, error) {
			assert.Equal(t, blockHash, gotHash)
			return rpc.VerboseBlock{
				Hash:          blockHash,
				Height:        int(height),
				Confirmations: 9,
				Tx: []rpc.RawTx{
					{TxID: "abc"},
				},
			}, nil
		},
	}, &fakeStore{
		listFn: func(_ context.Context, in ListTrackedTxStatusesInput) ([]TrackedTxStatus, error) {
			assert.Equal(t, userID, in.UserID)
			assert.Equal(t, "testnet4", in.Network)
			assert.Equal(t, "abc", in.TxID)
			assert.Equal(t, 1, in.Limit)
			return []TrackedTxStatus{{
				TxID:          "abc",
				Status:        TxStatusConfirmed,
				Confirmations: 4,
				BlockHash:     &blockHash,
				BlockHeight:   &height,
			}}, nil
		},
	})

	result, err := svc.GetTxStatus(context.Background(), GetTxStatusInput{UserID: userID, TxID: "abc"})

	require.NoError(t, err)
	assert.Equal(t, TxStatusConfirmed, result.Status)
	assert.Equal(t, 9, result.Confirmations)
	assert.Equal(t, int(height), result.BlockHeight)
}

func TestGetTxStatusBatch_UsesTrackedFallbackForSavedConfirmedTx(t *testing.T) {
	t.Parallel()

	const txid = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	userID := uuid.NewString()
	blockHash := strings.Repeat("e", 64)
	height := int64(555)
	svc := newCRUDService(&fakeCRUDRPC{
		getTransactionFn: func(_ context.Context, got string, _ bool) (rpc.WalletTx, error) {
			assert.Equal(t, txid, got)
			return rpc.WalletTx{}, &rpc.RPCError{Code: -5, Message: "No such wallet transaction"}
		},
		getMempoolFn: func(_ context.Context, got string) (rpc.MempoolEntry, error) {
			assert.Equal(t, txid, got)
			return rpc.MempoolEntry{}, &rpc.RPCError{Code: -5, Message: "Transaction not in mempool"}
		},
		getBlockHeaderFn: func(_ context.Context, gotHash string) (rpc.BlockHeader, error) {
			assert.Equal(t, blockHash, gotHash)
			return rpc.BlockHeader{Hash: blockHash, Height: int(height), Confirmations: 3}, nil
		},
	}, &fakeStore{
		listFn: func(_ context.Context, in ListTrackedTxStatusesInput) ([]TrackedTxStatus, error) {
			assert.Equal(t, userID, in.UserID)
			assert.Equal(t, txid, in.TxID)
			return []TrackedTxStatus{{
				TxID:          txid,
				Status:        TxStatusConfirmed,
				Confirmations: 1,
				BlockHash:     &blockHash,
				BlockHeight:   &height,
			}}, nil
		},
	})

	results, err := svc.GetTxStatusBatch(context.Background(), GetTxStatusBatchInput{
		UserID: userID,
		TxIDs:  []string{txid},
	})

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, TxStatusConfirmed, results[txid].Status)
	assert.Equal(t, 3, results[txid].Confirmations)
	assert.Equal(t, int(height), results[txid].BlockHeight)
}

func TestCreateTrackedTxStatus_PersistsResolvedTx(t *testing.T) {
	t.Parallel()

	userID := "11111111-1111-1111-1111-111111111111"
	var got trackedTxStatusWriteInput
	store := &fakeStore{
		createFn: func(_ context.Context, in trackedTxStatusWriteInput) (TrackedTxStatus, error) {
			got = in
			return TrackedTxStatus{ID: 7, UserID: userID, Network: in.Network, TxID: in.TxID, Status: in.Status}, nil
		},
	}
	rpcClient := &fakeCRUDRPC{
		getTransactionFn: func(_ context.Context, _ string, _ bool) (rpc.WalletTx, error) {
			return rpc.WalletTx{Confirmations: 3, BlockHeight: 101}, nil
		},
	}

	row, err := newCRUDService(rpcClient, store).CreateTrackedTxStatus(context.Background(), CreateTrackedTxStatusInput{
		UserID:  userID,
		Network: "testnet4",
		TxID:    strings.Repeat("a", 64),
	})

	require.NoError(t, err)
	assert.Equal(t, int64(7), row.ID)
	assert.Equal(t, TxStatusConfirmed, got.Status)
	assert.Equal(t, 3, got.Confirmations)
	require.NotNil(t, got.ConfirmedAt)
	require.NotNil(t, got.BlockHeight)
	assert.Equal(t, int64(101), *got.BlockHeight)
}

func TestCreateTrackedTxStatus_InvalidUserID_ReturnsError(t *testing.T) {
	t.Parallel()

	rpcClient := &fakeCRUDRPC{
		getTransactionFn: func(_ context.Context, _ string, _ bool) (rpc.WalletTx, error) {
			return rpc.WalletTx{Confirmations: 0}, nil
		},
	}

	_, err := newCRUDService(rpcClient, &fakeStore{}).CreateTrackedTxStatus(context.Background(), CreateTrackedTxStatusInput{
		UserID:  "bad-uuid",
		Network: "testnet4",
		TxID:    strings.Repeat("a", 64),
	})

	require.Error(t, err)
}

func TestGetTrackedTxStatus_DelegatesToStore(t *testing.T) {
	t.Parallel()

	want := TrackedTxStatus{ID: 9}
	svc := newCRUDService(nil, &fakeStore{
		getFn: func(_ context.Context, in GetTrackedTxStatusInput) (TrackedTxStatus, error) {
			assert.Equal(t, int64(9), in.ID)
			return want, nil
		},
	})

	got, err := svc.GetTrackedTxStatus(context.Background(), GetTrackedTxStatusInput{ID: 9, UserID: "11111111-1111-1111-1111-111111111111"})

	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestListTrackedTxStatuses_DelegatesToStore(t *testing.T) {
	t.Parallel()

	want := []TrackedTxStatus{{ID: 1}, {ID: 2}}
	svc := newCRUDService(nil, &fakeStore{
		listFn: func(_ context.Context, in ListTrackedTxStatusesInput) ([]TrackedTxStatus, error) {
			assert.Equal(t, "testnet4", in.Network)
			assert.Equal(t, 10, in.Limit)
			return want, nil
		},
	})

	got, err := svc.ListTrackedTxStatuses(context.Background(), ListTrackedTxStatusesInput{
		UserID:  "11111111-1111-1111-1111-111111111111",
		Network: "testnet4",
		Limit:   10,
	})

	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestUpdateTrackedTxStatus_RejectsWatchManagedRows(t *testing.T) {
	t.Parallel()

	svc := newCRUDService(&fakeCRUDRPC{
		getTransactionFn: func(_ context.Context, _ string, _ bool) (rpc.WalletTx, error) {
			return rpc.WalletTx{Confirmations: 0}, nil
		},
	}, &fakeStore{
		getFn: func(_ context.Context, _ GetTrackedTxStatusInput) (TrackedTxStatus, error) {
			return TrackedTxStatus{TrackingMode: TrackingModeWatch}, nil
		},
	})

	_, err := svc.UpdateTrackedTxStatus(context.Background(), UpdateTrackedTxStatusInput{
		ID:     7,
		UserID: "11111111-1111-1111-1111-111111111111",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrWatchManagedTrackedTxStatus)
}

func TestUpdateTrackedTxStatus_RefreshesResolvedState(t *testing.T) {
	t.Parallel()

	userID := "11111111-1111-1111-1111-111111111111"
	now := time.Date(2026, time.March, 30, 18, 0, 0, 0, time.UTC)
	address := "tb1qfc74wvs6wnz3p2twgza26vukqct4emt2v47xwq"
	var got trackedTxStatusUpdateInput
	svc := newCRUDService(&fakeCRUDRPC{
		getTransactionFn: func(_ context.Context, _ string, _ bool) (rpc.WalletTx, error) {
			return rpc.WalletTx{Confirmations: 0}, nil
		},
	}, &fakeStore{
		getFn: func(_ context.Context, _ GetTrackedTxStatusInput) (TrackedTxStatus, error) {
			return TrackedTxStatus{
				ID:            7,
				UserID:        userID,
				Network:       "testnet4",
				TrackingMode:  TrackingModeTxID,
				Address:       &address,
				TxID:          strings.Repeat("b", 64),
				Status:        TxStatusConfirmed,
				Confirmations: 1,
				FirstSeenAt:   now.Add(-10 * time.Minute),
			}, nil
		},
		updateFn: func(_ context.Context, in trackedTxStatusUpdateInput) (TrackedTxStatus, error) {
			got = in
			return TrackedTxStatus{ID: in.ID, TxID: in.TxID, Status: in.Status}, nil
		},
	})

	row, err := svc.UpdateTrackedTxStatus(context.Background(), UpdateTrackedTxStatusInput{
		ID:      7,
		UserID:  userID,
		Address: &address,
	})

	require.NoError(t, err)
	assert.Equal(t, int64(7), row.ID)
	assert.Equal(t, strings.Repeat("b", 64), got.TxID)
	assert.Equal(t, TxStatusMempool, got.Status)
	assert.Equal(t, 0, got.Confirmations)
	assert.Nil(t, got.ConfirmedAt)
	assert.Equal(t, now.Add(-10*time.Minute), got.FirstSeenAt)
}

func TestDeleteTrackedTxStatus_DelegatesToStore(t *testing.T) {
	t.Parallel()

	var got DeleteTrackedTxStatusInput
	svc := newCRUDService(nil, &fakeStore{
		deleteFn: func(_ context.Context, in DeleteTrackedTxStatusInput) error {
			got = in
			return nil
		},
	})

	err := svc.DeleteTrackedTxStatus(context.Background(), DeleteTrackedTxStatusInput{
		ID:     7,
		UserID: "11111111-1111-1111-1111-111111111111",
	})

	require.NoError(t, err)
	assert.Equal(t, int64(7), got.ID)
}

func TestDeleteTrackedTxStatus_StoreErrorPropagates(t *testing.T) {
	t.Parallel()

	svc := newCRUDService(nil, &fakeStore{
		deleteFn: func(_ context.Context, _ DeleteTrackedTxStatusInput) error {
			return errors.New("db down")
		},
	})

	err := svc.DeleteTrackedTxStatus(context.Background(), DeleteTrackedTxStatusInput{
		ID:     7,
		UserID: uuid.NewString(),
	})

	require.Error(t, err)
}
