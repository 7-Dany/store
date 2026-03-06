package authshared

import (
	"errors"
	"fmt"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

var (
	dummyPasswordHashOnce  sync.Once
	dummyPasswordHashValue string
)

// HashPassword returns a bcrypt hash of plaintext safe to store in
// users.password_hash.
func HashPassword(plaintext string) (string, error) {
	if plaintext == "" {
		return "", errors.New("authshared.HashPassword: plaintext password must not be empty")
	}
	if bcryptCost != bcrypt.MinCost && bcryptCost < minProductionBcryptCost {
		return "", fmt.Errorf("authshared.HashPassword: bcryptCost %d is below minimum %d", bcryptCost, minProductionBcryptCost)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("authshared.HashPassword: bcrypt: %w", err)
	}
	return string(hash), nil
}

// CheckPassword returns nil if plaintext matches hash, ErrInvalidCredentials
// on mismatch, or a wrapped internal error if the hash is malformed.
func CheckPassword(hash, plaintext string) error {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext))
	if err == nil {
		return nil
	}
	if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		return ErrInvalidCredentials
	}
	if errors.Is(err, bcrypt.ErrHashTooShort) {
		return fmt.Errorf("authshared.CheckPassword: malformed hash: %w", err)
	}
	return fmt.Errorf("authshared.CheckPassword: %w", err)
}

// GetDummyPasswordHash returns a stable bcrypt hash of a fixed sentinel
// password, computed exactly once on first call. Used on the no-rows path of
// service.Login to equalise response latency (anti-enumeration).
//
// Always call SetBcryptCostForTest in TestMain before the first invocation.
func GetDummyPasswordHash() string {
	dummyPasswordHashOnce.Do(func() {
		h, err := HashPassword("Dummy!P@ssw0rd#1")
		if err != nil {
			panic("authshared: cannot init dummyPasswordHash: " + err.Error())
		}
		dummyPasswordHashValue = h
	})
	return dummyPasswordHashValue
}

// SetBcryptCostForTest lowers the bcrypt work factor for password hashing to
// bcrypt.MinCost so that tests run quickly.
// Must only be called from TestMain, before any test function runs.
func SetBcryptCostForTest(cost int) {
	if cost != bcrypt.MinCost {
		panic(fmt.Sprintf("authshared.SetBcryptCostForTest: cost must be bcrypt.MinCost (%d), got %d", bcrypt.MinCost, cost))
	}
	bcryptCost = cost
}

// SetBcryptCostUnsafeForTest sets bcryptCost to an arbitrary value so tests
// can exercise the "cost too low" guard in HashPassword / GenerateCodeHash.
// Must be restored after use (e.g. via t.Cleanup).
func SetBcryptCostUnsafeForTest(cost int) { bcryptCost = cost }
