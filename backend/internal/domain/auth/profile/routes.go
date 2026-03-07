package profile

import (
	"context"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
)

// Routes was the HTTP registration point for auth/profile session routes. All
// routes have migrated to the profile domain. This function remains as a no-op
// until Phase 3 removes this package entirely.
func Routes(_ context.Context, _ chi.Router, _ *app.Deps) {
	// All routes migrated to internal/domain/profile/.
}
