// Package zmq provides the ZMQ subscriber for Bitcoin Core hashblock and hashtx
// events. It decodes raw ZMQ frames into typed Go structs and fans them out to
// registered handler callbacks.
//
// This package contains zero domain imports — it is a pure platform concern.
// Domain packages register handlers via Subscriber.Register* methods; they
// never read ZMQ frames directly.
package zmq

import (
	"encoding/hex"
	"time"
)

// BlockEvent is the decoded form of a hashblock ZMQ message from Bitcoin Core.
//
// Height is NOT included — the ZMQ hashblock topic delivers only the 32-byte
// hash and a monotonic sequence number. Handlers that need the height must call
// GetBlockHeader via the RPC client.
//
// BYTE ORDER: ZMQ delivers block hashes in internal byte order (little-endian).
// Bitcoin RPC and all block explorers use reversed byte order (big-endian).
// Always call HashHex() for RPC or external use — never call
// hex.EncodeToString(e.Hash[:]) directly, which returns the wrong byte order
// and causes RPC to return "Block not found" with no other indication. This
// pattern is banned by a CI lint rule in the bitcoin package tree.
type BlockEvent struct {
	// Hash is the raw 32-byte block hash in ZMQ internal (little-endian) byte order.
	Hash [32]byte

	// Sequence is the monotonically increasing ZMQ sequence number for the
	// hashblock topic. A gap (incoming != last+1) means the ZMQ layer dropped
	// messages and a RecoveryEvent will be fired before the next BlockEvent.
	Sequence uint32
}

// HashHex returns the block hash in RPC-compatible big-endian hex encoding.
// Use this for all GetBlock, GetBlockHeader, and block-explorer calls.
func (e BlockEvent) HashHex() string {
	var rev [32]byte
	for i, b := range e.Hash {
		rev[31-i] = b
	}
	return hex.EncodeToString(rev[:])
}

// TxEvent is the decoded form of a hashtx ZMQ message from Bitcoin Core.
// It represents a transaction entering the mempool; confirmed transactions are
// detected via the BlockEvent path, not here.
//
// Note: TxEvent and BlockEvent are structurally identical by design — same
// shape, different types. The compiler enforces that a block handler can never
// be wired to the tx topic by accident. It is a semantic boundary, not a
// structural one.
//
// BYTE ORDER: same caveat as BlockEvent — always use HashHex() for RPC calls.
type TxEvent struct {
	// Hash is the raw 32-byte txid in ZMQ internal (little-endian) byte order.
	Hash [32]byte

	// Sequence is the monotonically increasing ZMQ sequence number for the
	// hashtx topic. Tracked independently from the hashblock sequence.
	Sequence uint32
}

// HashHex returns the txid in RPC-compatible big-endian hex encoding.
// Use this for all GetRawTransaction and mempool lookup calls.
func (e TxEvent) HashHex() string {
	var rev [32]byte
	for i, b := range e.Hash {
		rev[31-i] = b
	}
	return hex.EncodeToString(rev[:])
}

// RecoveryEvent is fired after the subscriber reconnects to Bitcoin Core, or
// when a ZMQ sequence gap is detected, and always before the first
// post-reconnect event is delivered to handlers.
//
// Domain handlers use this to trigger gap-filling logic — for example, the
// settlement engine re-runs its block cursor from LastSeenSequence to catch
// any blocks that arrived while the subscriber was disconnected.
//
// Ordering guarantee: RecoveryEvent is always delivered before the first
// post-reconnect BlockEvent or TxEvent. Handlers will never see a new block
// arrive before their recovery handler has run.
type RecoveryEvent struct {
	// ReconnectedAt is the wall-clock time at which the ZMQ connection was
	// re-established or the sequence gap was detected.
	ReconnectedAt time.Time

	// LastSeenSequence is the last sequence number received before the
	// disconnect or gap, for the topic that triggered recovery (hashblock or
	// hashtx). Zero means no message was received before the very first
	// connection attempt.
	LastSeenSequence uint32
}
