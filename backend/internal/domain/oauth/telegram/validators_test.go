// Package telegram handles Telegram Login Widget authentication: callback, link,
// and unlink.
package telegram

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

const testBotToken = "1234567890:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef012"

// buildValidHash computes the HMAC-SHA256 hash that the Telegram widget would
// produce for req using botToken. Used to build "golden" test payloads.
func buildValidHash(req telegramCallbackRequest, botToken string) string {
	secretKey := sha256.Sum256([]byte(botToken))

	pairs := []string{
		fmt.Sprintf("auth_date=%d", req.AuthDate),
		fmt.Sprintf("id=%d", req.ID),
	}
	if req.FirstName != "" {
		pairs = append(pairs, "first_name="+req.FirstName)
	}
	if req.LastName != "" {
		pairs = append(pairs, "last_name="+req.LastName)
	}
	if req.Username != "" {
		pairs = append(pairs, "username="+req.Username)
	}
	if req.PhotoURL != "" {
		pairs = append(pairs, "photo_url="+req.PhotoURL)
	}
	sort.Strings(pairs)
	dataCheck := strings.Join(pairs, "\n")

	mac := hmac.New(sha256.New, secretKey[:])
	mac.Write([]byte(dataCheck))
	return hex.EncodeToString(mac.Sum(nil))
}

// freshReq returns a telegramCallbackRequest with an auth_date equal to now
// and a correct HMAC hash for testBotToken.
func freshReq() telegramCallbackRequest {
	req := telegramCallbackRequest{
		ID:        123456789,
		FirstName: "Alice",
		LastName:  "Smith",
		Username:  "alice",
		PhotoURL:  "https://t.me/alice.jpg",
		AuthDate:  time.Now().Unix(),
	}
	req.Hash = buildValidHash(req, testBotToken)
	return req
}

// ─────────────────────────────────────────────────────────────────────────────
// VerifyHMAC
// ─────────────────────────────────────────────────────────────────────────────

// T-V01: valid payload → no error.
func TestVerifyHMAC_ValidPayload(t *testing.T) {
	req := freshReq()
	require.NoError(t, VerifyHMAC(req, testBotToken))
}

// T-V02: wrong bot token → ErrInvalidTelegramSignature.
func TestVerifyHMAC_WrongBotToken(t *testing.T) {
	req := freshReq()
	err := VerifyHMAC(req, "wrong-token")
	assert.ErrorIs(t, err, ErrInvalidTelegramSignature)
}

// T-V03: tampered hash field → ErrInvalidTelegramSignature.
func TestVerifyHMAC_TamperedHash(t *testing.T) {
	req := freshReq()
	req.Hash = strings.Repeat("a", 64) // valid hex, wrong value
	assert.ErrorIs(t, VerifyHMAC(req, testBotToken), ErrInvalidTelegramSignature)
}

// T-V04: non-hex hash → ErrInvalidTelegramSignature (hex decode fails).
func TestVerifyHMAC_NonHexHash(t *testing.T) {
	req := freshReq()
	req.Hash = "not-hex-at-all!!!!"
	assert.ErrorIs(t, VerifyHMAC(req, testBotToken), ErrInvalidTelegramSignature)
}

// T-V05: tampered payload field (first_name changed after signing) →
// ErrInvalidTelegramSignature.
func TestVerifyHMAC_TamperedField(t *testing.T) {
	req := freshReq()
	req.FirstName = "Mallory" // mutate after hash was computed
	assert.ErrorIs(t, VerifyHMAC(req, testBotToken), ErrInvalidTelegramSignature)
}

// T-V06: minimal payload (only required fields; optional fields empty) →
// no error. Ensures optional fields are not included in the data_check_string
// when absent.
func TestVerifyHMAC_MinimalPayload(t *testing.T) {
	req := telegramCallbackRequest{
		ID:       999,
		AuthDate: time.Now().Unix(),
	}
	req.Hash = buildValidHash(req, testBotToken)
	require.NoError(t, VerifyHMAC(req, testBotToken))
}

// T-V07: payload with only first_name set (no last_name / username / photo_url).
func TestVerifyHMAC_OnlyFirstName(t *testing.T) {
	req := telegramCallbackRequest{
		ID:        42,
		FirstName: "Bob",
		AuthDate:  time.Now().Unix(),
	}
	req.Hash = buildValidHash(req, testBotToken)
	require.NoError(t, VerifyHMAC(req, testBotToken))
}

// ─────────────────────────────────────────────────────────────────────────────
// CheckAuthDate
// ─────────────────────────────────────────────────────────────────────────────

// T-V10: auth_date == now → no error.
func TestCheckAuthDate_Now(t *testing.T) {
	require.NoError(t, CheckAuthDate(time.Now().Unix()))
}

// T-V11: auth_date 1 second ago → no error.
func TestCheckAuthDate_OneSecondAgo(t *testing.T) {
	require.NoError(t, CheckAuthDate(time.Now().Unix()-1))
}

// T-V12: auth_date exactly at the 86400-second boundary (still valid) → no error.
func TestCheckAuthDate_AtBoundary(t *testing.T) {
	require.NoError(t, CheckAuthDate(time.Now().Unix()-maxAuthDateAgeSeconds))
}

// T-V13: auth_date one second past the 86400-second boundary → ErrTelegramAuthDateExpired.
func TestCheckAuthDate_JustExpired(t *testing.T) {
	assert.ErrorIs(t, CheckAuthDate(time.Now().Unix()-maxAuthDateAgeSeconds-1), ErrTelegramAuthDateExpired)
}

// T-V14: auth_date far in the past → ErrTelegramAuthDateExpired.
func TestCheckAuthDate_VeryOld(t *testing.T) {
	assert.ErrorIs(t, CheckAuthDate(time.Now().Unix()-7*24*3600), ErrTelegramAuthDateExpired)
}

// T-V15: auth_date exactly at the future-skew boundary (60 s ahead) → no error.
func TestCheckAuthDate_AtFutureBoundary(t *testing.T) {
	require.NoError(t, CheckAuthDate(time.Now().Unix()+maxAuthDateFutureSeconds))
}

// T-V16: auth_date 61 seconds in the future → ErrTelegramAuthDateExpired.
func TestCheckAuthDate_JustOverFuture(t *testing.T) {
	assert.ErrorIs(t, CheckAuthDate(time.Now().Unix()+maxAuthDateFutureSeconds+1), ErrTelegramAuthDateExpired)
}

// T-V17: auth_date far in the future → ErrTelegramAuthDateExpired.
func TestCheckAuthDate_FarFuture(t *testing.T) {
	assert.ErrorIs(t, CheckAuthDate(time.Now().Unix()+3600), ErrTelegramAuthDateExpired)
}

// ─────────────────────────────────────────────────────────────────────────────
// buildDataCheckFields
// ─────────────────────────────────────────────────────────────────────────────

// T-V20: pairs are sorted alphabetically and hash is never included.
func TestBuildDataCheckFields_Sorted(t *testing.T) {
	req := telegramCallbackRequest{
		ID:        1,
		FirstName: "Zara",
		LastName:  "Adams",
		Username:  "zara",
		PhotoURL:  "https://example.com/z.jpg",
		AuthDate:  1700000000,
		Hash:      "deadbeef", // must be excluded
	}
	pairs := buildDataCheckFields(req)

	// Verify sorted.
	for i := 1; i < len(pairs); i++ {
		assert.LessOrEqual(t, pairs[i-1], pairs[i], "pairs must be sorted")
	}

	// Verify hash is absent.
	for _, p := range pairs {
		assert.False(t, strings.HasPrefix(p, "hash="), "hash field must not appear in data_check_string")
	}
}

// T-V21: optional fields absent → pairs contain only auth_date and id.
func TestBuildDataCheckFields_OnlyRequiredFields(t *testing.T) {
	req := telegramCallbackRequest{
		ID:       7,
		AuthDate: 1700000001,
	}
	pairs := buildDataCheckFields(req)
	assert.Len(t, pairs, 2)
	assert.Contains(t, pairs, "auth_date=1700000001")
	assert.Contains(t, pairs, "id=7")
}

// T-V22: all optional fields present → six pairs total.
func TestBuildDataCheckFields_AllFields(t *testing.T) {
	req := telegramCallbackRequest{
		ID:        99,
		FirstName: "A",
		LastName:  "B",
		Username:  "c",
		PhotoURL:  "https://example.com/p.jpg",
		AuthDate:  1700000002,
	}
	pairs := buildDataCheckFields(req)
	assert.Len(t, pairs, 6)
}

// T-V23: correct key=value format for every field.
func TestBuildDataCheckFields_FieldFormats(t *testing.T) {
	req := telegramCallbackRequest{
		ID:        12345,
		FirstName: "First",
		LastName:  "Last",
		Username:  "user123",
		PhotoURL:  "https://photo.url/img.png",
		AuthDate:  9999999,
	}
	pairs := buildDataCheckFields(req)
	pairSet := make(map[string]bool, len(pairs))
	for _, p := range pairs {
		pairSet[p] = true
	}

	assert.True(t, pairSet["id=12345"])
	assert.True(t, pairSet["first_name=First"])
	assert.True(t, pairSet["last_name=Last"])
	assert.True(t, pairSet["username=user123"])
	assert.True(t, pairSet["photo_url=https://photo.url/img.png"])
	assert.True(t, pairSet["auth_date=9999999"])
}
