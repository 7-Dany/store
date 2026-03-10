package login

import (
	"time"

	"github.com/7-Dany/store/backend/internal/platform/token"
)

// loginRequest is the JSON payload for POST /auth/login.
// Identifier accepts either an email address or a username.
type loginRequest struct {
	Identifier string `json:"identifier"`
	Password   string `json:"password"`
}

// loginResponse is the JSON body for a successful POST /auth/login.
// It embeds the standard token result and adds scheduled_deletion_at when the
// account is pending soft-deletion (D-04).
type loginResponse struct {
	token.TokenResult
	ScheduledDeletionAt *time.Time `json:"scheduled_deletion_at,omitempty"`
}
