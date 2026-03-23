package bitcoinshared

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// ── Address normalisation and validation ──────────────────────────────────────

// ValidateAndNormalise trims whitespace, selectively lowercases, and validates
// the address format against the given network. Returns the normalised address
// or ErrInvalidAddress.
//
// Supported address types (D-24):
//   - P2PKH  (base58check): starts with "1" (mainnet) or "m"/"n" (testnet4)
//   - P2SH   (base58check): starts with "3" (mainnet) or "2" (testnet4)
//   - P2WPKH / P2WSH (bech32 segwit v0): starts with "bc1" (mainnet) or "tb1" (testnet4)
//   - P2TR   (bech32m taproot): starts with "bc1p" (mainnet) or "tb1p" (testnet4)
//
// Normalisation rules:
//   - Bech32/bech32m addresses (bc1*, tb1*) are lowercased. ZMQ emits these in
//     lowercase, and the watch-set lookup uses exact string matching.
//   - Base58check addresses (P2PKH, P2SH) are NOT lowercased. Their encoding is
//     case-sensitive — the mixed upper/lower-case characters are derived from the
//     checksum. Lowercasing would produce a string that ZMQ never emits and that
//     would never produce a match in the watch set.
func ValidateAndNormalise(address, network string) (string, error) {
	address = strings.TrimSpace(address)
	// Only lowercase bech32 and bech32m (segwit) addresses.
	// Base58check addresses must preserve their original casing.
	if isBech32Address(address) {
		address = strings.ToLower(address)
	}
	if !isValidBitcoinAddress(address, network) {
		return "", ErrInvalidAddress
	}
	return address, nil
}

// isBech32Address reports whether addr uses bech32 or bech32m encoding
// (i.e. a segwit address with the "bc1" or "tb1" human-readable part).
// The check is case-insensitive so that mixed-case or fully-uppercase user
// input is correctly identified before being lowercased.
func isBech32Address(addr string) bool {
	lower := strings.ToLower(addr)
	return strings.HasPrefix(lower, "bc1") || strings.HasPrefix(lower, "tb1")
}

// isValidBitcoinAddress validates a Bitcoin address against the expected network.
// It checks the human-readable prefix and minimum length; it does NOT perform a
// full cryptographic checksum verification (which would require btcsuite/btcutil,
// not present in go.mod).
//
// This is sufficient for the watch registration endpoint: invalid strings are
// rejected; valid-looking addresses with wrong checksums are accepted but would
// never match a real ZMQ event so they cause no harm beyond wasted Redis space.
//
// Precondition: bech32 addresses must already be lowercased by the caller
// (ValidateAndNormalise does this); base58check addresses are received verbatim.
func isValidBitcoinAddress(addr, network string) bool {
	if len(addr) < 26 || len(addr) > 90 {
		return false
	}
	switch network {
	case "mainnet":
		return isMainnetAddress(addr)
	case "testnet4":
		return isTestnet4Address(addr)
	default:
		return false
	}
}

// isMainnetAddress returns true for addresses with a valid mainnet prefix.
// Bech32 branches receive a pre-lowercased input; base58 branches receive the
// original-case input.
func isMainnetAddress(addr string) bool {
	switch {
	case strings.HasPrefix(addr, "bc1p"):
		// P2TR bech32m — witness program is 32 bytes; encoded length is exactly
		// 62 chars: "bc1p" (4) + 52 data chars + 6 checksum chars.
		return len(addr) == 62 && isValidBech32Chars(addr[4:])
	case strings.HasPrefix(addr, "bc1"):
		// P2WPKH (42 chars, 20-byte witness program) or
		// P2WSH  (62 chars, 32-byte witness program).
		rest := addr[3:]
		return (len(addr) == 42 || len(addr) == 62) && isValidBech32Chars(rest)
	case addr[0] == '1':
		// P2PKH base58check — 26–35 chars.
		// Base58 encoding of 25 raw bytes (1 version + 20 hash + 4 checksum)
		// produces 26–35 characters depending on the version byte value.
		return len(addr) >= 26 && len(addr) <= 35 && isValidBase58Chars(addr)
	case addr[0] == '3':
		// P2SH base58check — 26–35 chars (same raw byte length as P2PKH).
		return len(addr) >= 26 && len(addr) <= 35 && isValidBase58Chars(addr)
	}
	return false
}

// isTestnet4Address returns true for addresses with a valid testnet4 prefix.
// Note: testnet4 and testnet3 share identical address prefixes (tb1, m, n, 2).
// A testnet3 address is structurally indistinguishable from a testnet4 address
// at the prefix/length level. Such an address would be accepted but would never
// match a testnet4 ZMQ event — a known, accepted limitation.
func isTestnet4Address(addr string) bool {
	switch {
	case strings.HasPrefix(addr, "tb1p"):
		// P2TR bech32m — exactly 62 chars (tb1p (4) + 52 data + 6 checksum).
		return len(addr) == 62 && isValidBech32Chars(addr[4:])
	case strings.HasPrefix(addr, "tb1"):
		// P2WPKH (42 chars) or P2WSH (62 chars).
		rest := addr[3:]
		return (len(addr) == 42 || len(addr) == 62) && isValidBech32Chars(rest)
	case addr[0] == 'm' || addr[0] == 'n':
		// P2PKH testnet base58check — 26–35 chars.
		return len(addr) >= 26 && len(addr) <= 35 && isValidBase58Chars(addr)
	case addr[0] == '2':
		// P2SH testnet base58check — 26–35 chars.
		// Testnet P2SH uses version byte 0xC4 (196), which is larger than the
		// mainnet value 0x05 (5). The higher version byte shifts the base58
		// encoding and can produce 35-character addresses, so the upper bound
		// must be 35, not 34.
		return len(addr) >= 26 && len(addr) <= 35 && isValidBase58Chars(addr)
	}
	return false
}

// isValidBech32Chars returns true if all characters belong to the bech32
// charset (qpzry9x8gf2tvdw0s3jn54khce6mua7l) — all lowercase.
// Callers must ensure the input has been lowercased before calling this.
func isValidBech32Chars(s string) bool {
	const charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
	for _, c := range s {
		if !strings.ContainsRune(charset, c) {
			return false
		}
	}
	return len(s) > 0
}

// isValidBase58Chars returns true if all characters belong to the base58
// alphabet (123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz).
// The alphabet intentionally excludes 0 (zero), O (uppercase oh), I (uppercase
// eye), and l (lowercase ell) to avoid visual ambiguity.
// Base58check addresses are mixed-case by design; do NOT lowercase before calling
// this function.
func isValidBase58Chars(s string) bool {
	const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	for _, c := range s {
		if !strings.ContainsRune(alphabet, c) {
			return false
		}
	}
	return true
}

// ── Audit HMAC ────────────────────────────────────────────────────────────────

// HmacInvalidAddress computes HMAC-SHA256(key, address) and returns the result
// as a base64-encoded string. The raw address is never stored in audit events
// to avoid retaining address PII — the HMAC allows cross-event correlation for
// abuse detection without exposing the raw value.
//
// Uses crypto/hmac (not raw SHA256 concatenation) to prevent length-extension
// attacks on the key material. The caller must ensure key is non-empty.
func HmacInvalidAddress(key, address string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(address))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
