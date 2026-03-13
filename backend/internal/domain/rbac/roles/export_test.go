package roles

// ExportValidateCreateRole exposes validateCreateRole for white-box testing.
func ExportValidateCreateRole(name string) error {
	return validateCreateRole(&createRoleRequest{Name: name})
}

// ExportValidateUpdateRole exposes validateUpdateRole for white-box testing.
func ExportValidateUpdateRole(name, description *string) error {
	return validateUpdateRole(&updateRoleRequest{Name: name, Description: description})
}

// ExportValidateAddRolePermission exposes validateAddRolePermission for white-box testing.
func ExportValidateAddRolePermission(permissionID, grantedReason, accessType, scope string) error {
	return validateAddRolePermission(&addRolePermissionRequest{
		PermissionID:  permissionID,
		GrantedReason: grantedReason,
		AccessType:    accessType,
		Scope:         scope,
	})
}
