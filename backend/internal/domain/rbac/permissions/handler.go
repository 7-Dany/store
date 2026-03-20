package permissions

import (
	"context"
	"net/http"

	"github.com/7-Dany/store/backend/internal/platform/respond"
)

// Servicer is the subset of the service that the handler requires.
// *Service satisfies this interface; tests supply a PermissionsFakeServicer.
type Servicer interface {
	ListPermissions(ctx context.Context) ([]Permission, error)
	ListPermissionGroups(ctx context.Context) ([]PermissionGroup, error)
}

// Handler is the HTTP layer for the permissions package.
type Handler struct {
	svc Servicer
}

// NewHandler constructs a Handler.
func NewHandler(svc Servicer) *Handler {
	return &Handler{svc: svc}
}

// ListPermissions handles GET /admin/rbac/permissions.
func (h *Handler) ListPermissions(w http.ResponseWriter, r *http.Request) {
	perms, err := h.svc.ListPermissions(r.Context())
	if err != nil {
		log.Error(r.Context(), "ListPermissions: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"permissions": toPermissionResponses(perms)})
}

// ListPermissionGroups handles GET /admin/rbac/permissions/groups.
func (h *Handler) ListPermissionGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.svc.ListPermissionGroups(r.Context())
	if err != nil {
		log.Error(r.Context(), "ListPermissionGroups: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"groups": toPermissionGroupResponses(groups)})
}

// ── Response mapping helpers ──────────────────────────────────────────────────

// toPermissionResponses converts a service-layer []Permission slice to its JSON
// response form. Always returns a non-nil slice so nil service results marshal
// as [] instead of null.
func toPermissionResponses(perms []Permission) []PermissionResponse {
	out := make([]PermissionResponse, len(perms))
	for i, p := range perms {
		out[i] = PermissionResponse{
			ID:            p.ID,
			CanonicalName: p.CanonicalName,
			ResourceType:  p.ResourceType,
			Name:          p.Name,
			Description:   p.Description,
			Capabilities: PermissionCapabilitiesResponse{
				ScopePolicy: p.Capabilities.ScopePolicy,
				AccessTypes: p.Capabilities.AccessTypes,
			},
		}
	}
	return out
}

// toPermissionGroupResponses converts a service-layer []PermissionGroup slice
// to its JSON response form. Always returns a non-nil slice so nil service
// results marshal as [] instead of null.
func toPermissionGroupResponses(groups []PermissionGroup) []PermissionGroupResponse {
	out := make([]PermissionGroupResponse, len(groups))
	for i, g := range groups {
		members := make([]PermissionGroupMemberResponse, len(g.Members))
		for j, m := range g.Members {
			members[j] = PermissionGroupMemberResponse{
				ID:            m.ID,
				CanonicalName: m.CanonicalName,
				ResourceType:  m.ResourceType,
				Name:          m.Name,
				Description:   m.Description,
				Capabilities: PermissionCapabilitiesResponse{
					ScopePolicy: m.Capabilities.ScopePolicy,
					AccessTypes: m.Capabilities.AccessTypes,
				},
			}
		}
		out[i] = PermissionGroupResponse{
			ID:           g.ID,
			Name:         g.Name,
			DisplayLabel: g.DisplayLabel,
			Icon:         g.Icon,
			ColorHex:     g.ColorHex,
			DisplayOrder: g.DisplayOrder,
			IsVisible:    g.IsVisible,
			Members:      members,
		}
	}
	return out
}
