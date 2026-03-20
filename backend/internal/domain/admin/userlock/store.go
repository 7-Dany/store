package userlock

import (
	"context"

	"github.com/7-Dany/store/backend/internal/db"
	rbacshared "github.com/7-Dany/store/backend/internal/domain/rbac/shared"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// compile-time check that *Store satisfies Storer.
var _ Storer = (*Store)(nil)

// Store is the data-access implementation for the userlock package.
type Store struct {
	rbacshared.BaseStore
}

// NewStore constructs a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{BaseStore: rbacshared.NewBaseStore(pool)}
}

// WithQuerier returns a copy of the Store with its querier replaced by q.
// Used in integration tests to bind writes to a rolled-back test transaction.
func (s *Store) WithQuerier(q db.Querier) *Store {
	c := *s
	c.BaseStore = s.BaseStore.WithQuerier(q)
	return &c
}

// IsOwnerUser returns true when userID holds a role with is_owner_role = TRUE.
// Returns false, nil on pgx.ErrNoRows (no role assignment = not owner).
func (s *Store) IsOwnerUser(ctx context.Context, userID [16]byte) (bool, error) {
	row, err := s.Queries.GetUserRole(ctx, s.ToPgtypeUUID(userID))
	if err != nil {
		if s.IsNoRows(err) {
			return false, nil
		}
		return false, telemetry.Store("IsOwnerUser.get_user_role", err)
	}
	return row.IsOwnerRole, nil
}

// GetLockStatus returns the full lock state for userID.
// Returns ErrUserNotFound on pgx.ErrNoRows (user not found or deleted).
func (s *Store) GetLockStatus(ctx context.Context, userID [16]byte) (UserLockStatus, error) {
	row, err := s.Queries.GetUserLockStatus(ctx, s.ToPgtypeUUID(userID))
	if err != nil {
		if s.IsNoRows(err) {
			return UserLockStatus{}, ErrUserNotFound
		}
		return UserLockStatus{}, telemetry.Store("GetLockStatus.query", err)
	}
	return mapLockStatus(row), nil
}

// LockUserTx sets admin_locked = TRUE with metadata in user_secrets.
// Uses WithActingUser so the audit trigger records the correct actor.
// Security: detach from the request context so a client-timed disconnect cannot
// abort the lock write (admin security action, §3.6 / ADR-004).
func (s *Store) LockUserTx(ctx context.Context, in LockUserTxInput) error {
	actingUserIDStr := uuid.UUID(in.LockedBy).String()
	safeCtx := context.WithoutCancel(ctx)
	err := s.WithActingUser(safeCtx, actingUserIDStr, func() error {
		return s.Queries.LockUser(safeCtx, db.LockUserParams{
			LockedBy: s.ToPgtypeUUID(in.LockedBy),
			Reason:   pgtype.Text{String: in.Reason, Valid: true},
			UserID:   s.ToPgtypeUUID(in.UserID),
		})
	})
	if err != nil {
		return telemetry.Store("LockUserTx.lock", err)
	}
	return nil
}

// UnlockUserTx clears admin_locked and all metadata in user_secrets.
// Uses WithActingUser so the audit trigger records the correct actor.
// Security: detach from the request context so a client-timed disconnect cannot
// abort the unlock write (§3.6 / ADR-004). Both SetActingUser and the write
// query use safeCtx so neither can be cancelled mid-operation.
func (s *Store) UnlockUserTx(ctx context.Context, userID [16]byte, actingUserID string) error {
	safeCtx := context.WithoutCancel(ctx)
	err := s.WithActingUser(safeCtx, actingUserID, func() error {
		return s.Queries.UnlockUser(safeCtx, s.ToPgtypeUUID(userID))
	})
	if err != nil {
		return telemetry.Store("UnlockUserTx.unlock", err)
	}
	return nil
}

// ── unexported helpers ────────────────────────────────────────────────────────

func mapLockStatus(row db.GetUserLockStatusRow) UserLockStatus {
	s := UserLockStatus{
		UserID:      uuid.UUID(row.ID).String(),
		AdminLocked: row.AdminLocked,
		IsLocked:    row.IsLocked,
	}
	if row.AdminLockedBy.Valid {
		str := uuid.UUID(row.AdminLockedBy.Bytes).String()
		s.LockedBy = &str
	}
	if row.AdminLockedReason.Valid {
		s.LockedReason = &row.AdminLockedReason.String
	}
	if row.AdminLockedAt.Valid {
		t := row.AdminLockedAt.Time
		s.LockedAt = &t
	}
	if row.LoginLockedUntil.Valid {
		t := row.LoginLockedUntil.Time
		s.LoginLockedUntil = &t
	}
	return s
}
