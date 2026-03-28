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
// BYTE ORDER: Bitcoin Core ZMQ publishes block hashes in the same byte order
// used by RPC and block explorers. Treat Hash as an opaque 32-byte blob in
// display/RPC order; avoid "endianness" terminology for hashes.
type BlockEvent struct {
	// Hash is the raw 32-byte block hash in the same order used by RPC and
	// block explorers.
	Hash [32]byte

	// Sequence is the monotonically increasing ZMQ sequence number for the
	// hashblock topic. A gap (incoming != last+1) means the ZMQ layer dropped
	// messages and a RecoveryEvent will be fired before the next BlockEvent.
	Sequence uint32
}

// HashHex returns the block hash in the same hex form accepted by RPC and used
// by block explorers.
func (e BlockEvent) HashHex() string {
	return hex.EncodeToString(e.Hash[:])
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
// BYTE ORDER: same contract as BlockEvent — Hash already matches RPC/display
// order.
type TxEvent struct {
	// Hash is the raw 32-byte txid in the same order used by RPC and block
	// explorers.
	Hash [32]byte

	// Sequence is the monotonically increasing ZMQ sequence number for the
	// hashtx topic. Tracked independently from the hashblock sequence.
	Sequence uint32
}

// HashHex returns the txid in the same hex form accepted by RPC and used by
// block explorers.
func (e TxEvent) HashHex() string {
	return hex.EncodeToString(e.Hash[:])
}

// RecoveryEvent is fired after a subscribed topic reconnects to Bitcoin Core,
// or when that topic detects a ZMQ sequence gap, and always before the first
// post-reconnect event is delivered for that topic.
//
// Domain handlers use Topic to decide whether recovery work is needed — for
// example, settlement logic may care about "hashblock" and "hashtx" but ignore
// "rawtx" reconnects entirely.
//
// Ordering guarantee: RecoveryEvent is always delivered before the first
// post-reconnect event for the triggering topic. Handlers will never see a new
// event for that topic arrive before their recovery handler has run.
type RecoveryEvent struct {
	// ReconnectedAt is the wall-clock time at which the ZMQ connection was
	// re-established or the sequence gap was detected.
	ReconnectedAt time.Time

	// Topic is the triggering ZMQ topic: "hashblock", "hashtx", or "rawtx".
	Topic string

	// LastSeenSequence is the last sequence number received before the
	// disconnect or gap for Topic. Zero means no message was received before
	// the very first connection attempt for that topic.
	LastSeenSequence uint32
}

// ── RawTxEvent ────────────────────────────────────────────────────────────────

// RawTxEvent is the decoded form of a rawtx ZMQ message from Bitcoin Core.
//
// Unlike TxEvent (which carries only the txid hash from the hashtx topic),
// RawTxEvent carries the full transaction decoded from the raw bytes pushed over
// the rawtx topic. This eliminates the GetRawTransaction RPC call and the race
// condition it creates on pruned nodes without txindex=1: by the time the RPC
// call fires, the transaction may have confirmed and become unreachable.
//
// Field summary:
//   - TxIDBytes: raw 32-byte txid in RPC/display byte order; call TxIDHex() for hex.
//   - Inputs: decoded prevouts for RBF detection (empty PrevTxIDHex = coinbase).
//   - Outputs: decoded values + addresses for watch-address matching.
//
// Address encoding: Output.Address is encoded in the same format as
// bitcoinshared.ValidateAndNormalise — bech32 (P2WPKH/P2WSH) and bech32m (P2TR)
// are lowercased; P2PKH and P2SH are base58check. Empty string for OP_RETURN or
// non-standard scripts. The network HRP must be configured via SetNetwork() once
// at startup before any ZMQ messages are processed.
//
// BYTE ORDER: TxIDBytes uses the same order as RPC and block explorers.
type RawTxEvent struct {
	// TxIDBytes is the txid in the same order used by RPC and block explorers.
	TxIDBytes [32]byte

	// Sequence is the monotonically increasing ZMQ sequence number for the rawtx
	// topic. Tracked independently from the hashblock and hashtx sequences.
	Sequence uint32

	// Inputs is the decoded list of transaction inputs.
	Inputs []RawTxInput

	// Outputs is the decoded list of transaction outputs.
	Outputs []RawTxOutput
}

// TxIDHex returns the txid in the same hex form accepted by RPC and used by
// block explorers.
// Use this for all GetMempoolEntry and watch-address lookups.
func (e *RawTxEvent) TxIDHex() string {
	return hex.EncodeToString(e.TxIDBytes[:])
}

// RawTxInput is one decoded input from a Bitcoin transaction.
type RawTxInput struct {
	// PrevTxIDHex is the txid of the spent UTXO in RPC big-endian hex.
	// Empty string for coinbase inputs.
	PrevTxIDHex string

	// PrevVout is the output index of the spent UTXO.
	// 0xFFFFFFFF for coinbase inputs.
	PrevVout uint32
}

// IsCoinbase reports whether this input is a coinbase (newly minted) input.
func (i RawTxInput) IsCoinbase() bool { return i.PrevTxIDHex == "" }

// RawTxOutput is one decoded output from a Bitcoin transaction.
type RawTxOutput struct {
	// ValueSat is the output value in satoshis.
	ValueSat int64

	// N is the output index (vout).
	N uint32

	// Address is the decoded address in the same encoding used by
	// bitcoinshared.ValidateAndNormalise: bech32/bech32m for segwit outputs,
	// base58check for P2PKH/P2SH. Empty for OP_RETURN or non-standard scripts.
	Address string
}
