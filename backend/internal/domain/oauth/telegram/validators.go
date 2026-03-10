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
	"time"
)

const (
	// maxAuthDateAgeSeconds is the maximum age of a Telegram auth_date payload
	// before it is considered a replay (24 hours).
	maxAuthDateAgeSeconds = 86400

	// maxAuthDateFutureSeconds is the maximum number of seconds a Telegram
	// auth_date may be in the future before it is rejected (clock-skew tolerance).
	maxAuthDateFutureSeconds = 60
)

// VerifyHMAC verifies the HMAC-SHA256 signature on a Telegram Login Widget
// payload.
//
// The verification algorithm (Telegram spec):
//  1. secret_key = SHA256(raw_bytes(botToken))
//  2. data_check_string = alphabetically sorted "key=value" pairs of all
//     received fields except "hash", joined by "\n"
//  3. expected_hash = hex(HMAC_SHA256(secret_key, data_check_string))
//  4. valid = hmac.Equal(expected_hash_bytes, received_hash_bytes)
//
// Security: uses hmac.Equal for constant-time comparison (D-08).
// Returns ErrInvalidTelegramSignature on any mismatch or hex decode error.
func VerifyHMAC(req telegramCallbackRequest, botToken string) error {
	secretKey := sha256.Sum256([]byte(botToken))

	// Build the data_check_string: sorted "key=value" pairs, excluding "hash".
	fields := buildDataCheckFields(req)
	dataCheckString := strings.Join(fields, "\n")

	mac := hmac.New(sha256.New, secretKey[:])
	mac.Write([]byte(dataCheckString))
	expectedMAC := mac.Sum(nil)

	receivedMAC, err := hex.DecodeString(req.Hash)
	if err != nil {
		return ErrInvalidTelegramSignature
	}

	// Security: constant-time comparison prevents timing attacks (D-08).
	if !hmac.Equal(expectedMAC, receivedMAC) {
		return ErrInvalidTelegramSignature
	}
	return nil
}

// CheckAuthDate validates the auth_date field for replay protection.
// Rejects the payload if auth_date is more than 86400 seconds old or more
// than 60 seconds in the future (D-09).
// Returns ErrTelegramAuthDateExpired if the check fails.
func CheckAuthDate(authDate int64) error {
	now := time.Now().Unix()
	age := now - authDate
	// Reject stale payloads (older than 24 hours).
	if age > maxAuthDateAgeSeconds {
		return ErrTelegramAuthDateExpired
	}
	// Reject future-dated payloads (clock skew tolerance: 60 seconds).
	if authDate-now > maxAuthDateFutureSeconds {
		return ErrTelegramAuthDateExpired
	}
	return nil
}

// VerifyHMACFields verifies the HMAC-SHA256 signature for callers outside this
// package that hold individual payload fields rather than a
// telegramCallbackRequest. lastName may be empty.
//
// Returns ErrInvalidTelegramSignature on any mismatch or hex decode error.
func VerifyHMACFields(id, authDate int64, firstName, lastName, username, photoURL, hash, botToken string) error {
	return VerifyHMAC(telegramCallbackRequest{
		ID:        id,
		FirstName: firstName,
		LastName:  lastName,
		Username:  username,
		PhotoURL:  photoURL,
		AuthDate:  authDate,
		Hash:      hash,
	}, botToken)
}

// buildDataCheckFields returns the sorted "key=value" pairs for the
// data_check_string, excluding the "hash" field and any fields with a zero /
// empty value that would not have been transmitted by the widget.
func buildDataCheckFields(req telegramCallbackRequest) []string {
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
	return pairs
}
