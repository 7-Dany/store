// Package crypto provides AES-256-GCM encryption and decryption for sensitive fields stored at rest.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

// KeySize is the required AES-256 key length in bytes.
// Pass a slice of exactly this length to New; any other length returns ErrInvalidKeySize.
const KeySize = 32

// NonceSize is the GCM nonce length in bytes. The first NonceSize bytes of every
// ciphertext produced by Encrypt contain the nonce; the remainder is ciphertext+tag.
const NonceSize = 12

const prefix = "enc:"

// ErrInvalidKeySize is returned by New when the provided key is not exactly 32 bytes.
var ErrInvalidKeySize = errors.New("key must be exactly 32 bytes")

// ErrWeakKey is returned by New when the provided key is all zero bytes,
// which is almost certainly a startup misconfiguration (e.g. an unset env var).
var ErrWeakKey = errors.New("key must not be all zero bytes")

// ErrMissingSentinel is returned by Decrypt when the ciphertext value does
// not begin with the expected "enc:" prefix.
var ErrMissingSentinel = errors.New(`value is missing enc: sentinel — was it encrypted?`)

// ErrCiphertextTooShort is returned by Decrypt when the base64-decoded payload
// is shorter than the GCM nonce size and therefore cannot contain valid ciphertext.
var ErrCiphertextTooShort = errors.New("ciphertext too short")

// Encryptor holds the AES-256-GCM cipher block built from a 32-byte key.
// Create one at startup (e.g. from TOKEN_ENCRYPTION_KEY env var) and
// inject it wherever OAuth tokens are read or written.
type Encryptor struct {
	aead cipher.AEAD
}

// injectable for testing
var (
	newCipher  = func(key []byte) (cipher.Block, error) { return aes.NewCipher(key) }
	newGCM     = func(block cipher.Block) (cipher.AEAD, error) { return cipher.NewGCM(block) }
	randReader io.Reader = rand.Reader
)

// New creates an Encryptor from a 32-byte AES-256 key.
// Returns ErrInvalidKeySize if key is not exactly 32 bytes.
// Returns ErrWeakKey if key is all zero bytes (misconfiguration guard).
func New(key []byte) (*Encryptor, error) {
	if len(key) != 32 {
		return nil, telemetry.Crypto("New.invalid_key_size", fmt.Errorf("%w: got %d", ErrInvalidKeySize, len(key)))
	}

	// Security: an all-zero key almost always means the env var was not loaded.
	// A zero key is cryptographically weak and should never reach production.
	allZero := true
	for _, b := range key {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return nil, telemetry.Crypto("New.weak_key", ErrWeakKey)
	}

	block, err := newCipher(key)
	if err != nil {
		return nil, telemetry.Crypto("New.aes_new_cipher", err)
	}

	aead, err := newGCM(block)
	if err != nil {
		return nil, telemetry.Crypto("New.cipher_new_gcm", err)
	}

	return &Encryptor{aead: aead}, nil
}

// Encrypt encrypts plaintext with AES-256-GCM and returns a base64-encoded string
// prefixed with "enc:". The output embeds a random 12-byte nonce so encrypting the
// same plaintext twice always produces different ciphertexts. No additional
// authenticated data (AAD) is bound to the ciphertext; a value encrypted for one
// field decrypts successfully if moved to another field protected by the same key.
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, e.aead.NonceSize())
	// Security: the nonce must come from a cryptographically secure source.
	// Using math/rand here would make every ciphertext trivially predictable and
	// break GCM confidentiality. randReader is crypto/rand.Reader in production
	// and is swapped for a deterministic source only in unit tests.
	if _, err := io.ReadFull(randReader, nonce); err != nil {
		return "", telemetry.Crypto("Encrypt.generate_nonce", err)
	}

	// Seal appends ciphertext+tag to nonce in one allocation.
	ciphertext := e.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	encoded := base64.StdEncoding.EncodeToString(ciphertext)

	return prefix + encoded, nil
}

// Decrypt decrypts a value produced by Encrypt.
// Returns ErrMissingSentinel if the "enc:" prefix is absent, ErrCiphertextTooShort if
// the decoded payload is smaller than the GCM nonce, or a wrapped error if base64
// decoding fails or GCM authentication fails (indicating tampering or a wrong key).
func (e *Encryptor) Decrypt(ciphertext string) (string, error) {
	if len(ciphertext) < len(prefix) || ciphertext[:len(prefix)] != prefix {
		return "", telemetry.Crypto("Decrypt.missing_sentinel", ErrMissingSentinel)
	}

	data, err := base64.StdEncoding.DecodeString(ciphertext[len(prefix):])
	if err != nil {
		return "", telemetry.Crypto("Decrypt.base64_decode", err)
	}

	nonceSize := e.aead.NonceSize()
	if len(data) < nonceSize {
		return "", telemetry.Crypto("Decrypt.ciphertext_too_short", ErrCiphertextTooShort)
	}

	nonce, data := data[:nonceSize], data[nonceSize:]
	plaintext, err := e.aead.Open(nil, nonce, data, nil)
	if err != nil {
		return "", telemetry.Crypto("Decrypt.decrypt_authenticate", err)
	}

	return string(plaintext), nil
}
