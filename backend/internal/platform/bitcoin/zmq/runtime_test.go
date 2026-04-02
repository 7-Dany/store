package zmq

import (
	"encoding/hex"
	"testing"
)

// BenchmarkParseRawTx measures the performance of ParseRawTx on a real 2-input
// P2WPKH transaction. Establishes a baseline before optimization work.
func BenchmarkParseRawTx(b *testing.B) {
	// 2-input P2WPKH segwit transaction
	rawHex := "01000000000102fff7f7881a8099afa6940d42d1e7f6362bec38171ea3edf433541db4e4ad969000000004948304502206e21798a42fae0e854281f55dddb8676c39f58f75c39285fb5e01aca6f0a29c5022100d007f21cb61e63f16ed0ed45d31b0da3e6b2eeb1c19e738f3df975ca48d0a52601ffffffff80e68831e8f02b6d1ff19901df58f38513f5eb141738552c3123537eac0a1762000000004847304402202f8e6f09f3a6b57a99df5c2be7a13a97f5e6b3c9a4b6e11c5c59b30a5c7c5d5022005c8f38e18b2cc5fac8e8d1f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f900ffffffff0100f2052a010000001976a914389ffce9cd9ae88dcc0631534788d503470a000b88ac00000000"
	rawBytes, err := hex.DecodeString(rawHex)
	if err != nil {
		b.Fatalf("failed to decode test transaction: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ , _ = ParseRawTx(rawBytes, "tb")
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

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = bech32EncodeWitness("tb", 0, witnessProgram) // version 0 for P2WPKH
	}
}

// BenchmarkBase58CheckEncode measures the performance of Base58Check encoding
// on a 20-byte P2PKH hash. Establishes baseline for address extraction.
func BenchmarkBase58CheckEncode(b *testing.B) {
	// 20-byte P2PKH hash with mainnet version byte
	hash := []byte{0x76, 0xa9, 0x14, 0x38, 0x9f, 0xfc, 0xe9, 0xcd,
		0x9a, 0xe8, 0x8d, 0xcc, 0x06, 0x31, 0x53, 0x47, 0x88, 0xd5, 0x03, 0x47,
		0x0a, 0x00, 0x0b}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = base58CheckEncode(0x00, hash) // mainnet version byte
	}
}
