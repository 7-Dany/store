package txstatus_test

// Black-box service tests — package txstatus_test. The fakeRPC satisfies the
// TxQuerier interface — only GetTransaction and GetMempoolEntry are wired.

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/domain/bitcoin/txstatus"
	"github.com/7-Dany/store/backend/internal/platform/bitcoin/rpc"
)

// ── fakeRPC ───────────────────────────────────────────────────────────────────

// fakeRPC is a minimal implementation of txstatus.TxQuerier for service unit tests.
// GetTransaction delegates to fn. GetMempoolEntry delegates to mempoolFn if set;
// otherwise it panics so accidental calls in tests that don't expect it fail loudly.
type fakeRPC struct {
	fn        func(ctx context.Context, txid string, verbose bool) (rpc.WalletTx, error)
	mempoolFn func(ctx context.Context, txid string) (rpc.MempoolEntry, error)
}

// compile-time check that *fakeRPC satisfies txstatus.TxQuerier.
var _ txstatus.TxQuerier = (*fakeRPC)(nil)

func (f *fakeRPC) GetTransaction(ctx context.Context, txid string, verbose bool) (rpc.WalletTx, error) {
	return f.fn(ctx, txid, verbose)
}

func (f *fakeRPC) GetMempoolEntry(ctx context.Context, txid string) (rpc.MempoolEntry, error) {
	if f.mempoolFn != nil {
		return f.mempoolFn(ctx, txid)
	}
	panic("fakeRPC.GetMempoolEntry: mempoolFn not set — configure it for this test")
}

// ── helpers ───────────────────────────────────────────────────────────────────

// newServiceUnderTest constructs the service under test with a nil recorder
// (substituted internally with a no-op).
func newServiceUnderTest(rpcClient txstatus.TxQuerier) *txstatus.Service {
	return txstatus.NewService(rpcClient, nil)
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

	result, err := svc.GetTxStatus(context.Background(), txstatus.GetTxStatusInput{TxID: "abc"})

	require.NoError(t, err)
	assert.Equal(t, txstatus.TxStatusConfirmed, result.Status)
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

	result, err := svc.GetTxStatus(context.Background(), txstatus.GetTxStatusInput{TxID: "abc"})

	require.NoError(t, err)
	assert.Equal(t, txstatus.TxStatusMempool, result.Status)
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

	result, err := svc.GetTxStatus(context.Background(), txstatus.GetTxStatusInput{TxID: "abc"})

	require.NoError(t, err)
	assert.Equal(t, txstatus.TxStatusNotFound, result.Status)
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

	result, err := svc.GetTxStatus(context.Background(), txstatus.GetTxStatusInput{TxID: "abc"})

	require.NoError(t, err)
	assert.Equal(t, txstatus.TxStatusMempool, result.Status)
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

	_, err := svc.GetTxStatus(context.Background(), txstatus.GetTxStatusInput{TxID: "abc"})

	require.Error(t, err)
	assert.True(t, errors.Is(err, txstatus.ErrRPCUnavailable))
}

// TestGetTxStatus_Conflicting verifies that Confirmations==-1 maps to "conflicting".
func TestGetTxStatus_Conflicting(t *testing.T) {
	t.Parallel()

	fake := &fakeRPC{fn: func(_ context.Context, _ string, _ bool) (rpc.WalletTx, error) {
		return rpc.WalletTx{Confirmations: -1}, nil
	}}
	svc := newServiceUnderTest(fake)

	result, err := svc.GetTxStatus(context.Background(), txstatus.GetTxStatusInput{TxID: "abc"})

	require.NoError(t, err)
	assert.Equal(t, txstatus.TxStatusConflicting, result.Status)
}

// TestGetTxStatus_RPCDown verifies that a non-404 RPC error is wrapped as
// ErrRPCUnavailable and propagated to the caller.
func TestGetTxStatus_RPCDown(t *testing.T) {
	t.Parallel()

	fake := &fakeRPC{fn: func(_ context.Context, _ string, _ bool) (rpc.WalletTx, error) {
		return rpc.WalletTx{}, &rpc.RPCError{Code: -8, Message: "other error"}
	}}
	svc := newServiceUnderTest(fake)

	_, err := svc.GetTxStatus(context.Background(), txstatus.GetTxStatusInput{TxID: "abc"})

	require.Error(t, err)
	assert.True(t, errors.Is(err, txstatus.ErrRPCUnavailable))
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

	result, err := svc.GetTxStatus(context.Background(), txstatus.GetTxStatusInput{TxID: "abc"})

	require.NoError(t, err)
	assert.Equal(t, txstatus.TxStatusAbandoned, result.Status)
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

	results, err := svc.GetTxStatusBatch(context.Background(), txstatus.GetTxStatusBatchInput{
		TxIDs: []string{txConfirmed, txNotFound, txMempool},
	})

	require.NoError(t, err)
	require.Len(t, results, 3)
	assert.Equal(t, txstatus.TxStatusConfirmed, results[txConfirmed].Status)
	assert.Equal(t, txstatus.TxStatusNotFound, results[txNotFound].Status)
	assert.Equal(t, txstatus.TxStatusMempool, results[txMempool].Status)
}

// TestGetTxStatusBatch_RPCDown_AbortsEntireBatch verifies that a non-404 RPC
// error on the first call causes the entire batch to abort and return a nil map.
func TestGetTxStatusBatch_RPCDown_AbortsEntireBatch(t *testing.T) {
	t.Parallel()

	fake := &fakeRPC{fn: func(_ context.Context, _ string, _ bool) (rpc.WalletTx, error) {
		return rpc.WalletTx{}, &rpc.RPCError{Code: -8, Message: "node error"}
	}}
	svc := newServiceUnderTest(fake)

	results, err := svc.GetTxStatusBatch(context.Background(), txstatus.GetTxStatusBatchInput{
		TxIDs: []string{
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		},
	})

	require.Error(t, err)
	assert.True(t, errors.Is(err, txstatus.ErrRPCUnavailable))
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

	results, err := svc.GetTxStatusBatch(context.Background(), txstatus.GetTxStatusBatchInput{
		TxIDs: []string{},
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

	_, err := svc.GetTxStatusBatch(ctx, txstatus.GetTxStatusBatchInput{
		TxIDs: []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	})

	require.Error(t, err)
}
