package permissions

// PermissionCapabilitiesResponse is the JSON form of PermissionCapabilities.
// Consumed by the admin UI when building the AddRolePermission form.
type PermissionCapabilitiesResponse struct {
	// ScopePolicy: "none" | "own" | "all" | "any".
	// "none"  → hide scope field in the UI.
	// "own"   → pre-fill scope = 'own' and lock (read-only).
	// "all"   → pre-fill scope = 'all' and lock (read-only).
	// "any"   → show scope selector with both options.
	ScopePolicy string `json:"scope_policy"`

	// AccessTypes: always contains "direct" and "denied".
	// Contains "conditional" when the permission allows conditional grants.
	// Contains "request" when the permission requires approval.
	AccessTypes []string `json:"access_types"`
}

// PermissionResponse is the JSON response element for a single RBAC permission.
type PermissionResponse struct {
	ID            string                         `json:"id"`
	CanonicalName string                         `json:"canonical_name"`
	ResourceType  string                         `json:"resource_type"`
	Name          string                         `json:"name"`
	Description   string                         `json:"description,omitempty"`
	Capabilities  PermissionCapabilitiesResponse `json:"capabilities"`
}

// PermissionGroupMemberResponse is the JSON element for a permission embedded inside
// a PermissionGroupResponse.
type PermissionGroupMemberResponse struct {
	ID            string                         `json:"id"`
	CanonicalName string                         `json:"canonical_name"`
	ResourceType  string                         `json:"resource_type"`
	Name          string                         `json:"name"`
	Description   string                         `json:"description,omitempty"`
	Capabilities  PermissionCapabilitiesResponse `json:"capabilities"`
}

// PermissionGroupResponse is the JSON response element for a permission group
// with its members embedded.
type PermissionGroupResponse struct {
	ID           string                          `json:"id"`
	Name         string                          `json:"name"`
	DisplayLabel string                          `json:"display_label,omitempty"`
	Icon         string                          `json:"icon,omitempty"`
	ColorHex     string                          `json:"color_hex,omitempty"`
	DisplayOrder int32                           `json:"display_order"`
	IsVisible    bool                            `json:"is_visible"`
	Members      []PermissionGroupMemberResponse `json:"members"`
}
