// Package permissions provides the read-only HTTP handler, service, and store
// for listing RBAC permissions and permission groups.
package permissions

// Permission is the service-layer representation of a single RBAC permission.
type Permission struct {
	ID            string
	CanonicalName string
	ResourceType  string
	Name          string
	Description   string
}

// PermissionGroupMember is a slim permission summary embedded inside a PermissionGroup.
type PermissionGroupMember struct {
	ID            string
	CanonicalName string
	ResourceType  string
	Name          string
	Description   string
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
