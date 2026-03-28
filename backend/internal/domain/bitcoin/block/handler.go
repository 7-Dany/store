package block

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// Handler handles HTTP requests for block-detail lookups.
type Handler struct {
	svc Servicer
}

// NewHandler constructs a Handler with the given Servicer.
func NewHandler(svc Servicer) *Handler {
	return &Handler{svc: svc}
}

// GetBlock handles GET /bitcoin/block/{hash}.
//
// Guard order:
//  1. JWT user-ID extraction — 401 if absent
//  2. hash path-param validation — 400 if not 64-char hex string
//  3. Service call — 404 if ErrBlockNotFound, 502 if ErrRPCUnavailable
//  4. 200 with blockResponse
func (h *Handler) GetBlock(w http.ResponseWriter, r *http.Request) {
	// 1. Authentication guard.
	_, ok := token.UserIDFromContext(r.Context())
	if !ok {
		//nolint:contextcheck // respond helpers write directly to the ResponseWriter and do not accept a context.
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	// 2. Path-param validation. Normalise to lowercase so callers may submit
	// block hashes in any case.
	hash := strings.ToLower(chi.URLParam(r, "hash"))
	if len(hash) != 64 || !isHex(hash) {
		//nolint:contextcheck // respond helpers write directly to the ResponseWriter and do not accept a context.
		respond.Error(w, http.StatusBadRequest, "invalid_block_hash", "block hash must be a 64-character hex string")
		return
	}

	// 3. Service call.
	result, err := h.svc.GetBlock(r.Context(), GetBlockInput{Hash: hash})
	if err != nil {
		if errors.Is(err, ErrBlockNotFound) {
			//nolint:contextcheck // respond helpers write directly to the ResponseWriter and do not accept a context.
			respond.Error(w, http.StatusNotFound, "block_not_found", "block not found")
			return
		}
		if errors.Is(err, ErrRPCUnavailable) {
			//nolint:contextcheck // respond helpers write directly to the ResponseWriter and do not accept a context.
			respond.Error(w, http.StatusBadGateway, "service_unavailable", "bitcoin node is temporarily unreachable")
			return
		}
		log.Error(r.Context(), "block.GetBlock: unexpected service error", "error", err)
		//nolint:contextcheck // respond helpers write directly to the ResponseWriter and do not accept a context.
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	// 4. Success response.
	//nolint:contextcheck // respond helpers write directly to the ResponseWriter and do not accept a context.
	respond.JSON(w, http.StatusOK, buildBlockResponse(&result))
}

// buildBlockResponse converts a Result to its JSON response shape.
func buildBlockResponse(r *Result) blockResponse {
	return blockResponse(*r)
}

// isHex reports whether every byte in s is a lowercase hexadecimal digit.
// Iterates over bytes (not runes) since hex characters are always ASCII.
// The caller must normalise s to lowercase before calling this function.
func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
