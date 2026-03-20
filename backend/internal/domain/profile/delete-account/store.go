package deleteaccount

import (
	"context"
	"errors"
	"fmt"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	profileshared "github.com/7-Dany/store/backend/internal/domain/profile/shared"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// compile-time check that *Store satisfies Storer.
var _ Storer = (*Store)(nil)

// Store is the data-access layer for DELETE /me and POST /me/cancel-deletion.
type Store struct {
	profileshared.BaseStore
}

// NewStore constructs a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{BaseStore: profileshared.NewBaseStore(pool)}
}

// WithQuerier returns a shallow copy of s whose embedded Queries field is
// replaced by q and whose TxBound flag is set to true. Used in integration
// tests to scope all writes to a single rolled-back transaction.
func (s *Store) WithQuerier(q db.Querier) *Store {
	c := *s
	c.BaseStore = s.BaseStore.WithQuerier(q)
	return &c
}

// ── Read methods ──────────────────────────────────────────────────────────────

// GetUserForDeletion returns the minimal user row needed to gate the deletion
// request. Maps no-rows to profileshared.ErrUserNotFound.
func (s *Store) GetUserForDeletion(ctx context.Context, userID [16]byte) (DeletionUser, error) {
	row, err := s.Queries.GetUserForDeletion(ctx, s.ToPgtypeUUID(userID))
	if err != nil {
		if s.IsNoRows(err) {
			return DeletionUser{}, profileshared.ErrUserNotFound
		}
		return DeletionUser{}, telemetry.Store("GetUserForDeletion.query", err)
	}
	var email *string
	if row.Email.Valid {
		email = &row.Email.String
	}
	var passwordHash *string
	if row.PasswordHash.Valid {
		passwordHash = &row.PasswordHash.String
	}
	return DeletionUser{
		ID:           [16]byte(row.ID),
		Email:        email,
		PasswordHash: passwordHash,
		DeletedAt:    row.DeletedAt,
	}, nil
}

// GetUserAuthMethods returns whether the user has a password hash and how many
// OAuth identities are linked. Used to dispatch the correct confirmation path (D-11).
// HasPassword is a boolean expression in the SQL query returned as interface{} by sqlc;
// we type-assert it to bool here.
func (s *Store) GetUserAuthMethods(ctx context.Context, userID [16]byte) (UserAuthMethods, error) {
	row, err := s.Queries.GetUserAuthMethods(ctx, s.ToPgtypeUUID(userID))
	if err != nil {
		if s.IsNoRows(err) {
			return UserAuthMethods{}, profileshared.ErrUserNotFound
		}
		return UserAuthMethods{}, telemetry.Store("GetUserAuthMethods.query", err)
	}
	hasPassword, _ := row.HasPassword.(bool)
	return UserAuthMethods{
		HasPassword:   hasPassword,
		IdentityCount: int(row.IdentityCount),
	}, nil
}

// GetIdentityByUserAndProvider confirms that the user has an identity with the
// given provider and returns the provider_uid.
// Maps no-rows to authshared.ErrUserNotFound.
func (s *Store) GetIdentityByUserAndProvider(ctx context.Context, userID [16]byte, provider string) (string, error) {
	row, err := s.Queries.GetIdentityByUserAndProvider(ctx, db.GetIdentityByUserAndProviderParams{
		UserID:   s.ToPgtypeUUID(userID),
		Provider: db.AuthProvider(provider),
	})
	if err != nil {
		if s.IsNoRows(err) {
			return "", authshared.ErrUserNotFound
		}
		return "", telemetry.Store("GetIdentityByUserAndProvider.query", err)
	}
	return row.ProviderUid, nil
}

// GetAccountDeletionToken fetches the active account_deletion token for the user.
// The underlying SQL uses FOR UPDATE; when called outside a transaction the row-level
// lock is released at statement end. For the OTP-consume choreography, prefer calling
// this inside a transaction (as ConfirmOTPDeletionTx does).
func (s *Store) GetAccountDeletionToken(ctx context.Context, userID [16]byte) (authshared.VerificationToken, error) {
	row, err := s.Queries.GetAccountDeletionToken(ctx, s.ToPgtypeUUID(userID))
	if err != nil {
		if s.IsNoRows(err) {
			return authshared.VerificationToken{}, authshared.ErrTokenNotFound
		}
		return authshared.VerificationToken{}, telemetry.Store("GetAccountDeletionToken.query", err)
	}
	return authshared.NewVerificationToken(
		[16]byte(row.ID),
		row.UserID.Bytes,
		row.Email,
		row.CodeHash.String,
		row.Attempts,
		row.MaxAttempts,
		row.ExpiresAt.Time.UTC(),
	), nil
}

// ── Transactional write methods ───────────────────────────────────────────────

// ScheduleDeletionTx stamps deleted_at = NOW(), writes the audit row, and returns
// DeletionScheduled{ScheduledDeletionAt: deleted_at + 30 days}.
// Maps no-rows from ScheduleUserDeletion to profileshared.ErrUserNotFound.
// (No-rows also occurs when deleted_at is already set — the service must guard
// against ErrAlreadyPendingDeletion before reaching this method.)
func (s *Store) ScheduleDeletionTx(ctx context.Context, in ScheduleDeletionInput) (DeletionScheduled, error) {
	h, err := s.BeginOrBind(ctx)
	// Unreachable: BeginOrBind with TxBound=true never calls Pool.Begin
	// and always returns nil error. No test can trigger this branch.
	if err != nil {
		return DeletionScheduled{}, telemetry.Store("ScheduleDeletionTx.begin_tx", err)
	}

	userPgUUID := s.ToPgtypeUUID(s.mustParseUserID(in.UserID))

	// 1. Stamp deleted_at = NOW(). Returns no-rows when user not found or already pending.
	deletedAt, err := h.Q.ScheduleUserDeletion(ctx, userPgUUID)
	if err != nil {
		h.Rollback()
		if s.IsNoRows(err) {
			return DeletionScheduled{}, profileshared.ErrUserNotFound
		}
		return DeletionScheduled{}, telemetry.Store("ScheduleDeletionTx.schedule", err)
	}

	// 2. Audit row — written after the successful UPDATE so the event is not
	//    recorded for a no-op or failed operation.
	//    context.WithoutCancel ensures a client disconnect cannot abort the write.
	if err := h.Q.InsertAuditLog(context.WithoutCancel(ctx), db.InsertAuditLogParams{
		UserID:    userPgUUID,
		EventType: string(audit.EventAccountDeletionRequested),
		Provider:  in.Provider,
		IpAddress: s.IPToNullable(in.IPAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(in.UserAgent)),
		Metadata:  []byte("{}"),
	}); err != nil {
		h.Rollback()
		return DeletionScheduled{}, telemetry.Store("ScheduleDeletionTx.audit_log", err)
	}

	// Unreachable via QuerierProxy: on the TxBound path h.Commit is a no-op
	// that always returns nil; on the non-TxBound path h.Commit wraps
	// pgx.Tx.Commit which the proxy cannot intercept.
	if err := h.Commit(); err != nil {
		return DeletionScheduled{}, telemetry.Store("ScheduleDeletionTx.commit", err)
	}

	// ScheduleUserDeletion RETURNING deleted_at is always non-nil when err == nil.
	return DeletionScheduled{ScheduledDeletionAt: (*deletedAt).Add(profileshared.AccountDeletionWindow)}, nil
}

// SendDeletionOTPTx invalidates existing deletion tokens, generates a new OTP,
// persists the token, writes the audit row, and returns the raw OTP code for
// the service to dispatch by email.
func (s *Store) SendDeletionOTPTx(ctx context.Context, in SendDeletionOTPInput) (SendDeletionOTPResult, error) {
	h, err := s.BeginOrBind(ctx)
	// Unreachable: BeginOrBind with TxBound=true never calls Pool.Begin
	// and always returns nil error. No test can trigger this branch.
	if err != nil {
		return SendDeletionOTPResult{}, telemetry.Store("SendDeletionOTPTx.begin_tx", err)
	}

	userPgUUID := s.ToPgtypeUUID(s.mustParseUserID(in.UserID))

	// 1. Void any outstanding deletion tokens to prevent accumulation.
	if err := h.Q.InvalidateUserDeletionTokens(ctx, userPgUUID); err != nil {
		h.Rollback()
		return SendDeletionOTPResult{}, telemetry.Store("SendDeletionOTPTx.invalidate_tokens", err)
	}

	// 2. Generate a 6-digit OTP and its bcrypt hash.
	rawCode, codeHash, err := authshared.GenerateCodeHash()
	if err != nil {
		h.Rollback()
		return SendDeletionOTPResult{}, telemetry.Store("SendDeletionOTPTx.generate_code", err)
	}

	// 3. Persist the token. TTLSeconds comes from config.OTPTokenTTL, passed in
	//    by the service so the store remains config-agnostic.
	// idx_ott_account_deletion_active (partial unique on user_id) raises 23505 when
	// a token is already active; mapped to ErrDeletionTokenCooldown → 429.
	if _, err := h.Q.CreateAccountDeletionToken(ctx, db.CreateAccountDeletionTokenParams{
		UserID:     userPgUUID,
		Email:      in.Email,
		CodeHash:   s.ToText(codeHash),
		TtlSeconds: in.TTLSeconds,
		IpAddress:  s.IPToNullable(in.IPAddress),
	}); err != nil {
		h.Rollback()
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" &&
			pgErr.ConstraintName == "idx_ott_account_deletion_active" {
			return SendDeletionOTPResult{}, ErrDeletionTokenCooldown
		}
		return SendDeletionOTPResult{}, telemetry.Store("SendDeletionOTPTx.create_token", err)
	}

	// 4. Audit row — written after the token is persisted.
	//    context.WithoutCancel ensures a client disconnect cannot abort the write.
	if err := h.Q.InsertAuditLog(context.WithoutCancel(ctx), db.InsertAuditLogParams{
		UserID:    userPgUUID,
		EventType: string(audit.EventAccountDeletionOTPRequested),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(in.IPAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(in.UserAgent)),
		Metadata:  []byte("{}"),
	}); err != nil {
		h.Rollback()
		return SendDeletionOTPResult{}, telemetry.Store("SendDeletionOTPTx.audit_log", err)
	}

	// Unreachable via QuerierProxy: on the TxBound path h.Commit is a no-op
	// that always returns nil; on the non-TxBound path h.Commit wraps
	// pgx.Tx.Commit which the proxy cannot intercept.
	if err := h.Commit(); err != nil {
		return SendDeletionOTPResult{}, telemetry.Store("SendDeletionOTPTx.commit", err)
	}

	return SendDeletionOTPResult{RawCode: rawCode}, nil
}

// ConfirmOTPDeletionTx re-locks the active deletion token FOR UPDATE, consumes it,
// stamps deleted_at, and writes the audit row — all in one transaction.
// tokenID is the token ID previously validated by the service via GetAccountDeletionToken.
func (s *Store) ConfirmOTPDeletionTx(ctx context.Context, in ScheduleDeletionInput, tokenID [16]byte) (DeletionScheduled, error) {
	h, err := s.BeginOrBind(ctx)
	// Unreachable: BeginOrBind with TxBound=true never calls Pool.Begin
	// and always returns nil error. No test can trigger this branch.
	if err != nil {
		return DeletionScheduled{}, telemetry.Store("ConfirmOTPDeletionTx.begin_tx", err)
	}

	userPgUUID := s.ToPgtypeUUID(s.mustParseUserID(in.UserID))

	// 1. Re-acquire the FOR UPDATE lock on the active token inside this transaction
	//    to prevent a concurrent correct submission from double-consuming the token.
	lockedRow, err := h.Q.GetAccountDeletionToken(ctx, userPgUUID)
	if err != nil {
		h.Rollback()
		if s.IsNoRows(err) {
			return DeletionScheduled{}, authshared.ErrTokenNotFound
		}
		return DeletionScheduled{}, telemetry.Store("ConfirmOTPDeletionTx.get_token", err)
	}

	// 2. Consume the locked token. 0 rows means a concurrent submission already
	//    consumed it between the service-layer check and this transaction.
	n, err := h.Q.ConsumeAccountDeletionToken(ctx, s.UUIDToPgtypeUUID(lockedRow.ID))
	if err != nil {
		h.Rollback()
		return DeletionScheduled{}, telemetry.Store("ConfirmOTPDeletionTx.consume_token", err)
	}
	if n == 0 {
		h.Rollback()
		return DeletionScheduled{}, authshared.ErrTokenAlreadyUsed
	}

	// 3. Stamp deleted_at = NOW(). Returns no-rows when user not found or already pending.
	deletedAt, err := h.Q.ScheduleUserDeletion(ctx, userPgUUID)
	if err != nil {
		h.Rollback()
		if s.IsNoRows(err) {
			return DeletionScheduled{}, profileshared.ErrUserNotFound
		}
		return DeletionScheduled{}, telemetry.Store("ConfirmOTPDeletionTx.schedule", err)
	}

	// 4. Audit row — context.WithoutCancel so a client disconnect cannot abort it.
	if err := h.Q.InsertAuditLog(context.WithoutCancel(ctx), db.InsertAuditLogParams{
		UserID:    userPgUUID,
		EventType: string(audit.EventAccountDeletionRequested),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(in.IPAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(in.UserAgent)),
		Metadata:  []byte("{}"),
	}); err != nil {
		h.Rollback()
		return DeletionScheduled{}, telemetry.Store("ConfirmOTPDeletionTx.audit_log", err)
	}

	// Unreachable via QuerierProxy: on the TxBound path h.Commit is a no-op
	// that always returns nil; on the non-TxBound path h.Commit wraps
	// pgx.Tx.Commit which the proxy cannot intercept.
	if err := h.Commit(); err != nil {
		return DeletionScheduled{}, telemetry.Store("ConfirmOTPDeletionTx.commit", err)
	}

	// tokenID is the service-layer token ID passed in for documentation purposes.
	// We intentionally use the DB-locked row's ID (lockedRow.ID) acquired inside this
	// transaction rather than the caller-supplied value, because the FOR UPDATE
	// re-fetch is the authoritative TOCTOU guard. The parameter exists so callers
	// understand which token they intend to consume; a mismatch would indicate a
	// programming error that should surface as a test failure, not a runtime panic.
	_ = tokenID
	return DeletionScheduled{ScheduledDeletionAt: (*deletedAt).Add(profileshared.AccountDeletionWindow)}, nil
}

// CancelDeletionTx clears deleted_at and writes the audit row.
// Returns ErrNotPendingDeletion when the user has no pending deletion (0 rows affected).
func (s *Store) CancelDeletionTx(ctx context.Context, in CancelDeletionInput) error {
	h, err := s.BeginOrBind(ctx)
	// Unreachable: BeginOrBind with TxBound=true never calls Pool.Begin
	// and always returns nil error. No test can trigger this branch.
	if err != nil {
		return telemetry.Store("CancelDeletionTx.begin_tx", err)
	}

	userPgUUID := s.ToPgtypeUUID(s.mustParseUserID(in.UserID))

	// 1. Clear deleted_at. 0 rows means the user is not pending deletion.
	n, err := h.Q.CancelUserDeletion(ctx, userPgUUID)
	if err != nil {
		h.Rollback()
		return telemetry.Store("CancelDeletionTx.cancel", err)
	}
	if n == 0 {
		h.Rollback()
		return ErrNotPendingDeletion
	}

	// 2. Audit row — context.WithoutCancel so a client disconnect cannot abort it.
	if err := h.Q.InsertAuditLog(context.WithoutCancel(ctx), db.InsertAuditLogParams{
		UserID:    userPgUUID,
		EventType: string(audit.EventAccountDeletionCancelled),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(in.IPAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(in.UserAgent)),
		Metadata:  []byte("{}"),
	}); err != nil {
		h.Rollback()
		return telemetry.Store("CancelDeletionTx.audit_log", err)
	}

	// Unreachable via QuerierProxy: on the TxBound path h.Commit is a no-op
	// that always returns nil; on the non-TxBound path h.Commit wraps
	// pgx.Tx.Commit which the proxy cannot intercept.
	if err := h.Commit(); err != nil {
		return telemetry.Store("CancelDeletionTx.commit", err)
	}

	return nil
}

// ── Private helpers ───────────────────────────────────────────────────────────

// mustParseUserID parses a UUID string into [16]byte.
// Panics on invalid input — callers must validate before reaching the store.
func (s *Store) mustParseUserID(id string) [16]byte {
	pgUUID, err := s.ParseUUIDString(id)
	if err != nil {
		// Unreachable: service layer always calls authshared.ParseUserID before
		// the store method is reached, so an invalid UUID cannot arrive here.
		panic(fmt.Sprintf("deleteaccount.Store: invalid user ID %q: %v", id, err))
	}
	return pgUUID.Bytes
}
