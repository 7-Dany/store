package profile

import "errors"

// ErrAvatarURLInvalid is returned when avatar_url is present but is not a
// valid absolute URL with an http or https scheme, or is an empty string.
var ErrAvatarURLInvalid = errors.New("avatar_url must be a valid http or https URL")

// ErrAvatarURLTooLong is returned when avatar_url exceeds 2048 bytes.
var ErrAvatarURLTooLong = errors.New("avatar_url must not exceed 2048 characters")

// ErrEmptyPatch is returned when both display_name and avatar_url are absent
// or null in the PATCH /me/profile request body (nothing to update).
var ErrEmptyPatch = errors.New("at least one field (display_name or avatar_url) must be provided")
