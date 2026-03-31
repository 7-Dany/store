package txstatus

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	bitcoinshared "github.com/7-Dany/store/backend/internal/domain/bitcoin/shared"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

const maxCRUDBodyBytes = 4 << 10

// Handler handles HTTP requests for persisted transaction tracking resources.
type Handler struct {
	svc     Servicer
	network string
}

// NewHandler constructs a Handler with the given Servicer.
func NewHandler(svc Servicer, network string) *Handler {
	return &Handler{svc: svc, network: network}
}

// CreateTrackedTxStatus handles POST /bitcoin/tx.
func (h *Handler) CreateTrackedTxStatus(w http.ResponseWriter, r *http.Request) {
	userID, ok := token.UserIDFromContext(r.Context())
	if !ok {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxCRUDBodyBytes)
	req, ok := respond.DecodeJSON[createTrackedTxStatusRequest](w, r)
	if !ok {
		return
	}

	if req.Network != h.network {
		respond.Error(w, http.StatusBadRequest, "network_mismatch", "request network does not match server network")
		return
	}

	txid := strings.ToLower(strings.TrimSpace(req.TxID))
	if len(txid) != 64 || !isHex(txid) {
		respond.Error(w, http.StatusBadRequest, "invalid_txid", "txid must be a 64-character hex string")
		return
	}

	address, err := normaliseOptionalAddress(req.Address, h.network)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_address", "one or more addresses failed validation")
		return
	}

	row, err := h.svc.CreateTrackedTxStatus(r.Context(), CreateTrackedTxStatusInput{
		UserID:  userID,
		Network: h.network,
		TxID:    txid,
		Address: address,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrTrackedTxStatusExists):
			respond.Error(w, http.StatusConflict, "tx_status_exists", "transaction status record already exists")
		case errors.Is(err, ErrWalletNotLoaded):
			respond.Error(w, http.StatusServiceUnavailable, "wallet_not_loaded", "bitcoin node has no wallet loaded — contact support")
		case errors.Is(err, ErrRPCUnavailable):
			respond.Error(w, http.StatusBadGateway, "service_unavailable", "bitcoin node is temporarily unreachable")
		default:
			log.Error(r.Context(), "txstatus.CreateTrackedTxStatus: unexpected service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	respond.JSON(w, http.StatusCreated, buildTrackedResponse(row))
}

// GetTrackedTxStatus handles GET /bitcoin/tx/{id}.
func (h *Handler) GetTrackedTxStatus(w http.ResponseWriter, r *http.Request) {
	userID, ok := token.UserIDFromContext(r.Context())
	if !ok {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	id, ok := parsePathID(w, r)
	if !ok {
		return
	}

	row, err := h.svc.GetTrackedTxStatus(r.Context(), GetTrackedTxStatusInput{ID: id, UserID: userID})
	if err != nil {
		switch {
		case errors.Is(err, ErrTrackedTxStatusNotFound):
			respond.Error(w, http.StatusNotFound, "tx_status_not_found", "transaction status record was not found")
		default:
			log.Error(r.Context(), "txstatus.GetTrackedTxStatus: unexpected service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	respond.JSON(w, http.StatusOK, buildTrackedResponse(row))
}

// ListTrackedTxStatuses handles GET /bitcoin/tx.
func (h *Handler) ListTrackedTxStatuses(w http.ResponseWriter, r *http.Request) {
	userID, ok := token.UserIDFromContext(r.Context())
	if !ok {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	query := r.URL.Query()
	address, err := normaliseOptionalAddress(query.Get("address"), h.network)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_address", "one or more addresses failed validation")
		return
	}

	txid := strings.ToLower(strings.TrimSpace(query.Get("txid")))
	if txid != "" && (len(txid) != 64 || !isHex(txid)) {
		respond.Error(w, http.StatusBadRequest, "invalid_txid", "txid must be a 64-character hex string")
		return
	}

	mode := strings.TrimSpace(query.Get("tracking_mode"))
	if mode != "" && mode != string(TrackingModeTxID) && mode != string(TrackingModeWatch) {
		respond.Error(w, http.StatusBadRequest, "invalid_tracking_mode", "tracking_mode must be txid or watch")
		return
	}

	limit := 100
	if rawLimit := strings.TrimSpace(query.Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 || parsed > 100 {
			respond.Error(w, http.StatusBadRequest, "invalid_limit", "limit must be an integer between 1 and 100")
			return
		}
		limit = parsed
	}

	beforeSortTime, beforeID, err := parseTrackedTxStatusesCursor(query.Get("cursor"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_cursor", "cursor must be a valid txstatus pagination cursor")
		return
	}

	rows, err := h.svc.ListTrackedTxStatuses(r.Context(), ListTrackedTxStatusesInput{
		UserID:         userID,
		Network:        h.network,
		Address:        valueOrEmpty(address),
		TxID:           txid,
		TrackingMode:   mode,
		BeforeSortTime: beforeSortTime,
		BeforeID:       beforeID,
		Limit:          limit + 1,
	})
	if err != nil {
		log.Error(r.Context(), "txstatus.ListTrackedTxStatuses: unexpected service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	items := make([]trackedTxStatusResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, buildTrackedResponse(row))
	}

	resp := trackedTxStatusesResponse{Items: items}
	if hasMore && len(rows) > 0 {
		resp.NextCursor = buildTrackedTxStatusesCursor(rows[len(rows)-1])
	}
	respond.JSON(w, http.StatusOK, resp)
}

// UpdateTrackedTxStatus handles PUT /bitcoin/tx/{id}.
func (h *Handler) UpdateTrackedTxStatus(w http.ResponseWriter, r *http.Request) {
	userID, ok := token.UserIDFromContext(r.Context())
	if !ok {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	id, ok := parsePathID(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxCRUDBodyBytes)
	req, ok := respond.DecodeJSON[updateTrackedTxStatusRequest](w, r)
	if !ok {
		return
	}

	address, err := normaliseOptionalAddress(req.Address, h.network)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_address", "one or more addresses failed validation")
		return
	}

	row, err := h.svc.UpdateTrackedTxStatus(r.Context(), UpdateTrackedTxStatusInput{
		ID:      id,
		UserID:  userID,
		Address: address,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrTrackedTxStatusNotFound):
			respond.Error(w, http.StatusNotFound, "tx_status_not_found", "transaction status record was not found")
		case errors.Is(err, ErrTrackedTxStatusExists):
			respond.Error(w, http.StatusConflict, "tx_status_exists", "transaction status record already exists")
		case errors.Is(err, ErrWatchManagedTrackedTxStatus):
			respond.Error(w, http.StatusConflict, "watch_managed_record", "watch-managed transaction status records cannot be updated here")
		case errors.Is(err, ErrWalletNotLoaded):
			respond.Error(w, http.StatusServiceUnavailable, "wallet_not_loaded", "bitcoin node has no wallet loaded — contact support")
		case errors.Is(err, ErrRPCUnavailable):
			respond.Error(w, http.StatusBadGateway, "service_unavailable", "bitcoin node is temporarily unreachable")
		default:
			log.Error(r.Context(), "txstatus.UpdateTrackedTxStatus: unexpected service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	respond.JSON(w, http.StatusOK, buildTrackedResponse(row))
}

// DeleteTrackedTxStatus handles DELETE /bitcoin/tx/{id}.
func (h *Handler) DeleteTrackedTxStatus(w http.ResponseWriter, r *http.Request) {
	userID, ok := token.UserIDFromContext(r.Context())
	if !ok {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	id, ok := parsePathID(w, r)
	if !ok {
		return
	}

	if err := h.svc.DeleteTrackedTxStatus(r.Context(), DeleteTrackedTxStatusInput{ID: id, UserID: userID}); err != nil {
		switch {
		case errors.Is(err, ErrTrackedTxStatusNotFound):
			respond.Error(w, http.StatusNotFound, "tx_status_not_found", "transaction status record was not found")
		default:
			log.Error(r.Context(), "txstatus.DeleteTrackedTxStatus: unexpected service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	respond.NoContent(w)
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

// buildTrackedResponse converts one durable row to its JSON response shape.
func buildTrackedResponse(row TrackedTxStatus) trackedTxStatusResponse {
	addresses := make([]trackedTxStatusAddressResponse, 0, len(row.Addresses))
	for _, item := range row.Addresses {
		addresses = append(addresses, trackedTxStatusAddressResponse{
			Address:   item.Address,
			AmountSat: item.AmountSat,
		})
	}

	return trackedTxStatusResponse{
		ID:              row.ID,
		Network:         row.Network,
		TrackingMode:    string(row.TrackingMode),
		Address:         row.Address,
		Addresses:       addresses,
		TxID:            row.TxID,
		Status:          row.Status,
		Confirmations:   row.Confirmations,
		AmountSat:       row.AmountSat,
		FeeRateSatVByte: row.FeeRateSatVByte,
		FirstSeenAt:     row.FirstSeenAt,
		LastSeenAt:      row.LastSeenAt,
		ConfirmedAt:     row.ConfirmedAt,
		BlockHash:       row.BlockHash,
		BlockHeight:     row.BlockHeight,
		ReplacementTxID: row.ReplacementTxID,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
}

// parsePathID parses the positive integer resource ID from the route params.
func parsePathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	rawID := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id < 1 {
		respond.Error(w, http.StatusBadRequest, "invalid_id", "id must be a positive integer")
		return 0, false
	}
	return id, true
}

func parseTrackedTxStatusesCursor(raw string) (*time.Time, int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, 0, nil
	}

	decoded, err := base64.RawURLEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, 0, err
	}
	parts := strings.SplitN(string(decoded), "|", 2)
	if len(parts) != 2 {
		return nil, 0, errors.New("invalid txstatus cursor shape")
	}

	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return nil, 0, err
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id < 1 {
		return nil, 0, errors.New("invalid txstatus cursor id")
	}
	utc := t.UTC()
	return &utc, id, nil
}

func buildTrackedTxStatusesCursor(row TrackedTxStatus) string {
	payload := trackedTxStatusSortTime(row).UTC().Format(time.RFC3339Nano) + "|" + strconv.FormatInt(row.ID, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

func trackedTxStatusSortTime(row TrackedTxStatus) time.Time {
	if row.ConfirmedAt != nil {
		return *row.ConfirmedAt
	}
	return row.FirstSeenAt
}

// normaliseOptionalAddress validates and canonicalises an optional address filter.
func normaliseOptionalAddress(raw, network string) (*string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	addr, err := bitcoinshared.ValidateAndNormalise(trimmed, network)
	if err != nil {
		return nil, err
	}
	return &addr, nil
}

// valueOrEmpty unwraps v for service inputs that model omitted fields as "".
func valueOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
