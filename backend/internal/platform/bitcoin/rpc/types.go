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
func BtcToSat(btc btcRawAmount) (int64, error) {
	if btc < 0 {
		return 0, fmt.Errorf("BtcToSat: negative amount %v", btc)
	}
	const maxBitcoinSupplyBTC = 21_000_000
	if btc > maxBitcoinSupplyBTC {
		return 0, fmt.Errorf("BtcToSat: amount %v BTC exceeds the maximum Bitcoin supply of %d BTC",
			btc, maxBitcoinSupplyBTC)
	}
	sat := math.Round(float64(btc) * 1e8)
	if sat > math.MaxInt64 {
		return 0, fmt.Errorf("BtcToSat: overflow for amount %v BTC", btc)
	}
	return int64(sat), nil
}

// ── Blockchain types ──────────────────────────────────────────────────────────

// BlockHeader is returned by getblockheader.
type BlockHeader struct {
	Confirmations int     `json:"confirmations"`
	Height        int     `json:"height"`
	Hash          string  `json:"hash"`
	Version       int     `json:"version"`
	MerkleRoot    string  `json:"merkleroot"`
	Time          int64   `json:"time"`
	MedianTime    int64   `json:"mediantime"`
	Nonce         uint32  `json:"nonce"`
	Bits          string  `json:"bits"`
	Difficulty    float64 `json:"difficulty"`
	Chainwork     string  `json:"chainwork"`
	NTx           int     `json:"nTx"`
	PreviousBlock string  `json:"previousblockhash"`
	NextBlock     string  `json:"nextblockhash"`
}

// VerboseBlock is returned by getblock with verbosity=2.
type VerboseBlock struct {
	Confirmations int     `json:"confirmations"`
	Height        int     `json:"height"`
	Hash          string  `json:"hash"`
	Tx            []RawTx `json:"tx"`
}

// BlockchainInfo is returned by getblockchaininfo.
type BlockchainInfo struct {
	Chain         string `json:"chain"`
	Blocks        int    `json:"blocks"`
	BestBlockHash string `json:"bestblockhash"`
	Pruned        bool   `json:"pruned"`
	PruneHeight   int    `json:"pruneheight"`
}

// ── Wallet transaction types ──────────────────────────────────────────────────

// WalletTx is returned by gettransaction.
type WalletTx struct {
	TxID          string     `json:"txid"`
	Confirmations int        `json:"confirmations"`
	BlockHash     string     `json:"blockhash"`
	BlockHeight   int        `json:"blockheight"`
	BlockTime     int64      `json:"blocktime"`
	TimeReceived  int64      `json:"timereceived"`
	Details       []TxDetail `json:"details"`
	Decoded       *DecodedTx `json:"decoded,omitempty"`
}

// TxDetail is one entry in WalletTx.Details.
type TxDetail struct {
	Address  string       `json:"address"`
	Category string       `json:"category"`
	Amount   btcRawAmount `json:"amount"`
	Vout     int          `json:"vout"`
	Label    string       `json:"label"`
}

// DecodedTx is the full decoded transaction embedded in a verbose gettransaction response.
type DecodedTx struct {
	TxID string   `json:"txid"`
	Vout []TxVout `json:"vout"`
}

// TxVout is one output in a DecodedTx.
type TxVout struct {
	Value        btcRawAmount `json:"value"`
	N            int          `json:"n"`
	ScriptPubKey struct {
		Address string `json:"address"`
		Type    string `json:"type"`
	} `json:"scriptPubKey"`
}

// ── Raw transaction types ─────────────────────────────────────────────────────

// RawTx is returned by getrawtransaction with verbosity=1.
//
// Unlike GetTransaction (wallet-only), GetRawTransaction works for ANY
// transaction currently in the mempool, regardless of wallet ownership.
// This makes it the correct tool for the SSE display path which watches
// arbitrary user-registered addresses.
//
// Key property: getrawtransaction works on MEMPOOL transactions WITHOUT
// txindex. Once confirmed the tx leaves the mempool and getrawtransaction
// requires txindex — but by then the events service has already captured
// address data in pendingMempool.
type RawTx struct {
	TxID string      `json:"txid"`
	Vin  []RawTxVin  `json:"vin"`
	Vout []RawTxVout `json:"vout"`
}

// RawTxVin is one input in a RawTx.
// TxID is empty and Vout is 0 for coinbase (newly minted) inputs.
type RawTxVin struct {
	TxID string `json:"txid"`
	Vout int    `json:"vout"`
}

// RawTxVout is one output in a RawTx.
// Value is typed as btcRawAmount — callers must use BtcToSat(), never cast directly.
type RawTxVout struct {
	Value        btcRawAmount `json:"value"`
	N            int          `json:"n"`
	ScriptPubKey struct {
		Address string `json:"address"` // empty for OP_RETURN or unrecognised scripts
		Type    string `json:"type"`
	} `json:"scriptPubKey"`
}

// ── Address types ─────────────────────────────────────────────────────────────

// AddressInfo is returned by getaddressinfo.
type AddressInfo struct {
	Address     string `json:"address"`
	IsMine      bool   `json:"ismine"`
	IsWatchOnly bool   `json:"iswatchonly"`
	Solvable    bool   `json:"solvable"`
	IsChange    bool   `json:"ischange"`
	Label       string `json:"label"`
	HDKeyPath   string `json:"hdkeypath"`
}

// ── Mempool types ─────────────────────────────────────────────────────────────

// MempoolEntry is returned by getmempoolentry.
type MempoolEntry struct {
	VSize  int   `json:"vsize"`
	Weight int   `json:"weight"`
	Time   int64 `json:"time"`
	Height int   `json:"height"`
	Fees   struct {
		Base     btcRawAmount `json:"base"`
		Modified btcRawAmount `json:"modified"`
	} `json:"fees"`
	BIP125Replaceable bool `json:"bip125-replaceable"`
}

// ── Wallet info ───────────────────────────────────────────────────────────────

// WalletInfo is returned by getwalletinfo.
type WalletInfo struct {
	WalletName            string `json:"walletname"`
	WalletVersion         int    `json:"walletversion"`
	KeypoolSize           int    `json:"keypoolsize"`
	KeypoolSizeHDInternal int    `json:"keypoolsize_hd_internal"`
	KeypoolOldest         int64  `json:"keypoololdest"`
	Descriptors           bool   `json:"descriptors"`
}

// ── Fee estimation ────────────────────────────────────────────────────────────

// FeeEstimate is returned by estimatesmartfee.
type FeeEstimate struct {
	FeeRate btcRawAmount `json:"feerate"`
	Blocks  int          `json:"blocks"`
}

// ── PSBT types ────────────────────────────────────────────────────────────────

// FundedPSBT is returned by walletcreatefundedpsbt.
type FundedPSBT struct {
	PSBT      string       `json:"psbt"`
	Fee       btcRawAmount `json:"fee"`
	ChangePos int          `json:"changepos"`
}

// ProcessedPSBT is returned by walletprocesspsbt.
type ProcessedPSBT struct {
	PSBT     string `json:"psbt"`
	Complete bool   `json:"complete"`
}

// FinalizedPSBT is returned by finalizepsbt.
type FinalizedPSBT struct {
	Hex      string `json:"hex"`
	Complete bool   `json:"complete"`
}
