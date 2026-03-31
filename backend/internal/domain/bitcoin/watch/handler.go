package watch

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/audit"
	bitcoinshared "github.com/7-Dany/store/backend/internal/domain/bitcoin/shared"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

const maxWatchCRUDBodyBytes = 4 << 10

var (
	errInvalidWatchType    = errors.New("invalid watch type")
	errInvalidWatchAddress = errors.New("invalid watch address")
	errInvalidWatchTxID    = errors.New("invalid watch txid")
	errInvalidWatchShape   = errors.New("invalid watch target shape")
)

// Servicer is the subset of the Service that the Handler requires.
type Servicer interface {
	CreateWatch(ctx context.Context, in CreateWatchInput) (Watch, error)
	GetWatch(ctx context.Context, in GetWatchInput) (Watch, error)
	ListWatches(ctx context.Context, in ListWatchesInput) ([]Watch, error)
	UpdateWatch(ctx context.Context, in UpdateWatchInput) (Watch, error)
	DeleteWatch(ctx context.Context, in DeleteWatchInput) error
	WriteAuditLog(ctx context.Context, event audit.EventType, userID, sourceIP string, metadata map[string]string) error
}

// Handler handles HTTP requests for the watch feature.
type Handler struct {
	svc     Servicer
	rec     bitcoinshared.BitcoinRecorder
	network string
	hmacKey string
}

// NewHandler constructs a Handler.
func NewHandler(svc Servicer, rec bitcoinshared.BitcoinRecorder, network, hmacKey string) *Handler {
	return &Handler{
		svc:     svc,
		rec:     rec,
		network: network,
		hmacKey: hmacKey,
	}
}

// CreateWatch handles POST /bitcoin/watch.
func (h *Handler) CreateWatch(w http.ResponseWriter, r *http.Request) {
	userID, ok := token.UserIDFromContext(r.Context())
	if !ok {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxWatchCRUDBodyBytes)
	req, ok := respond.DecodeJSON[CreateWatchRequest](w, r)
	if !ok {
		return
	}
	sourceIP := respond.ClientIP(r)
	if req.Network != h.network {
		respond.Error(w, http.StatusBadRequest, "network_mismatch", "request network does not match server network")
		return
	}

	watchType, address, txid, err := h.normaliseWatchTarget(r.Context(), userID, sourceIP, req.WatchType, req.Address, req.TxID)
	if err != nil {
		h.handleInputError(w, err)
		return
	}

	row, err := h.svc.CreateWatch(r.Context(), CreateWatchInput{
		UserID:    userID,
		Network:   h.network,
		WatchType: watchType,
		Address:   address,
		TxID:      txid,
		SourceIP:  sourceIP,
	})
	if err != nil {
		switch {
		case errors.Is(err, bitcoinshared.ErrWatchLimitExceeded):
			h.rec.OnWatchRejected("limit_exceeded")
			respond.Error(w, http.StatusBadRequest, "watch_limit_exceeded", "watch address limit exceeded")
		case errors.Is(err, ErrWatchExists):
			respond.Error(w, http.StatusConflict, "watch_exists", "watch already exists")
		default:
			log.Error(r.Context(), "watch.CreateWatch: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	respond.JSON(w, http.StatusCreated, buildWatchResponse(row))
}

// GetWatch handles GET /bitcoin/watch/{id}.
func (h *Handler) GetWatch(w http.ResponseWriter, r *http.Request) {
	userID, ok := token.UserIDFromContext(r.Context())
	if !ok {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	id, ok := parseWatchID(w, r)
	if !ok {
		return
	}

	row, err := h.svc.GetWatch(r.Context(), GetWatchInput{ID: id, UserID: userID})
	if err != nil {
		if errors.Is(err, ErrWatchNotFound) {
			respond.Error(w, http.StatusNotFound, "watch_not_found", "watch resource was not found")
			return
		}
		log.Error(r.Context(), "watch.GetWatch: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	respond.JSON(w, http.StatusOK, buildWatchResponse(row))
}

// ListWatches handles GET /bitcoin/watch.
func (h *Handler) ListWatches(w http.ResponseWriter, r *http.Request) {
	userID, ok := token.UserIDFromContext(r.Context())
	if !ok {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	query := r.URL.Query()
	watchType := strings.TrimSpace(query.Get("watch_type"))
	if watchType != "" && watchType != string(WatchTypeAddress) && watchType != string(WatchTypeTransaction) {
		respond.Error(w, http.StatusBadRequest, "invalid_watch_type", "watch_type must be address or transaction")
		return
	}

	address, err := normaliseOptionalAddress(query.Get("address"), h.network)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_address", "address failed validation")
		return
	}
	txid := strings.ToLower(strings.TrimSpace(query.Get("txid")))
	if txid != "" && !isHex64(txid) {
		respond.Error(w, http.StatusBadRequest, "invalid_txid", "txid must be a 64-character hex string")
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

	beforeCreatedAt, beforeID, err := parseWatchCursor(query.Get("cursor"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_cursor", "cursor must be a valid watch pagination cursor")
		return
	}

	rows, err := h.svc.ListWatches(r.Context(), ListWatchesInput{
		UserID:          userID,
		Network:         h.network,
		WatchType:       watchType,
		Address:         valueOrEmpty(address),
		TxID:            txid,
		BeforeCreatedAt: beforeCreatedAt,
		BeforeID:        beforeID,
		Limit:           limit + 1,
	})
	if err != nil {
		log.Error(r.Context(), "watch.ListWatches: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	items := make([]watchResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, buildWatchResponse(row))
	}

	resp := listWatchesResponse{Items: items}
	if hasMore && len(rows) > 0 {
		resp.NextCursor = buildWatchCursor(rows[len(rows)-1])
	}
	respond.JSON(w, http.StatusOK, resp)
}

// UpdateWatch handles PUT /bitcoin/watch/{id}.
func (h *Handler) UpdateWatch(w http.ResponseWriter, r *http.Request) {
	userID, ok := token.UserIDFromContext(r.Context())
	if !ok {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	id, ok := parseWatchID(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxWatchCRUDBodyBytes)
	req, ok := respond.DecodeJSON[UpdateWatchRequest](w, r)
	if !ok {
		return
	}
	sourceIP := respond.ClientIP(r)

	watchType, address, txid, err := h.normaliseWatchTarget(r.Context(), userID, sourceIP, req.WatchType, req.Address, req.TxID)
	if err != nil {
		h.handleInputError(w, err)
		return
	}

	row, err := h.svc.UpdateWatch(r.Context(), UpdateWatchInput{
		ID:        id,
		UserID:    userID,
		WatchType: watchType,
		Address:   address,
		TxID:      txid,
		SourceIP:  sourceIP,
	})
	if err != nil {
		switch {
		case errors.Is(err, bitcoinshared.ErrWatchLimitExceeded):
			h.rec.OnWatchRejected("limit_exceeded")
			respond.Error(w, http.StatusBadRequest, "watch_limit_exceeded", "watch address limit exceeded")
		case errors.Is(err, ErrWatchExists):
			respond.Error(w, http.StatusConflict, "watch_exists", "watch already exists")
		case errors.Is(err, ErrWatchNotFound):
			respond.Error(w, http.StatusNotFound, "watch_not_found", "watch resource was not found")
		default:
			log.Error(r.Context(), "watch.UpdateWatch: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	respond.JSON(w, http.StatusOK, buildWatchResponse(row))
}

// DeleteWatch handles DELETE /bitcoin/watch/{id}.
func (h *Handler) DeleteWatch(w http.ResponseWriter, r *http.Request) {
	userID, ok := token.UserIDFromContext(r.Context())
	if !ok {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	id, ok := parseWatchID(w, r)
	if !ok {
		return
	}

	if err := h.svc.DeleteWatch(r.Context(), DeleteWatchInput{
		ID:       id,
		UserID:   userID,
		SourceIP: respond.ClientIP(r),
	}); err != nil {
		if errors.Is(err, ErrWatchNotFound) {
			respond.Error(w, http.StatusNotFound, "watch_not_found", "watch resource was not found")
			return
		}
		log.Error(r.Context(), "watch.DeleteWatch: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	respond.NoContent(w)
}

func buildWatchResponse(row Watch) watchResponse {
	return watchResponse{
		ID:        row.ID,
		Network:   row.Network,
		WatchType: string(row.WatchType),
		Address:   row.Address,
		TxID:      row.TxID,
		Status:    row.Status,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
}

func (h *Handler) normaliseWatchTarget(ctx context.Context, userID, sourceIP, rawType, rawAddress, rawTxID string) (WatchType, *string, *string, error) {
	watchType := WatchType(strings.TrimSpace(rawType))
	switch watchType {
	case WatchTypeAddress:
		address, err := bitcoinshared.ValidateAndNormalise(strings.TrimSpace(rawAddress), h.network)
		if err != nil {
			auditCtx := context.WithoutCancel(ctx)
			_ = h.svc.WriteAuditLog(auditCtx, audit.EventBitcoinWatchInvalidAddress, userID, sourceIP, map[string]string{
				"invalid_address_hmac": bitcoinshared.HmacInvalidAddress(h.hmacKey, rawAddress),
			})
			h.rec.OnWatchRejected("invalid_address")
			return "", nil, nil, errInvalidWatchAddress
		}
		if strings.TrimSpace(rawTxID) != "" {
			return "", nil, nil, errInvalidWatchShape
		}
		return watchType, &address, nil, nil
	case WatchTypeTransaction:
		txid := strings.ToLower(strings.TrimSpace(rawTxID))
		if strings.TrimSpace(rawAddress) != "" {
			return "", nil, nil, errInvalidWatchShape
		}
		if !isHex64(txid) {
			return "", nil, nil, errInvalidWatchTxID
		}
		return watchType, nil, &txid, nil
	default:
		return "", nil, nil, errInvalidWatchType
	}
}

func (h *Handler) handleInputError(w http.ResponseWriter, err error) {
	switch err {
	case errInvalidWatchType:
		respond.Error(w, http.StatusBadRequest, "invalid_watch_type", "watch_type must be address or transaction")
	case errInvalidWatchAddress:
		respond.Error(w, http.StatusBadRequest, "invalid_address", "address failed validation")
	case errInvalidWatchTxID:
		respond.Error(w, http.StatusBadRequest, "invalid_txid", "txid must be a 64-character hex string")
	default:
		respond.Error(w, http.StatusBadRequest, "invalid_watch_target", "address watches require address only and transaction watches require txid only")
	}
}

func parseWatchID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	rawID := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id < 1 {
		respond.Error(w, http.StatusBadRequest, "invalid_id", "id must be a positive integer")
		return 0, false
	}
	return id, true
}

func parseWatchCursor(raw string) (*time.Time, int64, error) {
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
		return nil, 0, errors.New("invalid watch cursor shape")
	}

	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return nil, 0, err
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id < 1 {
		return nil, 0, errors.New("invalid watch cursor id")
	}
	utc := t.UTC()
	return &utc, id, nil
}

func buildWatchCursor(row Watch) string {
	payload := row.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + strconv.FormatInt(row.ID, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

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

func valueOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func isHex64(txid string) bool {
	if len(txid) != 64 {
		return false
	}
	for i := 0; i < len(txid); i++ {
		c := txid[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
