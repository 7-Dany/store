package session

import (
	"context"
	"fmt"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	profileshared "github.com/7-Dany/store/backend/internal/domain/profile/shared"
	"github.com/jackc/pgx/v5/pgxpool"
)

// compile-time check that *Store satisfies Storer.
var _ Storer = (*Store)(nil)

// Store holds the profileshared.BaseStore (pool + querier + txBound flag) and
// implements the session.Storer interface.
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

// GetActiveSessions returns all open sessions for the user, newest first.
// Returns an empty slice (not an error) when no sessions exist.
func (s *Store) GetActiveSessions(ctx context.Context, userID [16]byte) ([]ActiveSession, error) {
	rows, err := s.Queries.GetActiveSessions(ctx, s.ToPgtypeUUID(userID))
	if err != nil {
		return nil, fmt.Errorf("store.GetActiveSessions: query: %w", err)
	}

	sessions := make([]ActiveSession, 0, len(rows))
	for _, r := range rows {
		ipStr := ""
		if r.IpAddress != nil {
			// No BaseStore helper for reverse IP conversion; use .String() directly.
			ipStr = r.IpAddress.String()
		}
		sessions = append(sessions, ActiveSession{
			ID:           s.UUIDToBytes(r.ID),
			IPAddress:    ipStr,
			UserAgent:    r.UserAgent.String,
			StartedAt:    r.StartedAt.Time.UTC(),
			LastActiveAt: r.LastActiveAt.Time.UTC(),
		})
	}
	return sessions, nil
}

// RevokeSessionTx revokes a session in a single transaction: it fetches the
// session by ID, verifies the caller owns it (returning ErrSessionNotFound on
// mismatch to prevent IDOR), ends the session, revokes all associated refresh
// tokens, and writes a session_revoked audit row.
func (s *Store) RevokeSessionTx(ctx context.Context, sessionID, ownerUserID [16]byte, ipAddress, userAgent string) error {
	h, err := s.BeginOrBind(ctx)
	if err != nil {
		// Unreachable: BeginOrBind with TxBound=true never calls Pool.Begin
		// and always returns nil error. No test can trigger this branch.
		return fmt.Errorf("store.RevokeSessionTx: begin tx: %w", err)
	}

	sessionPgUUID := s.ToPgtypeUUID(sessionID)
	userPgUUID := s.ToPgtypeUUID(ownerUserID)

	// 1. Fetch session by ID.
	session, err := h.Q.GetSessionByID(ctx, sessionPgUUID)
	if err != nil {
		h.Rollback()
		if s.IsNoRows(err) {
			return authshared.ErrSessionNotFound
		}
		return fmt.Errorf("store.RevokeSessionTx: get session: %w", err)
	}

	// 2. Verify ownership.
	// Security: returning ErrSessionNotFound here prevents IDOR — an attacker
	// cannot distinguish "session not found" from "session belongs to another user".
	if session.UserID.Bytes != ownerUserID {
		h.Rollback()
		return authshared.ErrSessionNotFound
	}

	// 3. End the session.
	if err := h.Q.EndUserSession(ctx, sessionPgUUID); err != nil {
		h.Rollback()
		return fmt.Errorf("store.RevokeSessionTx: end session: %w", err)
	}

	// 4. Revoke all non-revoked refresh tokens for this session.
	if err := h.Q.RevokeSessionRefreshTokens(ctx, sessionPgUUID); err != nil {
		h.Rollback()
		return fmt.Errorf("store.RevokeSessionTx: revoke tokens: %w", err)
	}

	// 5. Audit row.
	if err := h.Q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		UserID:    userPgUUID,
		EventType: string(audit.EventSessionRevoked),
		// Provider is fixed to Email; the session's originating OAuth provider is not
		// persisted on the session row and cannot be recovered here.
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(ipAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(userAgent)),
		Metadata:  []byte("{}"),
	}); err != nil {
		h.Rollback()
		return fmt.Errorf("store.RevokeSessionTx: audit log: %w", err)
	}

	if err := h.Commit(); err != nil {
		// Unreachable via QuerierProxy: on the TxBound path commitFn is a no-op
		// that always returns nil; on the non-TxBound path commitFn wraps
		// pgx.Tx.Commit which the proxy cannot intercept.
		return fmt.Errorf("store.RevokeSessionTx: commit: %w", err)
	}
	return nil
}
