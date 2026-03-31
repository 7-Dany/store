package events

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/platform/bitcoin/rpc"
	"github.com/7-Dany/store/backend/internal/platform/bitcoin/zmq"
)

// ── fakeRPC ───────────────────────────────────────────────────────────────────

// fakeRPCClient is a hand-written rpc.Client for mempool unit tests.
// Only the methods exercised by MempoolTracker are implemented; every other
// method panics so accidental calls fail loudly.
type fakeRPCClient struct {
	GetMempoolEntryFn func(ctx context.Context, txid string) (rpc.MempoolEntry, error)
	GetBlockHeaderFn  func(ctx context.Context, hash string) (rpc.BlockHeader, error)
	GetBlockFn        func(ctx context.Context, hash string, verbosity int) (json.RawMessage, error)
	GetBlockVerboseFn func(ctx context.Context, hash string) (rpc.VerboseBlock, error)
}

var _ rpc.Client = (*fakeRPCClient)(nil)

func (f *fakeRPCClient) GetRawTransaction(ctx context.Context, txid string, verbosity int) (rpc.RawTx, error) {
	return rpc.RawTx{}, rpcErrNotFound("GetRawTransaction: not used in tests")
}
func (f *fakeRPCClient) GetMempoolEntry(ctx context.Context, txid string) (rpc.MempoolEntry, error) {
	if f.GetMempoolEntryFn != nil {
		return f.GetMempoolEntryFn(ctx, txid)
	}
	return rpc.MempoolEntry{}, rpcErrNotFound("GetMempoolEntry: not configured")
}
func (f *fakeRPCClient) GetBlockHeader(ctx context.Context, hash string) (rpc.BlockHeader, error) {
	if f.GetBlockHeaderFn != nil {
		return f.GetBlockHeaderFn(ctx, hash)
	}
	return rpc.BlockHeader{}, nil
}
func (f *fakeRPCClient) GetBlock(ctx context.Context, hash string, verbosity int) (json.RawMessage, error) {
	if f.GetBlockFn != nil {
		return f.GetBlockFn(ctx, hash, verbosity)
	}
	return json.RawMessage(`{"tx":[]}`), nil
}
func (f *fakeRPCClient) GetBlockVerbose(ctx context.Context, hash string) (rpc.VerboseBlock, error) {
	if f.GetBlockVerboseFn != nil {
		return f.GetBlockVerboseFn(ctx, hash)
	}
	return rpc.VerboseBlock{}, nil
}

// Unimplemented stubs — panic if called to surface accidental usage.
func (f *fakeRPCClient) GetBlockchainInfo(ctx context.Context) (rpc.BlockchainInfo, error) {
	panic("fakeRPCClient.GetBlockchainInfo not implemented")
}
func (f *fakeRPCClient) GetBlockHash(ctx context.Context, height int) (string, error) {
	panic("fakeRPCClient.GetBlockHash not implemented")
}
func (f *fakeRPCClient) GetBlockCount(ctx context.Context) (int, error) {
	panic("fakeRPCClient.GetBlockCount not implemented")
}
func (f *fakeRPCClient) GetTransaction(ctx context.Context, txid string, verbose bool) (rpc.WalletTx, error) {
	panic("fakeRPCClient.GetTransaction not implemented")
}
func (f *fakeRPCClient) GetNewAddress(ctx context.Context, label, addressType string) (string, error) {
	panic("fakeRPCClient.GetNewAddress not implemented")
}
func (f *fakeRPCClient) GetAddressInfo(ctx context.Context, address string) (rpc.AddressInfo, error) {
	panic("fakeRPCClient.GetAddressInfo not implemented")
}
func (f *fakeRPCClient) GetWalletInfo(ctx context.Context) (rpc.WalletInfo, error) {
	panic("fakeRPCClient.GetWalletInfo not implemented")
}
func (f *fakeRPCClient) KeypoolRefill(ctx context.Context, newSize int) error {
	panic("fakeRPCClient.KeypoolRefill not implemented")
}
func (f *fakeRPCClient) EstimateSmartFee(ctx context.Context, confTarget int, mode string) (rpc.FeeEstimate, error) {
	panic("fakeRPCClient.EstimateSmartFee not implemented")
}
func (f *fakeRPCClient) WalletCreateFundedPSBT(ctx context.Context, outputs []map[string]any, options map[string]any) (rpc.FundedPSBT, error) {
	panic("fakeRPCClient.WalletCreateFundedPSBT not implemented")
}
func (f *fakeRPCClient) WalletProcessPSBT(ctx context.Context, psbt string) (rpc.ProcessedPSBT, error) {
	panic("fakeRPCClient.WalletProcessPSBT not implemented")
}
func (f *fakeRPCClient) FinalizePSBT(ctx context.Context, psbt string) (rpc.FinalizedPSBT, error) {
	panic("fakeRPCClient.FinalizePSBT not implemented")
}
func (f *fakeRPCClient) SendRawTransaction(ctx context.Context, hexTx string, maxFeeRate float64) (string, error) {
	panic("fakeRPCClient.SendRawTransaction not implemented")
}
func (f *fakeRPCClient) Close() {}

// rpcErrNotFound constructs a Bitcoin Core "not found" RPCError (code -5) for use in fakes.
func rpcErrNotFound(msg string) error {
	return &rpc.RPCError{Code: -5, Message: msg}
}

// ── zmq event helpers ─────────────────────────────────────────────────────────

// makeRawTxEvent builds a zmq.RawTxEvent with the given txid, inputs, and
// outputs. TxIDBytes uses the same byte order as RPC and block explorers.
func makeRawTxEvent(txidHex string, inputs []zmq.RawTxInput, outputs []zmq.RawTxOutput) zmq.RawTxEvent {
	b := mustDecodeHex32(txidHex)
	return zmq.RawTxEvent{TxIDBytes: b, Sequence: 0, Inputs: inputs, Outputs: outputs}
}

func makeBlockEvent(hashHex string) zmq.BlockEvent {
	b := mustDecodeHex32(hashHex)
	return zmq.BlockEvent{Hash: b}
}

func mustDecodeHex32(h string) [32]byte {
	if len(h) != 64 {
		panic("mustDecodeHex32: expected 64-char hex string, got " + h)
	}
	var b [32]byte
	for i := 0; i < 32; i++ {
		n := hexNibble(h[2*i])<<4 | hexNibble(h[2*i+1])
		b[i] = n
	}
	return b
}

func hexNibble(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	panic("hexNibble: invalid hex char")
}

// ── test fixture ──────────────────────────────────────────────────────────────

const (
	testTxID1     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testTxID2     = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testTxID3     = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	testBlockHash = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	testAddr1     = "tb1qaddr1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testAddr2     = "tb1qaddr2bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func testTrackerCfg() EventsConfig {
	return EventsConfig{
		BlockRPCTimeout:          2 * time.Second,
		PendingMempoolMaxSize:    100,
		MempoolPendingMaxAgeDays: 1,
	}
}

// newTestTracker builds a MempoolTracker with the given store, broker, and RPC.
// The pruning goroutine is started; ctx cancels it on test cleanup.
func newTestTracker(t *testing.T, st Storer, broker *Broker, rpcClient rpc.Client) *MempoolTracker {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return NewMempoolTracker(ctx, st, broker, rpcClient, nil, testTrackerCfg())
}

// drainEvents reads up to n events from ch within timeout, returning them all.
func drainEvents(ch <-chan Event, n int, timeout time.Duration) []Event {
	var got []Event
	deadline := time.After(timeout)
	for len(got) < n {
		select {
		case e := <-ch:
			got = append(got, e)
		case <-deadline:
			return got
		}
	}
	return got
}

// ── HandleTxEvent ─────────────────────────────────────────────────────────────

func TestMempoolTracker_HandleTxEvent_MatchedAddress_EmitsPendingMempool(t *testing.T) {
	t.Parallel()
	broker := NewBroker(10, nil)
	ch, _ := broker.Subscribe("user-1")

	st := &localFakeStorer{
		GetUserWatchAddressesFn: func(_ context.Context, _, _ string) ([]string, error) {
			return []string{testAddr1}, nil
		},
	}
	rpcClient := &fakeRPCClient{}

	tracker := newTestTracker(t, st, broker, rpcClient)
	tracker.HandleRawTxEvent(context.Background(), makeRawTxEvent(testTxID1, nil, []zmq.RawTxOutput{{ValueSat: 0, N: 0, Address: testAddr1}}))

	events := drainEvents(ch, 1, time.Second)
	require.Len(t, events, 1, "expected exactly one pending_mempool event")
	assert.Equal(t, "pending_mempool", events[0].Type)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(events[0].Payload, &payload))
	assert.Equal(t, testTxID1, payload["txid"])
	addrs, _ := payload["addresses"].([]any)
	assert.Contains(t, addrs, testAddr1)
}

func TestMempoolTracker_HandleTxEvent_NoAddressMatch_NoEvent(t *testing.T) {
	t.Parallel()
	broker := NewBroker(10, nil)
	ch, _ := broker.Subscribe("user-1")

	st := &localFakeStorer{
		GetUserWatchAddressesFn: func(_ context.Context, _, _ string) ([]string, error) {
			return []string{"tb1qwatchedaddr_not_in_tx"}, nil
		},
	}
	rpcClient := &fakeRPCClient{}

	tracker := newTestTracker(t, st, broker, rpcClient)
	tracker.HandleRawTxEvent(context.Background(), makeRawTxEvent(testTxID1, nil, []zmq.RawTxOutput{{ValueSat: 0, N: 0, Address: testAddr1}}))

	events := drainEvents(ch, 1, 100*time.Millisecond)
	assert.Empty(t, events, "no event expected when tx outputs don't match watch addresses")
}

func TestMempoolTracker_HandleTxEvent_NoConnectedUsers_NoEvent(t *testing.T) {
	t.Parallel()
	broker := NewBroker(10, nil) // no subscribers
	st := &localFakeStorer{}
	rpcClient := &fakeRPCClient{}

	tracker := newTestTracker(t, st, broker, rpcClient)
	// Must not panic.
	tracker.HandleRawTxEvent(context.Background(), makeRawTxEvent(testTxID1, nil, []zmq.RawTxOutput{{ValueSat: 0, N: 0, Address: testAddr1}}))
}

func TestMempoolTracker_HandleRawTxEvent_NoAddresses_NoEvent(t *testing.T) {
	t.Parallel()
	broker := NewBroker(10, nil)
	ch, _ := broker.Subscribe("user-1")

	st := &localFakeStorer{
		GetUserWatchAddressesFn: func(_ context.Context, _, _ string) ([]string, error) {
			return []string{testAddr1}, nil
		},
	}
	rpcClient := &fakeRPCClient{}

	tracker := newTestTracker(t, st, broker, rpcClient)
	tracker.HandleRawTxEvent(context.Background(), makeRawTxEvent(testTxID1, nil, []zmq.RawTxOutput{{ValueSat: 0, N: 0, Address: ""}}))

	events := drainEvents(ch, 1, 100*time.Millisecond)
	assert.Empty(t, events, "no addresses must not emit any event")
}

func TestMempoolTracker_HandleTxEvent_AddedToPendingMempool(t *testing.T) {
	t.Parallel()
	broker := NewBroker(10, nil)
	_, _ = broker.Subscribe("user-1")

	st := &localFakeStorer{
		GetUserWatchAddressesFn: func(_ context.Context, _, _ string) ([]string, error) {
			return []string{testAddr1}, nil
		},
	}
	rpcClient := &fakeRPCClient{}

	tracker := newTestTracker(t, st, broker, rpcClient)
	tracker.HandleRawTxEvent(context.Background(), makeRawTxEvent(testTxID1, nil, []zmq.RawTxOutput{{ValueSat: 0, N: 0, Address: testAddr1}}))

	tracker.mu.Lock()
	_, exists := tracker.pendingMempool[testTxID1]
	tracker.mu.Unlock()
	assert.True(t, exists, "matched tx must be added to pendingMempool")
}

func TestMempoolTracker_HandleTxEvent_SpentOutpointRecorded(t *testing.T) {
	t.Parallel()
	broker := NewBroker(10, nil)
	_, _ = broker.Subscribe("user-1")

	prevTxid := testTxID2
	st := &localFakeStorer{
		GetUserWatchAddressesFn: func(_ context.Context, _, _ string) ([]string, error) {
			return []string{testAddr1}, nil
		},
	}
	rpcClient := &fakeRPCClient{}

	tracker := newTestTracker(t, st, broker, rpcClient)
	tracker.HandleRawTxEvent(context.Background(), makeRawTxEvent(testTxID1, []zmq.RawTxInput{{PrevTxIDHex: prevTxid, PrevVout: 0}}, []zmq.RawTxOutput{{ValueSat: 0, N: 0, Address: testAddr1}}))

	tracker.mu.Lock()
	owner, exists := tracker.spentOutpoints[spentOutpoint{txid: prevTxid, vout: 0}]
	tracker.mu.Unlock()
	assert.True(t, exists, "spent outpoint must be recorded")
	assert.Equal(t, testTxID1, owner)
}

func TestMempoolTracker_HandleTxEvent_PendingMempoolCapReached_DropsEntry(t *testing.T) {
	t.Parallel()
	broker := NewBroker(10, nil)
	_, _ = broker.Subscribe("user-1")

	st := &localFakeStorer{
		GetUserWatchAddressesFn: func(_ context.Context, _, _ string) ([]string, error) {
			return []string{testAddr1}, nil
		},
	}
	rpcClient := &fakeRPCClient{}

	cfg := testTrackerCfg()
	cfg.PendingMempoolMaxSize = 1

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	tracker := NewMempoolTracker(ctx, st, broker, rpcClient, nil, cfg)

	// First tx fills the cap.
	tracker.HandleRawTxEvent(context.Background(), makeRawTxEvent(testTxID1, nil, []zmq.RawTxOutput{{ValueSat: 0, N: 0, Address: testAddr1}}))
	// Second tx must be dropped (cap=1 already reached).
	tracker.HandleRawTxEvent(context.Background(), makeRawTxEvent(testTxID2, nil, []zmq.RawTxOutput{{ValueSat: 0, N: 0, Address: testAddr1}}))

	tracker.mu.Lock()
	size := len(tracker.pendingMempool)
	tracker.mu.Unlock()
	assert.Equal(t, 1, size, "pendingMempool must not exceed cap")
}

// ── RBF detection ─────────────────────────────────────────────────────────────

func TestMempoolTracker_HandleTxEvent_RBF_EmitsReplacedBeforePending(t *testing.T) {
	t.Parallel()
	broker := NewBroker(10, nil)
	ch, _ := broker.Subscribe("user-1")

	st := &localFakeStorer{
		GetUserWatchAddressesFn: func(_ context.Context, _, _ string) ([]string, error) {
			return []string{testAddr1}, nil
		},
	}

	rpcClient := &fakeRPCClient{}

	tracker := newTestTracker(t, st, broker, rpcClient)

	// Emit original tx so its outpoint is tracked.
	tracker.HandleRawTxEvent(context.Background(), makeRawTxEvent(testTxID2, []zmq.RawTxInput{{PrevTxIDHex: "a" + testTxID1[1:], PrevVout: 0}}, []zmq.RawTxOutput{{ValueSat: 0, N: 0, Address: testAddr1}}))

	// Drain the pending_mempool event for the original.
	events1 := drainEvents(ch, 1, time.Second)
	require.Len(t, events1, 1)
	assert.Equal(t, "pending_mempool", events1[0].Type)

	// Emit replacement tx — must fire mempool_replaced then pending_mempool.
	tracker.HandleRawTxEvent(context.Background(), makeRawTxEvent(testTxID3, []zmq.RawTxInput{{PrevTxIDHex: "a" + testTxID1[1:], PrevVout: 0}}, []zmq.RawTxOutput{{ValueSat: 0, N: 0, Address: testAddr1}}))
	events2 := drainEvents(ch, 2, time.Second)

	types := make([]string, len(events2))
	for i, e := range events2 {
		types[i] = e.Type
	}
	assert.Contains(t, types, "mempool_replaced", "replacement must fire mempool_replaced")
	assert.Contains(t, types, "pending_mempool", "replacement must also fire pending_mempool")
}

// ── HandleBlockEvent ──────────────────────────────────────────────────────────

func TestMempoolTracker_HandleBlockEvent_EmitsNewBlock(t *testing.T) {
	t.Parallel()
	broker := NewBroker(10, nil)
	ch, _ := broker.Subscribe("user-1")

	rpcClient := &fakeRPCClient{
		GetBlockHeaderFn: func(_ context.Context, _ string) (rpc.BlockHeader, error) {
			return rpc.BlockHeader{Height: 42, Hash: testBlockHash, Time: 1700000000}, nil
		},
		GetBlockFn: func(_ context.Context, _ string, _ int) (json.RawMessage, error) {
			return json.RawMessage(`{"tx":[]}`), nil
		},
	}

	tracker := newTestTracker(t, &localFakeStorer{}, broker, rpcClient)
	tracker.HandleBlockEvent(context.Background(), makeBlockEvent(testBlockHash))

	events := drainEvents(ch, 1, time.Second)
	require.Len(t, events, 1)
	assert.Equal(t, "new_block", events[0].Type)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(events[0].Payload, &payload))
	assert.Equal(t, float64(42), payload["height"])
}

// TestMempoolTracker_HandleBlockEvent_RPCHeaderError_EmitsDegradedNewBlock verifies
// the degraded fallback: when GetBlockHeader returns -5 on every attempt and
// BlockRPCTimeout expires, HandleBlockEvent must still emit a new_block event
// with height=0 so connected SSE clients are notified of the block.
//
// This test uses a short BlockRPCTimeout to bound the retry window to ~350 ms.
//
// Previously this test was named "RPCHeaderError_NoEvents" and asserted that ALL
// events were suppressed — which was the exact bug. That assertion let the bug
// ship silently because the test validated the broken behaviour.
func TestMempoolTracker_HandleBlockEvent_RPCHeaderError_EmitsDegradedNewBlock(t *testing.T) {
	t.Parallel()
	broker := NewBroker(10, nil)
	ch, _ := broker.Subscribe("user-1")

	rpcClient := &fakeRPCClient{
		GetBlockHeaderFn: func(_ context.Context, _ string) (rpc.BlockHeader, error) {
			return rpc.BlockHeader{}, rpcErrNotFound("block not found")
		},
	}

	cfg := testTrackerCfg()
	cfg.BlockRPCTimeout = 350 * time.Millisecond // short timeout: ~2 retries then context expires

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	tracker := NewMempoolTracker(ctx, &localFakeStorer{}, broker, rpcClient, nil, cfg)
	tracker.HandleBlockEvent(context.Background(), makeBlockEvent(testBlockHash))

	events := drainEvents(ch, 1, 2*time.Second)
	require.Len(t, events, 1, "degraded new_block must be emitted even when GetBlockHeader always fails")
	assert.Equal(t, "new_block", events[0].Type)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(events[0].Payload, &payload))
	assert.Equal(t, float64(0), payload["height"], "degraded event must have height=0")
	assert.Equal(t, testBlockHash, payload["hash"], "degraded event must carry the correct block hash")
	assert.NotZero(t, payload["time"], "degraded event must carry a non-zero timestamp")
}

// TestMempoolTracker_HandleBlockEvent_ZMQRPCTimingRace_SucceedsOnRetry verifies
// that the ZMQ/RPC race window is closed: when GetBlockHeader returns -5 on the
// first call (block not yet committed to the index) but succeeds on the second,
// HandleBlockEvent must emit a full new_block event with the correct height.
//
// This is the exact failure mode observed in production: Bitcoin Core fires the
// ZMQ hashblock notification from UpdateTip before the block index is updated.
func TestMempoolTracker_HandleBlockEvent_ZMQRPCTimingRace_SucceedsOnRetry(t *testing.T) {
	t.Parallel()
	broker := NewBroker(10, nil)
	ch, _ := broker.Subscribe("user-1")

	var callCount int32
	rpcClient := &fakeRPCClient{
		GetBlockHeaderFn: func(_ context.Context, _ string) (rpc.BlockHeader, error) {
			n := atomic.AddInt32(&callCount, 1)
			if n == 1 {
				// First call: ZMQ fired before block index was updated.
				return rpc.BlockHeader{}, rpcErrNotFound("block not found")
			}
			// Subsequent calls: block index is now ready.
			return rpc.BlockHeader{Height: 127672, Hash: testBlockHash, Time: 1700000000}, nil
		},
		GetBlockFn: func(_ context.Context, _ string, _ int) (json.RawMessage, error) {
			return json.RawMessage(`{"tx":[]}`), nil
		},
	}

	tracker := newTestTracker(t, &localFakeStorer{}, broker, rpcClient)
	tracker.HandleBlockEvent(context.Background(), makeBlockEvent(testBlockHash))

	events := drainEvents(ch, 1, 2*time.Second)
	require.Len(t, events, 1, "must emit new_block after retry succeeds")
	assert.Equal(t, "new_block", events[0].Type)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(events[0].Payload, &payload))
	assert.Equal(t, float64(127672), payload["height"], "full event must carry the height returned by RPC")
	assert.Equal(t, testBlockHash, payload["hash"])

	assert.GreaterOrEqual(t, atomic.LoadInt32(&callCount), int32(2),
		"GetBlockHeader must have been called at least twice (first fails, second succeeds)")
}

func TestMempoolTracker_HandleBlockEvent_UsesCanonicalHashEverywhere(t *testing.T) {
	t.Parallel()
	broker := NewBroker(10, nil)
	ch, _ := broker.Subscribe("user-1")

	const blockHash = "000000000b2ac1f75ad909ca14329139a7767e8bea15e65c908e8bad6249c945"
	event := zmq.BlockEvent{Hash: mustDecodeHex32(blockHash)}

	st := &localFakeStorer{
		GetUserWatchAddressesFn: func(_ context.Context, _, _ string) ([]string, error) {
			return []string{testAddr1}, nil
		},
	}
	rpcClient := &fakeRPCClient{
		GetBlockHeaderFn: func(_ context.Context, hash string) (rpc.BlockHeader, error) {
			require.Equal(t, blockHash, hash)
			return rpc.BlockHeader{Height: 127724, Hash: blockHash, Time: 1700000000}, nil
		},
		GetBlockFn: func(_ context.Context, hash string, _ int) (json.RawMessage, error) {
			require.Equal(t, blockHash, hash)
			return json.RawMessage(`{"tx":["` + testTxID1 + `"]}`), nil
		},
	}

	tracker := newTestTracker(t, st, broker, rpcClient)
	tracker.HandleRawTxEvent(context.Background(), makeRawTxEvent(testTxID1, nil, []zmq.RawTxOutput{{ValueSat: 0, N: 0, Address: testAddr1}}))
	_ = drainEvents(ch, 1, time.Second)

	tracker.HandleBlockEvent(context.Background(), event)

	events := drainEvents(ch, 2, 2*time.Second)
	require.Len(t, events, 2)

	var confirmed Event
	for _, evt := range events {
		if evt.Type == "confirmed_tx" {
			confirmed = evt
			break
		}
	}
	require.Equal(t, "confirmed_tx", confirmed.Type)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(confirmed.Payload, &payload))
	assert.Equal(t, blockHash, payload["block"])
}

// TestMempoolTracker_HandleBlockEvent_NonTransientRPCError_EmitsDegradedNewBlock
// verifies that non-404 RPC errors (e.g. pruned block, code -1) are NOT retried
// by the -5 retry loop and still produce a degraded new_block event immediately,
// with exactly one call to GetBlockHeader.
func TestMempoolTracker_HandleBlockEvent_NonTransientRPCError_EmitsDegradedNewBlock(t *testing.T) {
	t.Parallel()
	broker := NewBroker(10, nil)
	ch, _ := broker.Subscribe("user-1")

	var callCount int32
	rpcClient := &fakeRPCClient{
		GetBlockHeaderFn: func(_ context.Context, _ string) (rpc.BlockHeader, error) {
			atomic.AddInt32(&callCount, 1)
			// Code -1 (pruned block) — not a -5, so the retry loop must not retry.
			return rpc.BlockHeader{}, &rpc.RPCError{Code: -1, Message: "Block not available (pruned data)"}
		},
	}

	tracker := newTestTracker(t, &localFakeStorer{}, broker, rpcClient)
	tracker.HandleBlockEvent(context.Background(), makeBlockEvent(testBlockHash))

	events := drainEvents(ch, 1, time.Second)
	require.Len(t, events, 1, "degraded new_block must be emitted on non-transient RPC error")
	assert.Equal(t, "new_block", events[0].Type)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(events[0].Payload, &payload))
	assert.Equal(t, float64(0), payload["height"], "degraded event must have height=0")
	assert.Equal(t, testBlockHash, payload["hash"])

	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount),
		"non-transient RPC error must not trigger retries — exactly one call expected")
}

func TestMempoolTracker_HandleBlockEvent_ConfirmedTx_EmitsConfirmedTxAndRemovesFromPending(t *testing.T) {
	t.Parallel()
	broker := NewBroker(10, nil)
	ch, _ := broker.Subscribe("user-1")

	// Pre-seed pendingMempool with testTxID1 watching testAddr1.
	st := &localFakeStorer{
		GetUserWatchAddressesFn: func(_ context.Context, _, _ string) ([]string, error) {
			return []string{testAddr1}, nil
		},
	}
	rpcClient := &fakeRPCClient{
		GetBlockHeaderFn: func(_ context.Context, _ string) (rpc.BlockHeader, error) {
			return rpc.BlockHeader{Height: 100, Hash: testBlockHash}, nil
		},
		GetBlockFn: func(_ context.Context, _ string, _ int) (json.RawMessage, error) {
			// Block contains testTxID1.
			payload, _ := json.Marshal(map[string]any{"tx": []string{testTxID1}})
			return payload, nil
		},
	}

	tracker := newTestTracker(t, st, broker, rpcClient)

	// Add tx to pending via HandleRawTxEvent.
	tracker.HandleRawTxEvent(context.Background(), makeRawTxEvent(testTxID1, nil, []zmq.RawTxOutput{{ValueSat: 0, N: 0, Address: testAddr1}}))
	// Drain the pending_mempool event.
	_ = drainEvents(ch, 1, time.Second)

	// Now fire the block event.
	tracker.HandleBlockEvent(context.Background(), makeBlockEvent(testBlockHash))

	// Expect new_block + confirmed_tx.
	events := drainEvents(ch, 2, time.Second)
	types := make(map[string]bool)
	for _, e := range events {
		types[e.Type] = true
	}
	assert.True(t, types["new_block"], "must emit new_block")
	assert.True(t, types["confirmed_tx"], "must emit confirmed_tx for tracked pending tx")

	// pendingMempool must be empty after confirmation.
	tracker.mu.Lock()
	_, stillPending := tracker.pendingMempool[testTxID1]
	tracker.mu.Unlock()
	assert.False(t, stillPending, "tx must be removed from pendingMempool after confirmation")
}

func TestMempoolTracker_HandleBlockEvent_UnknownTx_NoConfirmedTxEvent(t *testing.T) {
	t.Parallel()
	broker := NewBroker(10, nil)
	ch, _ := broker.Subscribe("user-1")

	rpcClient := &fakeRPCClient{
		GetBlockHeaderFn: func(_ context.Context, _ string) (rpc.BlockHeader, error) {
			return rpc.BlockHeader{Height: 1}, nil
		},
		GetBlockFn: func(_ context.Context, _ string, _ int) (json.RawMessage, error) {
			// Block contains a tx we never saw in the mempool.
			payload, _ := json.Marshal(map[string]any{"tx": []string{testTxID3}})
			return payload, nil
		},
	}

	tracker := newTestTracker(t, &localFakeStorer{}, broker, rpcClient)
	tracker.HandleBlockEvent(context.Background(), makeBlockEvent(testBlockHash))

	events := drainEvents(ch, 2, 200*time.Millisecond)
	for _, e := range events {
		assert.NotEqual(t, "confirmed_tx", e.Type, "must not emit confirmed_tx for untracked tx")
	}
}

// ── pruneOldEntries ───────────────────────────────────────────────────────────

func TestMempoolTracker_PruneOldEntries_RemovesStaleEntries(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	tracker := NewMempoolTracker(ctx, &localFakeStorer{}, NewBroker(10, nil), &fakeRPCClient{}, nil, testTrackerCfg())

	// Manually insert a stale entry (added 2 days ago).
	tracker.mu.Lock()
	tracker.pendingMempool["stale-tx"] = &pendingEntry{
		addrs:   map[string]struct{}{"addr": {}},
		addedAt: time.Now().AddDate(0, 0, -2),
	}
	tracker.spentOutpoints[spentOutpoint{txid: "prev", vout: 0}] = "stale-tx"
	// Also populate the reverse index introduced by finding P3/D4 so that
	// pruneOldEntries can clean up spentOutpoints via the O(1) path.
	tracker.txidToOutpoints["stale-tx"] = []spentOutpoint{{txid: "prev", vout: 0}}
	// Insert a fresh entry (just now).
	tracker.pendingMempool["fresh-tx"] = &pendingEntry{
		addrs:   map[string]struct{}{"addr2": {}},
		addedAt: time.Now(),
	}
	tracker.mu.Unlock()

	tracker.pruneOldEntries()

	tracker.mu.Lock()
	_, staleExists := tracker.pendingMempool["stale-tx"]
	_, freshExists := tracker.pendingMempool["fresh-tx"]
	_, spentExists := tracker.spentOutpoints[spentOutpoint{txid: "prev", vout: 0}]
	tracker.mu.Unlock()

	assert.False(t, staleExists, "stale entry must be pruned")
	assert.True(t, freshExists, "fresh entry must be kept")
	assert.False(t, spentExists, "stale tx's spentOutpoints must be pruned")
}

func TestMempoolTracker_PruneOldEntries_EmptyMap_NoOp(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	tracker := NewMempoolTracker(ctx, &localFakeStorer{}, NewBroker(10, nil), &fakeRPCClient{}, nil, testTrackerCfg())
	// Must not panic on empty map.
	tracker.pruneOldEntries()
}

// TestMempoolTracker_PruneOldEntries_BoundaryConditions exercises the exact
// cutoff boundary of pruneOldEntries. The implementation uses addedAt.Before(cutoff)
// which is strict: an entry AT the cutoff instant is NOT pruned.
// Finding: T-42 / test coverage gap.
func TestMempoolTracker_PruneOldEntries_BoundaryConditions(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cfg := testTrackerCfg() // MempoolPendingMaxAgeDays = 1

	// pruneOldEntries uses addedAt.Before(cutoff) which is strict (<, not <=).
	// We test with generous margins rather than nanosecond boundaries because
	// pruneOldEntries recomputes time.Now() independently — a nanosecond margin
	// becomes inherently racy without clock injection.
	//
	//   "old":    clearly older than MaxAgeDays (cutoff - 1h) → must be pruned
	//   "recent": time.Now()                                   → must NOT be pruned

	tracker := NewMempoolTracker(ctx, &localFakeStorer{}, NewBroker(10, nil), &fakeRPCClient{}, nil, cfg)

	tracker.mu.Lock()
	tracker.pendingMempool["old"] = &pendingEntry{
		addrs:   map[string]struct{}{"a": {}},
		addedAt: time.Now().AddDate(0, 0, -cfg.MempoolPendingMaxAgeDays).Add(-time.Hour),
	}
	tracker.pendingMempool["recent"] = &pendingEntry{
		addrs:   map[string]struct{}{"c": {}},
		addedAt: time.Now(),
	}
	tracker.mu.Unlock()

	tracker.pruneOldEntries()

	tracker.mu.Lock()
	_, oldExists := tracker.pendingMempool["old"]
	_, recentExists := tracker.pendingMempool["recent"]
	tracker.mu.Unlock()

	assert.False(t, oldExists, "entry older than MaxAgeDays must be pruned")
	assert.True(t, recentExists, "entry added just now must NOT be pruned")
}

// TestMempoolTracker_ConcurrentTxAndBlockEvent_NoRace exercises concurrent
// HandleRawTxEvent and HandleBlockEvent invocations on the same txid.
// Run with -race to detect any mutex misuse.
// Finding: T-43 / concurrency test gap.
func TestMempoolTracker_ConcurrentTxAndBlockEvent_NoRace(t *testing.T) {
	t.Parallel()
	broker := NewBroker(200, nil)
	_, _ = broker.Subscribe("user-race")

	st := &localFakeStorer{
		GetUserWatchAddressesFn: func(_ context.Context, _, _ string) ([]string, error) {
			return []string{testAddr1}, nil
		},
	}

	blockJSON := []byte(`{"tx":["` + testTxID1 + `"]}`)

	rpcClient := &fakeRPCClient{
		GetBlockHeaderFn: func(_ context.Context, _ string) (rpc.BlockHeader, error) {
			return rpc.BlockHeader{Height: 100}, nil
		},
		GetBlockFn: func(_ context.Context, _ string, _ int) (json.RawMessage, error) {
			return blockJSON, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	tracker := NewMempoolTracker(ctx, st, broker, rpcClient, nil, testTrackerCfg())

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			tracker.HandleRawTxEvent(ctx, makeRawTxEvent(testTxID1, nil, []zmq.RawTxOutput{{ValueSat: 0, N: 0, Address: testAddr1}}))
		}()
		go func() {
			defer wg.Done()
			tracker.HandleBlockEvent(ctx, makeBlockEvent(testBlockHash))
		}()
	}
	wg.Wait()
}
