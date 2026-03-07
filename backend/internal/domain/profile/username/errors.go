package username

import "errors"

// ErrUsernameTaken is returned when the requested username is already registered.
var ErrUsernameTaken = errors.New("username is already taken")

// ErrSameUsername is returned when the new username is identical to the current one.
var ErrSameUsername = errors.New("new username is the same as the current username")

// ErrUsernameEmpty is returned when the username field is absent or blank after trimming.
var ErrUsernameEmpty = errors.New("username is required")

// ErrUsernameTooShort is returned when the username is shorter than 3 characters.
var ErrUsernameTooShort = errors.New("username must be at least 3 characters")

// ErrUsernameTooLong is returned when the username exceeds 30 characters.
var ErrUsernameTooLong = errors.New("username must not exceed 30 characters")

// ErrUsernameInvalidChars is returned when the username contains characters
// outside the allowed set [a-z0-9_].
var ErrUsernameInvalidChars = errors.New("username may only contain lowercase letters, digits, and underscores")

// ErrUsernameInvalidFormat is returned when the username starts or ends with an
// underscore, or contains consecutive underscores.
var ErrUsernameInvalidFormat = errors.New("username must not start or end with an underscore, and must not contain consecutive underscores")
