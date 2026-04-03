package zmq

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// ── Network HRP configuration ─────────────────────────────────────────────────

// networkToHRP converts a Bitcoin network name to its bech32 human-readable part.
// The network must be one of: "mainnet", "testnet", "testnet4", "signet", "regtest".
// Any other value is an error — unknown networks are not silently defaulted.
//
// Mapping:
//   - "mainnet"    → "bc" (mainnet)
//   - "testnet", "testnet4", "signet"  → "tb" (all testnet variants)
//   - "regtest"    → "bcrt" (regtest)
func networkToHRP(network string) (string, error) {
	switch network {
	case "mainnet":
		return "bc", nil
	case "testnet", "testnet4", "signet":
		return "tb", nil
	case "regtest":
		return "bcrt", nil
	default:
		return "", fmt.Errorf("zmq: unknown network %q (must be mainnet, testnet, testnet4, signet, or regtest)", network)
	}
}

// ── ParseRawTx ────────────────────────────────────────────────────────────────

// ParseRawTx decodes a Bitcoin transaction from its wire-format byte slice and
// returns a RawTxEvent with the txid, inputs, and outputs populated.
//
// Hrp is the bech32 human-readable part for the target network ("bc" mainnet,
// "tb" testnet4/signet, "bcrt" regtest). It is used for address extraction in
// P2WPKH, P2WSH, and P2TR outputs. Passing the correct hrp ensures that decoded
// addresses match what bitcoinshared.ValidateAndNormalise expects.
//
// The txid is computed per BIP 141:
//   - For legacy transactions: SHA256d of the full raw bytes.
//   - For SegWit transactions: SHA256d of the non-witness serialization
//     (version + inputs + outputs + locktime, excluding the 0x00 0x01 marker/flag
//     and all witness stacks). This matches RPC getrawtransaction and block
//     explorers. The full-bytes hash (including witness) is the wtxid.
//
// Only the fields needed by the SSE display path are decoded:
//   - Input prevouts (txid + vout) — for O(1) RBF detection via spentOutpoints
//   - Output values (satoshis) and addresses — for watch-address matching
//
// Script and witness data is read but not decoded beyond address extraction.
// This function supports both legacy and SegWit (BIP 141) transactions.
//
// Returns a non-nil error if the byte slice is truncated or structurally invalid.
// Never panics on malformed input — all reads use io.ReadFull with explicit
// bounds checks.
func ParseRawTx(raw []byte, hrp string) (RawTxEvent, error) {
	if len(raw) < 10 {
		return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: too short (%d bytes)", len(raw))
	}

	r := newPushBackReader(raw)

	// Version: 4 bytes LE (value not validated — any version is accepted)
	if _, err := readUint32LE(r); err != nil {
		return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: version: %w", err)
	}

	// SegWit detection: peek at the next two bytes.
	// BIP 141: if marker=0x00 and flag=0x01 → SegWit format.
	// Otherwise → legacy format; push both bytes back.
	isSegWit := false
	var peek [2]byte
	if n, err := io.ReadFull(r, peek[:]); err != nil || n < 2 {
		return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: peek marker/flag: %w", err)
	}
	if peek[0] == 0x00 && peek[1] == 0x01 {
		isSegWit = true
	} else {
		r.pushBack(peek[0], peek[1])
	}

	// Input count
	inputCount, err := readVarInt(r)
	if err != nil {
		return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: input count: %w", err)
	}
	if inputCount > 100_000 {
		return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: implausible input count %d", inputCount)
	}

	inputs := make([]RawTxInput, 0, inputCount)
	for i := range inputCount {
		input, parseErr := parseTxInput(r)
		if parseErr != nil {
			return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: input[%d]: %w", i, parseErr)
		}
		inputs = append(inputs, input)
	}

	// outputCount is read after inputCount; both use the same varint encoding.
	// An excessively large count is bounded by the enclosing zmtpMaxFrameBody
	// cap on the raw transaction frame.
	outputCount, err := readVarInt(r)
	if err != nil {
		return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: output count: %w", err)
	}
	if outputCount > 100_000 {
		return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: implausible output count %d", outputCount)
	}

	outputs := make([]RawTxOutput, 0, outputCount)
	for i := range outputCount {
		out, err := parseTxOutput(r, uint32(i), hrp)
		if err != nil {
			return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: output[%d]: %w", i, err)
		}
		outputs = append(outputs, out)
	}

	// Witness data: one stack per input for SegWit transactions. Skip entirely.
	if isSegWit {
		for i := range inputCount {
			stackCount, err := readVarInt(r)
			if err != nil {
				return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: witness[%d] stack count: %w", i, err)
			}
			for j := range stackCount {
				itemLen, err := readVarInt(r)
				if err != nil {
					return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: witness[%d][%d] len: %w", i, j, err)
				}
				if err := skipN(r, itemLen); err != nil {
					return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: witness[%d][%d] data: %w", i, j, err)
				}
			}
		}
	}

	// Locktime: 4 bytes LE — skip
	if _, err := readUint32LE(r); err != nil {
		return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: locktime: %w", err)
	}

	// Compute txid: for legacy transactions, hash the full raw bytes.
	// For SegWit transactions, hash only the non-witness serialization
	// (version + inputs + outputs + locktime), excluding the 0x00 0x01 marker/flag
	// and all witness stacks. This matches BIP 141 and RPC behavior.
	var txid [32]byte
	if isSegWit {
		var err error
		txid, err = txidSegWit(raw)
		if err != nil {
			return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: txid (segwit): %w", err)
		}
	} else {
		txid = doubleSHA256(raw)
	}

	return RawTxEvent{
		TxIDBytes: txid,
		Inputs:    inputs,
		Outputs:   outputs,
	}, nil
}

// txidSegWit computes the txid of a SegWit transaction (BIP 141) as SHA256d of
// the non-witness serialization: version + inputs + outputs + locktime, excluding
// the 0x00 0x01 marker/flag and all witness stacks.
//
// The input raw must be a valid SegWit transaction (marker=0x00, flag=0x01).
// This is guaranteed by the caller (ParseRawTx checks isSegWit before calling).
func txidSegWit(raw []byte) ([32]byte, error) {
	// Non-witness serialization: version (4) + inputs + outputs + locktime (4)
	// We reconstruct by reading from the original wire format and skipping witness data.
	r := newPushBackReader(raw)

	// Buffer for the non-witness bytes. Start with a reasonable capacity
	// and let it grow as needed — most transactions are < 1 KB.
	buf := bytes.NewBuffer(make([]byte, 0, len(raw)/2))

	// Version: 4 bytes LE
	var ver [4]byte
	if _, err := io.ReadFull(r, ver[:]); err != nil {
		return [32]byte{}, fmt.Errorf("read version: %w", err)
	}
	buf.Write(ver[:])

	// Skip marker (0x00) and flag (0x01)
	var skip [2]byte
	if _, err := io.ReadFull(r, skip[:]); err != nil {
		return [32]byte{}, fmt.Errorf("read marker/flag: %w", err)
	}

	// Input count and inputs
	inputCount, err := readVarInt(r)
	if err != nil {
		return [32]byte{}, fmt.Errorf("read input count: %w", err)
	}
	writeVarInt(buf, inputCount)

	for i := range inputCount {
		// Prevout: 32 bytes (txid) + 4 bytes (vout)
		var prevout [36]byte
		if _, readErr := io.ReadFull(r, prevout[:]); readErr != nil {
			return [32]byte{}, fmt.Errorf("read input[%d] prevout: %w", i, readErr)
		}
		buf.Write(prevout[:])

		// scriptSig
		scriptLen, scriptErr := readVarInt(r)
		if scriptErr != nil {
			return [32]byte{}, fmt.Errorf("read input[%d] scriptSig length: %w", i, scriptErr)
		}
		// Cap scriptSig at the frame body limit to prevent excessive allocation
		// from a malformed SegWit tx. This is consistent with the frame-level cap.
		if scriptLen > zmtpMaxFrameBody {
			return [32]byte{}, fmt.Errorf("scriptSig length %d exceeds frame cap %d", scriptLen, zmtpMaxFrameBody)
		}
		writeVarInt(buf, scriptLen)
		scriptBytes := make([]byte, scriptLen)
		if _, readErr := io.ReadFull(r, scriptBytes); readErr != nil {
			return [32]byte{}, fmt.Errorf("read input[%d] scriptSig: %w", i, readErr)
		}
		buf.Write(scriptBytes)

		// sequence: 4 bytes LE
		var seq [4]byte
		if _, readErr := io.ReadFull(r, seq[:]); readErr != nil {
			return [32]byte{}, fmt.Errorf("read input[%d] sequence: %w", i, readErr)
		}
		buf.Write(seq[:])
	}

	// Output count and outputs
	outputCount, err := readVarInt(r)
	if err != nil {
		return [32]byte{}, fmt.Errorf("read output count: %w", err)
	}
	writeVarInt(buf, outputCount)

	for i := range outputCount {
		// value: 8 bytes LE
		var val [8]byte
		if _, err := io.ReadFull(r, val[:]); err != nil {
			return [32]byte{}, fmt.Errorf("read output[%d] value: %w", i, err)
		}
		buf.Write(val[:])

		// scriptPubKey
		scriptLen, err := readVarInt(r)
		if err != nil {
			return [32]byte{}, fmt.Errorf("read output[%d] scriptPubKey length: %w", i, err)
		}
		writeVarInt(buf, scriptLen)
		scriptBytes := make([]byte, scriptLen)
		if _, err := io.ReadFull(r, scriptBytes); err != nil {
			return [32]byte{}, fmt.Errorf("read output[%d] scriptPubKey: %w", i, err)
		}
		buf.Write(scriptBytes)
	}

	// Locktime: 4 bytes LE
	var locktime [4]byte
	if _, err := io.ReadFull(r, locktime[:]); err != nil {
		return [32]byte{}, fmt.Errorf("read locktime: %w", err)
	}
	buf.Write(locktime[:])

	// Hash the non-witness serialization
	return doubleSHA256(buf.Bytes()), nil
}

// writeVarInt writes a variable-length integer to buf using Bitcoin's varint encoding.
func writeVarInt(buf *bytes.Buffer, n uint64) {
	if n < 0xFD { //nolint:gocritic // varint encoding requires if-else chain per Bitcoin spec
		buf.WriteByte(byte(n))
	} else if n <= 0xFFFF {
		buf.WriteByte(0xFD)
		var b [2]byte
		binary.LittleEndian.PutUint16(b[:], uint16(n))
		buf.Write(b[:])
	} else if n <= 0xFFFFFFFF {
		buf.WriteByte(0xFE)
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], uint32(n))
		buf.Write(b[:])
	} else {
		buf.WriteByte(0xFF)
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], n)
		buf.Write(b[:])
	}
}

// ── Wire-format field parsers ─────────────────────────────────────────────────

// parseTxInput reads one transaction input from r.
func parseTxInput(r *pushBackReader) (RawTxInput, error) {
	// Prevout txid: 32 bytes LE on the wire
	var prevLE [32]byte
	if _, err := io.ReadFull(r, prevLE[:]); err != nil {
		return RawTxInput{}, fmt.Errorf("prevout txid: %w", err)
	}

	// Prevout vout: 4 bytes LE
	prevVout, err := readUint32LE(r)
	if err != nil {
		return RawTxInput{}, fmt.Errorf("prevout vout: %w", err)
	}

	// scriptSig: skip
	scriptLen, err := readVarInt(r)
	if err != nil {
		return RawTxInput{}, fmt.Errorf("scriptSig len: %w", err)
	}
	if scriptLen > 10_000 {
		return RawTxInput{}, fmt.Errorf("implausible scriptSig length %d", scriptLen)
	}
	if err := skipN(r, scriptLen); err != nil {
		return RawTxInput{}, fmt.Errorf("scriptSig data: %w", err)
	}

	// Sequence: 4 bytes LE — skip
	if _, err := readUint32LE(r); err != nil {
		return RawTxInput{}, fmt.Errorf("sequence: %w", err)
	}

	// Coinbase detection: the coinbase sentinel is all-zero prevout txid AND
	// vout == 0xFFFFFFFF. Check isCoinbase first so we don't reverse the bytes
	// of a coinbase input into a misleading hex string.
	isCoinbase := prevVout == 0xFFFFFFFF
	if isCoinbase {
		for _, b := range prevLE {
			if b != 0x00 {
				isCoinbase = false
				break
			}
		}
	}
	if isCoinbase {
		return RawTxInput{PrevTxIDHex: "", PrevVout: prevVout}, nil
	}

	// Reverse LE → BE for RPC-compatible hex
	var prevBE [32]byte
	for i, b := range prevLE {
		prevBE[31-i] = b
	}
	return RawTxInput{
		PrevTxIDHex: hex.EncodeToString(prevBE[:]),
		PrevVout:    prevVout,
	}, nil
}

// parseTxOutput reads one transaction output from r and extracts its address.
// Hrp is the bech32 human-readable part used for witness address encoding.
func parseTxOutput(r *pushBackReader, n uint32, hrp string) (RawTxOutput, error) {
	// Value: 8 bytes LE (satoshis)
	var valueBuf [8]byte
	if _, err := io.ReadFull(r, valueBuf[:]); err != nil {
		return RawTxOutput{}, fmt.Errorf("value: %w", err)
	}
	valueSatU64 := binary.LittleEndian.Uint64(valueBuf[:])
	// Maximum valid bitcoin value: 21,000,000 BTC = 2.1e15 satoshis, well below int64 max.
	// But check for overflow on 32-bit platforms where int64 is still 64-bit.
	const maxValidValue uint64 = 2_100_000_000_000_000
	if valueSatU64 > maxValidValue {
		return RawTxOutput{}, fmt.Errorf("value overflows valid bitcoin range: %d", valueSatU64)
	}
	valueSat := int64(valueSatU64)

	// scriptPubKey
	scriptLen, err := readVarInt(r)
	if err != nil {
		return RawTxOutput{}, fmt.Errorf("scriptPubKey len: %w", err)
	}
	if scriptLen > 10_000 {
		return RawTxOutput{}, fmt.Errorf("implausible scriptPubKey length %d", scriptLen)
	}
	script := make([]byte, scriptLen)
	if _, err := io.ReadFull(r, script); err != nil {
		return RawTxOutput{}, fmt.Errorf("scriptPubKey data: %w", err)
	}

	return RawTxOutput{
		ValueSat: valueSat,
		N:        n,
		Address:  extractAddress(script, hrp),
	}, nil
}

// ── Address extraction ────────────────────────────────────────────────────────

// extractAddress returns the human-readable address for standard scriptPubKey
// patterns, or "" for OP_RETURN, multisig, and other non-standard scripts.
//
// The output encoding matches bitcoinshared.ValidateAndNormalise:
//   - P2WPKH / P2WSH  → bech32  (lowercase, witness version 0)
//   - P2TR            → bech32m (lowercase, witness version 1)
//   - P2PKH           → base58check (mixed-case, version byte 0x00 mainnet / 0x6F testnet)
//   - P2SH            → base58check (mixed-case, version byte 0x05 mainnet / 0xC4 testnet)
//
// hrp selects the network prefix: "bc" mainnet, "tb" testnet4/signet, "bcrt" regtest.
// The P2PKH/P2SH version bytes are derived from the hrp for correct encoding.
func extractAddress(script []byte, hrp string) string {
	switch {
	// P2WPKH: OP_0 PUSH20 <20-byte key hash>  →  0x00 0x14 <20 bytes>
	case len(script) == 22 && script[0] == 0x00 && script[1] == 0x14:
		return bech32EncodeWitness(hrp, 0, script[2:22])

	// P2WSH: OP_0 PUSH32 <32-byte script hash>  →  0x00 0x20 <32 bytes>
	case len(script) == 34 && script[0] == 0x00 && script[1] == 0x20:
		return bech32EncodeWitness(hrp, 0, script[2:34])

	// P2TR: OP_1 PUSH32 <32-byte tweaked pubkey>  →  0x51 0x20 <32 bytes>
	case len(script) == 34 && script[0] == 0x51 && script[1] == 0x20:
		return bech32EncodeWitness(hrp, 1, script[2:34])

	// P2PKH: OP_DUP OP_HASH160 PUSH20 <20 bytes> OP_EQUALVERIFY OP_CHECKSIG
	//        0x76  0xa9         0x14   ...         0x88           0xac
	case len(script) == 25 &&
		script[0] == 0x76 && script[1] == 0xa9 && script[2] == 0x14 &&
		script[23] == 0x88 && script[24] == 0xac:
		ver := p2pkhVersion(hrp)
		return base58CheckEncode(ver, script[3:23])

	// P2SH: OP_HASH160 PUSH20 <20 bytes> OP_EQUAL
	//       0xa9        0x14   ...         0x87
	case len(script) == 23 &&
		script[0] == 0xa9 && script[1] == 0x14 && script[22] == 0x87:
		ver := p2shVersion(hrp)
		return base58CheckEncode(ver, script[2:22])

	default:
		return ""
	}
}

// p2pkhVersion returns the P2PKH version byte for the given HRP.
// Mainnet=0x00, testnet/regtest=0x6F.
func p2pkhVersion(hrp string) byte {
	if hrp == "bc" {
		return 0x00
	}
	return 0x6F
}

// p2shVersion returns the P2SH version byte for the given HRP.
// Mainnet=0x05, testnet/regtest=0xC4.
func p2shVersion(hrp string) byte {
	if hrp == "bc" {
		return 0x05
	}
	return 0xC4
}

// ── Bech32 / Bech32m encoding (BIP 173 / BIP 350) ────────────────────────────
//
// Stdlib-only implementation. No external dependency on btcutil or any bech32 library.

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

// bech32EncodeWitness encodes a witness program as a bech32 (version 0) or
// bech32m (version 1+) address string, matching Bitcoin Core's output format.
func bech32EncodeWitness(hrp string, witVersion byte, program []byte) string {
	// Convert 8-bit program bytes → 5-bit groups (base32), prepend witness version.
	data := make([]byte, 0, 1+(len(program)*8+4)/5)
	data = append(data, witVersion) // witness version as-is (already 0–16)

	acc, bits := 0, 0
	for _, b := range program {
		acc = (acc << 8) | int(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			data = append(data, byte((acc>>bits)&0x1f))
		}
	}
	if bits > 0 {
		data = append(data, byte((acc<<(5-bits))&0x1f))
	}

	useBech32m := witVersion != 0
	chk := bech32Checksum(hrp, data, useBech32m)

	// Pre-allocate strings.Builder capacity: hrp + separator + data + checksum
	var sb strings.Builder
	sb.Grow(len(hrp) + 1 + len(data) + len(chk))
	sb.WriteString(hrp)
	sb.WriteByte('1') // separator
	for _, b := range data {
		sb.WriteByte(bech32Charset[b])
	}
	for _, b := range chk {
		sb.WriteByte(bech32Charset[b])
	}
	return sb.String()
}

// bech32Checksum computes the 6-character bech32/bech32m checksum.
// UseBech32m=true selects the BIP 350 constant; false selects BIP 173.
func bech32Checksum(hrp string, data []byte, useBech32m bool) [6]byte {
	// Build the values slice: HRP expanded + data + 6 zero bytes for checksum slot.
	vals := make([]byte, 0, len(hrp)*2+1+len(data)+6)
	for _, c := range hrp {
		vals = append(vals, byte(c>>5)) //nolint:gosec // c is ASCII rune, safe to convert to byte
	}
	vals = append(vals, 0)
	for _, c := range hrp {
		vals = append(vals, byte(c&0x1f))
	}
	vals = append(vals, data...)
	vals = append(vals, 0, 0, 0, 0, 0, 0)

	var constant uint32 = 1
	if useBech32m {
		constant = 0x2bc830a3
	}
	poly := bech32Polymod(vals) ^ constant

	var chk [6]byte
	for i := range chk {
		//nolint:gosec // G115: uint8→int shift is safe; all inputs are ASCII
		//              // printable characters validated by the bech32 charset.
		chk[i] = byte((poly >> uint(5*(5-i))) & 0x1f)
	}
	return chk
}

// bech32Polymod computes the BCH polynomial checksum per BIP 173.
func bech32Polymod(values []byte) uint32 {
	gen := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := uint32(1)
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i, g := range gen {
			if (top>>uint(i))&1 != 0 {
				chk ^= g
			}
		}
	}
	return chk
}

// ── Base58Check encoding ──────────────────────────────────────────────────────

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// base58CheckEncode encodes a version byte + payload as a Bitcoin Base58Check
// address string. Used for P2PKH (version 0x00/0x6F) and P2SH (0x05/0xC4).
func base58CheckEncode(version byte, payload []byte) string {
	// Prepend version byte
	full := make([]byte, 0, 1+len(payload)+4)
	full = append(full, version)
	full = append(full, payload...)

	// Append 4-byte checksum = first 4 bytes of SHA256(SHA256(full))
	chk := doubleSHA256(full)
	full = append(full, chk[0], chk[1], chk[2], chk[3])

	// Count leading zero bytes → one leading '1' per zero byte
	leadingOnes := 0
	for _, b := range full {
		if b != 0x00 {
			break
		}
		leadingOnes++
	}

	// Big-integer base58 encoding via repeated division
	// digits accumulates base-58 digits in reverse (least-significant first)
	digits := make([]byte, 0, len(full)*136/100+1)
	for _, b := range full {
		carry := int(b)
		for i := range digits {
			carry += 256 * int(digits[i])
			digits[i] = byte(carry % 58)
			carry /= 58
		}
		for carry > 0 {
			digits = append(digits, byte(carry%58))
			carry /= 58
		}
	}

	// Build final string: leading '1's first, then digits in reverse order
	out := make([]byte, leadingOnes, leadingOnes+len(digits))
	for i := range out {
		out[i] = '1'
	}
	for i := len(digits) - 1; i >= 0; i-- {
		out = append(out, base58Alphabet[digits[i]])
	}
	return string(out)
}

// ── SHA-256 helpers ───────────────────────────────────────────────────────────

// doubleSHA256 computes SHA256(SHA256(b)), the digest used for Bitcoin
// transaction IDs (txid) and script hashes per BIP-141.
func doubleSHA256(data []byte) [32]byte {
	h1 := sha256.Sum256(data)
	return sha256.Sum256(h1[:])
}

// ── io.Reader helpers ─────────────────────────────────────────────────────────

// pushBackReader is a minimal buffered reader that supports pushing back at most
// 2 bytes. Used to "un-read" the SegWit marker/flag bytes when the transaction
// turns out to be a legacy (non-SegWit) format.
type pushBackReader struct {
	data   []byte
	pos    int
	pushed [2]byte
	nPush  int
}

func newPushBackReader(data []byte) *pushBackReader {
	return &pushBackReader{data: data}
}

func (r *pushBackReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	// Drain pushback buffer first.
	if r.nPush > 0 {
		n := copy(p, r.pushed[:r.nPush])
		// Shift remaining pushed bytes left.
		if n < r.nPush {
			copy(r.pushed[:], r.pushed[n:r.nPush])
		}
		r.nPush -= n
		return n, nil
	}
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

// pushBack puts at most 2 bytes back into the read buffer.
// Panics if more than 2 bytes are pushed — this is an internal invariant.
func (r *pushBackReader) pushBack(b ...byte) {
	if len(b)+r.nPush > 2 {
		panic("pushBackReader: pushback buffer overflow (max 2 bytes)")
	}
	// Insert at front: shift existing bytes right, prepend new ones.
	for i := r.nPush - 1; i >= 0; i-- {
		r.pushed[i+len(b)] = r.pushed[i]
	}
	copy(r.pushed[:], b)
	r.nPush += len(b)
}

// Skip advances the read position by n bytes without allocating a throwaway
// buffer. This is an O(1) operation for all callers since pushBackReader is
// an in-memory slice — all data is already in RAM.
//
// Skip panics if there are any pushed-back bytes (nPush > 0). The invariant
// is that Skip is only called after all pushed-back bytes have been consumed
// by a prior read. This is currently guaranteed: pushBack is called once
// (to un-read the SegWit marker/flag), and the pushed bytes are fully consumed
// by the next readVarInt before any skipN call. However, documenting and
// enforcing this invariant protects against future regressions.
func (r *pushBackReader) Skip(n uint64) error {
	if r.nPush > 0 {
		panic("pushBackReader.Skip: called with buffered pushback bytes (must consume with Read first)")
	}
	if n == 0 {
		return nil
	}
	// Avoid overflow: if n > remaining bytes, return error.
	// This handles the case where n > math.MaxInt on 32-bit platforms.
	if n > uint64(len(r.data)-r.pos) { //nolint:gosec // safe: len(r.data) >= r.pos, no overflow
		return io.ErrUnexpectedEOF
	}
	r.pos += int(n) //nolint:gosec // n already validated to fit in int range
	return nil
}

// readUint32LE reads a 4-byte little-endian uint32.
func readUint32LE(r io.Reader) (uint32, error) {
	var buf [4]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(buf[:]), nil
}

// readVarInt reads a Bitcoin compact-size (variable-length) integer.
//
// Encoding:
//   - 0x00–0xFC  → single byte value
//   - 0xFD       → followed by 2-byte LE uint16
//   - 0xFE       → followed by 4-byte LE uint32
//   - 0xFF       → followed by 8-byte LE uint64
func readVarInt(r io.Reader) (uint64, error) {
	var first [1]byte
	if _, err := io.ReadFull(r, first[:]); err != nil {
		return 0, fmt.Errorf("varint prefix: %w", err)
	}
	switch first[0] {
	case 0xfd:
		var buf [2]byte
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return 0, fmt.Errorf("varint uint16: %w", err)
		}
		return uint64(binary.LittleEndian.Uint16(buf[:])), nil
	case 0xfe:
		var buf [4]byte
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return 0, fmt.Errorf("varint uint32: %w", err)
		}
		return uint64(binary.LittleEndian.Uint32(buf[:])), nil
	case 0xff:
		var buf [8]byte
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return 0, fmt.Errorf("varint uint64: %w", err)
		}
		return binary.LittleEndian.Uint64(buf[:]), nil
	default:
		return uint64(first[0]), nil
	}
}

// skipN advances past n bytes in r without allocating any buffer.
// The maxSkip guard is applied here so pushBackReader.Skip stays a simple
// bounds check reusable independently of the guard.
//
// H1: changed to accept *pushBackReader instead of io.Reader to eliminate the
// throwaway heap allocation that io.ReadFull(r, make([]byte, n)) caused on
// every scriptSig, witness-item, and sequence-field skip.
func skipN(r *pushBackReader, n uint64) error {
	const maxSkip = 4 << 20 // 4 MiB safety guard
	if n > maxSkip {
		return fmt.Errorf("skip size %d exceeds 4 MiB guard", n)
	}
	return r.Skip(n)
}
