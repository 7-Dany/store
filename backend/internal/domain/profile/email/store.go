package email

import (
	"context"
	"encoding/json"
	"errors"
	"time"

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

// Store holds the profileshared.BaseStore and implements the email.Storer interface.
type Store struct {
	profileshared.BaseStore
}

// NewStore creates a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{BaseStore: profileshared.NewBaseStore(pool)}
}

// WithQuerier returns a shallow copy of s whose underlying querier is replaced
// by q and whose TxBound flag is set. Used in tests to scope all writes to a
// single rolled-back transaction.
func (s *Store) WithQuerier(q db.Querier) *Store {
	c := *s
	c.BaseStore = s.BaseStore.WithQuerier(q)
	return &c
}

// ── Input/output types ────────────────────────────────────────────────────────

// RequestEmailChangeTxInput carries all parameters for step 1's store transaction.
type RequestEmailChangeTxInput struct {
	UserID       [16]byte
	CurrentEmail string  // user's current email — stored in token.email for audit readability
	NewEmail     string  // stored in token.metadata as {"new_email":"..."}
	CodeHash     string  // bcrypt hash of the OTP code
	TTLSeconds   float64 // authoritative TTL from config.OTPTokenTTL
	IPAddress    string
	UserAgent    string
}

// VerifyCurrentEmailTxInput carries parameters for step 2's store transaction.
type VerifyCurrentEmailTxInput struct {
	UserID           [16]byte
	NewEmailCodeHash string // bcrypt hash for the new confirm token OTP
	TTLSeconds       float64
	IPAddress        string
	UserAgent        string
}

// VerifyCurrentEmailStoreResult is returned by VerifyCurrentEmailTx on success.
// It carries the new confirm token's id (for audit) and the new_email extracted
// from the verify token's metadata (so the service can send the confirm OTP email).
type VerifyCurrentEmailStoreResult struct {
	NewEmail         string    // extracted from verify token metadata
	ConfirmTokenID   [16]byte  // id of the newly created confirm token (for audit)
	ConfirmExpiresAt time.Time // expires_at of the confirm token
}

// ConfirmEmailChangeTxInput carries parameters for step 3's store transaction.
type ConfirmEmailChangeTxInput struct {
	UserID    [16]byte
	IPAddress string
	UserAgent string
}

// ── Store methods ─────────────────────────────────────────────────────────────

// GetCurrentUserEmail returns the email address of userID.
// Returns profileshared.ErrUserNotFound when the user row does not exist.
func (s *Store) GetCurrentUserEmail(ctx context.Context, userID [16]byte) (string, error) {
	row, err := s.Queries.GetUserProfile(ctx, s.ToPgtypeUUID(userID))
	if err != nil {
		if s.IsNoRows(err) {
			return "", profileshared.ErrUserNotFound
		}
		return "", telemetry.Store("GetCurrentUserEmail.query", err)
	}
	return row.Email.String, nil
}

// CheckEmailAvailableForChange returns true when no active user other than the
// caller holds newEmail. The query uses SELECT EXISTS so pgx.ErrNoRows is never
// returned.
func (s *Store) CheckEmailAvailableForChange(ctx context.Context, newEmail string, userID [16]byte) (bool, error) {
	exists, err := s.Queries.CheckEmailAvailableForChange(ctx, db.CheckEmailAvailableForChangeParams{
		NewEmail: s.ToText(newEmail),
		UserID:   s.ToPgtypeUUID(userID),
	})
	if err != nil {
		return false, telemetry.Store("CheckEmailAvailableForChange.query", err)
	}
	// EXISTS returns true when the email IS taken by another user; negate for "available".
	return !exists, nil
}

// GetLatestEmailChangeVerifyTokenCreatedAt returns the created_at of the most
// recent active email_change_verify token for userID. Returns
// authshared.ErrTokenNotFound when no active token exists (pgx.ErrNoRows).
func (s *Store) GetLatestEmailChangeVerifyTokenCreatedAt(ctx context.Context, userID [16]byte) (time.Time, error) {
	createdAt, err := s.Queries.GetLatestEmailChangeVerifyTokenCreatedAt(ctx, s.ToPgtypeUUID(userID))
	if err != nil {
		if s.IsNoRows(err) {
			return time.Time{}, authshared.ErrTokenNotFound
		}
		return time.Time{}, telemetry.Store("GetLatestEmailChangeVerifyTokenCreatedAt.query", err)
	}
	return createdAt.UTC(), nil
}

// RequestEmailChangeTx atomically invalidates any existing email_change_verify
// tokens, creates a new one with the new_email in its metadata, and writes an
// audit log row.
//
// Transaction steps:
//  1. InvalidateUserEmailChangeVerifyTokens — void any existing active tokens.
//  2. Build metadata JSON {"new_email":"..."}.
//  3. CreateEmailChangeVerifyToken — RETURNING id + expires_at.
//  4. InsertAuditLog — event email_change_requested.
//  5. Commit.
func (s *Store) RequestEmailChangeTx(ctx context.Context, in RequestEmailChangeTxInput) error {
	h, err := s.BeginOrBind(ctx)
	if err != nil {
		return telemetry.Store("RequestEmailChangeTx.begin_tx", err)
	}

	userPgUUID := s.ToPgtypeUUID(in.UserID)

	// 1. Void any existing active verify tokens.
	if err := h.Q.InvalidateUserEmailChangeVerifyTokens(ctx, userPgUUID); err != nil {
		h.Rollback()
		return telemetry.Store("RequestEmailChangeTx.invalidate_tokens", err)
	}

	// 2. Build metadata JSON.
	metadata := s.MustJSON(map[string]string{"new_email": in.NewEmail})

	// 3. Create the new verify token.
	_, err = h.Q.CreateEmailChangeVerifyToken(ctx, db.CreateEmailChangeVerifyTokenParams{
		UserID:     userPgUUID,
		Email:      in.CurrentEmail,
		CodeHash:   s.ToText(in.CodeHash),
		Metadata:   metadata,
		TtlSeconds: in.TTLSeconds,
		IpAddress:  s.IPToNullable(in.IPAddress),
	})
	if err != nil {
		h.Rollback()
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// Race: invalidate ran but a concurrent insert sneaked in before us.
			return ErrCooldownActive
		}
		return telemetry.Store("RequestEmailChangeTx.create_token", err)
	}

	// 4. Audit log.
	if err := h.Q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		UserID:    userPgUUID,
		EventType: string(audit.EventEmailChangeRequested),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(in.IPAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(in.UserAgent)),
		Metadata:  s.MustJSON(map[string]string{"new_email": in.NewEmail}),
	}); err != nil {
		h.Rollback()
		return telemetry.Store("RequestEmailChangeTx.audit_log", err)
	}

	// 5. Commit.
	if err := h.Commit(); err != nil {
		return telemetry.Store("RequestEmailChangeTx.commit", err)
	}
	return nil
}

// VerifyCurrentEmailTx validates and consumes the email_change_verify token, then
// issues a new email_change_confirm token addressed to the new email.
//
// Transaction steps:
//  1. GetEmailChangeVerifyToken FOR UPDATE — pgx.ErrNoRows → ErrTokenNotFound.
//  2. Build VerificationToken; call checkFn — on error rollback and return.
//  3. ConsumeEmailChangeToken — rows==0 → ErrTokenAlreadyUsed.
//  4. Extract new_email from metadata JSON.
//  5. InvalidateUserEmailChangeConfirmTokens.
//  6. CreateEmailChangeConfirmToken — RETURNING id + expires_at.
//  7. InsertAuditLog — event email_change_current_verified.
//  8. Commit.
func (s *Store) VerifyCurrentEmailTx(ctx context.Context, in VerifyCurrentEmailTxInput, checkFn func(authshared.VerificationToken) error) (VerifyCurrentEmailStoreResult, error) {
	h, err := s.BeginOrBind(ctx)
	if err != nil {
		return VerifyCurrentEmailStoreResult{}, telemetry.Store("VerifyCurrentEmailTx.begin_tx", err)
	}

	userPgUUID := s.ToPgtypeUUID(in.UserID)

	// 1. Fetch and lock the active verify token.
	row, err := h.Q.GetEmailChangeVerifyToken(ctx, userPgUUID)
	if err != nil {
		h.Rollback()
		if s.IsNoRows(err) {
			return VerifyCurrentEmailStoreResult{}, authshared.ErrTokenNotFound
		}
		return VerifyCurrentEmailStoreResult{}, telemetry.Store("VerifyCurrentEmailTx.get_token", err)
	}

	// 2. Build VerificationToken and run application-layer checks (expiry, attempts, hash).
	token := authshared.NewVerificationToken(
		[16]byte(row.ID),
		row.UserID.Bytes,
		row.Email,
		row.CodeHash.String,
		row.Attempts,
		row.MaxAttempts,
		row.ExpiresAt.Time.UTC(),
	)
	if err := checkFn(token); err != nil {
		h.Rollback()
		return VerifyCurrentEmailStoreResult{}, err
	}

	// 3. Consume the verify token.
	rowsAffected, err := h.Q.ConsumeEmailChangeToken(ctx, s.ToPgtypeUUID(token.ID))
	if err != nil {
		h.Rollback()
		return VerifyCurrentEmailStoreResult{}, telemetry.Store("VerifyCurrentEmailTx.consume_token", err)
	}
	if rowsAffected == 0 {
		h.Rollback()
		return VerifyCurrentEmailStoreResult{}, authshared.ErrTokenAlreadyUsed
	}

	// 4. Extract new_email from the token's metadata JSON.
	var meta struct {
		NewEmail string `json:"new_email"`
	}
	if err := json.Unmarshal(row.Metadata, &meta); err != nil {
		h.Rollback()
		return VerifyCurrentEmailStoreResult{}, telemetry.Store("VerifyCurrentEmailTx.parse_metadata", err)
	}
	newEmail := meta.NewEmail

	// 5. Void any existing confirm tokens before issuing a new one.
	if err := h.Q.InvalidateUserEmailChangeConfirmTokens(ctx, userPgUUID); err != nil {
		h.Rollback()
		return VerifyCurrentEmailStoreResult{}, telemetry.Store("VerifyCurrentEmailTx.invalidate_confirm_tokens", err)
	}

	// 6. Create the confirm token (email column = new_email per D-09).
	confirmRow, err := h.Q.CreateEmailChangeConfirmToken(ctx, db.CreateEmailChangeConfirmTokenParams{
		UserID:     userPgUUID,
		NewEmail:   newEmail,
		CodeHash:   s.ToText(in.NewEmailCodeHash),
		TtlSeconds: in.TTLSeconds,
		IpAddress:  s.IPToNullable(in.IPAddress),
	})
	if err != nil {
		h.Rollback()
		return VerifyCurrentEmailStoreResult{}, telemetry.Store("VerifyCurrentEmailTx.create_confirm_token", err)
	}

	// 7. Audit log.
	if err := h.Q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		UserID:    userPgUUID,
		EventType: string(audit.EventEmailChangeCurrentVerified),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(in.IPAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(in.UserAgent)),
		Metadata:  s.MustJSON(map[string]string{"new_email": newEmail}),
	}); err != nil {
		h.Rollback()
		return VerifyCurrentEmailStoreResult{}, telemetry.Store("VerifyCurrentEmailTx.audit_log", err)
	}

	// 8. Commit.
	if err := h.Commit(); err != nil {
		return VerifyCurrentEmailStoreResult{}, telemetry.Store("VerifyCurrentEmailTx.commit", err)
	}

	return VerifyCurrentEmailStoreResult{
		NewEmail:         newEmail,
		ConfirmTokenID:   [16]byte(confirmRow.ID),
		ConfirmExpiresAt: confirmRow.ExpiresAt.Time.UTC(),
	}, nil
}

// IncrementAttemptsTx records a failed OTP attempt and optionally locks the account.
// Delegates to authshared.BaseStore.IncrementAttemptsTx, which always opens its
// own fresh transaction on the production path (ADR-003: counter commits are
// independent of the caller's context).
func (s *Store) IncrementAttemptsTx(ctx context.Context, in authshared.IncrementInput) error {
	return s.BaseStore.IncrementAttemptsTx(ctx, in)
}

// ConfirmEmailChangeTx validates and consumes the email_change_confirm token, then
// atomically sets the user's email and invalidates all sessions.
//
// Transaction steps:
//  1. GetEmailChangeConfirmToken FOR UPDATE — pgx.ErrNoRows → ErrTokenNotFound.
//  2. Build VerificationToken; call checkFn — on error rollback and return.
//  3. ConsumeEmailChangeToken — rows==0 → ErrTokenAlreadyUsed.
//  4. GetUserForEmailChangeTx FOR UPDATE — pgx.ErrNoRows → ErrUserNotFound.
//  5. SetUserEmail — 23505 on idx_users_email_active → ErrEmailTaken; rows==0 → ErrUserNotFound.
//  6. RevokeAllUserRefreshTokens (reason="email_changed").
//  7. EndAllUserSessions.
//  8. InsertAuditLog — event email_changed.
//  9. Commit.
func (s *Store) ConfirmEmailChangeTx(ctx context.Context, in ConfirmEmailChangeTxInput, checkFn func(authshared.VerificationToken) error) error {
	h, err := s.BeginOrBind(ctx)
	if err != nil {
		return telemetry.Store("ConfirmEmailChangeTx.begin_tx", err)
	}

	userPgUUID := s.ToPgtypeUUID(in.UserID)

	// 1. Fetch and lock the active confirm token.
	confirmRow, err := h.Q.GetEmailChangeConfirmToken(ctx, userPgUUID)
	if err != nil {
		h.Rollback()
		if s.IsNoRows(err) {
			return authshared.ErrTokenNotFound
		}
		return telemetry.Store("ConfirmEmailChangeTx.get_confirm_token", err)
	}

	// 2. Build VerificationToken (email column holds new_email per D-09) and check.
	token := authshared.NewVerificationToken(
		[16]byte(confirmRow.ID),
		confirmRow.UserID.Bytes,
		confirmRow.Email, // new_email stored in email column
		confirmRow.CodeHash.String,
		confirmRow.Attempts,
		confirmRow.MaxAttempts,
		confirmRow.ExpiresAt.Time.UTC(),
	)
	if err := checkFn(token); err != nil {
		h.Rollback()
		return err
	}

	// 3. Consume the confirm token.
	rowsAffected, err := h.Q.ConsumeEmailChangeToken(ctx, s.ToPgtypeUUID(token.ID))
	if err != nil {
		h.Rollback()
		return telemetry.Store("ConfirmEmailChangeTx.consume_token", err)
	}
	if rowsAffected == 0 {
		h.Rollback()
		return authshared.ErrTokenAlreadyUsed
	}

	// 4. Fetch and lock the user row to capture the current (old) email for the audit log.
	userRow, err := h.Q.GetUserForEmailChangeTx(ctx, userPgUUID)
	if err != nil {
		h.Rollback()
		if s.IsNoRows(err) {
			return authshared.ErrUserNotFound
		}
		return telemetry.Store("ConfirmEmailChangeTx.get_user", err)
	}
	oldEmail := userRow.Email.String
	newEmail := token.Email // new_email from the confirm token

	// 5. Swap the email.
	rowsAffected, err = h.Q.SetUserEmail(ctx, db.SetUserEmailParams{
		NewEmail: s.ToText(newEmail),
		UserID:   userPgUUID,
	})
	if err != nil {
		h.Rollback()
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrEmailTaken
		}
		return telemetry.Store("ConfirmEmailChangeTx.set_email", err)
	}
	if rowsAffected == 0 {
		h.Rollback()
		return authshared.ErrUserNotFound
	}

	// 6. Revoke all active refresh tokens so every existing session loses its token.
	if err := h.Q.RevokeAllUserRefreshTokens(ctx, db.RevokeAllUserRefreshTokensParams{
		UserID: userPgUUID,
		Reason: "email_changed",
	}); err != nil {
		h.Rollback()
		return telemetry.Store("ConfirmEmailChangeTx.revoke_refresh_tokens", err)
	}

	// 7. End all open sessions.
	if err := h.Q.EndAllUserSessions(ctx, userPgUUID); err != nil {
		h.Rollback()
		return telemetry.Store("ConfirmEmailChangeTx.end_sessions", err)
	}

	// 8. Audit log — record both old and new email for a complete change trail.
	if err := h.Q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		UserID:    userPgUUID,
		EventType: string(audit.EventEmailChanged),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(in.IPAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(in.UserAgent)),
		Metadata: s.MustJSON(map[string]string{
			"old_email": oldEmail,
			"new_email": newEmail,
		}),
	}); err != nil {
		h.Rollback()
		return telemetry.Store("ConfirmEmailChangeTx.audit_log", err)
	}

	// 9. Commit.
	if err := h.Commit(); err != nil {
		return telemetry.Store("ConfirmEmailChangeTx.commit", err)
	}
	return nil
}
