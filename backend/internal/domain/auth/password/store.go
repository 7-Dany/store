package password

import (
	"context"
	"errors"
	"fmt"
	"time"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// activeResetTokenConstraint is the partial-unique-index name that enforces
// one active password-reset token per user. Defined in
// sql/migrations/NNNN_add_password_reset_tokens.sql.
const activeResetTokenConstraint = "idx_password_reset_tokens_user_active"

// compile-time check that *Store satisfies Storer.
var _ Storer = (*Store)(nil)

// isDuplicateActiveToken reports whether err is a Postgres unique-violation
// (23505) on idx_password_reset_tokens_user_active — meaning a valid reset
// token already exists for this user.
func isDuplicateActiveToken(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505" &&
			pgErr.ConstraintName == activeResetTokenConstraint
	}
	return false
}

// Store implements Storer using a PostgreSQL connection pool.
// It embeds authshared.BaseStore for transaction helpers and type-conversion
// methods (ToPgtypeUUID, ToText, IPToNullable, etc.).
type Store struct {
	authshared.BaseStore
}

// NewStore creates a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{BaseStore: authshared.NewBaseStore(pool)}
}

// WithQuerier returns a shallow copy of s bound to q with TxBound=true.
// Used in tests to scope all writes inside a rolled-back transaction.
func (s *Store) WithQuerier(q db.Querier) *Store {
	c := *s
	c.BaseStore = s.BaseStore.WithQuerier(q)
	return &c
}

// ── GetUserForPasswordReset ───────────────────────────────────────────────────

// GetUserForPasswordReset fetches the minimal user fields needed to gate a
// password-reset request. Returns authshared.ErrUserNotFound on no-rows.
func (s *Store) GetUserForPasswordReset(ctx context.Context, email string) (GetUserForPasswordResetResult, error) {
	row, err := s.Queries.GetUserForPasswordReset(ctx, s.ToText(email))
	if err != nil {
		if s.IsNoRows(err) {
			return GetUserForPasswordResetResult{}, authshared.ErrUserNotFound
		}
		return GetUserForPasswordResetResult{}, fmt.Errorf("store.GetUserForPasswordReset: query: %w", err)
	}
	return GetUserForPasswordResetResult{
		ID:            s.UUIDToBytes(row.ID),
		EmailVerified: row.EmailVerified,
		IsLocked:      row.IsLocked || row.AdminLocked,
		IsActive:      row.IsActive,
	}, nil
}

// ── RequestPasswordResetTx ────────────────────────────────────────────────────

// RequestPasswordResetTx inserts a new password_reset OTP token and writes the
// password_reset_requested audit row — all in a single transaction:
//  1. Guards: checks for an existing active token (application-level cooldown).
//  2. Creates the new password-reset token (using in.CodeHash).
//  3. Writes the password_reset_requested audit row.
//
// If an active token already exists for the user, the partial unique index
// idx_password_reset_tokens_user_active (user_id WHERE token_type =
// 'password_reset' AND used_at IS NULL) raises a 23505 violation which is
// mapped to ErrResetTokenCooldown. The service layer also enforces a 60-second
// cooldown before calling this method, but the store-level guard ensures the
// invariant even when called directly (e.g. in integration tests).
func (s *Store) RequestPasswordResetTx(ctx context.Context, in RequestPasswordResetStoreInput) error {
	// Unreachable: BeginOrBind with TxBound=true never calls Pool.Begin and
	// always returns nil error. No test can trigger this branch.
	h, err := s.BeginOrBind(ctx)
	if err != nil {
		return fmt.Errorf("store.RequestPasswordResetTx: begin tx: %w", err)
	}

	userPgUUID := s.ToPgtypeUUID(in.UserID)

	// 1. Guard: check for an existing active token before attempting the insert.
	// This provides an explicit application-level cooldown that mirrors the service-layer
	// check and acts as the primary guard when the DB unique index alone is insufficient
	// (e.g. test databases that were set up before idx_password_reset_tokens_user_active
	// was added to the schema). The unique index remains as a concurrency safety net.
	if _, lookupErr := h.Q.GetPasswordResetTokenCreatedAt(ctx, in.Email); lookupErr == nil {
		// An active token already exists for this email — enforce the cooldown.
		h.Rollback()
		return authshared.ErrResetTokenCooldown
	} else if !s.IsNoRows(lookupErr) {
		h.Rollback()
		return fmt.Errorf("store.RequestPasswordResetTx: check existing token: %w", lookupErr)
	}

	// 2. Create password-reset token.
	// The partial unique index idx_password_reset_tokens_user_active provides a
	// DB-level concurrency guard against two simultaneous requests racing past
	// the application-level check above.
	if _, err := h.Q.CreatePasswordResetToken(ctx, db.CreatePasswordResetTokenParams{
		UserID:     userPgUUID,
		Email:      in.Email,
		CodeHash:   s.ToText(in.CodeHash),
		TtlSeconds: in.TTL.Seconds(),
		IpAddress:  s.IPToNullable(in.IPAddress),
	}); err != nil {
		h.Rollback()
		if isDuplicateActiveToken(err) {
			return authshared.ErrResetTokenCooldown
		}
		return fmt.Errorf("store.RequestPasswordResetTx: create token: %w", err)
	}

	// 3. Audit row.
	if err := h.Q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		UserID:    userPgUUID,
		EventType: string(audit.EventPasswordResetRequested),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(in.IPAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(in.UserAgent)),
		Metadata:  []byte("{}"),
	}); err != nil {
		h.Rollback()
		return fmt.Errorf("store.RequestPasswordResetTx: audit log: %w", err)
	}

	// Unreachable via QuerierProxy: on the TxBound path Commit is a no-op
	// that always returns nil; on the non-TxBound path it wraps pgx.Tx.Commit,
	// which the proxy cannot intercept.
	if err := h.Commit(); err != nil {
		return fmt.Errorf("store.RequestPasswordResetTx: commit: %w", err)
	}
	return nil
}

// ── ConsumeAndUpdatePasswordTx ────────────────────────────────────────────────

// ConsumeAndUpdatePasswordTx validates the OTP, consumes the reset token,
// checks for same-password reuse, updates the password hash, revokes all active
// refresh tokens, ends all sessions, and writes both the password_reset_confirmed
// and password_changed audit rows — all in one transaction. This eliminates the
// partial-failure window that existed when token consumption and the hash update
// ran in separate transactions.
//
// in.NewHash must be pre-computed by the caller (service layer) before this call
// so that the slow bcrypt generation (~300 ms) does not hold a DB lock. The TX
// itself only holds two bcrypt compares (~200 ms total: OTP code + same-password
// check) plus the DB writes.
//
// Returns the affected user's ID on success.
func (s *Store) ConsumeAndUpdatePasswordTx(ctx context.Context, in ConsumeAndUpdateInput, checkFn func(authshared.VerificationToken) error) ([16]byte, error) {
	// Unreachable: BeginOrBind with TxBound=true never calls Pool.Begin and
	// always returns nil error. No test can trigger this branch.
	h, err := s.BeginOrBind(ctx)
	if err != nil {
		return [16]byte{}, fmt.Errorf("store.ConsumeAndUpdatePasswordTx: begin tx: %w", err)
	}

	// 1. Fetch token (FOR UPDATE in query).
	row, err := h.Q.GetPasswordResetToken(ctx, in.Email)
	if err != nil {
		h.Rollback()
		if s.IsNoRows(err) {
			return [16]byte{}, authshared.ErrTokenNotFound
		}
		return [16]byte{}, fmt.Errorf("store.ConsumeAndUpdatePasswordTx: get token: %w", err)
	}

	// 2. Map to domain type.
	tok := authshared.NewVerificationToken(
		s.UUIDToBytes(row.ID),
		row.UserID.Bytes,
		row.Email,
		row.CodeHash.String,
		row.Attempts,
		row.MaxAttempts,
		row.ExpiresAt.Time.UTC(),
	)

	// 3. Caller-supplied check (expiry / attempts / OTP hash).
	if err := checkFn(tok); err != nil {
		h.Rollback()
		return [16]byte{}, err
	}

	// 4. Consume the token (idempotent: AND used_at IS NULL guard).
	consumed, err := h.Q.ConsumePasswordResetToken(ctx, s.UUIDToPgtypeUUID(row.ID))
	if err != nil {
		h.Rollback()
		return [16]byte{}, fmt.Errorf("store.ConsumeAndUpdatePasswordTx: consume token: %w", err)
	}
	if consumed == 0 {
		h.Rollback()
		return [16]byte{}, authshared.ErrTokenAlreadyUsed
	}

	// 5. GetUserPasswordHash failure is intentionally swallowed: if the user row
	// disappears between token consumption and the hash lookup (e.g. concurrent
	// delete), the same-password reuse check is skipped and the new hash is
	// written anyway — the password update takes priority over the reuse check.
	// Security: this path cannot be exploited to bypass the OTP because the token
	// was already consumed at step 4 before this lookup runs.
	currentRow, lookupErr := h.Q.GetUserPasswordHash(ctx, row.UserID)
	if lookupErr == nil {
		if authshared.CheckPassword(currentRow.PasswordHash.String, in.NewPassword) == nil {
			h.Rollback()
			return [16]byte{}, ErrSamePassword
		}
	}

	// 6. Update the password hash with the pre-computed value.
	if err := h.Q.UpdatePasswordHash(ctx, db.UpdatePasswordHashParams{
		PasswordHash: s.ToText(in.NewHash),
		UserID:       row.UserID,
	}); err != nil {
		h.Rollback()
		return [16]byte{}, fmt.Errorf("store.ConsumeAndUpdatePasswordTx: update hash: %w", err)
	}

	// 7. Revoke all active refresh tokens so no session survives the password change.
	if err := h.Q.RevokeAllUserRefreshTokens(ctx, db.RevokeAllUserRefreshTokensParams{
		UserID: row.UserID,
		Reason: "password_changed",
	}); err != nil {
		h.Rollback()
		return [16]byte{}, fmt.Errorf("store.ConsumeAndUpdatePasswordTx: revoke refresh tokens: %w", err)
	}

	// 8. End all open sessions.
	if err := h.Q.EndAllUserSessions(ctx, row.UserID); err != nil {
		h.Rollback()
		return [16]byte{}, fmt.Errorf("store.ConsumeAndUpdatePasswordTx: end sessions: %w", err)
	}

	// 9. password_reset_confirmed audit row — ip/ua are captured here now that
	// both events share one transaction.
	if err := h.Q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		UserID:    row.UserID,
		EventType: string(audit.EventPasswordResetConfirmed),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(in.IPAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(in.UserAgent)),
		Metadata:  []byte("{}"),
	}); err != nil {
		h.Rollback()
		return [16]byte{}, fmt.Errorf("store.ConsumeAndUpdatePasswordTx: audit log (confirmed): %w", err)
	}

	// 10. password_changed audit row.
	if err := h.Q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		UserID:    row.UserID,
		EventType: string(audit.EventPasswordChanged),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(in.IPAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(in.UserAgent)),
		Metadata:  []byte("{}"),
	}); err != nil {
		h.Rollback()
		return [16]byte{}, fmt.Errorf("store.ConsumeAndUpdatePasswordTx: audit log (changed): %w", err)
	}

	// Unreachable via QuerierProxy: on the TxBound path Commit is a no-op
	// that always returns nil; on the non-TxBound path it wraps pgx.Tx.Commit,
	// which the proxy cannot intercept.
	if err := h.Commit(); err != nil {
		return [16]byte{}, fmt.Errorf("store.ConsumeAndUpdatePasswordTx: commit: %w", err)
	}
	return row.UserID.Bytes, nil
}

// ── GetUserPasswordHash ─────────────────────────────────────────────────────

// GetUserPasswordHash returns the current password hash for the user.
// Returns authshared.ErrUserNotFound on no-rows.
func (s *Store) GetUserPasswordHash(ctx context.Context, userID [16]byte) (CurrentCredentials, error) {
	row, err := s.Queries.GetUserPasswordHash(ctx, s.ToPgtypeUUID(userID))
	if err != nil {
		if s.IsNoRows(err) {
			return CurrentCredentials{}, authshared.ErrUserNotFound
		}
		return CurrentCredentials{}, fmt.Errorf("store.GetUserPasswordHash: query: %w", err)
	}
	return CurrentCredentials{
		PasswordHash: row.PasswordHash.String,
	}, nil
}

// ── IncrementChangePasswordFailuresTx ────────────────────────────────────────

// IncrementChangePasswordFailuresTx writes a password_change_failed audit row,
// increments failed_change_password_attempts, and returns the new count.
// Called when the user submits a wrong old_password on POST /change-password.
func (s *Store) IncrementChangePasswordFailuresTx(ctx context.Context, userID [16]byte, ipAddress, userAgent string) (int16, error) {
	h, err := s.BeginOrBind(ctx)
	if err != nil {
		// Unreachable: BeginOrBind with TxBound=true never calls Pool.Begin
		// and always returns nil error. No test can trigger this branch.
		return 0, fmt.Errorf("store.IncrementChangePasswordFailuresTx: begin tx: %w", err)
	}

	// 1. Increment the per-user failure counter and return the new count.
	// Done first so that a ghost user (deleted between GetUserPasswordHash and here)
	// returns no-rows before we attempt the audit INSERT, which would fail with a
	// foreign-key violation (auth_audit_log_user_id_fkey) for a non-existent user.
	count, incrErr := h.Q.IncrementChangePasswordFailures(ctx, s.ToPgtypeUUID(userID))
	if incrErr != nil {
		if s.IsNoRows(incrErr) {
			// Ghost user: user was deleted between GetUserPasswordHash and here.
			// Return 0 so the service returns ErrInvalidCredentials rather than
			// ErrTooManyAttempts, and skip the audit row to avoid a FK violation.
			h.Rollback()
			return 0, nil
		}
		h.Rollback()
		return 0, fmt.Errorf("store.IncrementChangePasswordFailuresTx: increment counter: %w", incrErr)
	}

	// 2. Audit row — written only after confirming the user exists.
	if err := h.Q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		UserID:    s.ToPgtypeUUID(userID),
		EventType: string(audit.EventPasswordChangeFailed),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(ipAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(userAgent)),
		Metadata:  []byte("{}"),
	}); err != nil {
		h.Rollback()
		return 0, fmt.Errorf("store.IncrementChangePasswordFailuresTx: audit log: %w", err)
	}

	// Unreachable via QuerierProxy: commitFn is a no-op on the TxBound path.
	if err := h.Commit(); err != nil {
		return 0, fmt.Errorf("store.IncrementChangePasswordFailuresTx: commit: %w", err)
	}
	return count, nil
}

// ── ResetChangePasswordFailuresTx ────────────────────────────────────────────

// ResetChangePasswordFailuresTx resets failed_change_password_attempts to 0
// after a successful password change. Best-effort — a failure here must not
// prevent the 200 response; the caller logs the error and continues.
func (s *Store) ResetChangePasswordFailuresTx(ctx context.Context, userID [16]byte) error {
	if err := s.Queries.ResetChangePasswordFailures(ctx, s.ToPgtypeUUID(userID)); err != nil {
		return fmt.Errorf("store.ResetChangePasswordFailuresTx: reset counter: %w", err)
	}
	return nil
}

// ── GetPasswordResetTokenForVerify ──────────────────────────────────────────

// GetPasswordResetTokenForVerify fetches the active password-reset token for
// email without locking the row. Used by VerifyResetCode to check the OTP
// before advancing the user to the password-entry screen.
// Returns authshared.ErrTokenNotFound on no-rows.
func (s *Store) GetPasswordResetTokenForVerify(ctx context.Context, email string) (authshared.VerificationToken, error) {
	row, err := s.Queries.GetPasswordResetTokenForVerify(ctx, email)
	if err != nil {
		if s.IsNoRows(err) {
			return authshared.VerificationToken{}, authshared.ErrTokenNotFound
		}
		return authshared.VerificationToken{}, fmt.Errorf("store.GetPasswordResetTokenForVerify: query: %w", err)
	}
	return authshared.NewVerificationToken(
		s.UUIDToBytes(row.ID),
		row.UserID.Bytes,
		row.Email,
		row.CodeHash.String,
		row.Attempts,
		row.MaxAttempts,
		row.ExpiresAt.Time.UTC(),
	), nil
}

// ── GetPasswordResetTokenCreatedAt ────────────────────────────────────────────────────────────────────────────

// GetPasswordResetTokenCreatedAt returns the created_at of the most recent
// active password-reset token for email.
// Returns authshared.ErrTokenNotFound when no active token exists.
func (s *Store) GetPasswordResetTokenCreatedAt(ctx context.Context, email string) (time.Time, error) {
	t, err := s.Queries.GetPasswordResetTokenCreatedAt(ctx, email)
	if err != nil {
		if s.IsNoRows(err) {
			return time.Time{}, authshared.ErrTokenNotFound
		}
		return time.Time{}, fmt.Errorf("store.GetPasswordResetTokenCreatedAt: query: %w", err)
	}
	return t.UTC(), nil
}
