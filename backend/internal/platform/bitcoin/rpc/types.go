package rpc

import (
	"fmt"
	"math"
)

// ── BTC amount precision ──────────────────────────────────────────────────────

// btcRawAmount is an unexported named alias for float64.
//
// Bitcoin Core returns all BTC amounts as floating-point JSON numbers. Callers
// receive btcRawAmount from response struct fields and must call BtcToSat() to
// convert to integer satoshis. They cannot construct btcRawAmount directly —
// it is unexported — so bypassing BtcToSat is a compile error.
type btcRawAmount float64

// BtcToSat converts a BTC amount to integer satoshis using math.Round.
//
// IEEE 754 double precision cannot represent most decimal fractions exactly, so
// plain multiplication produces wrong results: 0.1 * 1e8 = 9999999.999999776,
// which truncates to 9999999 (off by 1 sat). math.Round corrects this.
//
// Returns an error for negative amounts, amounts above Bitcoin's 21M supply cap,
// or amounts that would overflow int64. The supply-cap check is preferred over a
// raw MaxInt64 comparison because float64(math.MaxInt64) rounds to 2^63 in IEEE
// 754, making that comparison imprecise near the boundary. 21M BTC is an exact
// float64 value and is well within the safe range.
func BtcToSat(btc btcRawAmount) (int64, error) {
	if btc < 0 {
		return 0, fmt.Errorf("BtcToSat: negative amount %v", btc)
	}
	// Any value above 21M BTC is an invalid RPC response — either a parsing
	// error or a misbehaving node.
	const maxBitcoinSupplyBTC = 21_000_000
	if btc > maxBitcoinSupplyBTC {
		return 0, fmt.Errorf("BtcToSat: amount %v BTC exceeds the maximum Bitcoin supply of %d BTC",
			btc, maxBitcoinSupplyBTC)
	}
	sat := math.Round(float64(btc) * 1e8)
	// Unreachable for any value ≤ 21M BTC, but guards against misuse of this
	// function with non-BTC amounts.
	if sat > math.MaxInt64 {
		return 0, fmt.Errorf("BtcToSat: overflow for amount %v BTC", btc)
	}
	return int64(sat), nil
}

// ── Blockchain types ──────────────────────────────────────────────────────────

// BlockHeader is returned by getblockheader.
// Used by the events domain to get the block height after receiving a hashblock
// ZMQ notification. Does not require txindex. Works on pruned nodes.
type BlockHeader struct {
	Height int    `json:"height"` // same type as all other heights in this package
	Hash   string `json:"hash"`
	Time   int64  `json:"time"`
}

// BlockchainInfo is returned by getblockchaininfo.
// Used at startup to verify the node's active chain matches BTC_NETWORK, and by
// the liveness goroutine to monitor chain tip progress.
type BlockchainInfo struct {
	Chain         string `json:"chain"`         // "main", "testnet4", "regtest"
	Blocks        int    `json:"blocks"`        // current chain height
	BestBlockHash string `json:"bestblockhash"` // current tip hash (big-endian hex)
	Pruned        bool   `json:"pruned"`
	PruneHeight   int    `json:"pruneheight"` // only present when Pruned=true
}

// ── Wallet transaction types ──────────────────────────────────────────────────

// WalletTx is returned by gettransaction.
//
// gettransaction queries the wallet's own transaction index, which is maintained
// independently of the global txindex. It works on pruned nodes without
// txindex=1. It covers every transaction the platform has ever sent or received —
// which is the only set of transactions this system needs to query.
//
// Confirmations semantics:
//
//	> 0: confirmed in the active chain at this depth
//	= 0: in the mempool (unconfirmed)
//	< 0: in a conflicting chain (displaced by a reorg) — use IsConflicting()
type WalletTx struct {
	TxID          string     `json:"txid"`
	Confirmations int        `json:"confirmations"`
	BlockHash     string     `json:"blockhash"`    // empty when unconfirmed
	BlockHeight   int        `json:"blockheight"`  // 0 when unconfirmed
	BlockTime     int64      `json:"blocktime"`    // unix timestamp; 0 when unconfirmed
	TimeReceived  int64      `json:"timereceived"` // unix timestamp when first seen locally
	Details       []TxDetail `json:"details"`
	// Decoded is only populated when GetTransaction is called with verbose=true.
	// It contains the full decoded transaction including all vouts.
	Decoded *DecodedTx `json:"decoded,omitempty"`
}

// TxDetail is one entry in WalletTx.Details.
// Each entry represents one vout relevant to the wallet — either because the
// wallet controls the output address (receive) or spent an input from it (send).
type TxDetail struct {
	Address  string       `json:"address"`
	Category string       `json:"category"` // "receive" | "send" | "generate" | "immature"
	Amount   btcRawAmount `json:"amount"`   // positive for receive, negative for send — use BtcToSat
	Vout     int          `json:"vout"`     // output index (0-based)
	Label    string       `json:"label"`    // label from getnewaddress (e.g. "invoice")
}

// DecodedTx is the full decoded transaction embedded in a verbose gettransaction
// response. Contains all inputs and outputs in parsed form.
type DecodedTx struct {
	TxID string   `json:"txid"`
	Vout []TxVout `json:"vout"`
}

// TxVout is one output in a DecodedTx.
// Value is typed as btcRawAmount — callers must use BtcToSat(), never cast directly.
type TxVout struct {
	Value        btcRawAmount `json:"value"` // must use BtcToSat()
	N            int          `json:"n"`     // output index (0-based)
	ScriptPubKey struct {
		Address string `json:"address"` // normalised to lowercase by Bitcoin Core
		Type    string `json:"type"`    // "witness_v0_keyhash", "witness_v1_taproot", etc.
	} `json:"scriptPubKey"`
}

// ── Address types ─────────────────────────────────────────────────────────────

// AddressInfo is returned by getaddressinfo.
// Used at invoice creation time to read the HD derivation path of a freshly
// generated address, and at validation time to detect platform-managed addresses
// via the IsMine field.
type AddressInfo struct {
	Address     string `json:"address"`
	IsMine      bool   `json:"ismine"`      // true if wallet controls the private key
	IsWatchOnly bool   `json:"iswatchonly"` // true if watch-only (no private key)
	Solvable    bool   `json:"solvable"`
	IsChange    bool   `json:"ischange"` // true if this is a change address
	Label       string `json:"label"`
	// HDKeyPath is the BIP-32 derivation path for this address.
	// Format: "m/84'/0'/0'/0/5200" — the leaf index is the derivation index
	// stored on the invoice for wallet recovery purposes.
	// Empty for addresses not derived from the wallet's HD seed.
	HDKeyPath string `json:"hdkeypath"`
}

// ── Mempool types ─────────────────────────────────────────────────────────────

// MempoolEntry is returned by getmempoolentry.
//
// A successful response means the transaction is currently in the node's mempool.
// Error code -5 ("Transaction not in mempool") is the normal absent response —
// callers must check IsNotFoundError(err) rather than treating it as a failure.
// The mempool drop watchdog uses this to distinguish a dropped transaction from
// a node connectivity problem.
type MempoolEntry struct {
	VSize  int   `json:"vsize"`
	Weight int   `json:"weight"`
	Time   int64 `json:"time"`   // unix timestamp when added to mempool
	Height int   `json:"height"` // block height when added to mempool
	Fees   struct {
		Base     btcRawAmount `json:"base"`     // base fee in BTC — use BtcToSat
		Modified btcRawAmount `json:"modified"` // effective fee after priority boost — use BtcToSat
	} `json:"fees"`
	// BIP125Replaceable is unreliable since Bitcoin Core v28+ enabled fullrbf by
	// default. With fullrbf all unconfirmed transactions are replaceable regardless
	// of opt-in signalling — this field always reports false on modern nodes.
	// Do not use it to gate RBF detection; treat every unconfirmed tx as replaceable.
	BIP125Replaceable bool `json:"bip125-replaceable"`
}

// ── Wallet info ───────────────────────────────────────────────────────────────

// WalletInfo is returned by getwalletinfo.
// Used by the keypool monitoring job to detect when the pre-generated address
// pool is running low and to trigger automatic refill.
type WalletInfo struct {
	WalletName            string `json:"walletname"`
	WalletVersion         int    `json:"walletversion"`
	KeypoolSize           int    `json:"keypoolsize"`             // pre-generated external addresses
	KeypoolSizeHDInternal int    `json:"keypoolsize_hd_internal"` // pre-generated change addresses
	KeypoolOldest         int64  `json:"keypoololdest"`           // unix timestamp of oldest keypool key
	Descriptors           bool   `json:"descriptors"`             // true if using descriptor wallets (Bitcoin Core ≥0.21)
}

// ── Fee estimation ────────────────────────────────────────────────────────────

// FeeEstimate is returned by estimatesmartfee.
// Used by the sweep engine to set appropriate miner fees for outgoing transactions.
type FeeEstimate struct {
	// FeeRate is the estimated fee in BTC/kB for the requested confirmation target.
	// Zero when the node has insufficient data for estimation (e.g. early testnet4).
	FeeRate btcRawAmount `json:"feerate"`
	// Blocks is the actual confirmation target the estimate applies to.
	// May differ from the requested target if the node adjusted it.
	Blocks int `json:"blocks"`
}

// ── PSBT types ────────────────────────────────────────────────────────────────

// FundedPSBT is returned by walletcreatefundedpsbt.
type FundedPSBT struct {
	// PSBT is the base64-encoded partially signed Bitcoin transaction.
	PSBT string       `json:"psbt"`
	Fee  btcRawAmount `json:"fee"` // estimated miner fee in BTC — use BtcToSat
	// ChangePos is the vout index of the change output, or -1 when there is no
	// change output (e.g. a full-sweep to a single destination). A zero value
	// means change is at output index 0 — not that there is no change.
	ChangePos int `json:"changepos"`
}

// ProcessedPSBT is returned by walletprocesspsbt.
type ProcessedPSBT struct {
	PSBT     string `json:"psbt"`
	Complete bool   `json:"complete"` // true if all inputs are signed and ready to finalize
}

// FinalizedPSBT is returned by finalizepsbt.
type FinalizedPSBT struct {
	Hex      string `json:"hex"`      // broadcast-ready raw transaction hex (when Complete=true)
	Complete bool   `json:"complete"` // true if finalization succeeded
}
