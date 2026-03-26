package txstatus

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// Handler handles HTTP requests for transaction-status lookups.
type Handler struct {
	svc Servicer
}

// NewHandler constructs a Handler with the given Servicer.
func NewHandler(svc Servicer) *Handler {
	return &Handler{svc: svc}
}

// GetTxStatus handles GET /bitcoin/tx/{txid}/status.
//
// Guard order:
//  1. JWT user-ID extraction — 401 if absent
//  2. txid path-param validation — 400 if not 64-char hex string
//  3. Service call — 502 if ErrRPCUnavailable
//  4. 200 with singleStatusResponse
func (h *Handler) GetTxStatus(w http.ResponseWriter, r *http.Request) {
	// 1. Authentication guard.
	_, ok := token.UserIDFromContext(r.Context())
	if !ok {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	// 2. Path-param validation. Normalise to lowercase so callers may submit
	// txids in any case (block explorers often return uppercase).
	txid := strings.ToLower(chi.URLParam(r, "txid"))
	if len(txid) != 64 || !isHex(txid) {
		respond.Error(w, http.StatusBadRequest, "invalid_txid", "txid must be a 64-character hex string")
		return
	}

	// 3. Service call.
	result, err := h.svc.GetTxStatus(r.Context(), GetTxStatusInput{TxID: txid})
	if err != nil {
		if errors.Is(err, ErrWalletNotLoaded) {
			respond.Error(w, http.StatusServiceUnavailable, "wallet_not_loaded", "bitcoin node has no wallet loaded — contact support")
			return
		}
		if errors.Is(err, ErrRPCUnavailable) {
			respond.Error(w, http.StatusBadGateway, "service_unavailable", "bitcoin node is temporarily unreachable")
			return
		}
		log.Error(r.Context(), "txstatus.GetTxStatus: unexpected service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	// 4. Success response.
	respond.JSON(w, http.StatusOK, buildSingleResponse(result))
}

// GetTxStatusBatch handles GET /bitcoin/tx/status?ids={txid,txid,...}.
//
// Guard order:
//  1. JWT user-ID extraction — 401 if absent
//  2. Query-param validation — 400 for missing/empty ids, >20 entries, any invalid txid
//  3. Service call — 502 if ErrRPCUnavailable
//  4. 200 with batchStatusResponse
func (h *Handler) GetTxStatusBatch(w http.ResponseWriter, r *http.Request) {
	// 1. Authentication guard.
	_, ok := token.UserIDFromContext(r.Context())
	if !ok {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	// 2. Query-param validation.
	raw := r.URL.Query().Get("ids")
	if raw == "" {
		respond.Error(w, http.StatusBadRequest, "missing_ids", "ids query parameter is required")
		return
	}

	// Normalise to lowercase before splitting so each txid is already in the
	// canonical form expected by isHex and the RPC layer.
	parts := strings.Split(strings.ToLower(raw), ",")
	if len(parts) > 20 {
		respond.Error(w, http.StatusBadRequest, "too_many_ids", "at most 20 transaction IDs may be queried per request")
		return
	}

	for _, part := range parts {
		if len(part) != 64 || !isHex(part) {
			respond.Error(w, http.StatusBadRequest, "invalid_txid", "each txid must be a 64-character hex string")
			return
		}
	}

	// Deduplicate txids: duplicates collapse to a single RPC call and one map
	// entry in the response, which matches the specified behaviour (a map keyed
	// by txid cannot represent two entries for the same key).
	seen := make(map[string]struct{}, len(parts))
	deduped := make([]string, 0, len(parts))
	for _, p := range parts {
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			deduped = append(deduped, p)
		}
	}

	// 3. Service call.
	results, err := h.svc.GetTxStatusBatch(r.Context(), GetTxStatusBatchInput{TxIDs: deduped})
	if err != nil {
		if errors.Is(err, ErrWalletNotLoaded) {
			respond.Error(w, http.StatusServiceUnavailable, "wallet_not_loaded", "bitcoin node has no wallet loaded — contact support")
			return
		}
		if errors.Is(err, ErrRPCUnavailable) {
			respond.Error(w, http.StatusBadGateway, "service_unavailable", "bitcoin node is temporarily unreachable")
			return
		}
		log.Error(r.Context(), "txstatus.GetTxStatusBatch: unexpected service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	// 4. Success response.
	statuses := make(map[string]singleStatusResponse, len(results))
	for txid, res := range results {
		statuses[txid] = buildSingleResponse(res)
	}
	respond.JSON(w, http.StatusOK, batchStatusResponse{Statuses: statuses})
}

// buildSingleResponse converts a TxStatusResult to its JSON response shape.
// Confirmations is included (even when zero) for confirmed and mempool statuses;
// BlockHeight is included only for confirmed transactions.
func buildSingleResponse(r TxStatusResult) singleStatusResponse {
	resp := singleStatusResponse{Status: r.Status}
	switch r.Status {
	case TxStatusConfirmed:
		conf := r.Confirmations
		height := r.BlockHeight
		resp.Confirmations = &conf
		resp.BlockHeight = &height
	case TxStatusMempool:
		zero := 0
		resp.Confirmations = &zero
	}
	return resp
}

// isHex reports whether every byte in s is a lowercase hexadecimal digit.
// Iterates over bytes (not runes) since hex characters are always ASCII.
// The caller must normalise s to lowercase before calling this function.
func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
