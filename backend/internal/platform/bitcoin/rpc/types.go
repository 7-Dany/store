package rpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// ── BTC amount precision ──────────────────────────────────────────────────────

// btcRawAmount is an unexported named alias for float64.
//
// Bitcoin Core returns all BTC amounts as floating-point JSON numbers. Callers
// receive btcRawAmount from response struct fields and must call BtcToSat() or
// BtcToSatSigned() to convert to integer satoshis. They cannot construct
// btcRawAmount directly — it is unexported — so bypassing the conversion
// functions is a compile error.
type btcRawAmount float64

// ErrNoFeeEstimate indicates Bitcoin Core lacks sufficient data for fee estimation.
// Callers should check FeeEstimate.HasEstimate() before converting FeeRate.
var ErrNoFeeEstimate = errors.New("rpc: Bitcoin Core lacks sufficient data for fee estimation")

// BtcToSat converts a non-negative BTC amount to integer satoshis.
//
// IEEE 754 double precision cannot represent most decimal fractions exactly, so
// plain multiplication produces wrong results: 0.1 * 1e8 = 9999999.999999776,
// which truncates to 9999999 (off by 1 sat). Math.Round corrects this.
//
// Note: IEEE 754 negative zero (-0.0) passes the btc < 0 check (false) and
// correctly rounds to 0.
//
// Returns an error for negative amounts, amounts exceeding the Bitcoin supply
// cap (~20,999,999.9769 BTC), and sub-satoshi amounts that round to zero.
func BtcToSat(btc btcRawAmount) (int64, error) {
	if btc < 0 {
		return 0, fmt.Errorf("BtcToSat: negative amount %v", btc)
	}
	// The actual Bitcoin supply cap is ~20,999,999.9769 BTC. Using 21M with
	// >= rejects the physically impossible 21,000,000.00000000 BTC while
	// accepting all valid amounts up to the real cap.
	const maxBitcoinSupplyBTC = 21_000_000
	if btc >= maxBitcoinSupplyBTC {
		return 0, fmt.Errorf("BtcToSat: amount %v BTC exceeds the maximum Bitcoin supply of %d BTC",
			btc, maxBitcoinSupplyBTC)
	}
	sat := math.Round(float64(btc) * 1e8)
	if sat > math.MaxInt64 {
		return 0, fmt.Errorf("BtcToSat: overflow for amount %v BTC", btc)
	}
	// Guard against sub-satoshi amounts that silently round to zero.
	// A non-zero BTC amount that rounds to 0 sat indicates precision loss.
	if btc > 0 && int64(sat) == 0 {
		return 0, fmt.Errorf("BtcToSat: amount %v BTC is below 1 satoshi (the minimum unit) and rounds to zero", btc)
	}
	return int64(sat), nil
}

// BtcToSatSigned converts a BTC amount that may be negative to integer satoshis.
//
// Bitcoin Core returns negative amounts for "send" category entries in
// gettransaction details (e.g., -0.001 BTC sent from the wallet). Use this
// function instead of BtcToSat for those values.
//
// The absolute value is converted via BtcToSat, then negated if the original
// was negative. The same supply-cap and sub-satoshi guards apply to abs(btc).
func BtcToSatSigned(btc btcRawAmount) (int64, error) {
	if btc >= 0 {
		return BtcToSat(btc)
	}
	sat, err := BtcToSat(btcRawAmount(-float64(btc)))
	if err != nil {
		return 0, err
	}
	return -sat, nil
}

// BtcToSatOptional converts a possibly-nil BTC amount to satoshis.
// Returns (0, nil) for nil input (e.g. absent fee on receive transactions).
func BtcToSatOptional(btc *btcRawAmount) (int64, error) {
	if btc == nil {
		return 0, nil
	}
	return BtcToSat(*btc)
}

// FeeRateToSatPerVB converts a fee rate from BTC/kvB to sat/vB.
//
// Bitcoin Core returns fee rates in BTC per 1000 virtual bytes (kvB).
// To get sat/vB: convert BTC to satoshis, then divide by 1000.
//
// Example: feerate=0.00001000 BTC/kvB → 1000 sat/kvB → 1 sat/vB.
//
// Returns ErrNoFeeEstimate when feeRate is negative (Bitcoin Core's signal
// that it lacks sufficient data for estimation).
func FeeRateToSatPerVB(feeRate btcRawAmount) (int64, error) {
	if feeRate < 0 {
		return 0, ErrNoFeeEstimate
	}
	satKvB, err := BtcToSat(feeRate)
	if err != nil {
		return 0, err
	}
	return satKvB / 1000, nil
}

// ── Blockchain types ──────────────────────────────────────────────────────────

// BlockHeader is returned by getblockheader.
type BlockHeader struct {
	Confirmations int     `json:"confirmations"`
	Height        int     `json:"height"`
	Hash          string  `json:"hash"`
	Version       int     `json:"version"`
	VersionHex    string  `json:"versionHex"`
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
	StrippedSize  int     `json:"strippedsize"`
	Size          int     `json:"size"`
	Weight        int     `json:"weight"`
}

// VerboseBlock is returned by getblock with verbosity=2.
type VerboseBlock struct {
	Confirmations int     `json:"confirmations"`
	Height        int     `json:"height"`
	Hash          string  `json:"hash"`
	Time          int64   `json:"time"`
	MedianTime    int64   `json:"mediantime"`
	Nonce         uint32  `json:"nonce"`
	Bits          string  `json:"bits"`
	Difficulty    float64 `json:"difficulty"`
	Version       int     `json:"version"`
	MerkleRoot    string  `json:"merkleroot"`
	NTx           int     `json:"nTx"`
	PreviousBlock string  `json:"previousblockhash"`
	NextBlock     string  `json:"nextblockhash"`
	StrippedSize  int     `json:"strippedsize"`
	Size          int     `json:"size"`
	Weight        int     `json:"weight"`
	Tx            []RawTx `json:"tx"`
}

// BlockchainInfo is returned by getblockchaininfo.
type BlockchainInfo struct {
	Chain                string          `json:"chain"`
	Blocks               int             `json:"blocks"`
	Headers              int             `json:"headers"`
	BestBlockHash        string          `json:"bestblockhash"`
	Difficulty           float64         `json:"difficulty"`
	MedianTime           int64           `json:"mediantime"`
	VerificationProgress float64         `json:"verificationprogress"`
	InitialBlockDownload bool            `json:"initialblockdownload"`
	Chainwork            string          `json:"chainwork"`
	SizeOnDisk           int64           `json:"size_on_disk"`
	Pruned               bool            `json:"pruned"`
	PruneHeight          int             `json:"pruneheight"`
	SoftForks            json.RawMessage `json:"softforks"`
	Warnings             string          `json:"warnings"`
}

// ── Wallet transaction types ──────────────────────────────────────────────────

// WalletTx is returned by gettransaction.
type WalletTx struct {
	TxID            string        `json:"txid"`
	Confirmations   int           `json:"confirmations"`
	BlockHash       string        `json:"blockhash"`
	BlockHeight     int           `json:"blockheight"`
	BlockTime       int64         `json:"blocktime"`
	Time            int64         `json:"time"`
	TimeReceived    int64         `json:"timereceived"`
	Fee             *btcRawAmount `json:"fee,omitempty"`
	Details         []TxDetail    `json:"details"`
	Hex             string        `json:"hex"`
	WalletConflicts []string      `json:"walletconflicts"`
	ReplacedByTxID  string        `json:"replaced_by_txid"`
	ReplacesTxID    string        `json:"replaces_txid"`
	Decoded         *DecodedTx    `json:"decoded,omitempty"`
}

// TxDetail is one entry in WalletTx.Details.
//
// Amount is negative for "send" category transactions (funds leaving the wallet).
// Use BtcToSatSigned, not BtcToSat, to convert Amount to satoshis.
type TxDetail struct {
	Address  string       `json:"address"`
	Category string       `json:"category"`
	Amount   btcRawAmount `json:"amount"`
	Vout     int          `json:"vout"`
	Label    string       `json:"label"`
}

// DecodedTx is the full decoded transaction embedded in a verbose gettransaction response.
type DecodedTx struct {
	TxID     string   `json:"txid"`
	Vin      []TxVin  `json:"vin"`
	Vout     []TxVout `json:"vout"`
	Size     int      `json:"size"`
	VSize    int      `json:"vsize"`
	Weight   int      `json:"weight"`
	LockTime uint32   `json:"locktime"`
}

// TxVin is one input in a DecodedTx.
type TxVin struct {
	TxID     string   `json:"txid"`
	Vout     int      `json:"vout"`
	Sequence uint32   `json:"sequence"`
	Witness  []string `json:"txinwitness"`
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
	TxID          string      `json:"txid"`
	Hash          string      `json:"hash"` // wtxid (witness transaction ID)
	Vin           []RawTxVin  `json:"vin"`
	Vout          []RawTxVout `json:"vout"`
	Size          int         `json:"size"`
	VSize         int         `json:"vsize"`
	Weight        int         `json:"weight"`
	Version       int         `json:"version"`
	LockTime      uint32      `json:"locktime"`
	BlockHash     string      `json:"blockhash"`
	Confirmations int         `json:"confirmations"`
	Time          int64       `json:"time"`
	BlockTime     int64       `json:"blocktime"`
	Hex           string      `json:"hex"`
}

// RawTxVin is one input in a RawTx.
// TxID is empty and Vout is 0 for coinbase (newly minted) inputs.
type RawTxVin struct {
	TxID      string   `json:"txid"`
	Vout      int      `json:"vout"`
	Sequence  uint32   `json:"sequence"`
	Witness   []string `json:"txinwitness"`
	ScriptSig *struct {
		Asm string `json:"asm"`
		Hex string `json:"hex"`
	} `json:"scriptSig"`
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
	Address                string          `json:"address"`
	ScriptPubKey           string          `json:"scriptPubKey"`
	IsMine                 bool            `json:"ismine"`
	IsWatchOnly            bool            `json:"iswatchonly"`
	Solvable               bool            `json:"solvable"`
	IsChange               bool            `json:"ischange"`
	IsScript               bool            `json:"isscript"`
	IsWitness              bool            `json:"iswitness"`
	WitnessVersion         *int            `json:"witness_version"` // nil for non-witness addresses
	WitnessProgram         string          `json:"witness_program"`
	Label                  string          `json:"label"`
	HDKeyPath              string          `json:"hdkeypath"`
	HDMasterKeyFingerprint string          `json:"hdmasterfingerprint"`
	PubKey                 string          `json:"pubkey"`
	Descriptors            json.RawMessage `json:"descriptors"` // complex nested structure — unmarshal manually if needed
}

// ── Mempool types ─────────────────────────────────────────────────────────────

// MempoolEntry is returned by getmempoolentry.
type MempoolEntry struct {
	VSize           int   `json:"vsize"`
	Weight          int   `json:"weight"`
	Time            int64 `json:"time"`
	Height          int   `json:"height"`
	DescendantCount int   `json:"descendantcount"`
	DescendantSize  int   `json:"descendantsize"`
	DescendantFees  int64 `json:"descendantfees"`
	AncestorCount   int   `json:"ancestorcount"`
	AncestorSize    int   `json:"ancestorsize"`
	AncestorFees    int64 `json:"ancestorfees"`
	Fees            struct {
		Base       btcRawAmount `json:"base"`
		Modified   btcRawAmount `json:"modified"`
		Ancestor   btcRawAmount `json:"ancestor"`
		Descendant btcRawAmount `json:"descendant"`
	} `json:"fees"`
	Depends []string `json:"depends"`
	SpentBy []string `json:"spentby"`
	// BIP125Replaceable is deprecated: removed in Bitcoin Core 24.0.
	// Nil on Core ≥24.0. Use RawTxVin.Sequence for RBF detection instead
	// (sequence < 0xFFFFFFFE indicates opt-in RBF).
	BIP125Replaceable *bool `json:"bip125-replaceable"`
}

// ── Wallet info ───────────────────────────────────────────────────────────────

// WalletInfo is returned by getwalletinfo.
type WalletInfo struct {
	WalletName            string       `json:"walletname"`
	WalletVersion         int          `json:"walletversion"`
	Balance               btcRawAmount `json:"balance"`
	UnconfirmedBalance    btcRawAmount `json:"unconfirmed_balance"`
	ImmatureBalance       btcRawAmount `json:"immature_balance"`
	TxCount               int          `json:"txcount"`
	KeypoolSize           int          `json:"keypoolsize"`
	KeypoolSizeHDInternal int          `json:"keypoolsize_hd_internal"`
	KeypoolOldest         int64        `json:"keypoololdest"`
	Descriptors           bool         `json:"descriptors"`
	PrivateKeysEnabled    bool         `json:"private_keys_enabled"`
	PayTxFee              btcRawAmount `json:"paytxfee"`
}

// ── Fee estimation ────────────────────────────────────────────────────────────

// FeeEstimate is returned by estimatesmartfee.
//
// FeeRate is denominated in BTC/kvB (BTC per 1000 virtual bytes).
// To convert to sat/vB: satKvB, _ := BtcToSat(FeeRate); satPerVB := satKvB / 1000.
// Or use the FeeRateToSatPerVB helper.
//
// When the node lacks sufficient data for estimation, Bitcoin Core returns
// FeeRate=-1 and populates Errors. Always check HasEstimate() before converting
// FeeRate with BtcToSat or FeeRateToSatPerVB.
type FeeEstimate struct {
	FeeRate btcRawAmount `json:"feerate"`
	Blocks  int          `json:"blocks"`
	Errors  []string     `json:"errors"`
}

// HasEstimate reports whether Bitcoin Core returned a usable fee estimate.
// When false, FeeRate is -1 and Errors contains the reason. Callers must
// check HasEstimate before converting FeeRate with BtcToSat or FeeRateToSatPerVB.
func (f FeeEstimate) HasEstimate() bool {
	return len(f.Errors) == 0 && f.FeeRate >= 0
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
