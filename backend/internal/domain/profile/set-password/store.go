package setpassword

import (
	"context"
	"fmt"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	profileshared "github.com/7-Dany/store/backend/internal/domain/profile/shared"
	"github.com/jackc/pgx/v5/pgxpool"
)

// compile-time check that *Store satisfies Storer.
var _ Storer = (*Store)(nil)

// Store is the data-access layer for POST /set-password.
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

// GetUserForSetPassword fetches whether the user currently has no password.
// Returns ErrUserNotFound on no-rows.
func (s *Store) GetUserForSetPassword(ctx context.Context, userID [16]byte) (SetPasswordUser, error) {
	row, err := s.Queries.GetUserForSetPassword(ctx, s.ToPgtypeUUID(userID))
	if err != nil {
		if s.IsNoRows(err) {
			return SetPasswordUser{}, profileshared.ErrUserNotFound
		}
		return SetPasswordUser{}, fmt.Errorf("store.GetUserForSetPassword: query: %w", err)
	}
	// HasNoPassword is a computed column (password_hash IS NULL); sqlc generates
	// interface{} for boolean expressions — type-assert to bool safely.
	hasNoPassword, _ := row.HasNoPassword.(bool)
	return SetPasswordUser{HasNoPassword: hasNoPassword}, nil
}

// SetPasswordHashTx sets the password hash for an OAuth-only account and
// writes the password_set audit row — both in one transaction.
//
// The WHERE password_hash IS NULL guard in the SQL query is the DB-level
// concurrency check: a concurrent set-password call that races past the
// service guard returns 0 rows affected, which this method maps to
// ErrPasswordAlreadySet.
func (s *Store) SetPasswordHashTx(ctx context.Context, in SetPasswordInput, newHash string) error {
	h, err := s.BeginOrBind(ctx)
	if err != nil {
		return fmt.Errorf("store.SetPasswordHashTx: begin tx: %w", err)
	}

	userPgUUID := s.ToPgtypeUUID(s.mustParseUserID(in.UserID))

	// 1. Attempt to set the password hash. The WHERE password_hash IS NULL
	//    guard returns 0 rows when a concurrent call already set the hash,
	//    which we map to ErrPasswordAlreadySet.
	n, err := h.Q.SetPasswordHash(ctx, db.SetPasswordHashParams{
		PasswordHash: s.ToText(newHash),
		UserID:       userPgUUID,
	})
	if err != nil {
		h.Rollback()
		return fmt.Errorf("store.SetPasswordHashTx: set hash: %w", err)
	}
	if n == 0 {
		h.Rollback()
		return ErrPasswordAlreadySet
	}

	// 2. Audit row — written after the successful UPDATE so the event is not
	//    recorded for a no-op or failed operation.
	if err := h.Q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		UserID:    userPgUUID,
		EventType: string(audit.EventPasswordSet),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(in.IPAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(in.UserAgent)),
		Metadata:  []byte("{}"),
	}); err != nil {
		h.Rollback()
		return fmt.Errorf("store.SetPasswordHashTx: audit log: %w", err)
	}

	// Unreachable via QuerierProxy: on the TxBound path h.Commit is a no-op
	// that always returns nil; on the non-TxBound path it wraps pgx.Tx.Commit
	// which the proxy cannot intercept.
	if err := h.Commit(); err != nil {
		return fmt.Errorf("store.SetPasswordHashTx: commit: %w", err)
	}
	return nil
}

// mustParseUserID parses a UUID string into [16]byte.
// Panics on invalid input — callers must validate before reaching the store.
func (s *Store) mustParseUserID(id string) [16]byte {
	pgUUID, err := s.ParseUUIDString(id)
	if err != nil {
		panic(fmt.Sprintf("setpassword.Store: invalid user ID %q: %v", id, err))
	}
	return pgUUID.Bytes
}
