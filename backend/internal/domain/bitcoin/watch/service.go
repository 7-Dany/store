package watch

import (
	"context"
	"strconv"

	"github.com/7-Dany/store/backend/internal/audit"
	bitcoinshared "github.com/7-Dany/store/backend/internal/domain/bitcoin/shared"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

// log is the package-level logger for the watch feature.
var log = telemetry.New("watch")

// Storer is the data-access contract for the watch service.
type Storer interface {
	CreateWatch(ctx context.Context, in watchWriteInput) (Watch, error)
	GetWatch(ctx context.Context, in GetWatchInput) (Watch, error)
	ListWatches(ctx context.Context, in ListWatchesInput) ([]Watch, error)
	UpdateWatch(ctx context.Context, in watchUpdateInput) (Watch, error)
	DeleteWatch(ctx context.Context, in DeleteWatchInput) error
	CountActiveAddressWatches(ctx context.Context, userID, network string) (int, error)
	WriteAuditLog(ctx context.Context, event audit.EventType, userID, sourceIP string, metadata map[string]string) error
}

// Service implements the business logic for Bitcoin watch CRUD.
type Service struct {
	store Storer
	rec   bitcoinshared.BitcoinRecorder
	limit int
}

// NewService constructs a Service backed by the SQL store.
func NewService(store Storer, rec bitcoinshared.BitcoinRecorder, limit int) *Service {
	if rec == nil {
		rec = bitcoinshared.NoopBitcoinRecorder{}
	}
	return &Service{store: store, rec: rec, limit: limit}
}

// WriteAuditLog delegates to the store so handlers do not talk to persistence directly.
func (s *Service) WriteAuditLog(ctx context.Context, event audit.EventType, userID, sourceIP string, metadata map[string]string) error {
	return s.store.WriteAuditLog(ctx, event, userID, sourceIP, metadata)
}

// CreateWatch creates one address or transaction watch.
func (s *Service) CreateWatch(ctx context.Context, in CreateWatchInput) (Watch, error) {
	if err := s.ensureAddressWatchCapacity(ctx, in.UserID, in.Network, WatchType(""), in.WatchType); err != nil {
		return Watch{}, err
	}

	userID, err := parseUUID(in.UserID)
	if err != nil {
		return Watch{}, telemetry.Service("CreateWatch.parse_user_id", err)
	}

	row, err := s.store.CreateWatch(ctx, watchWriteInput{
		UserID:    userID,
		Network:   in.Network,
		WatchType: in.WatchType,
		Address:   in.Address,
		TxID:      in.TxID,
	})
	if err != nil {
		return Watch{}, telemetry.Service("CreateWatch.create", err)
	}

	if row.WatchType == WatchTypeAddress {
		s.auditAddressWatch(context.WithoutCancel(ctx), "create", row, in.UserID, in.SourceIP)
	}
	return row, nil
}

// GetWatch returns one watch resource by ID.
func (s *Service) GetWatch(ctx context.Context, in GetWatchInput) (Watch, error) {
	row, err := s.store.GetWatch(ctx, in)
	if err != nil {
		return Watch{}, telemetry.Service("GetWatch.get", err)
	}
	return row, nil
}

// ListWatches returns active watches for one user and optional filters.
func (s *Service) ListWatches(ctx context.Context, in ListWatchesInput) ([]Watch, error) {
	rows, err := s.store.ListWatches(ctx, in)
	if err != nil {
		return nil, telemetry.Service("ListWatches.list", err)
	}
	return rows, nil
}

// UpdateWatch mutates one existing watch resource.
func (s *Service) UpdateWatch(ctx context.Context, in UpdateWatchInput) (Watch, error) {
	current, err := s.store.GetWatch(ctx, GetWatchInput{ID: in.ID, UserID: in.UserID})
	if err != nil {
		return Watch{}, telemetry.Service("UpdateWatch.get", err)
	}

	if err := s.ensureAddressWatchCapacity(ctx, in.UserID, current.Network, current.WatchType, in.WatchType); err != nil {
		return Watch{}, err
	}

	userID, err := parseUUID(in.UserID)
	if err != nil {
		return Watch{}, telemetry.Service("UpdateWatch.parse_user_id", err)
	}

	row, err := s.store.UpdateWatch(ctx, watchUpdateInput{
		ID: in.ID,
		watchWriteInput: watchWriteInput{
			UserID:    userID,
			Network:   current.Network,
			WatchType: in.WatchType,
			Address:   in.Address,
			TxID:      in.TxID,
		},
	})
	if err != nil {
		return Watch{}, telemetry.Service("UpdateWatch.update", err)
	}

	if row.WatchType == WatchTypeAddress {
		s.auditAddressWatch(context.WithoutCancel(ctx), "update", row, in.UserID, in.SourceIP)
	}
	return row, nil
}

// DeleteWatch deletes one watch resource.
func (s *Service) DeleteWatch(ctx context.Context, in DeleteWatchInput) error {
	row, err := s.store.GetWatch(ctx, GetWatchInput{ID: in.ID, UserID: in.UserID})
	if err != nil {
		return telemetry.Service("DeleteWatch.get", err)
	}
	if err := s.store.DeleteWatch(ctx, in); err != nil {
		return telemetry.Service("DeleteWatch.delete", err)
	}
	if row.WatchType == WatchTypeAddress {
		s.auditAddressWatch(context.WithoutCancel(ctx), "delete", row, in.UserID, in.SourceIP)
	}
	return nil
}

func (s *Service) ensureAddressWatchCapacity(ctx context.Context, userID, network string, currentType, nextType WatchType) error {
	if nextType != WatchTypeAddress || currentType == WatchTypeAddress || s.limit <= 0 {
		return nil
	}

	count, err := s.store.CountActiveAddressWatches(ctx, userID, network)
	if err != nil {
		return telemetry.Service("ensureAddressWatchCapacity.count", err)
	}
	if count >= s.limit {
		s.rec.OnWatchRejected("limit_exceeded")
		return bitcoinshared.ErrWatchLimitExceeded
	}
	return nil
}

func (s *Service) auditAddressWatch(ctx context.Context, action string, row Watch, userID, sourceIP string) {
	metadata := map[string]string{
		"action":   action,
		"watch_id": strconv.FormatInt(row.ID, 10),
	}
	if row.Address != nil {
		metadata["address"] = *row.Address
	}
	if err := s.store.WriteAuditLog(ctx, audit.EventBitcoinAddressWatched, userID, sourceIP, metadata); err != nil {
		log.Warn(ctx, "watch: failed to write address watch audit log", "watch_id", row.ID, "error", err)
	}
}
