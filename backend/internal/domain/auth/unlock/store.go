package unlock

import (
	"context"
	"fmt"
	"time"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/jackc/pgx/v5/pgxpool"
)

// compile-time check that *Store satisfies Storer.
var _ Storer = (*Store)(nil)

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

// ── GetUserForUnlock ──────────────────────────────────────────────────────────

// GetUserForUnlock fetches the minimal user fields needed to gate a self-service
// unlock request. Returns authshared.ErrUserNotFound on no-rows.
func (s *Store) GetUserForUnlock(ctx context.Context, email string) (UnlockUser, error) {
	row, err := s.Queries.GetUserForUnlock(ctx, s.ToText(email))
	if err != nil {
		if s.IsNoRows(err) {
			return UnlockUser{}, authshared.ErrUserNotFound
		}
		return UnlockUser{}, fmt.Errorf("store.GetUserForUnlock: query: %w", err)
	}
	var loginLockedUntil *time.Time
	if row.LoginLockedUntil.Valid {
		t := row.LoginLockedUntil.Time.UTC()
		loginLockedUntil = &t
	}
	return UnlockUser{
		ID:               s.UUIDToBytes(row.ID),
		EmailVerified:    row.EmailVerified,
		IsLocked:         row.IsLocked,
		AdminLocked:      row.AdminLocked,
		LoginLockedUntil: loginLockedUntil,
	}, nil
}

// ── GetUnlockToken ───────────────────────────────────────────────────────────

// GetUnlockToken returns the active unlock token for the given email address.
// Returns authshared.ErrTokenNotFound when no unconsumed, non-expired row exists.
func (s *Store) GetUnlockToken(ctx context.Context, email string) (authshared.VerificationToken, error) {
	row, err := s.Queries.GetUnlockToken(ctx, email)
	if err != nil {
		if s.IsNoRows(err) {
			return authshared.VerificationToken{}, authshared.ErrTokenNotFound
		}
		return authshared.VerificationToken{}, fmt.Errorf("store.GetUnlockToken: query: %w", err)
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

// ── RequestUnlockTx ───────────────────────────────────────────────────────────

// RequestUnlockTx inserts an account_unlock OTP token and writes the
// unlock_requested audit row in a single transaction:
//  1. Creates the unlock token using the bcrypt code hash from in.CodeHash.
//  2. Writes the unlock_requested audit row.
func (s *Store) RequestUnlockTx(ctx context.Context, in RequestUnlockStoreInput) error {
	h, err := s.BeginOrBind(ctx)
	// Unreachable: BeginOrBind with TxBound=true never calls Pool.Begin
	// and always returns nil error. No test can trigger this branch.
	if err != nil {
		return fmt.Errorf("store.RequestUnlockTx: begin tx: %w", err)
	}

	userPgUUID := s.ToPgtypeUUID(in.UserID)

	// 1. Create unlock token.
	// Security: TtlSeconds is passed as a duration so PostgreSQL computes
	// expires_at = NOW() + ttl, keeping both timestamps on the same clock
	// and preventing chk_ott_au_ttl_max violations from app/DB clock skew.
	if _, err := h.Q.CreateUnlockToken(ctx, db.CreateUnlockTokenParams{
		UserID:     userPgUUID,
		Email:      in.Email,
		CodeHash:   s.ToText(in.CodeHash),
		TtlSeconds: in.TTL.Seconds(),
		IpAddress:  s.IPToNullable(in.IPAddress),
	}); err != nil {
		h.Rollback()
		return fmt.Errorf("store.RequestUnlockTx: create token: %w", err)
	}

	// 2. Audit row.
	if err := h.Q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		UserID:    userPgUUID,
		EventType: string(audit.EventUnlockRequested),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(in.IPAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(in.UserAgent)),
		Metadata:  []byte("{}"),
	}); err != nil {
		h.Rollback()
		return fmt.Errorf("store.RequestUnlockTx: audit log: %w", err)
	}

	// Unreachable via QuerierProxy: on the TxBound path commitFn is a no-op
	// that always returns nil; on the non-TxBound path commitFn wraps
	// pgx.Tx.Commit which the proxy cannot intercept.
	if err := h.Commit(); err != nil {
		return fmt.Errorf("store.RequestUnlockTx: commit: %w", err)
	}
	return nil
}

// ── ConsumeUnlockTokenTx ──────────────────────────────────────────────────────

// ConsumeUnlockTokenTx fetches the active unlock token for email under a FOR
// UPDATE lock, runs checkFn, and on success consumes the token:
//  1. Fetches the token row (FOR UPDATE).
//  2. Maps the row to authshared.VerificationToken.
//  3. Calls the caller-supplied checkFn for expiry/attempts/hash validation.
//  4. Consumes the token (AND used_at IS NULL guard for idempotency).
func (s *Store) ConsumeUnlockTokenTx(ctx context.Context, email string,
	checkFn func(authshared.VerificationToken) error) error {

	h, err := s.BeginOrBind(ctx)
	// Unreachable: BeginOrBind with TxBound=true never calls Pool.Begin
	// and always returns nil error. No test can trigger this branch.
	if err != nil {
		return fmt.Errorf("store.ConsumeUnlockTokenTx: begin tx: %w", err)
	}

	// 1. Fetch token (FOR UPDATE in query).
	row, err := h.Q.GetUnlockToken(ctx, email)
	if err != nil {
		h.Rollback()
		if s.IsNoRows(err) {
			// Distinguish "token was consumed" from "token never existed".
			// GetUnlockToken filters used_at IS NULL, so a consumed token is
			// invisible to it. Check the broader set before returning.
			consumed, checkErr := s.Queries.HasConsumedUnlockToken(ctx, email)
			if checkErr != nil {
				return fmt.Errorf("store.ConsumeUnlockTokenTx: check consumed: %w", checkErr)
			}
			if consumed {
				return authshared.ErrTokenAlreadyUsed
			}
			return authshared.ErrTokenNotFound
		}
		return fmt.Errorf("store.ConsumeUnlockTokenTx: get token: %w", err)
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

	// 3. Caller-supplied check (expiry / attempts / hash).
	if err := checkFn(tok); err != nil {
		h.Rollback()
		return err
	}

	// 4. Consume the token (idempotent: AND used_at IS NULL guard).
	consumed, err := h.Q.ConsumeUnlockToken(ctx, s.UUIDToPgtypeUUID(row.ID))
	if err != nil {
		h.Rollback()
		return fmt.Errorf("store.ConsumeUnlockTokenTx: consume token: %w", err)
	}
	if consumed == 0 {
		h.Rollback()
		return authshared.ErrTokenAlreadyUsed
	}

	// Unreachable via QuerierProxy: on the TxBound path commitFn is a no-op
	// that always returns nil; on the non-TxBound path commitFn wraps
	// pgx.Tx.Commit which the proxy cannot intercept.
	if err := h.Commit(); err != nil {
		return fmt.Errorf("store.ConsumeUnlockTokenTx: commit: %w", err)
	}
	return nil
}

// ── UnlockAccountTx ───────────────────────────────────────────────────────────

// UnlockAccountTx clears is_locked, failed_login_attempts, and
// login_locked_until, and writes an account_unlocked audit row:
//  1. Clears all lock state.
//  2. Writes the account_unlocked audit row.
func (s *Store) UnlockAccountTx(ctx context.Context, userID [16]byte, ipAddress, userAgent string) error {
	h, err := s.BeginOrBind(ctx)
	// Unreachable: BeginOrBind with TxBound=true never calls Pool.Begin
	// and always returns nil error. No test can trigger this branch.
	if err != nil {
		return fmt.Errorf("store.UnlockAccountTx: begin tx: %w", err)
	}

	userPgUUID := s.ToPgtypeUUID(userID)

	// 1. Clear all lock state.
	if err := h.Q.UnlockAccount(ctx, userPgUUID); err != nil {
		h.Rollback()
		return fmt.Errorf("store.UnlockAccountTx: unlock account: %w", err)
	}

	// 2. Audit row.
	if err := h.Q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		UserID:    userPgUUID,
		EventType: string(audit.EventAccountUnlocked),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(ipAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(userAgent)),
		Metadata:  []byte("{}"),
	}); err != nil {
		h.Rollback()
		return fmt.Errorf("store.UnlockAccountTx: audit log: %w", err)
	}

	// Unreachable via QuerierProxy: on the TxBound path commitFn is a no-op
	// that always returns nil; on the non-TxBound path commitFn wraps
	// pgx.Tx.Commit which the proxy cannot intercept.
	if err := h.Commit(); err != nil {
		return fmt.Errorf("store.UnlockAccountTx: commit: %w", err)
	}
	return nil
}

