package deleteaccount

import (
	"errors"

	telegram "github.com/7-Dany/store/backend/internal/domain/oauth/telegram"
)

// validateTelegramAuthPayload checks that the mandatory Telegram fields are present.
// Returns a descriptive error mapped to 400 validation_error by the handler.
func validateTelegramAuthPayload(p *TelegramAuthPayload) error {
	if p == nil {
		return errors.New("telegram_auth is required")
	}
	if p.ID == 0 {
		return errors.New("telegram_auth.id is required")
	}
	if p.AuthDate == 0 {
		return errors.New("telegram_auth.auth_date is required")
	}
	if p.Hash == "" {
		return errors.New("telegram_auth.hash is required")
	}
	return nil
}

// verifyTelegramHMAC verifies the HMAC-SHA256 signature on a TelegramAuthPayload.
// Delegates to telegram.VerifyHMACFields which implements the canonical Telegram
// Login Widget algorithm. TelegramAuthPayload has no LastName field (D-08).
func verifyTelegramHMAC(botToken string, p TelegramAuthPayload) bool {
	err := telegram.VerifyHMACFields(p.ID, p.AuthDate, p.FirstName, "", p.Username, p.PhotoURL, p.Hash, botToken)
	return err == nil
}
