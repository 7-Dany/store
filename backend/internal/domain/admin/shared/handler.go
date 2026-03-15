// Package adminshared holds handler-layer helpers shared across all admin
// feature sub-packages. It must never import any admin feature package.
package adminshared

import (
	"net/http"

	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// MustUserID extracts the authenticated user ID from the JWT context.
// If absent or empty it writes a 401 and returns ("", false).
// Handlers call this at the top of any mutation that needs the acting user.
func MustUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, ok := token.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "missing or invalid access token")
		return "", false
	}
	return userID, true
}
