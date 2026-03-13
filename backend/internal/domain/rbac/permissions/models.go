// Package permissions provides the read-only HTTP handler, service, and store
// for listing RBAC permissions and permission groups.
package permissions

// PermissionCapabilities carries the capability flags for a permission.
// Consumed by the admin UI to determine which access_type and scope values
// are valid for this permission when creating a role grant.
type PermissionCapabilities struct {
	// ScopePolicy declares which scope values are valid for grants of this permission.
	// "none"  = scope is not applicable; the scope field should be hidden in the UI.
	// "own"   = only 'own' is valid; pre-fill and lock the scope field.
	// "all"   = only 'all' is valid; pre-fill and lock the scope field.
	// "any"   = both 'own' and 'all' are valid; show scope selector.
	ScopePolicy string

	// AccessTypes is the ordered list of access_type values this permission allows.
	// Always includes "direct" and "denied".
	// Includes "conditional" when allow_conditional = TRUE.
	// Includes "request" when allow_request = TRUE.
	AccessTypes []string
}

// buildCapabilities constructs a PermissionCapabilities from raw capability
// columns returned by the DB. Called from both GetPermissions and
// GetPermissionGroupMembers store methods.
func buildCapabilities(scopePolicy string, allowConditional, allowRequest bool) PermissionCapabilities {
	types := []string{"direct"}
	if allowConditional {
		types = append(types, "conditional")
	}
	if allowRequest {
		types = append(types, "request")
	}
	types = append(types, "denied")
	return PermissionCapabilities{
		ScopePolicy: scopePolicy,
		AccessTypes: types,
	}
}

// Permission is the service-layer representation of a single RBAC permission.
type Permission struct {
	ID            string
	CanonicalName string
	ResourceType  string
	Name          string
	Description   string
	Capabilities  PermissionCapabilities
}

// PermissionGroupMember is a slim permission summary embedded inside a PermissionGroup.
type PermissionGroupMember struct {
	ID            string
	CanonicalName string
	ResourceType  string
	Name          string
	Description   string
	Capabilities  PermissionCapabilities
}

// PermissionGroup is the service-layer representation of a permission group
// with its member permissions embedded.
type PermissionGroup struct {
	ID           string
	Name         string
	DisplayLabel string
	Icon         string
	ColorHex     string
	DisplayOrder int32
	IsVisible    bool
	Members      []PermissionGroupMember
}
