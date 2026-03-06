package verification

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

// Store provides database access for the verification feature.
type Store struct {
	authshared.BaseStore
}

// NewStore constructs a Store backed by pool.
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

// ── VerifyEmailTx ─────────────────────────────────────────────────────────────

// VerifyEmailTx fetches the active verification token for email, runs checkFn,
// and on success marks the email verified — all inside a transaction.
// ipAddress and userAgent are recorded in the email_verified audit log row.
func (s *Store) VerifyEmailTx(ctx context.Context, email, ipAddress, userAgent string, checkFn func(authshared.VerificationToken) error) error {
	h, err := s.BeginOrBind(ctx)
	if err != nil {
		return fmt.Errorf("store.VerifyEmailTx: begin tx: %w", err)
	}

	// 1. Fetch token (query uses FOR UPDATE).
	row, err := h.Q.GetEmailVerificationToken(ctx, email)
	if err != nil {
		h.Rollback()
		if s.IsNoRows(err) {
			return authshared.ErrTokenNotFound
		}
		return fmt.Errorf("store.VerifyEmailTx: get token: %w", err)
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

	// 3. Caller-supplied check (e.g. code comparison, expiry).
	if err := checkFn(tok); err != nil {
		h.Rollback()
		return err
	}

	// 4a. Consume token (idempotent: AND used_at IS NULL guard).
	consumed, err := h.Q.ConsumeEmailVerificationToken(ctx, s.UUIDToPgtypeUUID(row.ID))
	if err != nil {
		h.Rollback()
		return fmt.Errorf("store.VerifyEmailTx: consume token: %w", err)
	}
	if consumed == 0 {
		// A concurrent request won the FOR UPDATE race and already consumed this token.
		h.Rollback()
		return authshared.ErrTokenAlreadyUsed
	}

	// 4b. Revoke all remaining pre-verification tokens for this user.
	if err := h.Q.RevokePreVerificationTokens(ctx, row.UserID); err != nil {
		h.Rollback()
		return fmt.Errorf("store.VerifyEmailTx: revoke prior tokens: %w", err)
	}

	// 4c. Mark email verified.
	marked, err := h.Q.MarkEmailVerified(ctx, row.UserID)
	if err != nil {
		h.Rollback()
		return fmt.Errorf("store.VerifyEmailTx: mark email verified: %w", err)
	}
	if marked == 0 {
		// Distinguish locked account from already-verified in a single query
		// to avoid TOCTOU race condition.
		state, stateErr := h.Q.GetUserVerifiedAndLocked(ctx, row.UserID)
		if stateErr != nil {
			h.Rollback()
			return fmt.Errorf("store.VerifyEmailTx: get user state: %w", stateErr)
		}
		h.Rollback()
		if state.IsLocked || state.AdminLocked {
			return authshared.ErrAccountLocked
		}
		return authshared.ErrAlreadyVerified
	}

	// 4d. Audit log.
	if err := h.Q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		UserID:    row.UserID,
		EventType: string(audit.EventEmailVerified),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(ipAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(userAgent)),
		Metadata:  []byte("{}"),
	}); err != nil {
		h.Rollback()
		return fmt.Errorf("store.VerifyEmailTx: audit log: %w", err)
	}

	if err := h.Commit(); err != nil {
		return fmt.Errorf("store.VerifyEmailTx: commit: %w", err)
	}

	return nil
}

// ── GetUserForResend ──────────────────────────────────────────────────────────

// GetUserForResend fetches the minimal user data needed to gate a resend request.
func (s *Store) GetUserForResend(ctx context.Context, email string) (ResendUser, error) {
	row, err := s.Queries.GetUserForResend(ctx, s.ToText(email))
	if err != nil {
		if s.IsNoRows(err) {
			return ResendUser{}, authshared.ErrUserNotFound
		}
		return ResendUser{}, fmt.Errorf("store.GetUserForResend: query: %w", err)
	}
	return ResendUser{
		ID:            s.UUIDToBytes(row.ID),
		EmailVerified: row.EmailVerified,
		IsLocked:      row.IsLocked || row.AdminLocked,
	}, nil
}

// ── GetLatestTokenCreatedAt ───────────────────────────────────────────────────

// GetLatestTokenCreatedAt returns the creation time of the most recent unused
// verification token for the user. Returns zero time (not an error) when no
// unused token exists — zero time is the sentinel the service uses to allow
// immediate resend.
func (s *Store) GetLatestTokenCreatedAt(ctx context.Context, userID [16]byte) (time.Time, error) {
	t, err := s.Queries.GetLatestVerificationTokenCreatedAt(ctx, s.ToPgtypeUUID(userID))
	if err != nil {
		if s.IsNoRows(err) {
			return time.Time{}, nil
		}
		return time.Time{}, fmt.Errorf("store.GetLatestTokenCreatedAt: query: %w", err)
	}
	return t.UTC(), nil
}

// ── ResendVerificationTx ──────────────────────────────────────────────────────

// ResendVerificationTx invalidates all prior tokens and creates a fresh one
// inside a transaction.
func (s *Store) ResendVerificationTx(ctx context.Context, in ResendStoreInput, codeHash string) error {
	h, err := s.BeginOrBind(ctx)
	if err != nil {
		return fmt.Errorf("store.ResendVerificationTx: begin tx: %w", err)
	}

	userPgUUID := s.ToPgtypeUUID(in.UserID)

	// 1. Invalidate all prior unused tokens.
	if err := h.Q.InvalidateAllUserTokens(ctx, userPgUUID); err != nil {
		h.Rollback()
		return fmt.Errorf("store.ResendVerificationTx: invalidate tokens: %w", err)
	}

	// 2. Create new token.
	if _, err := h.Q.CreateEmailVerificationToken(ctx, db.CreateEmailVerificationTokenParams{
		UserID:     userPgUUID,
		Email:      in.Email,
		CodeHash:   s.ToText(codeHash),
		TtlSeconds: in.TTL.Seconds(),
		IpAddress:  s.IPToNullable(in.IPAddress),
	}); err != nil {
		h.Rollback()
		return fmt.Errorf("store.ResendVerificationTx: create token: %w", err)
	}

	// 3. Audit log.
	if err := h.Q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		UserID:    userPgUUID,
		EventType: string(audit.EventResendVerification),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(in.IPAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(in.UserAgent)),
		Metadata:  []byte("{}"),
	}); err != nil {
		h.Rollback()
		return fmt.Errorf("store.ResendVerificationTx: audit log: %w", err)
	}

	if err := h.Commit(); err != nil {
		return fmt.Errorf("store.ResendVerificationTx: commit: %w", err)
	}

	return nil
}
