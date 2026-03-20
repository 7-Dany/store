package userroles

import (
	"context"
	"errors"
	"strings"

	"github.com/7-Dany/store/backend/internal/db"
	rbacshared "github.com/7-Dany/store/backend/internal/domain/rbac/shared"
	platformrbac "github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// compile-time check that *Store satisfies Storer.
var _ Storer = (*Store)(nil)

// Store is the data-access implementation for the userroles package.
type Store struct {
	rbacshared.BaseStore
}

// NewStore constructs a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{BaseStore: rbacshared.NewBaseStore(pool)}
}

// WithQuerier returns a copy of the Store with its querier replaced by q and
// TxBound set to true. Used in integration tests to bind writes to a
// rolled-back test transaction.
func (s *Store) WithQuerier(q db.Querier) *Store {
	c := *s
	c.BaseStore = s.BaseStore.WithQuerier(q)
	return &c
}

// GetUserRole returns the user's current active role assignment.
// Returns ErrUserRoleNotFound on no-rows.
func (s *Store) GetUserRole(ctx context.Context, userID [16]byte) (UserRole, error) {
	row, err := s.Queries.GetUserRole(ctx, s.ToPgtypeUUID(userID))
	if err != nil {
		if s.IsNoRows(err) {
			return UserRole{}, ErrUserRoleNotFound
		}
		return UserRole{}, telemetry.Store("GetUserRole.query", err)
	}
	return mapUserRole(row), nil
}

// AssignUserRoleTx upserts the user's role inside a transaction and returns
// the full UserRole. Verifies the role exists and is active before the upsert.
// Re-reads after upsert to get role_name, is_owner_role, and granted_reason.
func (s *Store) AssignUserRoleTx(ctx context.Context, in AssignRoleTxInput) (UserRole, error) {
	h, err := s.BeginOrBind(ctx)
	if err != nil {
		// Unreachable: BeginOrBind with TxBound=true never calls Pool.Begin
		// and always returns nil error. No test can trigger this branch.
		return UserRole{}, telemetry.Store("AssignUserRoleTx.begin", err)
	}
	defer func() { _ = h.Rollback() }()

	// 1. Verify the role exists and is active.
	// GetRoleByID has no is_active filter in the SQL query — other callers use
	// it to inspect inactive roles intentionally. We apply the filter here.
	role, err := h.Q.GetRoleByID(ctx, s.ToPgtypeUUID(in.RoleID))
	if err != nil {
		if s.IsNoRows(err) {
			return UserRole{}, ErrRoleNotFound
		}
		return UserRole{}, telemetry.Store("AssignUserRoleTx.check_role", err)
	}
	if !role.IsActive {
		return UserRole{}, ErrRoleNotFound
	}
	if role.IsOwnerRole {
		return UserRole{}, platformrbac.ErrCannotReassignOwner
	}

	// 2. Upsert.
	var expiresAt pgtype.Timestamptz
	if in.ExpiresAt != nil {
		expiresAt = pgtype.Timestamptz{Time: *in.ExpiresAt, Valid: true}
	}
	_, err = h.Q.AssignUserRole(ctx, db.AssignUserRoleParams{
		UserID:        s.ToPgtypeUUID(in.UserID),
		RoleID:        s.ToPgtypeUUID(in.RoleID),
		GrantedBy:     s.ToPgtypeUUID(in.GrantedBy),
		GrantedReason: in.GrantedReason,
		ExpiresAt:     expiresAt,
	})
	if err != nil {
		return UserRole{}, telemetry.Store("AssignUserRoleTx.upsert", err)
	}

	// 3. Re-read to get role_name, is_owner_role, and granted_reason.
	row, err := h.Q.GetUserRole(ctx, s.ToPgtypeUUID(in.UserID))
	if err != nil {
		return UserRole{}, telemetry.Store("AssignUserRoleTx.re_read", err)
	}

	if err := h.Commit(); err != nil {
		// Unreachable via QuerierProxy: on the TxBound path Commit is a no-op;
		// on the non-TxBound path it wraps pgx.Tx.Commit which the proxy cannot
		// intercept.
		return UserRole{}, telemetry.Store("AssignUserRoleTx.commit", err)
	}
	return mapUserRole(row), nil
}

// RemoveUserRole hard-deletes a user's active role assignment.
// Uses WithActingUser so fn_audit_user_roles records the correct deletion actor.
// Returns ErrUserRoleNotFound when the user has no active assignment.
// Returns ErrLastOwnerRemoval when fn_prevent_orphaned_owner fires (23000).
func (s *Store) RemoveUserRole(ctx context.Context, userID [16]byte, actingUserID string) error {
	var rowsAffected int64
	err := s.WithActingUser(ctx, actingUserID, func() error {
		n, err := s.Queries.RemoveUserRole(ctx, s.ToPgtypeUUID(userID))
		if err != nil {
			return err
		}
		rowsAffected = n
		return nil
	})
	if err != nil {
		if isOrphanedOwnerViolation(err) {
			return ErrLastOwnerRemoval
		}
		return telemetry.Store("RemoveUserRole.delete", err)
	}
	if rowsAffected == 0 {
		return ErrUserRoleNotFound
	}
	return nil
}

// isOrphanedOwnerViolation reports whether err is the fn_prevent_orphaned_owner
// trigger error (SQLSTATE 23000 + "last active owner" in the message).
func isOrphanedOwnerViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23000" && strings.Contains(pgErr.Message, "last active owner")
	}
	return false
}

// mapUserRole converts a db.GetUserRoleRow to a service-layer UserRole.
// GrantedBy is not included: the GetUserRole SQL query does not SELECT
// ur.granted_by — see models.go for the documented rationale.
func mapUserRole(row db.GetUserRoleRow) UserRole {
	ur := UserRole{
		UserID:        uuid.UUID(row.UserID.Bytes).String(),
		RoleID:        uuid.UUID(row.RoleID.Bytes).String(),
		RoleName:      row.RoleName,
		IsOwnerRole:   row.IsOwnerRole,
		GrantedReason: row.GrantedReason,
		GrantedAt:     row.GrantedAt,
	}
	if row.ExpiresAt.Valid {
		t := row.ExpiresAt.Time
		ur.ExpiresAt = &t
	}
	return ur
}
