package zmq

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

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
//   - TxIDBytes: raw 32-byte txid in ZMQ little-endian byte order; call TxIDHex() for RPC hex.
//   - Inputs: decoded prevouts for RBF detection (empty PrevTxIDHex = coinbase).
//   - Outputs: decoded values + addresses for watch-address matching.
//
// Address encoding: Output.Address is encoded in the same format as
// bitcoinshared.ValidateAndNormalise — bech32 (P2WPKH/P2WSH) and bech32m (P2TR)
// are lowercased; P2PKH and P2SH are base58check. Empty string for OP_RETURN or
// non-standard scripts. The network HRP must be configured via SetNetwork() once
// at startup before any ZMQ messages are processed.
//
// BYTE ORDER: TxIDBytes is in ZMQ little-endian order. Always call TxIDHex() for
// RPC-compatible big-endian hex — the same caveat as TxEvent.HashHex().
type RawTxEvent struct {
	// TxIDBytes is the txid in ZMQ internal (little-endian) byte order.
	TxIDBytes [32]byte

	// Sequence is the monotonically increasing ZMQ sequence number for the rawtx
	// topic. Tracked independently from the hashblock and hashtx sequences.
	Sequence uint32

	// Inputs is the decoded list of transaction inputs.
	Inputs []RawTxInput

	// Outputs is the decoded list of transaction outputs.
	Outputs []RawTxOutput
}

// TxIDHex returns the txid in RPC-compatible big-endian hex encoding.
// Use this for all GetMempoolEntry and watch-address lookups.
func (e RawTxEvent) TxIDHex() string {
	var rev [32]byte
	for i, b := range e.TxIDBytes {
		rev[31-i] = b
	}
	return hex.EncodeToString(rev[:])
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

// ── Network HRP configuration ─────────────────────────────────────────────────

// activeHRP is the bech32 human-readable part for the active network.
// Set once at startup via SetNetwork(). Default "tb" is safe for testnet4.
var activeHRP = "tb"

// SetNetwork configures the bech32 HRP used by address extraction in ParseRawTx.
// Must be called exactly once at startup — before the ZMQ subscriber starts —
// from routes.go or server.go.
//
// Mapping:
//   - "mainnet"  → HRP "bc"
//   - "testnet4" → HRP "tb"
//   - "signet"   → HRP "tb"
//   - "regtest"  → HRP "bcrt"
//   - anything else → HRP "tb" (safe default)
func SetNetwork(n string) {
	switch n {
	case "mainnet":
		activeHRP = "bc"
	case "regtest":
		activeHRP = "bcrt"
	default:
		activeHRP = "tb"
	}
}

// ── ParseRawTx ────────────────────────────────────────────────────────────────

// ParseRawTx decodes a Bitcoin transaction from its wire-format byte slice and
// returns a RawTxEvent with the txid, inputs, and outputs populated.
//
// The txid is computed as double-SHA256 of the full raw bytes (Bitcoin's standard
// txid definition). The result is stored in little-endian (ZMQ) byte order;
// call TxIDHex() for RPC big-endian hex.
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
func ParseRawTx(raw []byte) (RawTxEvent, error) {
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
	peek := make([]byte, 2)
	if n, err := io.ReadFull(r, peek); err != nil || n < 2 {
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
	for i := uint64(0); i < inputCount; i++ {
		in, err := parseTxInput(r)
		if err != nil {
			return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: input[%d]: %w", i, err)
		}
		inputs = append(inputs, in)
	}

	// Output count
	outputCount, err := readVarInt(r)
	if err != nil {
		return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: output count: %w", err)
	}
	if outputCount > 100_000 {
		return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: implausible output count %d", outputCount)
	}

	outputs := make([]RawTxOutput, 0, outputCount)
	for i := uint64(0); i < outputCount; i++ {
		out, err := parseTxOutput(r, uint32(i))
		if err != nil {
			return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: output[%d]: %w", i, err)
		}
		outputs = append(outputs, out)
	}

	// Witness data: one stack per input for SegWit transactions. Skip entirely.
	if isSegWit {
		for i := uint64(0); i < inputCount; i++ {
			stackCount, err := readVarInt(r)
			if err != nil {
				return RawTxEvent{}, fmt.Errorf("zmq.ParseRawTx: witness[%d] stack count: %w", i, err)
			}
			for j := uint64(0); j < stackCount; j++ {
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

	// Compute txid = SHA256(SHA256(raw)) — result is in standard (big-endian) byte
	// order. Store in little-endian to match ZMQ convention; TxIDHex() reverses.
	txidBE := doubleSHA256(raw)
	var txidLE [32]byte
	for i, b := range txidBE {
		txidLE[31-i] = b
	}

	return RawTxEvent{
		TxIDBytes: txidLE,
		Inputs:    inputs,
		Outputs:   outputs,
	}, nil
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
	if err := skipN(r, scriptLen); err != nil {
		return RawTxInput{}, fmt.Errorf("scriptSig data: %w", err)
	}

	// Sequence: 4 bytes LE — skip
	if _, err := readUint32LE(r); err != nil {
		return RawTxInput{}, fmt.Errorf("sequence: %w", err)
	}

	// Coinbase: all-zero prevout txid AND vout == 0xFFFFFFFF
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
func parseTxOutput(r *pushBackReader, n uint32) (RawTxOutput, error) {
	// Value: 8 bytes LE (satoshis)
	var valueBuf [8]byte
	if _, err := io.ReadFull(r, valueBuf[:]); err != nil {
		return RawTxOutput{}, fmt.Errorf("value: %w", err)
	}
	valueSat := int64(binary.LittleEndian.Uint64(valueBuf[:]))

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
		Address:  extractAddress(script, activeHRP),
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
// mainnet=0x00, testnet/regtest=0x6F.
func p2pkhVersion(hrp string) byte {
	if hrp == "bc" {
		return 0x00
	}
	return 0x6F
}

// p2shVersion returns the P2SH version byte for the given HRP.
// mainnet=0x05, testnet/regtest=0xC4.
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

	var sb strings.Builder
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
// useBech32m=true selects the BIP 350 constant; false selects BIP 173.
func bech32Checksum(hrp string, data []byte, useBech32m bool) [6]byte {
	// Build the values slice: HRP expanded + data + 6 zero bytes for checksum slot.
	vals := make([]byte, 0, len(hrp)*2+1+len(data)+6)
	for i := 0; i < len(hrp); i++ {
		vals = append(vals, hrp[i]>>5)
	}
	vals = append(vals, 0)
	for i := 0; i < len(hrp); i++ {
		vals = append(vals, hrp[i]&0x1f)
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
	full := make([]byte, 1+len(payload))
	full[0] = version
	copy(full[1:], payload)

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

// doubleSHA256 returns SHA256(SHA256(data)) — Bitcoin's standard hash function.
// The result is in natural (big-endian) byte order.
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

// skipN reads and discards exactly n bytes from r.
func skipN(r io.Reader, n uint64) error {
	const maxSkip = 4 << 20 // 4 MiB safety guard
	if n > maxSkip {
		return fmt.Errorf("skip size %d exceeds 4 MiB guard", n)
	}
	if n == 0 {
		return nil
	}
	_, err := io.ReadFull(r, make([]byte, n))
	return err
}
