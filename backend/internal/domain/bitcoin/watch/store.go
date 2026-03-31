package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

// compile-time check that *Store satisfies Storer.
var _ Storer = (*Store)(nil)

// Store is the SQL-backed data layer for watch CRUD operations.
type Store struct {
	pool      *pgxpool.Pool
	q         *db.Queries
	conflicts watchConflictInspector
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
		conflicts: watchConflictInspector{},
	}
}

// CreateWatch inserts one active watch resource.
func (s *Store) CreateWatch(ctx context.Context, in watchWriteInput) (Watch, error) {
	if s.q == nil {
		return Watch{}, fmt.Errorf("watch.CreateWatch: no database pool configured")
	}

	row, err := s.q.CreateBitcoinWatch(ctx, db.CreateBitcoinWatchParams{
		UserID:    toPgUUID(in.UserID),
		Network:   in.Network,
		WatchType: string(in.WatchType),
		Address:   toPgText(in.Address),
		Txid:      toPgText(in.TxID),
	})
	if err != nil {
		if s.conflicts.IsUniqueViolation(err, "uq_bw_active_address") || s.conflicts.IsUniqueViolation(err, "uq_bw_active_transaction") {
			return Watch{}, ErrWatchExists
		}
		return Watch{}, telemetry.Store("CreateWatch.create", err)
	}
	return watchFromDB(row), nil
}

// GetWatch returns one active watch resource by ID and owner.
func (s *Store) GetWatch(ctx context.Context, in GetWatchInput) (Watch, error) {
	if s.q == nil {
		return Watch{}, fmt.Errorf("watch.GetWatch: no database pool configured")
	}

	userID, err := parseUUID(in.UserID)
	if err != nil {
		return Watch{}, err
	}
	row, err := s.q.GetBitcoinWatchByID(ctx, db.GetBitcoinWatchByIDParams{
		ID:     in.ID,
		UserID: toPgUUID(userID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Watch{}, ErrWatchNotFound
		}
		return Watch{}, telemetry.Store("GetWatch.get", err)
	}
	return watchFromDB(row), nil
}

// ListWatches returns active watch resources for one user.
func (s *Store) ListWatches(ctx context.Context, in ListWatchesInput) ([]Watch, error) {
	if s.q == nil {
		return nil, fmt.Errorf("watch.ListWatches: no database pool configured")
	}

	userID, err := parseUUID(in.UserID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListBitcoinWatches(ctx, db.ListBitcoinWatchesParams{
		UserID:          toPgUUID(userID),
		Network:         in.Network,
		WatchType:       in.WatchType,
		Address:         in.Address,
		Txid:            in.TxID,
		BeforeCreatedAt: toPgTimePtr(in.BeforeCreatedAt),
		BeforeID:        in.BeforeID,
		LimitRows:       int32(in.Limit),
	})
	if err != nil {
		return nil, telemetry.Store("ListWatches.list", err)
	}

	items := make([]Watch, 0, len(rows))
	for _, row := range rows {
		items = append(items, watchFromDB(row))
	}
	return items, nil
}

// UpdateWatch updates one active watch resource.
func (s *Store) UpdateWatch(ctx context.Context, in watchUpdateInput) (Watch, error) {
	if s.q == nil {
		return Watch{}, fmt.Errorf("watch.UpdateWatch: no database pool configured")
	}

	row, err := s.q.UpdateBitcoinWatchByID(ctx, db.UpdateBitcoinWatchByIDParams{
		WatchType: string(in.WatchType),
		Address:   toPgText(in.Address),
		Txid:      toPgText(in.TxID),
		ID:        in.ID,
		UserID:    toPgUUID(in.UserID),
	})
	if err != nil {
		if s.conflicts.IsUniqueViolation(err, "uq_bw_active_address") || s.conflicts.IsUniqueViolation(err, "uq_bw_active_transaction") {
			return Watch{}, ErrWatchExists
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return Watch{}, ErrWatchNotFound
		}
		return Watch{}, telemetry.Store("UpdateWatch.update", err)
	}
	return watchFromDB(row), nil
}

// DeleteWatch deletes one watch resource.
func (s *Store) DeleteWatch(ctx context.Context, in DeleteWatchInput) error {
	if s.q == nil {
		return fmt.Errorf("watch.DeleteWatch: no database pool configured")
	}

	userID, err := parseUUID(in.UserID)
	if err != nil {
		return err
	}
	rows, err := s.q.DeleteBitcoinWatchByID(ctx, db.DeleteBitcoinWatchByIDParams{
		ID:     in.ID,
		UserID: toPgUUID(userID),
	})
	if err != nil {
		return telemetry.Store("DeleteWatch.delete", err)
	}
	if rows == 0 {
		return ErrWatchNotFound
	}
	return nil
}

// CountActiveAddressWatches returns the active address-watch count for one user.
func (s *Store) CountActiveAddressWatches(ctx context.Context, userID, network string) (int, error) {
	if s.q == nil {
		return 0, fmt.Errorf("watch.CountActiveAddressWatches: no database pool configured")
	}
	uid, err := parseUUID(userID)
	if err != nil {
		return 0, err
	}
	count, err := s.q.CountActiveBitcoinAddressWatchesByUser(ctx, db.CountActiveBitcoinAddressWatchesByUserParams{
		UserID:  toPgUUID(uid),
		Network: network,
	})
	if err != nil {
		return 0, telemetry.Store("CountActiveAddressWatches.count", err)
	}
	return int(count), nil
}

// WriteAuditLog inserts one row into auth_audit_log.
func (s *Store) WriteAuditLog(ctx context.Context, event audit.EventType, userID, sourceIP string, metadata map[string]string) error {
	if s.q == nil {
		return fmt.Errorf("watch.WriteAuditLog: no database pool configured")
	}

	var pgUserID pgtype.UUID
	if userID != "" {
		uid, err := uuid.Parse(userID)
		if err == nil {
			pgUserID = pgtype.UUID{Bytes: [16]byte(uid), Valid: true}
		}
	}

	var ipAddr *netip.Addr
	if sourceIP != "" {
		if parsed, err := netip.ParseAddr(sourceIP); err == nil {
			ipAddr = &parsed
		}
	}

	metaBytes := []byte("{}")
	if len(metadata) > 0 {
		if b, err := json.Marshal(metadata); err == nil {
			metaBytes = b
		}
	}

	return s.q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		UserID:    pgUserID,
		EventType: string(event),
		Provider:  db.AuthProviderEmail,
		IpAddress: ipAddr,
		UserAgent: pgtype.Text{},
		Metadata:  metaBytes,
	})
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

func toPgTimePtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func watchFromDB(row db.BtcWatch) Watch {
	var address *string
	if row.Address.Valid {
		address = &row.Address.String
	}
	var txid *string
	if row.Txid.Valid {
		txid = &row.Txid.String
	}
	return Watch{
		ID:        row.ID,
		Network:   row.Network,
		WatchType: WatchType(row.WatchType),
		Address:   address,
		TxID:      txid,
		Status:    WatchStatus(row.Status),
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
}

type watchConflictInspector struct{}

func (watchConflictInspector) IsUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

type watchWriteInput struct {
	UserID    uuid.UUID
	Network   string
	WatchType WatchType
	Address   *string
	TxID      *string
}

type watchUpdateInput struct {
	ID int64
	watchWriteInput
}
