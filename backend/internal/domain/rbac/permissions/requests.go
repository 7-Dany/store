package permissions

// PermissionResponse is the JSON response element for a single RBAC permission.
type PermissionResponse struct {
	ID            string `json:"id"`
	CanonicalName string `json:"canonical_name"`
	ResourceType  string `json:"resource_type"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
}

// PermissionGroupMemberResponse is the JSON element for a permission embedded inside
// a PermissionGroupResponse.
type PermissionGroupMemberResponse struct {
	ID            string `json:"id"`
	CanonicalName string `json:"canonical_name"`
	ResourceType  string `json:"resource_type"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
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
