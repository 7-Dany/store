package owner

import (
	"strings"

	"github.com/google/uuid"
)

// validateAssignOwnerRequest validates the decoded assign-owner request body.
// Returns ErrAssignSecretEmpty when the secret field is blank.
func validateAssignOwnerRequest(req *assignOwnerRequest) error {
	if strings.TrimSpace(req.Secret) == "" {
		return ErrAssignSecretEmpty
	}
	return nil
}

// validateInitiateRequest validates the decoded initiate-transfer request body.
// Returns an error if target_user_id is absent or not a valid UUID.
func validateInitiateRequest(req *initiateRequest) error {
	if strings.TrimSpace(req.TargetUserID) == "" {
		return errTargetUserIDRequired
	}
	if _, err := uuid.Parse(req.TargetUserID); err != nil {
		return errTargetUserIDInvalid
	}
	return nil
}

// validateAcceptRequest validates the decoded accept-transfer request body.
// Returns an error if the token field is empty.
func validateAcceptRequest(req *acceptRequest) error {
	if strings.TrimSpace(req.Token) == "" {
		return errTokenRequired
	}
	return nil
}

// ── Validation-only sentinels (unexported; handler maps them to respond.Error) ─

var errTargetUserIDRequired = errValidation("target_user_id is required")
var errTargetUserIDInvalid  = errValidation("target_user_id must be a valid UUID")
var errTokenRequired        = errValidation("token is required")

type errValidation string

func (e errValidation) Error() string { return string(e) }
