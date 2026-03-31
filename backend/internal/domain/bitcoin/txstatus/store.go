package txstatus

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/7-Dany/store/backend/internal/db"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// compile-time check that *Store satisfies Storer.
var _ Storer = (*Store)(nil)

// Store is the SQLC-backed data layer for txstatus CRUD operations.
type Store struct {
	pool      *pgxpool.Pool
	q         *db.Queries
	conflicts DBConflictInspector
}

// NewStore constructs a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	var q *db.Queries
	if pool != nil {
		q = db.New(pool)
	}
	return &Store{
		pool:      pool,
		q:         q,
		conflicts: pgConflictInspector{},
	}
}

// CreateTrackedTxStatus inserts one explicit txid tracking row.
func (s *Store) CreateTrackedTxStatus(ctx context.Context, in trackedTxStatusWriteInput) (TrackedTxStatus, error) {
	if s.q == nil {
		return TrackedTxStatus{}, fmt.Errorf("txstatus.CreateTrackedTxStatus: no database pool configured")
	}

	feeRateSatVByte, err := toPgNumeric(in.FeeRateSatVByte)
	if err != nil {
		return TrackedTxStatus{}, fmt.Errorf("txstatus.CreateTrackedTxStatus: invalid fee rate: %w", err)
	}

	row, err := s.q.CreateBitcoinTxStatus(ctx, db.CreateBitcoinTxStatusParams{
		UserID:          toPgUUID(in.UserID),
		Network:         in.Network,
		Address:         toPgText(in.Address),
		Txid:            in.TxID,
		Status:          string(in.Status),
		Confirmations:   int32(in.Confirmations),
		AmountSat:       in.AmountSat,
		FeeRateSatVbyte: feeRateSatVByte,
		FirstSeenAt:     toPgTime(in.FirstSeenAt),
		LastSeenAt:      toPgTime(in.LastSeenAt),
		ConfirmedAt:     toPgTimePtr(in.ConfirmedAt),
		BlockHash:       toPgText(in.BlockHash),
		BlockHeight:     toPgInt8(in.BlockHeight),
		ReplacementTxid: toPgText(in.ReplacementTxID),
	})
	if err != nil {
		if s.conflicts.IsUniqueViolation(err, "uq_btt_user_network_txid") {
			return TrackedTxStatus{}, ErrTrackedTxStatusExists
		}
		return TrackedTxStatus{}, telemetry.Store("CreateTrackedTxStatus.create", err)
	}
	return s.attachRelatedAddresses(ctx, trackedTxStatusFromCreateRow(row))
}

// GetTrackedTxStatus returns one durable txstatus row by ID and owner.
func (s *Store) GetTrackedTxStatus(ctx context.Context, in GetTrackedTxStatusInput) (TrackedTxStatus, error) {
	if s.q == nil {
		return TrackedTxStatus{}, fmt.Errorf("txstatus.GetTrackedTxStatus: no database pool configured")
	}

	userID, err := parseUUID(in.UserID)
	if err != nil {
		return TrackedTxStatus{}, err
	}
	row, err := s.q.GetBitcoinTxStatusByID(ctx, db.GetBitcoinTxStatusByIDParams{
		ID:     in.ID,
		UserID: toPgUUID(userID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TrackedTxStatus{}, ErrTrackedTxStatusNotFound
		}
		return TrackedTxStatus{}, telemetry.Store("GetTrackedTxStatus.get", err)
	}
	return s.attachRelatedAddresses(ctx, trackedTxStatusFromGetRow(row))
}

// ListTrackedTxStatuses returns durable txstatus rows for one owner with optional filters.
func (s *Store) ListTrackedTxStatuses(ctx context.Context, in ListTrackedTxStatusesInput) ([]TrackedTxStatus, error) {
	if s.q == nil {
		return nil, fmt.Errorf("txstatus.ListTrackedTxStatuses: no database pool configured")
	}

	userID, err := parseUUID(in.UserID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListBitcoinTxStatuses(ctx, db.ListBitcoinTxStatusesParams{
		UserID:         toPgUUID(userID),
		Network:        in.Network,
		Address:        in.Address,
		Txid:           in.TxID,
		TrackingMode:   in.TrackingMode,
		BeforeSortTime: toPgTimePtr(in.BeforeSortTime),
		BeforeID:       in.BeforeID,
		LimitRows:      int32(in.Limit),
	})
	if err != nil {
		return nil, telemetry.Store("ListTrackedTxStatuses.list", err)
	}

	items := make([]TrackedTxStatus, 0, len(rows))
	for _, row := range rows {
		items = append(items, trackedTxStatusFromListRow(row))
	}
	return s.attachRelatedAddressesBatch(ctx, items)
}

// UpdateTrackedTxStatus updates one explicit txid-tracking row.
func (s *Store) UpdateTrackedTxStatus(ctx context.Context, in trackedTxStatusUpdateInput) (TrackedTxStatus, error) {
	if s.q == nil {
		return TrackedTxStatus{}, fmt.Errorf("txstatus.UpdateTrackedTxStatus: no database pool configured")
	}

	feeRateSatVByte, err := toPgNumeric(in.FeeRateSatVByte)
	if err != nil {
		return TrackedTxStatus{}, fmt.Errorf("txstatus.UpdateTrackedTxStatus: invalid fee rate: %w", err)
	}

	row, err := s.q.UpdateBitcoinTxStatusByID(ctx, db.UpdateBitcoinTxStatusByIDParams{
		Address:         toPgText(in.Address),
		Status:          string(in.Status),
		Confirmations:   int32(in.Confirmations),
		AmountSat:       in.AmountSat,
		FeeRateSatVbyte: feeRateSatVByte,
		FirstSeenAt:     toPgTime(in.FirstSeenAt),
		LastSeenAt:      toPgTime(in.LastSeenAt),
		ConfirmedAt:     toPgTimePtr(in.ConfirmedAt),
		BlockHash:       toPgText(in.BlockHash),
		BlockHeight:     toPgInt8(in.BlockHeight),
		ReplacementTxid: toPgText(in.ReplacementTxID),
		ID:              in.ID,
		UserID:          toPgUUID(in.UserID),
	})
	if err != nil {
		if s.conflicts.IsUniqueViolation(err, "uq_btt_user_network_txid") {
			return TrackedTxStatus{}, ErrTrackedTxStatusExists
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return TrackedTxStatus{}, ErrTrackedTxStatusNotFound
		}
		return TrackedTxStatus{}, telemetry.Store("UpdateTrackedTxStatus.update", err)
	}
	return s.attachRelatedAddresses(ctx, trackedTxStatusFromUpdateRow(row))
}

// DeleteTrackedTxStatus removes one txstatus row.
func (s *Store) DeleteTrackedTxStatus(ctx context.Context, in DeleteTrackedTxStatusInput) error {
	if s.q == nil {
		return fmt.Errorf("txstatus.DeleteTrackedTxStatus: no database pool configured")
	}

	userID, err := parseUUID(in.UserID)
	if err != nil {
		return err
	}
	rows, err := s.q.DeleteBitcoinTxStatusByID(ctx, db.DeleteBitcoinTxStatusByIDParams{
		ID:     in.ID,
		UserID: toPgUUID(userID),
	})
	if err != nil {
		return telemetry.Store("DeleteTrackedTxStatus.delete", err)
	}
	if rows == 0 {
		return ErrTrackedTxStatusNotFound
	}
	return nil
}

func parseUUID(raw string) (uuid.UUID, error) {
	uid, err := uuid.Parse(raw)
	if err != nil {
		return uuid.UUID{}, telemetry.Store("parseUUID.parse", err)
	}
	return uid, nil
}

func toPgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte(id), Valid: true}
}

func toPgText(v *string) pgtype.Text {
	if v == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *v, Valid: true}
}

func toPgTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func toPgTimePtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func toPgInt8(v *int64) pgtype.Int8 {
	if v == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *v, Valid: true}
}

func toPgNumeric(v float64) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	err := n.Scan(v)
	return n, err
}

func numericToFloat64(v pgtype.Numeric) float64 {
	if !v.Valid {
		return 0
	}
	f, err := v.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return f.Float64
}

func (s *Store) attachRelatedAddresses(ctx context.Context, row TrackedTxStatus) (TrackedTxStatus, error) {
	items, err := s.attachRelatedAddressesBatch(ctx, []TrackedTxStatus{row})
	if err != nil {
		return TrackedTxStatus{}, err
	}
	return items[0], nil
}

func (s *Store) attachRelatedAddressesBatch(ctx context.Context, items []TrackedTxStatus) ([]TrackedTxStatus, error) {
	if len(items) == 0 {
		return items, nil
	}

	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}

	related, err := s.q.ListBitcoinTxStatusRelatedAddressesByStatusIDs(ctx, ids)
	if err != nil {
		return nil, telemetry.Store("ListBitcoinTxStatusRelatedAddressesByStatusIDs.list", err)
	}

	byID := make(map[int64][]TrackedTxStatusAddress, len(ids))
	for _, row := range related {
		byID[row.TxStatusID] = append(byID[row.TxStatusID], TrackedTxStatusAddress{
			Address:   row.Address,
			AmountSat: row.AmountSat,
		})
	}

	for i := range items {
		items[i].Addresses = byID[items[i].ID]
	}
	return items, nil
}

func trackedTxStatusFromFields(
	id int64,
	userID pgtype.UUID,
	network string,
	trackingMode string,
	addressValue pgtype.Text,
	txid string,
	status string,
	confirmations int32,
	amountSat int64,
	feeRateSatVByte pgtype.Numeric,
	firstSeenAt pgtype.Timestamptz,
	lastSeenAt pgtype.Timestamptz,
	confirmedAtValue pgtype.Timestamptz,
	blockHashValue pgtype.Text,
	blockHeightValue pgtype.Int8,
	replacementTxidValue pgtype.Text,
	createdAt time.Time,
	updatedAt time.Time,
) TrackedTxStatus {
	var address *string
	if addressValue.Valid {
		address = &addressValue.String
	}
	var confirmedAt *time.Time
	if confirmedAtValue.Valid {
		t := confirmedAtValue.Time
		confirmedAt = &t
	}
	var blockHash *string
	if blockHashValue.Valid {
		blockHash = &blockHashValue.String
	}
	var blockHeight *int64
	if blockHeightValue.Valid {
		h := blockHeightValue.Int64
		blockHeight = &h
	}
	var replacementTxID *string
	if replacementTxidValue.Valid {
		replacement := replacementTxidValue.String
		replacementTxID = &replacement
	}

	return TrackedTxStatus{
		ID:              id,
		UserID:          uuid.UUID(userID.Bytes).String(),
		Network:         network,
		TrackingMode:    TrackingMode(trackingMode),
		Address:         address,
		TxID:            txid,
		Status:          TxStatus(status),
		Confirmations:   int(confirmations),
		AmountSat:       amountSat,
		FeeRateSatVByte: numericToFloat64(feeRateSatVByte),
		FirstSeenAt:     firstSeenAt.Time,
		LastSeenAt:      lastSeenAt.Time,
		ConfirmedAt:     confirmedAt,
		BlockHash:       blockHash,
		BlockHeight:     blockHeight,
		ReplacementTxID: replacementTxID,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
	}
}

func trackedTxStatusFromCreateRow(row db.BtcTrackedTransaction) TrackedTxStatus {
	return trackedTxStatusFromFields(
		row.ID, row.UserID, row.Network, row.TrackingMode, row.Address, row.Txid, row.Status,
		row.Confirmations, row.AmountSat, row.FeeRateSatVbyte, row.FirstSeenAt, row.LastSeenAt,
		row.ConfirmedAt, row.BlockHash, row.BlockHeight, row.ReplacementTxid, row.CreatedAt, row.UpdatedAt,
	)
}

func trackedTxStatusFromGetRow(row db.BtcTrackedTransaction) TrackedTxStatus {
	return trackedTxStatusFromFields(
		row.ID, row.UserID, row.Network, row.TrackingMode, row.Address, row.Txid, row.Status,
		row.Confirmations, row.AmountSat, row.FeeRateSatVbyte, row.FirstSeenAt, row.LastSeenAt,
		row.ConfirmedAt, row.BlockHash, row.BlockHeight, row.ReplacementTxid, row.CreatedAt, row.UpdatedAt,
	)
}

func trackedTxStatusFromListRow(row db.ListBitcoinTxStatusesRow) TrackedTxStatus {
	return trackedTxStatusFromFields(
		row.ID, row.UserID, row.Network, row.TrackingMode, row.Address, row.Txid, row.Status,
		row.Confirmations, row.AmountSat, row.FeeRateSatVbyte, row.FirstSeenAt, row.LastSeenAt,
		row.ConfirmedAt, row.BlockHash, row.BlockHeight, row.ReplacementTxid, row.CreatedAt, row.UpdatedAt,
	)
}

func trackedTxStatusFromUpdateRow(row db.BtcTrackedTransaction) TrackedTxStatus {
	return trackedTxStatusFromFields(
		row.ID, row.UserID, row.Network, row.TrackingMode, row.Address, row.Txid, row.Status,
		row.Confirmations, row.AmountSat, row.FeeRateSatVbyte, row.FirstSeenAt, row.LastSeenAt,
		row.ConfirmedAt, row.BlockHash, row.BlockHeight, row.ReplacementTxid, row.CreatedAt, row.UpdatedAt,
	)
}
