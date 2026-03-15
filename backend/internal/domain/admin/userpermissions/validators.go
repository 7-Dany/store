package userpermissions

import (
	rbacshared "github.com/7-Dany/store/backend/internal/domain/rbac/shared"
	"strings"
	"time"
)

// ValidateGrantPermission validates the fields of a GrantPermissionInput.
func ValidateGrantPermission(in GrantPermissionInput) error {
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

// NormaliseScope returns "own" when scope is empty or unrecognised,
// and "all" only when the input is exactly "all".
func NormaliseScope(scope string) string {
	if scope == "all" {
		return "all"
	}
	return "own"
}

// ResolveScope resolves the requested scope against the permission's scope_policy.
// Policies:
//   - "none" / "own" → only "own" is allowed
//   - "all"          → only "all" is allowed
//   - "any"          → both "own" and "all" are allowed
func ResolveScope(policy, requested string) (string, error) {
	switch policy {
	case "any":
		if requested == "all" || requested == "own" {
			return requested, nil
		}
	case "all":
		if requested == "all" {
			return "all", nil
		}
	default: // "own", "none", or anything unrecognised
		if requested == "own" {
			return "own", nil
		}
	}
	return "", rbacshared.ErrScopeNotAllowed
}
