package profileshared

import authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"

// ErrUserNotFound is returned when the user record cannot be located.
// Aliased from authshared so profile packages do not import the auth domain.
var ErrUserNotFound = authshared.ErrUserNotFound
