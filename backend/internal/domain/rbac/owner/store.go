package owner

import (
	"context"
	"encoding/json"
	"time"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	rbacshared "github.com/7-Dany/store/backend/internal/domain/rbac/shared"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
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
	c := *s
	c.BaseStore = s.BaseStore.WithQuerier(q)
	return &c
}

// ── Assign-owner store methods ────────────────────────────────────────────────

// CountActiveOwners returns the number of active owner role assignments.
func (s *Store) CountActiveOwners(ctx context.Context) (int64, error) {
	c, err := s.Queries.CountActiveOwners(ctx)
	if err != nil {
		return 0, telemetry.Store("CountActiveOwners.query", err)
	}
	return c, nil
}

// GetOwnerRoleID returns the owner role's primary key as a [16]byte UUID.
func (s *Store) GetOwnerRoleID(ctx context.Context) ([16]byte, error) {
	id, err := s.Queries.GetOwnerRoleID(ctx)
	if err != nil {
		return [16]byte{}, telemetry.Store("GetOwnerRoleID.query", err)
	}
	return [16]byte(id), nil
}

// GetActiveUserByID fetches a user row by ID and maps it to AssignOwnerUser.
// Returns rbacshared.ErrUserNotFound on a no-rows result.
func (s *Store) GetActiveUserByID(ctx context.Context, userID [16]byte) (AssignOwnerUser, error) {
	row, err := s.Queries.GetActiveUserByID(ctx, s.ToPgtypeUUID(userID))
	if err != nil {
		if s.IsNoRows(err) {
			return AssignOwnerUser{}, rbacshared.ErrUserNotFound
		}
		return AssignOwnerUser{}, telemetry.Store("GetActiveUserByID.query", err)
	}
	return AssignOwnerUser{
		IsActive:      row.IsActive,
		EmailVerified: row.EmailVerified,
	}, nil
}

// AssignOwnerTx assigns the owner role to in.UserID in a single transaction
// and writes an owner_assigned audit record. The audit write uses
// context.WithoutCancel so a client disconnect cannot suppress the forensic
// trail for this irreversible privilege escalation.
func (s *Store) AssignOwnerTx(ctx context.Context, in AssignOwnerTxInput) (AssignOwnerResult, error) {
	h, err := s.BeginOrBind(ctx)
	if err != nil {
		// Unreachable via QuerierProxy: BeginOrBind with TxBound=true always returns
		// the injected querier with nil error. No test can trigger this branch.
		return AssignOwnerResult{}, telemetry.Store("AssignOwnerTx.begin_tx", err)
	}

	row, err := h.Q.AssignUserRole(ctx, db.AssignUserRoleParams{
		UserID:        s.ToPgtypeUUID(in.UserID),
		RoleID:        s.ToPgtypeUUID(in.RoleID),
		GrantedBy:     s.ToPgtypeUUID(in.UserID), // self-grant — valid only on the initial assign path
		GrantedReason: "initial owner assignment",
		ExpiresAt:     pgtype.Timestamptz{Valid: false}, // permanent grant
	})
	if err != nil {
		if rErr := h.Rollback(); rErr != nil {
			log.Warn(ctx, "AssignOwnerTx: rollback failed", "error", rErr)
		}
		return AssignOwnerResult{}, telemetry.Store("AssignOwnerTx.assign_role", err)
	}

	// Audit with context.WithoutCancel — client disconnect must not suppress
	// the forensic record of this irreversible privilege escalation.
	if err := h.Q.InsertAuditLog(context.WithoutCancel(ctx), db.InsertAuditLogParams{
		UserID:    s.ToPgtypeUUID(in.UserID),
		EventType: string(audit.EventOwnerAssigned),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(in.IPAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(in.UserAgent)),
		Metadata:  []byte("{}"),
	}); err != nil {
		if rErr := h.Rollback(); rErr != nil {
			log.Warn(ctx, "AssignOwnerTx: rollback after audit failed", "error", rErr)
		}
		return AssignOwnerResult{}, telemetry.Store("AssignOwnerTx.audit_log", err)
	}

	// Unreachable via QuerierProxy: on the TxBound path Commit is a no-op
	// returning nil; on the non-TxBound path Commit wraps pgx.Tx.Commit which
	// QuerierProxy cannot intercept.
	if err := h.Commit(); err != nil {
		return AssignOwnerResult{}, telemetry.Store("AssignOwnerTx.commit", err)
	}

	return AssignOwnerResult{
		UserID:    uuid.UUID(in.UserID).String(),
		RoleName:  "owner",
		GrantedAt: row.CreatedAt,
	}, nil
}

// ── Transfer store methods ────────────────────────────────────────────────────

// GetTransferTargetUser fetches a user row for transfer eligibility checks.
// Returns rbacshared.ErrUserNotFound on no-rows. Also checks whether the target
// is the current owner via CheckUserAccess.
func (s *Store) GetTransferTargetUser(ctx context.Context, userID [16]byte) (TransferTargetUser, error) {
	row, err := s.Queries.GetActiveUserByID(ctx, s.ToPgtypeUUID(userID))
	if err != nil {
		if s.IsNoRows(err) {
			return TransferTargetUser{}, rbacshared.ErrUserNotFound
		}
		return TransferTargetUser{}, telemetry.Store("GetTransferTargetUser.query", err)
	}

	ownerCheck, err := s.Queries.CheckUserAccess(ctx, db.CheckUserAccessParams{
		UserID:     s.ToPgtypeUUID(userID),
		Permission: pgtype.Text{String: "", Valid: true},
	})
	if err != nil {
		return TransferTargetUser{}, telemetry.Store("GetTransferTargetUser.check_owner", err)
	}

	// row.Email is pgtype.Text — extract the string value.
	return TransferTargetUser{
		Email:         row.Email.String,
		IsActive:      row.IsActive,
		EmailVerified: row.EmailVerified,
		IsOwner:       asBoolAny(ownerCheck.IsOwner),
	}, nil
}

// HasPendingTransferToken reports whether an active (unexpired, unconsumed)
// ownership transfer token exists.
func (s *Store) HasPendingTransferToken(ctx context.Context) (bool, error) {
	_, err := s.Queries.GetPendingOwnershipTransferToken(ctx)
	if err != nil {
		if s.IsNoRows(err) {
			return false, nil
		}
		return false, telemetry.Store("HasPendingTransferToken.query", err)
	}
	return true, nil
}

// InsertTransferToken inserts a new ownership transfer token and returns its
// ID and expiry timestamp. codeHash must be bcrypt(rawToken). initiatedByStr
// must be the acting owner's UUID string (stored in metadata).
func (s *Store) InsertTransferToken(
	ctx context.Context,
	targetUserID [16]byte,
	targetEmail, codeHash, initiatedByStr string,
) ([16]byte, time.Time, error) {
	metadata, err := json.Marshal(map[string]string{"initiated_by": initiatedByStr})
	if err != nil {
		return [16]byte{}, time.Time{}, telemetry.Store("InsertTransferToken.marshal_metadata", err)
	}
	row, err := s.Queries.InsertOwnershipTransferToken(ctx, db.InsertOwnershipTransferTokenParams{
		TargetUserID: s.ToPgtypeUUID(targetUserID),
		TargetEmail:  targetEmail,
		// code_hash column is nullable text → sqlc generates pgtype.Text.
		CodeHash: pgtype.Text{String: codeHash, Valid: true},
		Metadata: metadata,
	})
	if err != nil {
		return [16]byte{}, time.Time{}, telemetry.Store("InsertTransferToken.insert", err)
	}
	// row.ID is uuid.UUID (non-nullable primary key) — convert directly to [16]byte.
	return [16]byte(row.ID), row.ExpiresAt.Time, nil
}

// PendingTransferInfo is the full set of fields returned from a pending token row.
type PendingTransferInfo struct {
	TokenID     [16]byte
	NewOwnerID  [16]byte // token.user_id — the target who will become owner
	CodeHash    string
	InitiatedBy string // UUID string of the current owner who sent the invite
}

// GetPendingTransferToken retrieves the active transfer token row (FOR UPDATE).
// Returns ErrTransferTokenInvalid when no active token exists.
func (s *Store) GetPendingTransferToken(ctx context.Context) (PendingTransferInfo, error) {
	row, err := s.Queries.GetPendingOwnershipTransferToken(ctx)
	if err != nil {
		if s.IsNoRows(err) {
			return PendingTransferInfo{}, ErrTransferTokenInvalid
		}
		return PendingTransferInfo{}, telemetry.Store("GetPendingTransferToken.query", err)
	}

	var meta struct {
		InitiatedBy string `json:"initiated_by"`
	}
	if err := json.Unmarshal(row.Metadata, &meta); err != nil {
		return PendingTransferInfo{}, telemetry.Store("GetPendingTransferToken.parse_metadata", err)
	}

	// row.ID is uuid.UUID (non-nullable PK) — convert directly.
	// row.UserID is pgtype.UUID (nullable FK) — use .Bytes.
	return PendingTransferInfo{
		TokenID:     [16]byte(row.ID),
		NewOwnerID:  [16]byte(row.UserID.Bytes),
		CodeHash:    row.CodeHash.String,
		InitiatedBy: meta.InitiatedBy,
	}, nil
}

// DeletePendingTransferToken deletes the pending transfer token initiated by
// initiatedBy. Returns ErrNoPendingTransfer when no matching token is found.
func (s *Store) DeletePendingTransferToken(ctx context.Context, initiatedBy string) error {
	rows, err := s.Queries.DeletePendingOwnershipTransferToken(ctx, initiatedBy)
	if err != nil {
		return telemetry.Store("DeletePendingTransferToken.delete", err)
	}
	if rows == 0 {
		return ErrNoPendingTransfer
	}
	return nil
}

// WriteInitiateAuditLog writes an owner_transfer_initiated audit entry.
// Uses context.WithoutCancel — client disconnect must not abort the write.
func (s *Store) WriteInitiateAuditLog(ctx context.Context, actingOwnerID [16]byte, targetUserID, ipAddress, userAgent string) error {
	metadata, _ := json.Marshal(map[string]string{"target_user_id": targetUserID})
	return s.Queries.InsertAuditLog(context.WithoutCancel(ctx), db.InsertAuditLogParams{
		UserID:    s.ToPgtypeUUID(actingOwnerID),
		EventType: string(audit.EventOwnerTransferInitiated),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(ipAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(userAgent)),
		Metadata:  metadata,
	})
}

// WriteCancelAuditLog writes an owner_transfer_cancelled audit entry.
// Uses context.WithoutCancel — client disconnect must not abort the write.
func (s *Store) WriteCancelAuditLog(ctx context.Context, actingOwnerID [16]byte, ipAddress, userAgent string) error {
	return s.Queries.InsertAuditLog(context.WithoutCancel(ctx), db.InsertAuditLogParams{
		UserID:    s.ToPgtypeUUID(actingOwnerID),
		EventType: string(audit.EventOwnerTransferCancelled),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(ipAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(userAgent)),
		Metadata:  []byte("{}"),
	})
}

// AcceptTransferTx executes the atomic ownership transfer:
//
//  1. SET LOCAL rbac.skip_escalation_check = '1'   — suppress escalation trigger
//  2. ConsumeOwnershipTransferToken                — idempotency guard
//  3. CheckUserAccess(previousOwner)               — race guard: still owner?
//  4. AssignUserRole(newOwner)                     — grant owner role to target
//  5. RemoveUserRole(previousOwner)                — revoke owner role from initiator
//  6. InsertAuditLog (owner_transfer_accepted)     — WithoutCancel
//  7. RevokeAllUserRefreshTokens(previousOwner)    — WithoutCancel
//
// Returns the UTC timestamp of the transfer on success.
func (s *Store) AcceptTransferTx(ctx context.Context, in AcceptTransferTxInput) (time.Time, error) {
	h, err := s.BeginOrBind(ctx)
	if err != nil {
		// Unreachable via QuerierProxy: BeginOrBind with TxBound=true always returns
		// the injected querier with nil error. No test can trigger this branch.
		return time.Time{}, telemetry.Store("AcceptTransferTx.begin_tx", err)
	}

	rb := func(label string, origErr error) (time.Time, error) {
		if rErr := h.Rollback(); rErr != nil {
			// Rollback failure is a best-effort secondary op — log as Warn so the
			// primary error (origErr) is still visible and not masked.
			log.Warn(ctx, "AcceptTransferTx: rollback failed", "label", label, "error", rErr)
		}
		return time.Time{}, origErr
	}

	// Step 1 — Suppress fn_prevent_owner_role_escalation for this TX only.
	if err := h.Q.SetSkipEscalationCheck(ctx); err != nil {
		return rb("set_skip_escalation", telemetry.Store("AcceptTransferTx.set_skip_escalation", err))
	}

	// Step 2 — Consume the token (AND used_at IS NULL → idempotency guard).
	rows, err := h.Q.ConsumeOwnershipTransferToken(ctx, s.ToPgtypeUUID(in.TokenID))
	if err != nil {
		return rb("consume_token", telemetry.Store("AcceptTransferTx.consume_token", err))
	}
	if rows == 0 {
		return rb("token_already_consumed", ErrTransferTokenInvalid)
	}

	// Step 3 — Verify previous owner still holds the owner role (race guard).
	ownerCheck, err := h.Q.CheckUserAccess(ctx, db.CheckUserAccessParams{
		UserID:     s.ToPgtypeUUID(in.PreviousOwnerID),
		Permission: pgtype.Text{String: "", Valid: true},
	})
	if err != nil {
		return rb("check_prev_owner", telemetry.Store("AcceptTransferTx.check_prev_owner", err))
	}
	if !asBoolAny(ownerCheck.IsOwner) {
		return rb("initiator_not_owner", ErrInitiatorNotOwner)
	}

	// Step 4 — Assign owner role to new owner.
	_, err = h.Q.AssignUserRole(ctx, db.AssignUserRoleParams{
		UserID:        s.ToPgtypeUUID(in.NewOwnerID),
		RoleID:        s.ToPgtypeUUID(in.RoleID),
		GrantedBy:     s.ToPgtypeUUID(in.PreviousOwnerID),
		GrantedReason: "ownership transfer",
		ExpiresAt:     pgtype.Timestamptz{Valid: false}, // permanent grant
	})
	if err != nil {
		return rb("assign_new_owner", telemetry.Store("AcceptTransferTx.assign_new_owner", err))
	}

	// Step 5 — Remove owner role from previous owner.
	// WithActingUser is required so fn_audit_role_permissions records the new
	// owner (the accepting party) as the actor on the DELETE, not the original granter.
	actingStr := uuid.UUID(in.ActingUserID).String()
	if err := s.WithActingUser(ctx, actingStr, func() error {
		_, err := h.Q.RemoveUserRole(ctx, s.ToPgtypeUUID(in.PreviousOwnerID))
		return err
	}); err != nil {
		return rb("remove_prev_owner", telemetry.Store("AcceptTransferTx.remove_prev_owner", err))
	}

	transferredAt := time.Now().UTC()
	auditCtx := context.WithoutCancel(ctx)

	// Step 6 — Audit log (WithoutCancel).
	auditMeta, _ := json.Marshal(map[string]string{
		"previous_owner_id": uuid.UUID(in.PreviousOwnerID).String(),
		"new_owner_id":      uuid.UUID(in.NewOwnerID).String(),
	})
	if err := h.Q.InsertAuditLog(auditCtx, db.InsertAuditLogParams{
		UserID:    s.ToPgtypeUUID(in.NewOwnerID),
		EventType: string(audit.EventOwnerTransferAccepted),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(in.IPAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(in.UserAgent)),
		Metadata:  auditMeta,
	}); err != nil {
		return rb("audit_accept", telemetry.Store("AcceptTransferTx.audit_log", err))
	}

	// Step 7 — Revoke previous owner's refresh tokens (WithoutCancel).
	if err := h.Q.RevokeAllUserRefreshTokens(auditCtx, db.RevokeAllUserRefreshTokensParams{
		UserID: s.ToPgtypeUUID(in.PreviousOwnerID),
		Reason: "ownership_transferred",
	}); err != nil {
		return rb("revoke_sessions", telemetry.Store("AcceptTransferTx.revoke_sessions", err))
	}

	// Unreachable via QuerierProxy: on the TxBound path Commit is a no-op
	// returning nil; on the non-TxBound path Commit wraps pgx.Tx.Commit which
	// QuerierProxy cannot intercept.
	if err := h.Commit(); err != nil {
		return time.Time{}, telemetry.Store("AcceptTransferTx.commit", err)
	}

	return transferredAt, nil
}

// ── Private helpers ───────────────────────────────────────────────────────────

// asBoolAny extracts a bool from the any value pgx returns for COALESCE(…, FALSE) columns.
func asBoolAny(v any) bool {
	b, _ := v.(bool)
	return b
}
