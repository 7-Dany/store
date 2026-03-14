package owner

import (
	"errors"

	"github.com/7-Dany/store/backend/internal/platform/rbac"
)

// ── Assign-owner sentinels ────────────────────────────────────────────────────

// ErrOwnerAlreadyExists is returned by AssignOwner when an active owner role
// assignment already exists. Wraps platform/rbac.ErrOwnerAlreadyExists so that
// callers in the handler can use errors.Is against either sentinel.
var ErrOwnerAlreadyExists = rbac.ErrOwnerAlreadyExists

// ErrUserNotActive is returned when the target user's account is not active.
var ErrUserNotActive = errors.New("user account is not active")

// ErrUserNotVerified is returned when the target user's email address has not
// been verified.
var ErrUserNotVerified = errors.New("user email address is not verified")

// ErrAssignSecretEmpty is returned when the secret field is absent from the
// assign-owner request body.
var ErrAssignSecretEmpty = errors.New("secret is required")

// ErrInvalidAssignSecret is returned when the secret field does not match the
// value configured in BOOTSTRAP_SECRET.
var ErrInvalidAssignSecret = errors.New("invalid secret")

// ── Ownership transfer sentinels ─────────────────────────────────────────────

// ErrTransferAlreadyPending is returned when an unexpired, unconsumed transfer
// token already exists. Only one pending transfer is allowed at a time.
var ErrTransferAlreadyPending = errors.New("an ownership transfer is already pending")

// ErrTransferTokenInvalid is returned when the presented raw token does not
// match any active (unexpired, unconsumed) transfer record, or when the bcrypt
// comparison fails.
var ErrTransferTokenInvalid = errors.New("transfer token is invalid, expired, or already used")

// ErrUserIsAlreadyOwner is returned when the initiating owner attempts to
// transfer ownership to the user who is already the owner.
var ErrUserIsAlreadyOwner = errors.New("target user is already the owner")

// ErrCannotTransferToSelf is returned when the acting owner supplies their own
// user ID as the transfer target.
var ErrCannotTransferToSelf = errors.New("cannot transfer ownership to yourself")

// ErrNoPendingTransfer is returned by CancelTransfer when there is no active
// pending transfer token initiated by the calling owner.
var ErrNoPendingTransfer = errors.New("no pending ownership transfer found")

// ErrInitiatorNotOwner is returned during AcceptTransfer when the user who
// initiated the transfer no longer holds the owner role at accept time (race).
var ErrInitiatorNotOwner = errors.New("initiating user no longer holds the owner role")

// ErrUserNotEligible is returned by AcceptTransfer when the target user is no
// longer active or verified at the moment they attempt to accept.
var ErrUserNotEligible = errors.New("target user is no longer active or email-verified")
