package watch

import (
	"context"
	"errors"
	"net/http"

	"github.com/7-Dany/store/backend/internal/audit"
	bitcoinshared "github.com/7-Dany/store/backend/internal/domain/bitcoin/shared"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// ── Constants ─────────────────────────────────────────────────────────────────

// maxAddressesPerRequest is the maximum number of Bitcoin addresses that may be
// submitted in a single POST /bitcoin/watch request.
const maxAddressesPerRequest = 20

// retryAfterSeconds is the Retry-After header value returned on 503 responses
// when the Redis back-end is temporarily unavailable.
const retryAfterSeconds = "5"

// ── Servicer interface ────────────────────────────────────────────────────────

// Servicer is the subset of the Service that the Handler requires.
// *Service satisfies this interface; tests may supply a fake implementation.
type Servicer interface {
	Watch(ctx context.Context, in WatchInput) (WatchResult, error)
	// WriteAuditLog writes one audit-log row. The handler calls this for
	// pre-service-call events (e.g. invalid address) so all DB writes remain
	// in the store layer rather than in a separate audit writer.
	WriteAuditLog(ctx context.Context, event audit.EventType, userID, sourceIP string, metadata map[string]string) error
}

// ── Handler ───────────────────────────────────────────────────────────────────

// Handler handles HTTP requests for the watch feature.
type Handler struct {
	svc     Servicer
	rec     bitcoinshared.BitcoinRecorder
	network string // BTC_NETWORK — used for network mismatch check and audit
	hmacKey string // BTC_AUDIT_HMAC_KEY — used for invalid-address audit HMAC
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

// ── Watch handler ─────────────────────────────────────────────────────────────

// Watch handles POST /bitcoin/watch.
func (h *Handler) Watch(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	// Extract authenticated user ID from the validated JWT claim.
	userID, ok := token.UserIDFromContext(r.Context())
	if !ok {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	// Decode and bind the JSON request body.
	req, ok := respond.DecodeJSON[WatchRequest](w, r)
	if !ok {
		return
	}

	sourceIP := respond.ClientIP(r)

	// Network mismatch guard — cheapest validation, runs first.
	if req.Network != h.network {
		respond.Error(w, http.StatusBadRequest, "network_mismatch",
			"request network does not match server network")
		return
	}

	// Address array length guards.
	if len(req.Addresses) == 0 {
		respond.Error(w, http.StatusBadRequest, "too_few_addresses",
			"at least one address is required")
		return
	}
	if len(req.Addresses) > maxAddressesPerRequest {
		respond.Error(w, http.StatusBadRequest, "too_many_addresses",
			"at most 20 addresses may be submitted per request")
		return
	}

	// Normalise and validate each address.
	normalised := make([]string, len(req.Addresses))
	for i, raw := range req.Addresses {
		addr, err := bitcoinshared.ValidateAndNormalise(raw, h.network)
		if err != nil {
			// Security: WithoutCancel so a client disconnect cannot abort the
			// audit write for an invalid-address event. Tracing values from
			// r.Context() are preserved; only cancellation is stripped.
			auditCtx := context.WithoutCancel(r.Context())
			_ = h.svc.WriteAuditLog(auditCtx, audit.EventBitcoinWatchInvalidAddress, userID, sourceIP,
				map[string]string{
					"invalid_address_hmac": bitcoinshared.HmacInvalidAddress(h.hmacKey, raw),
				})
			h.rec.OnWatchRejected("invalid_address")
			respond.Error(w, http.StatusBadRequest, "invalid_address",
				"one or more addresses failed validation")
			return
		}
		normalised[i] = addr
	}

	// Delegate to the service.
	result, err := h.svc.Watch(r.Context(), WatchInput{
		UserID:    userID,
		Addresses: normalised,
		Network:   h.network,
		SourceIP:  sourceIP,
	})
	if err != nil {
		switch {
		case errors.Is(err, bitcoinshared.ErrRedisUnavailable):
			w.Header().Set("Retry-After", retryAfterSeconds)
			respond.Error(w, http.StatusServiceUnavailable, "service_unavailable",
				"service temporarily unavailable")
		case errors.Is(err, bitcoinshared.ErrWatchLimitExceeded):
			h.rec.OnWatchRejected("limit_exceeded")
			// Use JSON directly so we can include the reason field.
			respond.JSON(w, http.StatusBadRequest, watchLimitExceededBody{
				Code:    "watch_limit_exceeded",
				Message: "watch address limit exceeded",
				Reason:  "count_cap",
			})
		case errors.Is(err, bitcoinshared.ErrWatchRegistrationExpired):
			h.rec.OnWatchRejected("registration_window_expired")
			respond.JSON(w, http.StatusBadRequest, watchLimitExceededBody{
				Code:    "watch_limit_exceeded",
				Message: "watch registration window expired",
				Reason:  "registration_window_expired",
			})
		default:
			log.Error(r.Context(), "bitcoin.Watch: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	// Success — respond with the watching array.
	respond.JSON(w, http.StatusOK, WatchResponse{Watching: result.Watching})
}
