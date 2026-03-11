package rbacshared

import "errors"

// ── Input-validation sentinel errors ─────────────────────────────────────────

// ErrUserIDEmpty is returned when the user_id field is absent or blank after trimming.
var ErrUserIDEmpty = errors.New("user_id is required")
