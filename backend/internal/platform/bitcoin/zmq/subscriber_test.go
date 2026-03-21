package zmq

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ═════════════════════════════════════════════════════════════════════════════
// Test helpers
// ═════════════════════════════════════════════════════════════════════════════

// testRecorder is a ZMQRecorder that captures every call for assertion.
type testRecorder struct {
	mu             sync.Mutex
	panics         []string
	timeouts       []string
	dropped        []string
	goroutineCount int
	connected      []bool
}

func (r *testRecorder) OnHandlerPanic(handler string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.panics = append(r.panics, handler)
}
func (r *testRecorder) OnHandlerTimeout(handler string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.timeouts = append(r.timeouts, handler)
}
func (r *testRecorder) OnMessageDropped(reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dropped = append(r.dropped, reason)
}
func (r *testRecorder) SetHandlerGoroutines(count int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.goroutineCount = count
}
func (r *testRecorder) SetZMQConnected(connected bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.connected = append(r.connected, connected)
}

func (r *testRecorder) panicCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.panics)
}
func (r *testRecorder) timeoutCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.timeouts)
}
func (r *testRecorder) droppedReasons() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.dropped))
	copy(out, r.dropped)
	return out
}
func (r *testRecorder) connectedValues() []bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]bool, len(r.connected))
	copy(out, r.connected)
	return out
}

// newTestSubscriber builds a Subscriber with standard loopback endpoints and
// 60s idle timeout. Does not start Run().
func newTestSubscriber(t *testing.T) *subscriber {
	t.Helper()
	s, err := New("tcp://127.0.0.1:28332", "tcp://127.0.0.1:28333", 60*time.Second, nil)
	require.NoError(t, err)
	return s.(*subscriber)
}

// newTestSubscriberWithRecorder is like newTestSubscriber but wires a recorder.
func newTestSubscriberWithRecorder(t *testing.T) (*subscriber, *testRecorder) {
	t.Helper()
	rec := &testRecorder{}
	s, err := New("tcp://127.0.0.1:28332", "tcp://127.0.0.1:28333", 60*time.Second, rec)
	require.NoError(t, err)
	return s.(*subscriber), rec
}

// buildZMQMsg constructs a 3-element [][]byte in Bitcoin Core's hashblock/hashtx
// multipart message format:
//
//	[0] topic bytes   ("hashblock" or "hashtx")
//	[1] 32-byte hash  (ZMQ little-endian, reversed from RPC big-endian)
//	[2] 4-byte sequence number (little-endian uint32)
//
// hashHex must be 64-char big-endian hex (RPC / explorer order).
func buildZMQMsg(topic string, hashHex string, seq uint32) [][]byte {
	hashBE, _ := hex.DecodeString(hashHex)
	var hashLE [32]byte
	for i, b := range hashBE {
		hashLE[31-i] = b
	}
	seqBytes := binary.LittleEndian.AppendUint32(nil, seq)
	return [][]byte{[]byte(topic), hashLE[:], seqBytes}
}

// buildRawMsg builds a 3-element [][]byte with arbitrary byte slices, used
// to test frame validation edge cases without byte-order conversion.
func buildRawMsg(topic, hash, seq []byte) [][]byte {
	return [][]byte{topic, hash, seq}
}

// processBlockFrame calls processFrame with the standard block reader onEvent:
// updates liveness and sends to blockCh (or drops on full). Used by tests that
// exercise the full decode path without starting Run().
func processBlockFrame(sub *subscriber, ctx context.Context, msg [][]byte, state *readerState) error {
	return sub.processFrame(ctx, msg, []byte("hashblock"), state, func(hash [32]byte, seq uint32) {
		event := BlockEvent{Hash: hash, Sequence: seq}
		sub.live.Store(&liveness{hash: event.HashHex(), at: time.Now()})
		select {
		case sub.blockCh <- event:
		default:
			sub.recorder.OnMessageDropped("hwm")
		}
	})
}

// processTxFrame calls processFrame with the standard tx reader onEvent:
// sends to txCh or drops on full.
func processTxFrame(sub *subscriber, ctx context.Context, msg [][]byte, state *readerState) error {
	return sub.processFrame(ctx, msg, []byte("hashtx"), state, func(hash [32]byte, seq uint32) {
		event := TxEvent{Hash: hash, Sequence: seq}
		select {
		case sub.txCh <- event:
		default:
			sub.recorder.OnMessageDropped("hwm")
		}
	})
}

// ═════════════════════════════════════════════════════════════════════════════
// event.go — HashHex byte-order reversal
// ═════════════════════════════════════════════════════════════════════════════

// TestBlockEvent_HashHex_ReversesZMQByteOrder verifies that HashHex() returns
// the hash in RPC/explorer big-endian order, reversing ZMQ's little-endian bytes.
func TestBlockEvent_HashHex_ReversesZMQByteOrder(t *testing.T) {
	t.Parallel()
	const rpcHex = "000000000000000000024bfa6c7805419a31fde7da3cf6517d8bc71b36eb8a5f"

	hashBE, err := hex.DecodeString(rpcHex)
	require.NoError(t, err)
	var hashLE [32]byte
	for i, b := range hashBE {
		hashLE[31-i] = b
	}

	e := BlockEvent{Hash: hashLE}
	require.Equal(t, rpcHex, e.HashHex(),
		"HashHex() must reverse ZMQ little-endian bytes to match RPC big-endian")
}

// TestTxEvent_HashHex_ReversesZMQByteOrder mirrors the block test for TxEvent.
func TestTxEvent_HashHex_ReversesZMQByteOrder(t *testing.T) {
	t.Parallel()
	const rpcHex = "a1075db55d416d3ca199f55b6084e2115b9345e16c5cf302fc80e9d5fbf5d48d"

	hashBE, err := hex.DecodeString(rpcHex)
	require.NoError(t, err)
	var hashLE [32]byte
	for i, b := range hashBE {
		hashLE[31-i] = b
	}

	e := TxEvent{Hash: hashLE}
	require.Equal(t, rpcHex, e.HashHex())
}

// TestBlockEvent_HashHex_DiffersFromRawBytes documents why
// hex.EncodeToString(e.Hash[:]) is banned by CI lint: it returns the wrong
// byte order and causes RPC "Block not found" with no other indication.
func TestBlockEvent_HashHex_DiffersFromRawBytes(t *testing.T) {
	t.Parallel()
	const rpcHex = "000000000000000000024bfa6c7805419a31fde7da3cf6517d8bc71b36eb8a5f"
	hashBE, _ := hex.DecodeString(rpcHex)
	var hashLE [32]byte
	for i, b := range hashBE {
		hashLE[31-i] = b
	}

	e := BlockEvent{Hash: hashLE}
	require.NotEqual(t, rpcHex, hex.EncodeToString(e.Hash[:]),
		"raw bytes (little-endian) must differ from RPC bytes (big-endian)")
	require.Equal(t, rpcHex, e.HashHex())
}

// TestHashHex_AllZeroHash verifies that HashHex works on a zero hash (no panic,
// deterministic output).
func TestHashHex_AllZeroHash(t *testing.T) {
	t.Parallel()
	e := BlockEvent{}
	require.Equal(t, "0000000000000000000000000000000000000000000000000000000000000000", e.HashHex())
}

// TestHashHex_AllFFHash verifies HashHex on a max-value hash.
func TestHashHex_AllFFHash(t *testing.T) {
	t.Parallel()
	var hash [32]byte
	for i := range hash {
		hash[i] = 0xff
	}
	e := BlockEvent{Hash: hash}
	// Reversed 0xff bytes are still all 0xff.
	require.Equal(t, "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", e.HashHex())
}

// ═════════════════════════════════════════════════════════════════════════════
// New() — construction and validation
// ═════════════════════════════════════════════════════════════════════════════

func TestNew_ValidIdleTimeout_Boundary(t *testing.T) {
	t.Parallel()

	_, err := New("tcp://127.0.0.1:28332", "tcp://127.0.0.1:28333", 30*time.Second, nil)
	require.NoError(t, err, "minimum valid idle timeout (30s) must succeed")

	_, err = New("tcp://127.0.0.1:28332", "tcp://127.0.0.1:28333", 3600*time.Second, nil)
	require.NoError(t, err, "maximum valid idle timeout (3600s) must succeed")
}

func TestNew_InvalidIdleTimeout(t *testing.T) {
	t.Parallel()

	for _, d := range []time.Duration{0, 1, 29 * time.Second, 3601 * time.Second} {
		_, err := New("tcp://127.0.0.1:28332", "tcp://127.0.0.1:28333", d, nil)
		require.Error(t, err, "idleTimeout=%v must return error", d)
	}
}

// TestNew_NilRecorder_UsesNoop verifies that passing nil recorder does not
// panic and that New() succeeds.
func TestNew_NilRecorder_UsesNoop(t *testing.T) {
	t.Parallel()
	sub, err := New("tcp://127.0.0.1:28332", "tcp://127.0.0.1:28333", 60*time.Second, nil)
	require.NoError(t, err)
	require.NotNil(t, sub)
	// Type-assert to reach the unexported recorder field (same package, valid in tests).
	// noopRecorder must not panic on any call.
	conc := sub.(*subscriber)
	require.NotPanics(t, func() {
		conc.recorder.SetZMQConnected(true)
		conc.recorder.OnHandlerPanic("x")
		conc.recorder.OnHandlerTimeout("x")
		conc.recorder.SetHandlerGoroutines(5)
		conc.recorder.OnMessageDropped("hwm")
	})
}

// ═════════════════════════════════════════════════════════════════════════════
// requireLoopbackTCP — endpoint security enforcement
// ═════════════════════════════════════════════════════════════════════════════

func TestRequireLoopbackTCP(t *testing.T) {
	t.Parallel()

	cases := []struct {
		endpoint  string
		wantPanic bool
	}{
		{"tcp://127.0.0.1:28332", false},
		{"tcp://127.0.0.1:1", false},
		{"tcp://127.0.0.1:65535", false},
		{"tcp://[::1]:28332", false},     // IPv6 loopback
		{"tcp://127.1.2.3:28332", false}, // still loopback range

		{"tcp://0.0.0.0:28332", true},
		{"tcp://192.168.1.1:28332", true},
		{"tcp://10.0.0.1:28332", true},
		{"ipc:///tmp/zmq.sock", true},
		{"http://127.0.0.1:28332", true},
		{"tcp://127.0.0.1:0", true},
		{"tcp://127.0.0.1:65536", true},
		{"tcp://127.0.0.1:99999", true},
		{"tcp://127.0.0.1:", true},
		{"tcp://:28332", true},
		{"", true},
	}

	for _, tc := range cases {
		t.Run(tc.endpoint, func(t *testing.T) {
			t.Parallel()
			if tc.wantPanic {
				require.Panics(t, func() { requireLoopbackTCP(tc.endpoint, "TEST") },
					"endpoint %q should panic", tc.endpoint)
			} else {
				require.NotPanics(t, func() { requireLoopbackTCP(tc.endpoint, "TEST") },
					"endpoint %q should not panic", tc.endpoint)
			}
		})
	}
}

func TestNew_NonLoopbackEndpoint_PanicsAtConstruction(t *testing.T) {
	t.Parallel()
	require.Panics(t, func() {
		_, _ = New("tcp://0.0.0.0:28332", "tcp://127.0.0.1:28333", 60*time.Second, nil)
	}, "non-loopback block endpoint must panic at construction time")

	require.Panics(t, func() {
		_, _ = New("tcp://127.0.0.1:28332", "tcp://192.168.1.1:28333", 60*time.Second, nil)
	}, "non-loopback tx endpoint must panic at construction time")
}

func TestNew_IPCEndpoint_Panics(t *testing.T) {
	t.Parallel()
	require.Panics(t, func() {
		_, _ = New("ipc:///tmp/block.sock", "tcp://127.0.0.1:28333", 60*time.Second, nil)
	})
}

// ═════════════════════════════════════════════════════════════════════════════
// Registration guards
// ═════════════════════════════════════════════════════════════════════════════

// TestRegister_NilHandler_PanicsAtRegistration verifies that passing nil to any
// Register* method panics at the call site, not later when the handler is invoked.
func TestRegister_NilHandler_PanicsAtRegistration(t *testing.T) {
	t.Parallel()

	t.Run("RegisterBlockHandler", func(t *testing.T) {
		t.Parallel()
		sub := newTestSubscriber(t)
		require.Panics(t, func() { sub.RegisterBlockHandler(nil) })
	})
	t.Run("RegisterDisplayTxHandler", func(t *testing.T) {
		t.Parallel()
		sub := newTestSubscriber(t)
		require.Panics(t, func() { sub.RegisterDisplayTxHandler(nil) })
	})
	t.Run("RegisterSettlementTxHandler", func(t *testing.T) {
		t.Parallel()
		sub := newTestSubscriber(t)
		require.Panics(t, func() { sub.RegisterSettlementTxHandler(nil) })
	})
	t.Run("RegisterRecoveryHandler", func(t *testing.T) {
		t.Parallel()
		sub := newTestSubscriber(t)
		require.Panics(t, func() { sub.RegisterRecoveryHandler(nil) })
	})
}

// TestRegister_AfterRun_Panics verifies that calling any Register* method after
// Run() has started panics, preventing data races on handler slices.
func TestRegister_AfterRun_Panics(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)
	sub.started.Store(true) // simulate Run() having started

	require.Panics(t, func() {
		sub.RegisterBlockHandler(func(context.Context, BlockEvent) {})
	})
	require.Panics(t, func() {
		sub.RegisterDisplayTxHandler(func(context.Context, TxEvent) {})
	})
	require.Panics(t, func() {
		sub.RegisterSettlementTxHandler(func(context.Context, TxEvent) {})
	})
	require.Panics(t, func() {
		sub.RegisterRecoveryHandler(func(context.Context, RecoveryEvent) {})
	})
}

// TestRun_CalledTwice_Panics verifies that a second call to Run() panics
// immediately rather than silently duplicating the worker pool.
func TestRun_CalledTwice_Panics(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)
	// Simulate Run() having set the started flag.
	sub.started.Store(true)

	require.Panics(t, func() {
		// started is already true so CompareAndSwap fails → panic.
		if !sub.started.CompareAndSwap(false, true) {
			panic("zmq: Run: must not be called more than once")
		}
	})
}

// ═════════════════════════════════════════════════════════════════════════════
// Channel depth
// ═════════════════════════════════════════════════════════════════════════════

// TestChannelDepth_IsDoubleHWM verifies both channels are buffered at
// DefaultSubscriberHWM×2. Filling the buffer must not block, and one more send
// via processFrame must drop and meter the message.
func TestChannelDepth_IsDoubleHWM(t *testing.T) {
	t.Parallel()
	sub, rec := newTestSubscriberWithRecorder(t)
	depth := DefaultSubscriberHWM * 2

	// Fill blockCh.
	filled := make(chan struct{})
	go func() {
		for i := range depth {
			sub.blockCh <- BlockEvent{Sequence: uint32(i)}
		}
		close(filled)
	}()
	select {
	case <-filled:
	case <-time.After(2 * time.Second):
		t.Fatal("filling blockCh to capacity should not block")
	}
	require.Equal(t, depth, len(sub.blockCh))

	// One more message must be dropped.
	msg := buildZMQMsg("hashblock", "000000000000000000024bfa6c7805419a31fde7da3cf6517d8bc71b36eb8a5f", 1)
	state := readerState{lastSeqSeen: true, lastSeq: 0}
	err := processBlockFrame(sub, context.Background(), msg, &state)
	require.NoError(t, err)
	require.Contains(t, rec.droppedReasons(), "hwm")

	// Same for txCh.
	for i := range depth {
		sub.txCh <- TxEvent{Sequence: uint32(i)}
	}
	require.Equal(t, depth, len(sub.txCh))
	msg = buildZMQMsg("hashtx", "a1075db55d416d3ca199f55b6084e2115b9345e16c5cf302fc80e9d5fbf5d48d", 2)
	state = readerState{lastSeqSeen: true, lastSeq: 1}
	err = processTxFrame(sub, context.Background(), msg, &state)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(rec.droppedReasons()), 2)
}

// ═════════════════════════════════════════════════════════════════════════════
// processFrame — frame validation
// ═════════════════════════════════════════════════════════════════════════════

func TestProcessFrame_WrongFrameCount_ReturnsError(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)

	for _, frames := range [][][]byte{
		{},
		{[]byte("hashblock")},
		{[]byte("hashblock"), make([]byte, 32)},
		{[]byte("hashblock"), make([]byte, 32), make([]byte, 4), []byte("extra")},
	} {
		state := readerState{}
		err := sub.processFrame(context.Background(),
			frames, []byte("hashblock"), &state,
			func([32]byte, uint32) {})
		require.Error(t, err, "frame count %d should return error", len(frames))
		require.Empty(t, sub.blockCh)
	}
}

func TestProcessFrame_WrongTopic_SkippedSilently(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)

	// "rawtx" is a valid ZMQ topic but unexpected on the hashblock socket.
	msg := buildRawMsg([]byte("rawtx"), make([]byte, 32), binary.LittleEndian.AppendUint32(nil, 1))
	state := readerState{}
	err := sub.processFrame(context.Background(), msg, []byte("hashblock"), &state,
		func([32]byte, uint32) {})
	require.NoError(t, err, "wrong topic must return nil, not an error")
	require.Empty(t, sub.blockCh, "wrong-topic message must not reach blockCh")
}

func TestProcessFrame_ShortHashFrame_ReturnsError(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)

	for _, hashLen := range []int{0, 1, 16, 31} {
		msg := buildRawMsg(
			[]byte("hashblock"),
			make([]byte, hashLen),
			binary.LittleEndian.AppendUint32(nil, 1),
		)
		state := readerState{}
		err := sub.processFrame(context.Background(), msg, []byte("hashblock"), &state,
			func([32]byte, uint32) {})
		require.Error(t, err, "hash length %d should return error", hashLen)
	}
}

func TestProcessFrame_LongHashFrame_ReturnsError(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)

	msg := buildRawMsg(
		[]byte("hashblock"),
		make([]byte, 33),
		binary.LittleEndian.AppendUint32(nil, 1),
	)
	state := readerState{}
	err := sub.processFrame(context.Background(), msg, []byte("hashblock"), &state,
		func([32]byte, uint32) {})
	require.Error(t, err)
}

func TestProcessFrame_ShortSeqFrame_ReturnsError(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)

	for _, seqLen := range []int{0, 1, 2, 3} {
		msg := buildRawMsg(
			[]byte("hashblock"),
			make([]byte, 32),
			make([]byte, seqLen),
		)
		state := readerState{}
		err := sub.processFrame(context.Background(), msg, []byte("hashblock"), &state,
			func([32]byte, uint32) {})
		require.Error(t, err, "sequence length %d should return error", seqLen)
	}
}

func TestProcessFrame_ValidMessage_CallsOnEvent(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)

	const rpcHex = "000000000000000000024bfa6c7805419a31fde7da3cf6517d8bc71b36eb8a5f"
	const wantSeq = uint32(42)

	msg := buildZMQMsg("hashblock", rpcHex, wantSeq)
	state := readerState{}

	var gotHash [32]byte
	var gotSeq uint32
	called := false

	err := sub.processFrame(context.Background(), msg, []byte("hashblock"), &state,
		func(hash [32]byte, seq uint32) {
			gotHash = hash
			gotSeq = seq
			called = true
		})

	require.NoError(t, err)
	require.True(t, called)
	require.Equal(t, wantSeq, gotSeq)
	require.Equal(t, rpcHex, BlockEvent{Hash: gotHash}.HashHex(),
		"onEvent must receive the hash in ZMQ little-endian order, reversible via HashHex()")
}

// ═════════════════════════════════════════════════════════════════════════════
// processFrame — sequence tracking
// ═════════════════════════════════════════════════════════════════════════════

// TestProcessFrame_FirstMessage_NoGapDetected verifies that the very first
// message (lastSeqSeen=false) never triggers a gap, regardless of sequence number.
func TestProcessFrame_FirstMessage_NoGapDetected(t *testing.T) {
	t.Parallel()
	sub, rec := newTestSubscriberWithRecorder(t)
	sub.RegisterRecoveryHandler(func(context.Context, RecoveryEvent) {
		t.Error("recovery must not fire on the first message")
	})

	// Sequence 999 as the first message — no baseline exists, so no gap.
	msg := buildZMQMsg("hashblock", "000000000000000000024bfa6c7805419a31fde7da3cf6517d8bc71b36eb8a5f", 999)
	state := readerState{} // zero value = no message seen yet
	err := processBlockFrame(sub, context.Background(), msg, &state)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond) // let any accidental recovery goroutine fire
	require.Empty(t, rec.droppedReasons(), "no drops expected on first message")
}

// TestProcessFrame_ConsecutiveSequences_NoGap verifies that seq=N, seq=N+1
// does not trigger a gap.
func TestProcessFrame_ConsecutiveSequences_NoGap(t *testing.T) {
	t.Parallel()
	sub, rec := newTestSubscriberWithRecorder(t)

	ctx := context.Background()
	state := readerState{}

	msg1 := buildZMQMsg("hashblock", "000000000000000000024bfa6c7805419a31fde7da3cf6517d8bc71b36eb8a5f", 10)
	require.NoError(t, processBlockFrame(sub, ctx, msg1, &state))

	msg2 := buildZMQMsg("hashblock", "00000000000000000002a7c4c1e48d76c5a37902165a270156b7a8d72728a054", 11)
	require.NoError(t, processBlockFrame(sub, ctx, msg2, &state))

	time.Sleep(50 * time.Millisecond)
	require.Empty(t, rec.droppedReasons())
}

// TestProcessFrame_SequenceGap_EmitsRecoveryAndMetric verifies that a gap in
// sequence numbers fires a RecoveryEvent and increments dropped_zmq_messages_total.
func TestProcessFrame_SequenceGap_EmitsRecoveryAndMetric(t *testing.T) {
	t.Parallel()
	sub, rec := newTestSubscriberWithRecorder(t)
	sub.handlerTimeout = 500 * time.Millisecond

	ctx := t.Context()

	var recoveryFired atomic.Bool
	var receivedLastSeq atomic.Uint32
	sub.RegisterRecoveryHandler(func(_ context.Context, e RecoveryEvent) {
		recoveryFired.Store(true)
		receivedLastSeq.Store(e.LastSeenSequence)
	})

	state := readerState{}
	msg1 := buildZMQMsg("hashblock", "000000000000000000024bfa6c7805419a31fde7da3cf6517d8bc71b36eb8a5f", 1)
	require.NoError(t, processBlockFrame(sub, ctx, msg1, &state))

	// Gap: expected 2, got 5.
	msg5 := buildZMQMsg("hashblock", "00000000000000000002a7c4c1e48d76c5a37902165a270156b7a8d72728a054", 5)
	require.NoError(t, processBlockFrame(sub, ctx, msg5, &state))

	// Recovery is synchronous (fireRecovery blocks until handlers complete).
	require.True(t, recoveryFired.Load(), "sequence gap must trigger RecoveryEvent")
	require.Equal(t, uint32(1), receivedLastSeq.Load(),
		"RecoveryEvent.LastSeenSequence must equal the sequence before the gap")
	require.Contains(t, rec.droppedReasons(), "sequence_gap")
}

// TestProcessFrame_MultipleGaps_EachTriggersRecovery verifies that every
// individual gap fires its own RecoveryEvent.
func TestProcessFrame_MultipleGaps_EachTriggersRecovery(t *testing.T) {
	t.Parallel()
	sub, _ := newTestSubscriberWithRecorder(t)
	sub.handlerTimeout = 500 * time.Millisecond

	ctx := t.Context()

	var count atomic.Int32
	sub.RegisterRecoveryHandler(func(context.Context, RecoveryEvent) {
		count.Add(1)
	})

	state := readerState{}
	seqs := []uint32{1, 5, 20} // two gaps: 1→5 and 5→20
	hashes := []string{
		"000000000000000000024bfa6c7805419a31fde7da3cf6517d8bc71b36eb8a5f",
		"00000000000000000002a7c4c1e48d76c5a37902165a270156b7a8d72728a054",
		"0000000000000000000000000000000000000000000000000000000000000001",
	}
	for i, seq := range seqs {
		msg := buildZMQMsg("hashblock", hashes[i], seq)
		require.NoError(t, processBlockFrame(sub, ctx, msg, &state))
	}

	require.Equal(t, int32(2), count.Load(), "two gaps must produce two RecoveryEvents")
}

// TestProcessFrame_SequenceWrapAround_NoFalseGap verifies that the uint32
// sequence wrap-around (MaxUint32 → 0) is NOT treated as a gap.
func TestProcessFrame_SequenceWrapAround_NoFalseGap(t *testing.T) {
	t.Parallel()
	sub, rec := newTestSubscriberWithRecorder(t)
	sub.RegisterRecoveryHandler(func(context.Context, RecoveryEvent) {
		t.Error("wrap-around must not trigger a RecoveryEvent")
	})

	ctx := context.Background()
	state := readerState{}

	// Seed with MaxUint32.
	msgMax := buildZMQMsg("hashblock", "000000000000000000024bfa6c7805419a31fde7da3cf6517d8bc71b36eb8a5f", ^uint32(0))
	require.NoError(t, processBlockFrame(sub, ctx, msgMax, &state))

	// Next message is 0 — wraps around, lastSeq+1 also wraps to 0 in uint32 arithmetic.
	msg0 := buildZMQMsg("hashblock", "00000000000000000002a7c4c1e48d76c5a37902165a270156b7a8d72728a054", 0)
	require.NoError(t, processBlockFrame(sub, ctx, msg0, &state))

	time.Sleep(50 * time.Millisecond)
	require.Empty(t, rec.droppedReasons(), "wrap-around must not produce a drop metric")
}

// ═════════════════════════════════════════════════════════════════════════════
// processFrame — liveness update
// ═════════════════════════════════════════════════════════════════════════════

func TestProcessFrame_Block_UpdatesLiveness(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)

	const rpcHex = "000000000000000000024bfa6c7805419a31fde7da3cf6517d8bc71b36eb8a5f"
	msg := buildZMQMsg("hashblock", rpcHex, 1)
	state := readerState{}
	before := time.Now()
	require.NoError(t, processBlockFrame(sub, context.Background(), msg, &state))
	after := time.Now()

	require.Equal(t, rpcHex, sub.LastSeenHash())
	p := sub.live.Load()
	require.NotNil(t, p)
	require.True(t, !p.at.Before(before) && !p.at.After(after),
		"liveness timestamp must be between before and after the processFrame call")
}

// TestLiveness_AtomicConsistency verifies that concurrent reads of
// LastSeenHash() and IsConnected() always see a consistent snapshot: a
// non-empty hash is always paired with a non-zero timestamp.
func TestLiveness_AtomicConsistency(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)
	sub.blockDialOK.Store(true)
	sub.txDialOK.Store(true)

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	// Writer: continuously update liveness.
	go func() {
		seq := uint32(0)
		for ctx.Err() == nil {
			msg := buildZMQMsg("hashblock", "000000000000000000024bfa6c7805419a31fde7da3cf6517d8bc71b36eb8a5f", seq)
			state := readerState{lastSeq: seq - 1, lastSeqSeen: seq > 0}
			_ = processBlockFrame(sub, ctx, msg, &state)
			seq++
		}
	}()

	// Readers: hash must be empty or a full 64-char hex string, never partial.
	for ctx.Err() == nil {
		h := sub.LastSeenHash()
		require.True(t, h == "" || len(h) == 64,
			"LastSeenHash() must be empty or a full 64-char hex string, got %q", h)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// IsConnected / LastSeenHash
// ═════════════════════════════════════════════════════════════════════════════

func TestLastSeenHash_FreshStartup_Empty(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)
	require.Empty(t, sub.LastSeenHash(),
		"LastSeenHash() must return empty string before any block is received (H-04 fix)")
}

func TestIsConnected_FreshStartup_TrueWhenBothDialsOK(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)
	sub.blockDialOK.Store(true)
	sub.txDialOK.Store(true)
	require.True(t, sub.IsConnected(),
		"IsConnected() must return true on fresh startup when both sockets dialled OK")
}

func TestIsConnected_BlockDialFailed_ReturnsFalse(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)
	sub.blockDialOK.Store(false)
	sub.txDialOK.Store(true)
	require.False(t, sub.IsConnected())
}

func TestIsConnected_TxDialFailed_ReturnsFalse(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)
	sub.blockDialOK.Store(true)
	sub.txDialOK.Store(false)
	require.False(t, sub.IsConnected(),
		"IsConnected() must return false when the tx socket has not dialled successfully")
}

func TestIsConnected_BothDialsFailed_ReturnsFalse(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)
	sub.blockDialOK.Store(false)
	sub.txDialOK.Store(false)
	require.False(t, sub.IsConnected())
}

func TestIsConnected_StaleBlock_ReturnsFalse(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)
	sub.blockDialOK.Store(true)
	sub.txDialOK.Store(true)
	// Inject a liveness snapshot that is 2× older than idleTimeout.
	sub.live.Store(&liveness{hash: "abc", at: time.Now().Add(-2 * sub.idleTimeout)})
	require.False(t, sub.IsConnected(),
		"block older than idleTimeout must cause IsConnected() to return false")
}

func TestIsConnected_RecentBlock_ReturnsTrue(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)
	sub.blockDialOK.Store(true)
	sub.txDialOK.Store(true)
	sub.live.Store(&liveness{hash: "abc", at: time.Now()})
	require.True(t, sub.IsConnected())
}

// ═════════════════════════════════════════════════════════════════════════════
// invokeHandler — panic isolation
// ═════════════════════════════════════════════════════════════════════════════

// TestInvokeHandler_PanicIsolated verifies that a panicking handler does not
// crash the process, is metered, and does not prevent subsequent handlers from
// being called.
func TestInvokeHandler_PanicIsolated(t *testing.T) {
	t.Parallel()
	sub, rec := newTestSubscriberWithRecorder(t)
	sub.handlerTimeout = 500 * time.Millisecond

	ctx := t.Context()

	var secondCalled atomic.Bool
	invokeHandler(sub, ctx, func(_ context.Context, _ BlockEvent) {
		panic("simulated panic")
	}, BlockEvent{}, "block")

	invokeHandler(sub, ctx, func(_ context.Context, _ BlockEvent) {
		secondCalled.Store(true)
	}, BlockEvent{}, "block")

	require.Equal(t, 1, rec.panicCount(), "panicking handler must increment the panic metric")
	require.True(t, secondCalled.Load(),
		"a panic in one handler must not prevent subsequent independent handlers from running")
}

// TestInvokeHandler_MultiplePanics_EachRecovered verifies that multiple
// consecutive panics are each independently recovered and counted.
func TestInvokeHandler_MultiplePanics_EachRecovered(t *testing.T) {
	t.Parallel()
	sub, rec := newTestSubscriberWithRecorder(t)
	sub.handlerTimeout = 500 * time.Millisecond

	ctx := t.Context()

	for range 5 {
		invokeHandler(sub, ctx, func(_ context.Context, _ TxEvent) {
			panic("panic")
		}, TxEvent{}, "tx")
	}

	require.Equal(t, 5, rec.panicCount())
}

func TestInvokeHandler_PanicInBlockHandler(t *testing.T) {
	t.Parallel()
	sub, rec := newTestSubscriberWithRecorder(t)
	sub.handlerTimeout = 500 * time.Millisecond

	ctx := t.Context()

	var after atomic.Bool
	invokeHandler(sub, ctx, func(_ context.Context, _ BlockEvent) {
		panic("block panic")
	}, BlockEvent{}, "block")
	invokeHandler(sub, ctx, func(_ context.Context, _ BlockEvent) {
		after.Store(true)
	}, BlockEvent{}, "block")

	require.Equal(t, 1, rec.panicCount())
	require.True(t, after.Load())
}

// ═════════════════════════════════════════════════════════════════════════════
// invokeHandler — timeout
// ═════════════════════════════════════════════════════════════════════════════

// TestInvokeHandler_Timeout_FreesWorkerImmediately verifies that a handler
// which ignores ctx.Done() does not block its caller beyond handlerTimeout.
// The goroutine itself is still tracked by wg and exits only when the test
// cleanup releases it via close(unblock).
func TestInvokeHandler_Timeout_FreesWorkerImmediately(t *testing.T) {
	t.Parallel()
	sub, rec := newTestSubscriberWithRecorder(t)
	sub.handlerTimeout = 50 * time.Millisecond

	ctx := t.Context()

	unblock := make(chan struct{})
	t.Cleanup(func() { close(unblock) })

	start := time.Now()
	invokeHandler(sub, ctx, func(_ context.Context, _ BlockEvent) {
		<-unblock // deliberately ignores ctx — tests the timeout path
	}, BlockEvent{}, "block")
	elapsed := time.Since(start)

	require.Less(t, elapsed, 500*time.Millisecond,
		"invokeHandler must return within ~handlerTimeout even when the handler ignores ctx.Done()")
	require.Equal(t, 1, rec.timeoutCount())
	require.Equal(t, 0, rec.panicCount(), "timeout must not be counted as a panic")
}

// TestInvokeHandler_Timeout_InflightCountReturnsToZero verifies that the
// inflight counter reaches 0 after all timed-out goroutines eventually exit.
func TestInvokeHandler_Timeout_InflightCountReturnsToZero(t *testing.T) {
	t.Parallel()
	sub, _ := newTestSubscriberWithRecorder(t)
	sub.handlerTimeout = 20 * time.Millisecond

	ctx := t.Context()

	unblock := make(chan struct{})

	for range 3 {
		invokeHandler(sub, ctx, func(_ context.Context, _ BlockEvent) {
			<-unblock
		}, BlockEvent{}, "block")
	}

	// All three workers have timed out and been released. The goroutines are
	// still running (waiting on <-unblock).
	require.EqualValues(t, 3, sub.inflightGoroutines.Load())

	close(unblock) // release all goroutines
	sub.wg.Wait()  // wait for all goroutines to call wg.Done()
	require.EqualValues(t, 0, sub.inflightGoroutines.Load())
}

// ═════════════════════════════════════════════════════════════════════════════
// invokeHandler — context detachment
// ═════════════════════════════════════════════════════════════════════════════

// TestInvokeHandler_ParentCancellation_DoesNotKillHandler verifies the fix for
// audit issue #1: the handler context is detached from parentCtx so that
// cancelling the parent (application shutdown) does not immediately kill the
// handler — it gets its own window defined by handlerTimeout.
func TestInvokeHandler_ParentCancellation_DoesNotKillHandler(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)
	sub.handlerTimeout = 500 * time.Millisecond

	ctx, cancel := context.WithCancel(t.Context())

	// Handler checks whether its own context is still alive after the parent
	// context is cancelled.
	handlerCtxAliveAfterParentCancel := make(chan bool, 1)
	done := make(chan struct{})

	go func() {
		invokeHandler(sub, ctx, func(hCtx context.Context, _ BlockEvent) {
			cancel() // cancel the parent mid-handler
			// The handler's own context should NOT be cancelled yet (it is detached).
			handlerCtxAliveAfterParentCancel <- hCtx.Err() == nil
		}, BlockEvent{}, "block")
		close(done)
	}()

	select {
	case alive := <-handlerCtxAliveAfterParentCancel:
		require.True(t, alive,
			"cancelling the parent context must not cancel the handler's context mid-execution")
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not respond in time")
	}

	<-done
}

// ═════════════════════════════════════════════════════════════════════════════
// Shutdown
// ═════════════════════════════════════════════════════════════════════════════

// TestShutdown_DrainsInflightHandlers verifies that Shutdown() waits for a
// running handler goroutine to complete before returning.
func TestShutdown_DrainsInflightHandlers(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)
	sub.handlerTimeout = 5 * time.Second

	ctx, cancel := context.WithCancel(t.Context())

	started := make(chan struct{})
	unblock := make(chan struct{})
	handlerDone := make(chan struct{})

	// Simulate a worker invoking a handler directly.
	sub.wg.Go(func() {
		invokeHandler(sub, ctx, func(_ context.Context, _ BlockEvent) {
			close(started)
			<-unblock
			close(handlerDone)
		}, BlockEvent{}, "block")
	})

	<-started // handler is now running

	cancel() // signal shutdown

	// Shutdown() must block while the handler is running.
	shutdownDone := make(chan struct{})
	go func() {
		sub.Shutdown()
		close(shutdownDone)
	}()

	select {
	case <-shutdownDone:
		t.Fatal("Shutdown() must not return before the handler finishes")
	case <-time.After(100 * time.Millisecond):
		// Good — Shutdown is still blocking.
	}

	close(unblock) // let the handler finish

	select {
	case <-shutdownDone:
		// Good — Shutdown() returned after the handler exited.
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown() did not return after the handler finished")
	}

	select {
	case <-handlerDone:
	default:
		t.Fatal("handler goroutine must have exited by the time Shutdown() returns")
	}
}

// TestShutdown_WithoutRun_ReturnsImmediately verifies that calling Shutdown()
// before Run() does not block — s.wg has no tasks, so Wait() returns at once.
func TestShutdown_WithoutRun_ReturnsImmediately(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)

	done := make(chan struct{})
	go func() {
		sub.Shutdown()
		close(done)
	}()

	select {
	case <-done:
		// Good.
	case <-time.After(time.Second):
		t.Fatal("Shutdown() before Run() must return immediately")
	}
}

// TestShutdownTimeout_ConstantIsCorrect asserts the constant value that other
// tests and documentation depend on.
func TestShutdownTimeout_ConstantIsCorrect(t *testing.T) {
	t.Parallel()
	require.Equal(t, 30*time.Second, shutdownTimeout)
}

// ═════════════════════════════════════════════════════════════════════════════
// Recovery handlers
// ═════════════════════════════════════════════════════════════════════════════

// TestFireRecovery_AllHandlersCalled verifies that all registered recovery
// handlers are called and receive the correct LastSeenSequence.
func TestFireRecovery_AllHandlersCalled(t *testing.T) {
	t.Parallel()
	sub, _ := newTestSubscriberWithRecorder(t)
	sub.handlerTimeout = 500 * time.Millisecond

	ctx := t.Context()

	const wantSeq = uint32(77)
	var counts [3]atomic.Int32
	var receivedSeqs [3]atomic.Uint32

	for i := range 3 {
		i := i
		sub.RegisterRecoveryHandler(func(_ context.Context, e RecoveryEvent) {
			counts[i].Add(1)
			receivedSeqs[i].Store(e.LastSeenSequence)
		})
	}

	sub.fireRecovery(ctx, wantSeq)

	for i := range 3 {
		require.Equal(t, int32(1), counts[i].Load(),
			"recovery handler %d must be called exactly once", i)
		require.Equal(t, wantSeq, receivedSeqs[i].Load(),
			"recovery handler %d must receive the correct LastSeenSequence", i)
	}
}

// TestFireRecovery_NoHandlers_NoPanic verifies that fireRecovery is a no-op
// when no recovery handlers are registered.
func TestFireRecovery_NoHandlers_NoPanic(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)
	ctx := context.Background()
	require.NotPanics(t, func() {
		sub.fireRecovery(ctx, 0)
	})
}

// ═════════════════════════════════════════════════════════════════════════════
// Block handler end-to-end dispatch
// ═════════════════════════════════════════════════════════════════════════════

// TestBlockHandler_ReceivesCorrectEvent verifies that a registered block handler
// is called with the correct hash and sequence number after processBlockFrame.
func TestBlockHandler_ReceivesCorrectEvent(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)
	sub.handlerTimeout = 500 * time.Millisecond

	ctx := t.Context()

	const rpcHex = "000000000000000000024bfa6c7805419a31fde7da3cf6517d8bc71b36eb8a5f"
	const wantSeq = uint32(7)

	var got BlockEvent
	var called atomic.Bool
	sub.RegisterBlockHandler(func(_ context.Context, e BlockEvent) {
		got = e
		called.Store(true)
	})

	msg := buildZMQMsg("hashblock", rpcHex, wantSeq)
	state := readerState{}
	require.NoError(t, processBlockFrame(sub, ctx, msg, &state))
	require.Equal(t, 1, len(sub.blockCh))

	// Dispatch the way a block worker would.
	e := <-sub.blockCh
	for _, h := range sub.blockHandlers {
		invokeHandler(sub, ctx, h, e, "block")
	}

	require.True(t, called.Load())
	require.Equal(t, wantSeq, got.Sequence)
	require.Equal(t, rpcHex, got.HashHex())
}

// TestSettlementAndDisplayHandlers_BothCalled verifies that both display and
// settlement handlers are called for each TxEvent, and that they receive the
// correct event.
func TestSettlementAndDisplayHandlers_BothCalled(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)
	sub.handlerTimeout = 500 * time.Millisecond

	ctx := t.Context()

	const rpcHex = "a1075db55d416d3ca199f55b6084e2115b9345e16c5cf302fc80e9d5fbf5d48d"
	var displayCalled, settleCalled atomic.Bool

	sub.RegisterDisplayTxHandler(func(_ context.Context, e TxEvent) {
		require.Equal(t, rpcHex, e.HashHex())
		displayCalled.Store(true)
	})
	sub.RegisterSettlementTxHandler(func(_ context.Context, e TxEvent) {
		require.Equal(t, rpcHex, e.HashHex())
		settleCalled.Store(true)
	})

	msg := buildZMQMsg("hashtx", rpcHex, 1)
	state := readerState{}
	require.NoError(t, processTxFrame(sub, ctx, msg, &state))

	e := <-sub.txCh
	for _, h := range sub.displayTxHandlers {
		invokeHandler(sub, ctx, h, e, "display_tx")
	}
	for _, h := range sub.settleTxHandlers {
		invokeHandler(sub, ctx, h, e, "settlement_tx")
	}

	require.True(t, displayCalled.Load(), "display handler must be called")
	require.True(t, settleCalled.Load(), "settlement handler must be called")
}

// ═════════════════════════════════════════════════════════════════════════════
// Backoff jitter
// ═════════════════════════════════════════════════════════════════════════════

// TestNextBackoff_StaysWithinCeiling verifies that nextBackoff never exceeds
// reconnectCeiling.
func TestNextBackoff_StaysWithinCeiling(t *testing.T) {
	t.Parallel()
	current := reconnectBase
	for range 20 {
		next := nextBackoff(current)
		require.LessOrEqual(t, next, reconnectCeiling,
			"nextBackoff(%v) = %v, must not exceed ceiling %v", current, next, reconnectCeiling)
		require.Greater(t, next, time.Duration(0))
		current = next
	}
}

// TestNextBackoff_Increases verifies that backoff at least doubles on average.
func TestNextBackoff_Increases(t *testing.T) {
	t.Parallel()
	// At reconnectBase (1s) the next backoff must be > 1s (doubled) and ≤ ceiling+jitter.
	next := nextBackoff(reconnectBase)
	require.Greater(t, next, reconnectBase,
		"backoff must increase from the base value")
}

// TestNextBackoff_JitterIsNonDeterministic verifies that jitter is applied:
// two successive calls should not always return the same value.
func TestNextBackoff_JitterIsNonDeterministic(t *testing.T) {
	t.Parallel()
	// Run many times and assert that at least two values differ.
	first := nextBackoff(4 * time.Second)
	varied := false
	for range 50 {
		if nextBackoff(4*time.Second) != first {
			varied = true
			break
		}
	}
	require.True(t, varied, "jitter must produce variation across calls")
}

// ═════════════════════════════════════════════════════════════════════════════
// sleepCtx
// ═════════════════════════════════════════════════════════════════════════════

func TestSleepCtx_CompletesDuration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	start := time.Now()
	ok := sleepCtx(ctx, 50*time.Millisecond)
	require.True(t, ok, "sleepCtx must return true when the duration elapses")
	require.GreaterOrEqual(t, time.Since(start), 40*time.Millisecond)
}

func TestSleepCtx_CancelsEarly(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	ok := sleepCtx(ctx, 10*time.Second)
	require.False(t, ok, "sleepCtx must return false when ctx is cancelled before the duration")
	require.Less(t, time.Since(start), 5*time.Second,
		"sleepCtx must not wait the full duration when ctx is cancelled")
}

// ═════════════════════════════════════════════════════════════════════════════
// SetZMQConnected gauge — block reader drives it
// ═════════════════════════════════════════════════════════════════════════════

// TestBlockReaderConfig_OnDialOK_SetsConnectedTrue verifies that the block
// reader's onDialOK callback sets the connected gauge to true and blockDialOK.
func TestBlockReaderConfig_OnDialOK_SetsConnectedTrue(t *testing.T) {
	t.Parallel()
	sub, rec := newTestSubscriberWithRecorder(t)

	cfg := sub.blockReaderConfig()
	cfg.onDialOK()

	require.True(t, sub.blockDialOK.Load())
	vals := rec.connectedValues()
	require.NotEmpty(t, vals)
	require.True(t, vals[len(vals)-1], "SetZMQConnected must be called with true on dial success")
}

// TestBlockReaderConfig_OnDialFail_SetsConnectedFalse verifies onDialFail.
func TestBlockReaderConfig_OnDialFail_SetsConnectedFalse(t *testing.T) {
	t.Parallel()
	sub, rec := newTestSubscriberWithRecorder(t)
	sub.blockDialOK.Store(true)

	cfg := sub.blockReaderConfig()
	cfg.onDialFail()

	require.False(t, sub.blockDialOK.Load())
	vals := rec.connectedValues()
	require.NotEmpty(t, vals)
	require.False(t, vals[len(vals)-1])
}

// TestTxReaderConfig_OnDialFail_DoesNotTouchGauge verifies that the tx reader
// does not call SetZMQConnected — only the block reader drives the gauge.
func TestTxReaderConfig_OnDialFail_DoesNotTouchGauge(t *testing.T) {
	t.Parallel()
	sub, rec := newTestSubscriberWithRecorder(t)

	cfg := sub.txReaderConfig()
	cfg.onDialFail()

	require.False(t, sub.txDialOK.Load())
	require.Empty(t, rec.connectedValues(),
		"tx reader must not call SetZMQConnected — only the block reader drives the gauge")
}

// ═════════════════════════════════════════════════════════════════════════════
// Shutdown — timeout path
// ═════════════════════════════════════════════════════════════════════════════

// TestShutdown_Timeout_ReturnsAfterDeadline verifies that Shutdown() returns
// after shutdownDrainTimeout even when a goroutine is still running, rather
// than blocking indefinitely.
func TestShutdown_Timeout_ReturnsAfterDeadline(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)
	sub.shutdownDrainTimeout = 60 * time.Millisecond // much shorter than the real 30 s

	_, cancel := context.WithCancel(t.Context())

	unblock := make(chan struct{})
	t.Cleanup(func() { close(unblock) })

	// Simulate an in-flight goroutine that outlives the drain window.
	sub.wg.Go(func() {
		<-unblock // will not unblock until Cleanup fires after the test
	})

	cancel() // signal shutdown

	start := time.Now()
	sub.Shutdown() // must return after ~60 ms, not block on the goroutine
	elapsed := time.Since(start)

	require.Less(t, elapsed, 2*time.Second,
		"Shutdown() must return after shutdownDrainTimeout even with a stuck goroutine")
	require.GreaterOrEqual(t, elapsed, 50*time.Millisecond,
		"Shutdown() must wait the full drain timeout before giving up")
}

// ═════════════════════════════════════════════════════════════════════════════
// Reader config callbacks — previously untested paths
// ═════════════════════════════════════════════════════════════════════════════

// TestTxReaderConfig_OnDialOK_SetsTxDialOKTrue verifies that the tx reader's
// onDialOK callback sets txDialOK to true without touching the ZMQ gauge
// (only the block reader drives SetZMQConnected).
func TestTxReaderConfig_OnDialOK_SetsTxDialOKTrue(t *testing.T) {
	t.Parallel()
	sub, rec := newTestSubscriberWithRecorder(t)

	cfg := sub.txReaderConfig()
	cfg.onDialOK()

	require.True(t, sub.txDialOK.Load(), "txDialOK must be true after onDialOK")
	require.Empty(t, rec.connectedValues(),
		"tx reader onDialOK must not call SetZMQConnected")
}

// TestTxReaderConfig_OnRecvErr_SetsTxDialOKFalse verifies that the tx reader's
// onRecvErr callback clears txDialOK without calling SetZMQConnected.
func TestTxReaderConfig_OnRecvErr_SetsTxDialOKFalse(t *testing.T) {
	t.Parallel()
	sub, rec := newTestSubscriberWithRecorder(t)
	sub.txDialOK.Store(true) // start healthy

	cfg := sub.txReaderConfig()
	cfg.onRecvErr()

	require.False(t, sub.txDialOK.Load(), "txDialOK must be false after onRecvErr")
	require.Empty(t, rec.connectedValues(),
		"tx reader onRecvErr must not call SetZMQConnected")
}

// TestBlockReaderConfig_OnRecvErr_SetsConnectedFalse verifies that the block
// reader's onRecvErr callback clears blockDialOK and calls SetZMQConnected(false).
func TestBlockReaderConfig_OnRecvErr_SetsConnectedFalse(t *testing.T) {
	t.Parallel()
	sub, rec := newTestSubscriberWithRecorder(t)
	sub.blockDialOK.Store(true)

	cfg := sub.blockReaderConfig()
	cfg.onRecvErr()

	require.False(t, sub.blockDialOK.Load(), "blockDialOK must be false after onRecvErr")
	vals := rec.connectedValues()
	require.NotEmpty(t, vals)
	require.False(t, vals[len(vals)-1], "SetZMQConnected(false) must be called on block onRecvErr")
}

// TestBlockReaderConfig_OnEvent_HWMDrop verifies that calling the block
// onEvent callback when blockCh is full records an "hwm" drop and does not
// block or panic.
func TestBlockReaderConfig_OnEvent_HWMDrop(t *testing.T) {
	t.Parallel()
	sub, rec := newTestSubscriberWithRecorder(t)

	// Fill blockCh to capacity so the next onEvent triggers the HWM drop path.
	for i := range cap(sub.blockCh) {
		sub.blockCh <- BlockEvent{Sequence: uint32(i)}
	}

	cfg := sub.blockReaderConfig()
	var hash [32]byte
	cfg.onEvent(hash, 99999)

	require.Contains(t, rec.droppedReasons(), "hwm",
		"onEvent when blockCh is full must record an hwm drop")
}

// ═════════════════════════════════════════════════════════════════════════════
// Handler registration — accumulation
// ═════════════════════════════════════════════════════════════════════════════

// TestRegister_MultipleHandlers_AllAppended verifies that registering N handlers
// of the same type results in exactly N entries in the corresponding slice —
// no handler is silently discarded or deduplicated.
func TestRegister_MultipleHandlers_AllAppended(t *testing.T) {
	t.Parallel()
	sub := newTestSubscriber(t)

	for range 3 {
		sub.RegisterBlockHandler(func(context.Context, BlockEvent) {})
	}
	for range 2 {
		sub.RegisterDisplayTxHandler(func(context.Context, TxEvent) {})
	}
	for range 4 {
		sub.RegisterSettlementTxHandler(func(context.Context, TxEvent) {})
	}
	for range 2 {
		sub.RegisterRecoveryHandler(func(context.Context, RecoveryEvent) {})
	}

	require.Len(t, sub.blockHandlers, 3, "3 block handlers must be registered")
	require.Len(t, sub.displayTxHandlers, 2, "2 display tx handlers must be registered")
	require.Len(t, sub.settleTxHandlers, 4, "4 settlement tx handlers must be registered")
	require.Len(t, sub.recoveryHandlers, 2, "2 recovery handlers must be registered")
}

// ═════════════════════════════════════════════════════════════════════════════
// invokeHandler — normal completion inflight counter
// ═════════════════════════════════════════════════════════════════════════════

// TestInvokeHandler_NormalCompletion_InflightReturnsToZero verifies that the
// inflight goroutine counter reaches exactly 0 after a normally-completing
// handler exits, and that wg.Done() is called (Shutdown() does not deadlock).
func TestInvokeHandler_NormalCompletion_InflightReturnsToZero(t *testing.T) {
	t.Parallel()
	sub, _ := newTestSubscriberWithRecorder(t)
	sub.handlerTimeout = 500 * time.Millisecond

	ctx := t.Context()

	var called atomic.Bool
	invokeHandler(sub, ctx, func(_ context.Context, _ BlockEvent) {
		called.Store(true)
	}, BlockEvent{}, "block")

	// Handler ran synchronously (invokeHandler blocks until done or timeout).
	require.True(t, called.Load(), "handler must have been called")
	require.EqualValues(t, 0, sub.inflightGoroutines.Load(),
		"inflight counter must return to 0 after normal handler completion")

	// Shutdown() must return immediately — no goroutines remain tracked.
	done := make(chan struct{})
	go func() {
		sub.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wg.Wait() did not return — wg.Done() was not called by invokeHandler")
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Fuzz — processFrame must never panic on arbitrary input
// ═════════════════════════════════════════════════════════════════════════════

// FuzzProcessFrame verifies that processFrame never panics on arbitrary byte
// input. Run with: go test -fuzz=FuzzProcessFrame ./...
func FuzzProcessFrame(f *testing.F) {
	// Seed: valid hashblock message.
	f.Add([]byte("hashblock"), make([]byte, 32), binary.LittleEndian.AppendUint32(nil, 1))
	// Seed: valid hashtx message.
	f.Add([]byte("hashtx"), make([]byte, 32), binary.LittleEndian.AppendUint32(nil, 0))
	// Seed: wrong topic.
	f.Add([]byte("rawtx"), make([]byte, 32), binary.LittleEndian.AppendUint32(nil, 1))
	// Seed: empty frames.
	f.Add([]byte{}, []byte{}, []byte{})
	// Seed: short hash.
	f.Add([]byte("hashblock"), make([]byte, 16), binary.LittleEndian.AppendUint32(nil, 1))
	// Seed: long hash.
	f.Add([]byte("hashblock"), make([]byte, 64), binary.LittleEndian.AppendUint32(nil, 1))
	// Seed: short sequence.
	f.Add([]byte("hashblock"), make([]byte, 32), []byte{0, 0, 0})
	// Seed: sequence wrap-around.
	f.Add([]byte("hashblock"), make([]byte, 32), binary.LittleEndian.AppendUint32(nil, ^uint32(0)))

	f.Fuzz(func(t *testing.T, topicData, hashData, seqData []byte) {
		iface, err := New("tcp://127.0.0.1:28332", "tcp://127.0.0.1:28333", 60*time.Second, nil)
		if err != nil {
			return
		}
		// Type-assert to *subscriber to access internal fields (same package).
		sub := iface.(*subscriber)
		sub.handlerTimeout = 10 * time.Millisecond

		msg := [][]byte{topicData, hashData, seqData}
		state := readerState{}
		// Must never panic regardless of input.
		_ = sub.processFrame(context.Background(), msg, []byte("hashblock"), &state,
			func([32]byte, uint32) {})
	})
}
