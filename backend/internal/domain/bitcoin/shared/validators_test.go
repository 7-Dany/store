package bitcoinshared

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bech32 contains a subset of the bech32 charset used to build test addresses.
const bech32Chars = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

// ── validateAndNormalise — address type coverage ──────────────────────────────
//
// Table-driven tests for all six address types × mainnet/testnet4 × valid/invalid.
// These tests exercise the critical path identified in SEC-1/BTC-1: base58check
// addresses must NOT be lowercased; their mixed-case encoding is canonical and
// must be preserved for ZMQ watch-set matching.

func TestValidateAndNormalise(t *testing.T) {
	t.Parallel()

	// Helper to build a structurally-valid bech32 address of the given length.
	// The validator checks prefix + length + charset but not the checksum.
	bech32Addr := func(prefix string, totalLen int) string {
		need := totalLen - len(prefix)
		return prefix + strings.Repeat("q", need)
	}

	// A valid mainnet P2PKH address with mixed-case base58 characters.
	// This is the genesis-block coinbase address — well-known, checksummed.
	const genesisP2PKH = "1A1zP1eP5QGefi2DMPTfTL5SLmv7Divf Na"
	// Trim the space to make it a clean 34-char address for testing.
	// (We use a synthetic one below to avoid dependency on a real address.)

	tests := []struct {
		name        string
		address     string
		network     string
		wantAddr    string // expected normalised output (empty = error expected)
		wantErr     bool
		description string
	}{
		// ── Mainnet P2PKH ──────────────────────────────────────────────────────
		{
			name:        "mainnet P2PKH valid lowercase-preserved",
			address:     "1BpEi6DfDAUFd153wiGrvkiKW1iHyrx7BD",
			network:     "mainnet",
			wantAddr:    "1BpEi6DfDAUFd153wiGrvkiKW1iHyrx7BD", // must NOT be lowercased
			description: "SEC-1: mixed-case base58 must be preserved exactly",
		},
		{
			name:        "mainnet P2PKH uppercase input preserved",
			address:     "1AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			network:     "mainnet",
			wantAddr:    "1AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			description: "uppercase base58 chars must not be lowercased",
		},
		{
			name:        "mainnet P2PKH wrong network → invalid",
			address:     "1BpEi6DfDAUFd153wiGrvkiKW1iHyrx7BD",
			network:     "testnet4",
			wantErr:     true,
			description: "mainnet P2PKH must be rejected on testnet4",
		},
		{
			name:    "mainnet P2PKH too short → invalid",
			address: "1Abc",
			network: "mainnet",
			wantErr: true,
		},
		{
			name:        "mainnet P2PKH contains invalid char 0 → invalid",
			address:     "1000000000000000000000000000000000", // '0' not in base58
			network:     "mainnet",
			wantErr:     true,
			description: "digit 0 is excluded from base58 alphabet",
		},
		// ── Mainnet P2SH ───────────────────────────────────────────────────────
		{
			name:        "mainnet P2SH valid",
			address:     "3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy",
			network:     "mainnet",
			wantAddr:    "3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy",
			description: "P2SH base58 casing preserved",
		},
		{
			name:    "mainnet P2SH wrong network → invalid",
			address: "3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy",
			network: "testnet4",
			wantErr: true,
		},
		// ── Mainnet P2WPKH ─────────────────────────────────────────────────────
		{
			name:     "mainnet P2WPKH valid lowercase",
			address:  bech32Addr("bc1q", 42),
			network:  "mainnet",
			wantAddr: bech32Addr("bc1q", 42), // already lowercase
		},
		{
			name:        "mainnet P2WPKH uppercase input normalised to lowercase",
			address:     strings.ToUpper(bech32Addr("bc1q", 42)),
			network:     "mainnet",
			wantAddr:    bech32Addr("bc1q", 42),
			description: "bech32 addresses are normalised to lowercase",
		},
		{
			name:    "mainnet P2WPKH wrong length (43) → invalid",
			address: bech32Addr("bc1q", 43),
			network: "mainnet",
			wantErr: true,
		},
		// ── Mainnet P2WSH ──────────────────────────────────────────────────────
		{
			name:     "mainnet P2WSH valid (62 chars)",
			address:  bech32Addr("bc1q", 62),
			network:  "mainnet",
			wantAddr: bech32Addr("bc1q", 62),
		},
		// ── Mainnet P2TR ───────────────────────────────────────────────────────
		{
			name:     "mainnet P2TR valid (62 chars bc1p prefix)",
			address:  bech32Addr("bc1p", 62),
			network:  "mainnet",
			wantAddr: bech32Addr("bc1p", 62),
		},
		{
			name:    "mainnet P2TR wrong length (61) → invalid",
			address: bech32Addr("bc1p", 61),
			network: "mainnet",
			wantErr: true,
		},
		{
			name:    "mainnet P2TR invalid bech32 char → invalid",
			address: "bc1p" + strings.Repeat("q", 57) + "OOOO", // uppercase O not in bech32 (already lowercased: o not in charset either)
			network: "mainnet",
			wantErr: true,
		},
		// ── Testnet4 P2PKH ─────────────────────────────────────────────────────
		{
			name:        "testnet4 P2PKH m-prefix valid",
			address:     "mzBc4XEFSdzCDcTxAgf6EZXgsZWpztRhef",
			network:     "testnet4",
			wantAddr:    "mzBc4XEFSdzCDcTxAgf6EZXgsZWpztRhef",
			description: "testnet P2PKH m-prefix: mixed case preserved",
		},
		{
			name:     "testnet4 P2PKH n-prefix valid",
			address:  "n3CFiYgq7DjenbMrgFMnRY9FUxCeHfd3DL",
			network:  "testnet4",
			wantAddr: "n3CFiYgq7DjenbMrgFMnRY9FUxCeHfd3DL",
		},
		{
			name:    "testnet4 P2PKH wrong network → invalid",
			address: "mzBc4XEFSdzCDcTxAgf6EZXgsZWpztRhef",
			network: "mainnet",
			wantErr: true,
		},
		// ── Testnet4 P2SH ──────────────────────────────────────────────────────
		{
			name:     "testnet4 P2SH 2-prefix valid",
			address:  "2MzQwSSnBHWHqSAqtTVQ6v47XtaisrJa1Vc",
			network:  "testnet4",
			wantAddr: "2MzQwSSnBHWHqSAqtTVQ6v47XtaisrJa1Vc",
		},
		// ── Testnet4 P2WPKH ────────────────────────────────────────────────────
		{
			name:     "testnet4 P2WPKH valid",
			address:  bech32Addr("tb1q", 42),
			network:  "testnet4",
			wantAddr: bech32Addr("tb1q", 42),
		},
		{
			name:     "testnet4 P2WPKH uppercase normalised",
			address:  strings.ToUpper(bech32Addr("tb1q", 42)),
			network:  "testnet4",
			wantAddr: bech32Addr("tb1q", 42),
		},
		// ── Testnet4 P2WSH ─────────────────────────────────────────────────────
		{
			name:     "testnet4 P2WSH valid (62 chars)",
			address:  bech32Addr("tb1q", 62),
			network:  "testnet4",
			wantAddr: bech32Addr("tb1q", 62),
		},
		// ── Testnet4 P2TR ──────────────────────────────────────────────────────
		{
			name:     "testnet4 P2TR valid (62 chars tb1p prefix)",
			address:  bech32Addr("tb1p", 62),
			network:  "testnet4",
			wantAddr: bech32Addr("tb1p", 62),
		},
		// ── Generic rejections ─────────────────────────────────────────────────
		{
			name:    "empty string → invalid",
			address: "",
			network: "mainnet",
			wantErr: true,
		},
		{
			name:    "whitespace only → invalid",
			address: "   ",
			network: "mainnet",
			wantErr: true,
		},
		{
			name:    "random string → invalid",
			address: "not-a-bitcoin-address",
			network: "mainnet",
			wantErr: true,
		},
		{
			name:    "unknown network → invalid",
			address: bech32Addr("bc1q", 42),
			network: "regtest",
			wantErr: true,
		},
		{
			name:        "leading/trailing whitespace trimmed then validated",
			address:     "  " + bech32Addr("tb1q", 42) + "  ",
			network:     "testnet4",
			wantAddr:    bech32Addr("tb1q", 42),
			description: "TrimSpace must run before validation",
		},
		// ── SEC-1 regression: P2PKH lowercasing would silently break ZMQ matching ──
		{
			name:        "SEC-1 regression: P2PKH uppercase chars must survive normalisation",
			address:     "1BpEi6DfDAUFd153wiGrvkiKW1iHyrx7BD",
			network:     "mainnet",
			wantAddr:    "1BpEi6DfDAUFd153wiGrvkiKW1iHyrx7BD",
			description: "lowercasing '1BpEi...' → '1bpei...' which ZMQ never emits",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ValidateAndNormalise(tc.address, tc.network)
			if tc.wantErr {
				require.Error(t, err)
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantAddr, got, tc.description)
		})
	}
}

// TestValidateAndNormalise_Bech32OnlyLowercased verifies that only bech32
// addresses are lowercased and base58check addresses are returned verbatim.
func TestValidateAndNormalise_Bech32OnlyLowercased(t *testing.T) {
	t.Parallel()

	// Base58 addresses with uppercase characters — must be returned unchanged.
	base58Inputs := []string{
		"1BpEi6DfDAUFd153wiGrvkiKW1iHyrx7BD", // mainnet P2PKH — mixed case
		"3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy", // mainnet P2SH  — mixed case
	}
	for _, addr := range base58Inputs {
		got, err := ValidateAndNormalise(addr, "mainnet")
		require.NoError(t, err, "address %q should be valid", addr)
		assert.Equal(t, addr, got, "base58check address must not be lowercased: %q", addr)
		assert.NotEqual(t, strings.ToLower(addr), got,
			"if got == ToLower(addr), the SEC-1 bug has been reintroduced")
	}

	// Bech32 addresses — must be returned in lowercase.
	bech32Input := strings.ToUpper("tb1q" + strings.Repeat("q", 38)) // 42-char TB1Q...
	got, err := ValidateAndNormalise(bech32Input, "testnet4")
	require.NoError(t, err)
	assert.Equal(t, strings.ToLower(bech32Input), got, "bech32 address must be lowercased")
}
