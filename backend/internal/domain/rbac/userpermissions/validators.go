package userpermissions

import (
	"strings"
	"time"
)

func validateGrantPermission(in GrantPermissionInput) error {
	if strings.TrimSpace(in.PermissionID) == "" {
		return ErrPermissionIDEmpty
	}
	if strings.TrimSpace(in.GrantedReason) == "" {
		return ErrGrantedReasonEmpty
	}
	if in.ExpiresAt.IsZero() {
		return ErrExpiresAtRequired
	}
	if in.ExpiresAt.Before(time.Now()) {
		return ErrExpiresAtInPast
	}
	return nil
}

// normaliseScope returns "own" when scope is empty or unrecognised.
func normaliseScope(scope string) string {
	if scope == "all" {
		return "all"
	}
	return "own"
}
