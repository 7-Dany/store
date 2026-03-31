package watch

import "errors"

// ErrWatchExists indicates the user already has an active watch with the same target.
var ErrWatchExists = errors.New("watch already exists")

// ErrWatchNotFound indicates the requested watch resource does not exist for the user.
var ErrWatchNotFound = errors.New("watch not found")
