package profileshared

import (
	"time"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
)

// AccountDeletionWindow is the grace period between when a deletion is
// scheduled (deleted_at stamped) and when the account is permanently removed.
// ScheduledDeletionAt = deleted_at + AccountDeletionWindow.
const AccountDeletionWindow = 30 * 24 * time.Hour

// ErrUserNotFound is returned when the user record cannot be located.
// Aliased from authshared so profile packages do not import the auth domain.
var ErrUserNotFound = authshared.ErrUserNotFound
