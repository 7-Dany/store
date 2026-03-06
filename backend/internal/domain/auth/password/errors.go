package password

import "errors"

// ErrSamePassword is returned when the new password matches the current one.
// Resetting to the same password would revoke all sessions for no security benefit.
var ErrSamePassword = errors.New("new password must differ from the current password")
