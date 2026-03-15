package userroles

import "strings"

// validateAssignRole checks required fields on AssignRoleInput.
// Returns the first validation error encountered.
func validateAssignRole(in AssignRoleInput) error {
	if strings.TrimSpace(in.RoleID) == "" {
		return ErrRoleIDEmpty
	}
	if strings.TrimSpace(in.GrantedReason) == "" {
		return ErrGrantedReasonEmpty
	}
	return nil
}
