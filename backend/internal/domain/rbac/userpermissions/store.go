package userpermissions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/7-Dany/store/backend/internal/db"
	rbacshared "github.com/7-Dany/store/backend/internal/domain/rbac/shared"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// compile-time check that *Store satisfies Storer.
var _ Storer = (*Store)(nil)

// Store is the data-access implementation for the userpermissions package.
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

// GetUserPermissions returns all active (non-expired) direct grants for userID.
func (s *Store) GetUserPermissions(ctx context.Context, userID [16]byte) ([]UserPermission, error) {
	rows, err := s.Queries.GetUserPermissions(ctx, s.ToPgtypeUUID(userID))
	if err != nil {
		return nil, fmt.Errorf("store.GetUserPermissions: query: %w", err)
	}
	result := make([]UserPermission, len(rows))
	for i, row := range rows {
		result[i] = mapUserPermission(row)
	}
	return result, nil
}

// GrantPermissionTx validates caps, handles re-grant idempotency, and inserts.
func (s *Store) GrantPermissionTx(ctx context.Context, in GrantPermissionTxInput) (UserPermission, error) {
	// 1. Verify permission exists and is active; read capability flags.
	row, err := s.Queries.GetPermissionByID(ctx, s.ToPgtypeUUID(in.PermissionID))
	if err != nil {
		if s.IsNoRows(err) {
			return UserPermission{}, ErrPermissionNotFound
		}
		return UserPermission{}, fmt.Errorf("store.GrantPermissionTx: get permission by id: %w", err)
	}

	// 2. Validate scope against scope_policy (same rules as roles.AddRolePermission).
	scope, err := resolveScope(string(row.ScopePolicy), in.Scope)
	if err != nil {
		return UserPermission{}, err // roles.ErrScopeNotAllowed
	}

	// 3. Try insert; handle 23505 (re-grant) and 23514 (escalation).
	result, err := s.tryGrant(ctx, in, scope)
	if err != nil {
		return UserPermission{}, err
	}
	return result, nil
}

// tryGrant attempts to insert a user_permissions row, handling conflict cases.
func (s *Store) tryGrant(ctx context.Context, in GrantPermissionTxInput, scope string) (UserPermission, error) {
	conds := in.Conditions
	if len(conds) == 0 {
		conds = json.RawMessage(`{}`)
	}

	granted, err := s.Queries.GrantUserPermission(ctx, db.GrantUserPermissionParams{
		UserID:        s.ToPgtypeUUID(in.UserID),
		PermissionID:  s.ToPgtypeUUID(in.PermissionID),
		GrantedBy:     s.ToPgtypeUUID(in.GrantedBy),
		GrantedReason: in.GrantedReason,
		ExpiresAt:     pgtype.Timestamptz{Time: in.ExpiresAt, Valid: true},
		Scope:         db.PermissionScope(scope),
		Conditions:    []byte(conds),
	})
	if err != nil {
		// 23505 — duplicate grant; attempt idempotency re-grant (TODO-2).
		if s.IsUniqueViolation(err, "uq_up_one_active_grant_per_user_perm") {
			return s.handleDuplicateGrant(ctx, in, scope, conds)
		}
		// 23514 — privilege escalation trigger.
		if isPrivilegeEscalation(err) {
			return UserPermission{}, ErrPrivilegeEscalation
		}
		return UserPermission{}, fmt.Errorf("store.GrantPermissionTx: insert: %w", err)
	}

	// Re-read to get canonical_name, name, resource_type, granted_reason.
	rows, err := s.Queries.GetUserPermissions(ctx, s.ToPgtypeUUID(in.UserID))
	if err != nil {
		return UserPermission{}, fmt.Errorf("store.GrantPermissionTx: re-read: %w", err)
	}
	// Find the newly inserted grant by ID.
	grantID := uuid.UUID(granted.ID).String()
	for _, r := range rows {
		if r.ID.String() == grantID {
			return mapUserPermission(r), nil
		}
	}
	// Unreachable in normal operation: GetUserPermissions filters by
	// expires_at > NOW(), so a row just inserted with a future expires_at is
	// always present. Retained as a belt-and-suspenders guard against a race
	// in which a cleanup job expires the row between insert and re-read.
	return UserPermission{
		ID:        uuid.UUID(granted.ID).String(),
		Scope:     scope,
		ExpiresAt: granted.ExpiresAt.Time,
		GrantedAt: granted.CreatedAt,
	}, nil
}

// handleDuplicateGrant implements TODO-2 (V1 simplified path).
//
// After a 23505 unique violation on uq_up_one_active_grant_per_user_perm the
// pgx connection is in an aborted-transaction state (SQLSTATE 25P02). We must
// not issue any further queries on this connection.
//
// Since GetUserPermissions only returns rows where expires_at > NOW(), a truly
// expired blocking row would NOT have caused 23505 in the first place — the
// unique index is non-partial so an expired row still blocks. However, the
// cleanup job (TODO-1) removes expired rows regularly, so reaching this branch
// in production almost always means an active grant exists.
//
// V1 decision: always return ErrPermissionAlreadyGranted. Callers that need to
// re-grant must explicitly revoke the existing grant first.
func (s *Store) handleDuplicateGrant(_ context.Context, _ GrantPermissionTxInput, _ string, _ json.RawMessage) (UserPermission, error) {
	return UserPermission{}, ErrPermissionAlreadyGranted
}

// RevokePermission hard-deletes the grant identified by (grantID, userID).
// Uses WithActingUser so the audit trigger records the correct deletion actor.
func (s *Store) RevokePermission(ctx context.Context, grantID, userID [16]byte, actingUserID string) error {
	var rowsAffected int64
	err := s.WithActingUser(ctx, actingUserID, func() error {
		n, err := s.Queries.RevokeUserPermission(ctx, db.RevokeUserPermissionParams{
			ID:     s.ToPgtypeUUID(grantID),
			UserID: s.ToPgtypeUUID(userID),
		})
		if err != nil {
			return err
		}
		rowsAffected = n
		return nil
	})
	if err != nil {
		return fmt.Errorf("store.RevokePermission: with acting user: %w", err)
	}
	if rowsAffected == 0 {
		return ErrGrantNotFound
	}
	return nil
}

// ── unexported helpers ────────────────────────────────────────────────────────

// isPrivilegeEscalation reports whether err is the fn_prevent_privilege_escalation
// trigger error.
//
// The trigger raises one of two SQLSTATE codes depending on Postgres version and
// how the trigger function signals the violation:
//   - 42501 (insufficient_privilege) — raised via RAISE EXCEPTION … USING ERRCODE
//     in the fn_prevent_privilege_escalation function (observed in production).
//   - 23514 (check_violation) — alternative mapping documented in the spec.
//
// Both codes are checked; the message must contain "privilege escalation" to
// distinguish this trigger from unrelated 42501 / 23514 errors.
func isPrivilegeEscalation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		msg := strings.ToLower(pgErr.Message)
		hasMsg := strings.Contains(msg, "privilege escalation")
		return hasMsg && (pgErr.Code == "23514" || pgErr.Code == "42501")
	}
	return false
}

// resolveScope validates in.Scope against the permission's scope_policy.
// Returns the normalised scope string or roles.ErrScopeNotAllowed.
func resolveScope(policy, requested string) (string, error) {
	norm := normaliseScope(requested)
	switch db.PermissionScopePolicy(policy) {
	case db.PermissionScopePolicyNone, db.PermissionScopePolicyOwn:
		if norm != "own" {
			return "", rbacshared.ErrScopeNotAllowed
		}
	case db.PermissionScopePolicyAll:
		if norm != "all" {
			return "", rbacshared.ErrScopeNotAllowed
		}
	case db.PermissionScopePolicyAny:
		// any scope is valid
	}
	return norm, nil
}

// mapUserPermission converts a db.GetUserPermissionsRow to service-layer UserPermission.
func mapUserPermission(row db.GetUserPermissionsRow) UserPermission {
	up := UserPermission{
		ID:            row.ID.String(),
		CanonicalName: row.CanonicalName.String,
		Name:          row.Name,
		ResourceType:  row.ResourceType,
		Scope:         string(row.Scope),
		Conditions:    json.RawMessage(row.Conditions),
		GrantedAt:     row.GrantedAt,
		GrantedReason: row.GrantedReason,
	}
	if row.ExpiresAt.Valid {
		up.ExpiresAt = row.ExpiresAt.Time
	}
	return up
}
