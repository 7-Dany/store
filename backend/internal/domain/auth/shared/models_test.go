package authshared_test

import (
	"testing"
	"time"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ── NewVerificationToken ──────────────────────────────────────────────────────

func TestNewVerificationToken(t *testing.T) {
	id := [16]byte(uuid.New())
	userID := [16]byte(uuid.New())
	email := "user@example.com"
	codeHash := "$2a$04$somehash"
	attempts := int16(2)
	maxAttempts := int16(5)
	expiresAt := time.Now().UTC().Add(15 * time.Minute).Truncate(time.Second)

	tok := authshared.NewVerificationToken(id, userID, email, codeHash, attempts, maxAttempts, expiresAt)

	require.Equal(t, id, tok.ID)
	require.Equal(t, userID, tok.UserID)
	require.Equal(t, email, tok.Email)
	require.Equal(t, codeHash, tok.CodeHash)
	require.Equal(t, attempts, tok.Attempts)
	require.Equal(t, maxAttempts, tok.MaxAttempts)
	require.Equal(t, expiresAt, tok.ExpiresAt)
}

// ── NewOTPIssuanceResult ──────────────────────────────────────────────────────

func TestNewOTPIssuanceResult(t *testing.T) {
	id := [16]byte(uuid.New())
	email := "user@example.com"
	rawCode := "123456"

	result := authshared.NewOTPIssuanceResult(id, email, rawCode)

	require.Equal(t, uuid.UUID(id).String(), result.UserID)
	require.Equal(t, email, result.Email)
	require.Equal(t, rawCode, result.RawCode)
}

func TestNewOTPIssuanceResult_EmptyCode(t *testing.T) {
	// Empty RawCode signals the anti-enumeration suppression path.
	id := [16]byte(uuid.New())
	result := authshared.NewOTPIssuanceResult(id, "ghost@example.com", "")
	require.Empty(t, result.RawCode)
	require.Equal(t, uuid.UUID(id).String(), result.UserID)
}
