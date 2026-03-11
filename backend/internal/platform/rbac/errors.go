package rbac

import "errors"

var (
	ErrForbidden           = errors.New("insufficient permissions")
	ErrUnauthenticated     = errors.New("authentication required")
	ErrApprovalRequired    = errors.New("action requires approval — request submitted")
	ErrSystemRoleImmutable = errors.New("system roles cannot be modified")
	ErrCannotReassignOwner = errors.New("owner role cannot be reassigned via this route")
	ErrCannotModifyOwnRole = errors.New("you cannot modify your own role assignment")
	ErrOwnerAlreadyExists  = errors.New("an active owner already exists")
	ErrCannotLockOwner     = errors.New("owner accounts cannot be admin-locked")
	ErrCannotLockSelf      = errors.New("you cannot lock your own account")
)
