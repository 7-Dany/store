package owner

import "time"

// ── Assign-owner request/response ─────────────────────────────────────────────

// assignOwnerRequest is the decoded JSON body for POST /owner/assign.
// user_id is intentionally absent — it is derived from the authenticated
// caller's JWT so a logged-in user can only assign the owner role to themselves.
type assignOwnerRequest struct {
	Secret string `json:"secret"`
}

// assignOwnerResponse is the JSON body written on a successful POST /owner/assign.
type assignOwnerResponse struct {
	UserID    string    `json:"user_id"`
	RoleName  string    `json:"role_name"`
	GrantedAt time.Time `json:"granted_at"`
}

// ── Transfer request bodies ───────────────────────────────────────────────────

// initiateRequest is the decoded JSON body for POST /owner/transfer.
type initiateRequest struct {
	TargetUserID string `json:"target_user_id"`
}

// initiateResponse is the JSON body written on a successful POST /owner/transfer.
type initiateResponse struct {
	TransferID   string    `json:"transfer_id"`
	TargetUserID string    `json:"target_user_id"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// acceptRequest is the decoded JSON body for POST /owner/transfer/accept.
// No JWT is required on this route — the raw token is the credential.
type acceptRequest struct {
	Token string `json:"token"`
}

// acceptResponse is the JSON body written on a successful POST /owner/transfer/accept.
type acceptResponse struct {
	NewOwnerID      string    `json:"new_owner_id"`
	PreviousOwnerID string    `json:"previous_owner_id"`
	TransferredAt   time.Time `json:"transferred_at"`
}
