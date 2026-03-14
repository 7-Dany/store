package owner

import "time"

// ── Assign-owner models ───────────────────────────────────────────────────────

// AssignOwnerInput carries the parsed user_id and request metadata from the
// handler into the service for the initial owner assignment path.
type AssignOwnerInput struct {
	UserID    [16]byte
	IPAddress string
	UserAgent string
}

// AssignOwnerUser is the minimal user projection the store returns for the
// service-layer eligibility checks in AssignOwner.
type AssignOwnerUser struct {
	IsActive      bool
	EmailVerified bool
}

// AssignOwnerTxInput carries the IDs and request metadata needed for the
// transactional owner role assignment.
type AssignOwnerTxInput struct {
	UserID    [16]byte
	RoleID    [16]byte
	IPAddress string
	UserAgent string
}

// AssignOwnerResult is the service-layer output for a successful owner assignment.
type AssignOwnerResult struct {
	UserID    string
	RoleName  string
	GrantedAt time.Time
}

// ── Transfer models ───────────────────────────────────────────────────────────

// TransferTargetUser carries the fields fetched from the DB during
// InitiateTransfer to validate eligibility.
type TransferTargetUser struct {
	Email         string
	IsActive      bool
	EmailVerified bool
	IsOwner       bool
}

// InitiateInput is the service-layer input for starting an ownership transfer.
type InitiateInput struct {
	ActingOwnerID [16]byte // current owner (from JWT)
	TargetUserID  [16]byte // UUID parsed from request body
	IPAddress     string
	UserAgent     string
}

// InitiateResult is the service-layer output returned on successful transfer initiation.
type InitiateResult struct {
	TransferID   string
	TargetUserID string
	TargetEmail  string    // needed by the handler to enqueue the invite email; must not appear in the HTTP response
	ExpiresAt    time.Time
}

// AcceptInput is the service-layer input for accepting an ownership transfer.
type AcceptInput struct {
	RawToken  string // presented token (not hashed)
	IPAddress string
	UserAgent string
}

// AcceptResult is the service-layer output returned on successful acceptance.
type AcceptResult struct {
	NewOwnerID      string
	PreviousOwnerID string
	TransferredAt   time.Time
}

// AcceptTransferTxInput carries everything needed for the atomic role-swap
// transaction inside AcceptTransferTx.
type AcceptTransferTxInput struct {
	TokenID         [16]byte // one_time_tokens.id
	NewOwnerID      [16]byte // target user (becomes owner)
	PreviousOwnerID [16]byte // initiating owner (loses owner role)
	RoleID          [16]byte // owner role ID
	ActingUserID    [16]byte // the new owner, who authorised the transfer; used by WithActingUser in step 5
	IPAddress       string
	UserAgent       string
}
