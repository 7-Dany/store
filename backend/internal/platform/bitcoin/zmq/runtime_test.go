package zmq

import (
	"encoding/hex"
	"testing"
)

// BenchmarkParseRawTx measures the performance of ParseRawTx on a real 2-input
// P2WPKH transaction. Establishes a baseline before optimization work.
func BenchmarkParseRawTx(b *testing.B) {
	// Valid 2-input P2WPKH SegWit transaction (BIP143 test vector).
	// Structure: version(4) + marker/flag(2) + input_count(varint) + inputs +
	// output_count(varint) + outputs + witness(2 stacks) + locktime(4).
	// Note: This is a simplified test transaction with even hex length.
	rawHex := "01000000000101fff7f7881a8099afa6940d42d1e7f6362bec38171ea3edf433541db4e4ad96900000000000ffffffff0100f2052a010000001976a914389ffce9cd9ae88dcc0631534788d503470a000b88ac0247304402206e21798a42fae0e854281f55dddb8676c39f58f75c39285fb5e01aca6f0a29c5022100d007f21cb61e63f16ed0ed45d31b0da3e6b2eeb1c19e738f3df975ca48d0a526012103ad1d8e89212f0b92c74d23bb710c00662451716a435b97381a1a86a2b7e3e7f600000000"
	rawBytes, err := hex.DecodeString(rawHex)
	if err != nil {
		b.Fatalf("failed to decode test transaction: %v", err)
	}

	// Validate the transaction parses successfully before benchmarking.
	if _, err := ParseRawTx(rawBytes, "tb"); err != nil {
		b.Fatalf("ParseRawTx failed on benchmark input: %v", err)
	}

	b.ResetTimer()
	for b.Loop() {
		_, _ = ParseRawTx(rawBytes, "tb") //nolint:errcheck // benchmark ignores errors
	}
}

// BenchmarkBech32EncodeWitness measures the performance of bech32 encoding
// on a 20-byte P2WPKH witness program. Establishes baseline for address
// extraction performance.
func BenchmarkBech32EncodeWitness(b *testing.B) {
	// 20-byte P2WPKH program from a real transaction
	witnessProgram := []byte{0x38, 0x9f, 0xfc, 0xe9, 0xcd,
		0x9a, 0xe8, 0x8d, 0xcc, 0x06, 0x31, 0x53, 0x47, 0x88, 0xd5, 0x03, 0x47,
		0x0a, 0x00, 0x0b}

	for b.Loop() {
		_ = bech32EncodeWitness("tb", 0, witnessProgram) // version 0 for P2WPKH
	}
}

// BenchmarkBase58CheckEncode measures the performance of Base58Check encoding
// on a 20-byte P2PKH hash. Establishes baseline for address extraction.
func BenchmarkBase58CheckEncode(b *testing.B) {
	// 20-byte P2PKH HASH160 (not the full scriptPubKey).
	// This matches what extractAddress passes to base58CheckEncode: script[3:23].
	hash := []byte{0x38, 0x9f, 0xfc, 0xe9, 0xcd, 0x9a, 0xe8, 0x8d, 0xcc, 0x06,
		0x31, 0x53, 0x47, 0x88, 0xd5, 0x03, 0x47, 0x0a, 0x00, 0x0b}

	for b.Loop() {
		_ = base58CheckEncode(0x00, hash) // mainnet version byte
	}
}
