package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	rbacshared "github.com/7-Dany/store/backend/internal/domain/rbac/shared"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// compile-time check: Store implements Storer.
var _ Storer = (*Store)(nil)

// Store is the concrete implementation of Storer.
type Store struct {
	rbacshared.BaseStore
}

// NewStore constructs a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{BaseStore: rbacshared.NewBaseStore(pool)}
}

// WithQuerier returns a copy of the store with its querier replaced by q and
// TxBound set to true. Used in integration tests to bind the store to a
// rolled-back test transaction.
func (s *Store) WithQuerier(q db.Querier) *Store {
	return &Store{BaseStore: s.BaseStore.WithQuerier(q)}
}

// CountActiveOwners returns the number of active owner role assignments.
func (s *Store) CountActiveOwners(ctx context.Context) (int64, error) {
	c, err := s.Queries.CountActiveOwners(ctx)
	if err != nil {
		return 0, fmt.Errorf("store.CountActiveOwners: %w", err)
	}
	return c, nil
}

// GetOwnerRoleID returns the owner role's primary key as a [16]byte UUID.
func (s *Store) GetOwnerRoleID(ctx context.Context) ([16]byte, error) {
	id, err := s.Queries.GetOwnerRoleID(ctx)
	if err != nil {
		return [16]byte{}, fmt.Errorf("store.GetOwnerRoleID: %w", err)
	}
	return [16]byte(id), nil
}

// GetActiveUserByID fetches a user row by ID and maps it to BootstrapUser.
// Returns rbacshared.ErrUserNotFound on a no-rows result.
func (s *Store) GetActiveUserByID(ctx context.Context, userID [16]byte) (BootstrapUser, error) {
	row, err := s.Queries.GetActiveUserByID(ctx, s.ToPgtypeUUID(userID))
	if err != nil {
		if s.IsNoRows(err) {
			return BootstrapUser{}, rbacshared.ErrUserNotFound
		}
		return BootstrapUser{}, fmt.Errorf("store.GetActiveUserByID: %w", err)
	}
	return BootstrapUser{
		IsActive:      row.IsActive,
		EmailVerified: row.EmailVerified,
	}, nil
}

// BootstrapOwnerTx assigns the owner role to in.UserID in a single transaction
// and writes an owner_bootstrapped audit record. On error the transaction is
// rolled back. The audit write uses context.WithoutCancel so a client disconnect
// cannot suppress the forensic trail for this irreversible privilege escalation.
func (s *Store) BootstrapOwnerTx(ctx context.Context, in BootstrapTxInput) (BootstrapResult, error) {
	h, err := s.BeginOrBind(ctx)
	if err != nil {
		// Unreachable: BeginOrBind with TxBound=true never calls Pool.Begin;
		// no test can trigger this path.
		return BootstrapResult{}, fmt.Errorf("store.BootstrapOwnerTx: begin tx: %w", err)
	}

	row, err := h.Q.AssignUserRole(ctx, db.AssignUserRoleParams{
		UserID:        s.ToPgtypeUUID(in.UserID),
		RoleID:        s.ToPgtypeUUID(in.RoleID),
		GrantedBy:     s.ToPgtypeUUID(in.UserID), // self-grant — valid only on the bootstrap path
		GrantedReason: "system bootstrap",
		ExpiresAt:     pgtype.Timestamptz{Valid: false}, // permanent grant
	})
	if err != nil {
		if rErr := h.Rollback(); rErr != nil {
			slog.ErrorContext(ctx, "store.BootstrapOwnerTx: rollback", "error", rErr)
		}
		return BootstrapResult{}, fmt.Errorf("store.BootstrapOwnerTx: assign role: %w", err)
	}

	// Security: write audit record with context.WithoutCancel so a client
	// disconnect cannot suppress the forensic trail for this irreversible
	// privilege escalation.
	if err := h.Q.InsertAuditLog(context.WithoutCancel(ctx), db.InsertAuditLogParams{
		UserID:    s.ToPgtypeUUID(in.UserID),
		EventType: string(audit.EventOwnerBootstrapped),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(in.IPAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(in.UserAgent)),
		Metadata:  []byte("{}"),
	}); err != nil {
		if rErr := h.Rollback(); rErr != nil {
			slog.ErrorContext(ctx, "store.BootstrapOwnerTx: rollback after audit", "error", rErr)
		}
		return BootstrapResult{}, fmt.Errorf("store.BootstrapOwnerTx: audit log: %w", err)
	}

	if err := h.Commit(); err != nil {
		// Unreachable via QuerierProxy: Commit wraps pgx.Tx.Commit which the
		// proxy cannot intercept.
		return BootstrapResult{}, fmt.Errorf("store.BootstrapOwnerTx: commit: %w", err)
	}

	return BootstrapResult{
		UserID:    uuid.UUID(in.UserID).String(),
		RoleName:  "owner",
		GrantedAt: row.CreatedAt,
	}, nil
}
