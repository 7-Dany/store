package zmq

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

// ═════════════════════════════════════════════════════════════════════════════
// events.go — hash helpers
// ═════════════════════════════════════════════════════════════════════════════

// TestBlockEvent_HashHex_PreservesRPCOrder verifies that Hash already uses the
// same byte order as RPC and block explorers.
func TestBlockEvent_HashHex_PreservesRPCOrder(t *testing.T) {
	t.Parallel()
	const rpcHex = "000000000000000000024bfa6c7805419a31fde7da3cf6517d8bc71b36eb8a5f"

	hash, err := hex.DecodeString(rpcHex)
	require.NoError(t, err)
	var got [32]byte
	copy(got[:], hash)

	e := BlockEvent{Hash: got}
	require.Equal(t, rpcHex, e.HashHex(),
		"HashHex() must preserve the RPC/display hash bytes")
}

// TestTxEvent_HashHex_PreservesRPCOrder mirrors the block test for TxEvent.
func TestTxEvent_HashHex_PreservesRPCOrder(t *testing.T) {
	t.Parallel()
	const rpcHex = "a1075db55d416d3ca199f55b6084e2115b9345e16c5cf302fc80e9d5fbf5d48d"

	hash, err := hex.DecodeString(rpcHex)
	require.NoError(t, err)
	var got [32]byte
	copy(got[:], hash)

	e := TxEvent{Hash: got}
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
