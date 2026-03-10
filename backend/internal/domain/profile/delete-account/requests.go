package deleteaccount

import "time"

// deleteAccountRequest is the JSON body for DELETE /me.
// All fields are optional; the handler dispatches based on which fields are present.
type deleteAccountRequest struct {
	Password     string               `json:"password"`
	Code         string               `json:"code"`
	TelegramAuth *TelegramAuthPayload `json:"telegram_auth"`
}

// TelegramAuthPayload carries the Telegram Login Widget fields submitted by the
// client in step 2 of the Telegram confirmation path (D-08).
type TelegramAuthPayload struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
	PhotoURL  string `json:"photo_url"`
	AuthDate  int64  `json:"auth_date"`
	Hash      string `json:"hash"`
}

// cancelDeletionRequest is the JSON body for POST /me/cancel-deletion.
// No fields — the endpoint takes no body; the struct exists for DecodeJSON[T] consistency.
type cancelDeletionRequest struct{}

// ── Response types ───────────────────────────────────────────────────────────────────

// deletionScheduledResponse is the JSON body returned on successful account deletion scheduling.
type deletionScheduledResponse struct {
	Message             string    `json:"message"`
	ScheduledDeletionAt time.Time `json:"scheduled_deletion_at"`
}

// deletionInitiatedResponse is the JSON body returned on step-1 (OTP issued or Telegram prompt)
// and on successful CancelDeletion.
type deletionInitiatedResponse struct {
	Message    string `json:"message"`
	AuthMethod string `json:"auth_method,omitempty"`
	ExpiresIn  int    `json:"expires_in,omitempty"` // seconds until OTP expires; present only on Path B-1
}

func newDeletionScheduledResponse(result DeletionScheduled) deletionScheduledResponse {
	return deletionScheduledResponse{
		Message:             "account scheduled for deletion",
		ScheduledDeletionAt: result.ScheduledDeletionAt,
	}
}
