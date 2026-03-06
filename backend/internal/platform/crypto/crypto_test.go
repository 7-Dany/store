package crypto

import (
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
)

// validKey returns a 32-byte key for testing.
func validKey() []byte {
	return []byte("12345678901234567890123456789012") // exactly 32 bytes
}

// ── helpers ─────────────────────────────────────────────────────────────────────

type errorReader struct{ err error }

func (r *errorReader) Read(p []byte) (int, error) { return 0, r.err }

// ── New ───────────────────────────────────────────────────────────────────────

func TestNew_ValidKey(t *testing.T) {
	enc, err := New(validKey())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if enc == nil {
		t.Fatal("expected non-nil Encryptor")
	}
}

func TestNew_KeyTooShort(t *testing.T) {
	_, err := New([]byte("shortkey"))
	if err == nil {
		t.Fatal("expected error for short key")
	}
	if !errors.Is(err, ErrInvalidKeySize) {
		t.Errorf("expected ErrInvalidKeySize, got %v", err)
	}
}

func TestNew_KeyTooLong(t *testing.T) {
	_, err := New([]byte("this-key-is-definitely-longer-than-32-bytes"))
	if err == nil {
		t.Fatal("expected error for long key")
	}
	if !errors.Is(err, ErrInvalidKeySize) {
		t.Errorf("expected ErrInvalidKeySize, got %v", err)
	}
}

// TestNew_BoundaryKeySizes verifies that the off-by-one boundary around the
// required 32-byte key length is enforced on both sides.
func TestNew_BoundaryKeySizes(t *testing.T) {
	cases := []struct {
		name string
		key  []byte
	}{
		{"31 bytes", make([]byte, 31)},
		{"33 bytes", make([]byte, 33)},
	}
	for _, tc := range cases {
		_, err := New(tc.key)
		if err == nil {
			t.Errorf("New(%s): expected ErrInvalidKeySize, got nil", tc.name)
			continue
		}
		if !errors.Is(err, ErrInvalidKeySize) {
			t.Errorf("New(%s): expected ErrInvalidKeySize, got %v", tc.name, err)
		}
	}
}

func TestNew_KeyEmpty(t *testing.T) {
	_, err := New([]byte{})
	if err == nil {
		t.Fatal("expected error for empty key")
	}
	if !errors.Is(err, ErrInvalidKeySize) {
		t.Errorf("expected ErrInvalidKeySize, got %v", err)
	}
}

// TestNew_AllZeroKey verifies that a 32-byte all-zero key is rejected with
// ErrWeakKey. An all-zero key almost always means the env var was not loaded.
func TestNew_AllZeroKey(t *testing.T) {
	_, err := New(make([]byte, 32))
	if err == nil {
		t.Fatal("expected error for all-zero key")
	}
	if !errors.Is(err, ErrWeakKey) {
		t.Errorf("expected ErrWeakKey, got %v", err)
	}
}

func TestNew_NewCipherError(t *testing.T) {
	orig := newCipher
	t.Cleanup(func() { newCipher = orig })

	newCipher = func(key []byte) (cipher.Block, error) {
		return nil, errors.New("mock cipher error")
	}

	_, err := New(validKey())
	if err == nil {
		t.Fatal("expected error from newCipher failure")
	}
	if !strings.Contains(err.Error(), "mock cipher error") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNew_NewGCMError(t *testing.T) {
	orig := newGCM
	t.Cleanup(func() { newGCM = orig })

	newGCM = func(block cipher.Block) (cipher.AEAD, error) {
		return nil, errors.New("mock gcm error")
	}

	_, err := New(validKey())
	if err == nil {
		t.Fatal("expected error from newGCM failure")
	}
	if !strings.Contains(err.Error(), "mock gcm error") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ── Encrypt ────────────────────────────────────────────────────────────────────

func TestEncrypt_HasPrefix(t *testing.T) {
	enc, _ := New(validKey())
	result, err := enc.Encrypt("hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(result, prefix) {
		t.Errorf("expected result to start with %q, got %q", prefix, result)
	}
}

func TestEncrypt_NonDeterministic(t *testing.T) {
	enc, _ := New(validKey())
	a, _ := enc.Encrypt("same plaintext")
	b, _ := enc.Encrypt("same plaintext")
	if a == b {
		t.Error("expected two encryptions of the same plaintext to differ (random nonce)")
	}
}

func TestEncrypt_EmptyString(t *testing.T) {
	enc, _ := New(validKey())
	result, err := enc.Encrypt("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(result, prefix) {
		t.Errorf("expected enc: prefix, got %q", result)
	}
}

func TestEncrypt_RandReaderError(t *testing.T) {
	orig := randReader
	t.Cleanup(func() { randReader = orig })

	randReader = &errorReader{err: errors.New("mock rand error")}

	enc, _ := New(validKey())
	_, err := enc.Encrypt("plaintext")
	if err == nil {
		t.Fatal("expected error from randReader failure")
	}
	if !strings.Contains(err.Error(), "mock rand error") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ── Decrypt ────────────────────────────────────────────────────────────────────

func TestDecrypt_RoundTrip_SingleByte(t *testing.T) {
	enc, _ := New(validKey())
	const plaintext = "x"
	ct, err := enc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	got, err := enc.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if got != plaintext {
		t.Errorf("expected %q, got %q", plaintext, got)
	}
}

func TestDecrypt_RoundTrip_OAuthTokenLength(t *testing.T) {
	enc, _ := New(validKey())
	// Typical OAuth token is 100–500 bytes; use 200 as a representative size.
	plaintext := strings.Repeat("oauth-token-char", 200/16+1)[:200]
	ct, err := enc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	got, err := enc.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if got != plaintext {
		t.Errorf("round-trip mismatch at OAuth token length")
	}
}

func TestDecrypt_RoundTrip_LargePayload(t *testing.T) {
	enc, _ := New(validKey())
	// Exceeds 1 MiB to cover the large-allocation path in Seal.
	plaintext := strings.Repeat("a", 1<<20+1)
	ct, err := enc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	got, err := enc.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if got != plaintext {
		t.Error("round-trip mismatch for large payload")
	}
}

func TestDecrypt_RoundTrip(t *testing.T) {
	enc, _ := New(validKey())
	plaintext := "super secret token"

	ciphertext, err := enc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	result, err := enc.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	if result != plaintext {
		t.Errorf("expected %q, got %q", plaintext, result)
	}
}

func TestDecrypt_EmptyPlaintext(t *testing.T) {
	enc, _ := New(validKey())
	ciphertext, _ := enc.Encrypt("")
	result, err := enc.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestDecrypt_MissingSentinel(t *testing.T) {
	enc, _ := New(validKey())
	_, err := enc.Decrypt("notencryptedvalue")
	if err == nil {
		t.Fatal("expected error for missing sentinel")
	}
	if !errors.Is(err, ErrMissingSentinel) {
		t.Errorf("expected ErrMissingSentinel, got %v", err)
	}
}

func TestDecrypt_EmptyInput(t *testing.T) {
	enc, _ := New(validKey())
	_, err := enc.Decrypt("")
	if err == nil {
		t.Fatal("expected error for empty input")
	}
	if !errors.Is(err, ErrMissingSentinel) {
		t.Errorf("expected ErrMissingSentinel, got %v", err)
	}
}

func TestDecrypt_InvalidBase64(t *testing.T) {
	enc, _ := New(validKey())
	_, err := enc.Decrypt("enc:!!!not-valid-base64!!!")
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

func TestDecrypt_CiphertextTooShort(t *testing.T) {
	enc, _ := New(validKey())
	// base64 of a single byte — shorter than nonce size (12 bytes)
	_, err := enc.Decrypt("enc:AA==")
	if err == nil {
		t.Fatal("expected error for ciphertext too short")
	}
	if !errors.Is(err, ErrCiphertextTooShort) {
		t.Errorf("expected ErrCiphertextTooShort, got %v", err)
	}
}

func TestDecrypt_TamperedCiphertext(t *testing.T) {
	enc, _ := New(validKey())
	ciphertext, _ := enc.Encrypt("sensitive data")

	payload := ciphertext[len(prefix):]
	corrupted := prefix + payload[:len(payload)-4] + "AAAA"

	_, err := enc.Decrypt(corrupted)
	if err == nil {
		t.Fatal("expected error for tampered ciphertext")
	}
}

// TestDecrypt_TamperedNonce flips a byte within the nonce (first 12 bytes of the
// decoded payload) and asserts that GCM authentication rejects the result.
// This is distinct from TestDecrypt_TamperedCiphertext which corrupts the tag.
func TestDecrypt_TamperedNonce(t *testing.T) {
	enc, _ := New(validKey())
	ciphertext, err := enc.Encrypt("nonce tamper test")
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	// Decode the raw payload, flip the first nonce byte, re-encode.
	raw, err := base64.StdEncoding.DecodeString(ciphertext[len(prefix):])
	if err != nil {
		t.Fatalf("base64 decode failed: %v", err)
	}
	raw[0] ^= 0xFF // nonce occupies bytes 0–11
	corrupted := prefix + base64.StdEncoding.EncodeToString(raw)

	_, err = enc.Decrypt(corrupted)
	if err == nil {
		t.Fatal("expected error for tampered nonce, got nil")
	}
}

func TestDecrypt_WrongKey(t *testing.T) {
	enc1, _ := New(validKey())
	enc2, _ := New([]byte("99999999999999999999999999999999"))

	ciphertext, _ := enc1.Encrypt("secret")
	_, err := enc2.Decrypt(ciphertext)
	if err == nil {
		t.Fatal("expected error when decrypting with wrong key")
	}
}

// TestDecrypt_OnlyPrefix verifies that "enc:" with no payload is rejected with
// ErrCiphertextTooShort: empty base64 decodes to zero bytes, which is less than
// the 12-byte GCM nonce requirement.
func TestDecrypt_OnlyPrefix(t *testing.T) {
	enc, _ := New(validKey())
	_, err := enc.Decrypt("enc:")
	if err == nil {
		t.Fatal("expected error for enc: with no payload")
	}
	if !errors.Is(err, ErrCiphertextTooShort) {
		t.Errorf("expected ErrCiphertextTooShort, got %v", err)
	}
}

// ── Concurrency ────────────────────────────────────────────────────────────────

// TestEncryptDecrypt_Concurrent verifies that a single Encryptor is safe to use
// from multiple goroutines simultaneously. Run with -race to catch data races.
func TestEncryptDecrypt_Concurrent(t *testing.T) {
	enc, _ := New(validKey())
	const goroutines = 20
	const plaintext = "concurrent payload"

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			ct, err := enc.Encrypt(plaintext)
			if err != nil {
				t.Errorf("Encrypt failed: %v", err)
				return
			}
			got, err := enc.Decrypt(ct)
			if err != nil {
				t.Errorf("Decrypt failed: %v", err)
				return
			}
			if got != plaintext {
				t.Errorf("round-trip mismatch: got %q, want %q", got, plaintext)
			}
		}()
	}
	wg.Wait()
}
