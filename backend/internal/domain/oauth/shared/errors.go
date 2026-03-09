// Package oauthshared holds primitives shared by all oauth feature sub-packages.
package oauthshared

import "errors"

// ── Cross-feature sentinel errors ─────────────────────────────────────────────

// ErrIdentityNotFound is returned when a user_identities row cannot be located
// for the given (user_id, provider) or (provider, provider_uid) pair.
var ErrIdentityNotFound = errors.New("oauth identity not found")

// ErrProviderAlreadyLinked is returned when a Google UID is already linked to
// a different user account in link mode (link_user_id != row.UserID).
var ErrProviderAlreadyLinked = errors.New("provider already linked to another account")

// ErrLastAuthMethod is returned by UnlinkGoogle when removing the identity
// would leave the user with no remaining authentication method.
var ErrLastAuthMethod = errors.New("cannot remove the last authentication method")

// ErrAccountLocked is returned when the matched user is locked (is_locked or
// admin_locked) during an OAuth callback or link operation.
var ErrAccountLocked = errors.New("account is locked")

// ErrAccountInactive is returned when the matched user has is_active=false
// during an OAuth callback or link operation.
var ErrAccountInactive = errors.New("account is inactive")
