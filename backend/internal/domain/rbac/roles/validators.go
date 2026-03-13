package roles

import (
	"strings"

	"github.com/7-Dany/store/backend/internal/db"
)

// validateCreateRole validates a createRoleRequest.
func validateCreateRole(req *createRoleRequest) error {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return ErrNameEmpty
	}
	if len(name) > 100 {
		return ErrNameTooLong
	}
	return nil
}

// validateUpdateRole validates an updateRoleRequest.
// At least one field must be non-nil; if Name is provided it must be non-empty.
func validateUpdateRole(req *updateRoleRequest) error {
	if req.Name == nil && req.Description == nil {
		return ErrNoUpdateFields
	}
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			return ErrNameEmpty
		}
		if len(*req.Name) > 100 {
			return ErrNameTooLong
		}
	}
	return nil
}

// validateAddRolePermission validates an addRolePermissionRequest.
func validateAddRolePermission(req *addRolePermissionRequest) error {
	if strings.TrimSpace(req.PermissionID) == "" {
		return ErrPermissionIDEmpty
	}
	if strings.TrimSpace(req.GrantedReason) == "" {
		return ErrGrantedReasonEmpty
	}
	switch db.PermissionAccessType(req.AccessType) {
	case db.PermissionAccessTypeDirect, db.PermissionAccessTypeConditional,
		db.PermissionAccessTypeRequest, db.PermissionAccessTypeDenied:
		// valid
	default:
		return ErrInvalidAccessType
	}
	switch db.PermissionScope(req.Scope) {
	case db.PermissionScopeOwn, db.PermissionScopeAll:
		// valid
	default:
		return ErrInvalidScope
	}
	return nil
}
