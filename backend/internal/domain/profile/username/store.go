package username

import (
	"context"
	"errors"
	"fmt"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	profileshared "github.com/7-Dany/store/backend/internal/domain/profile/shared"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// compile-time check that *Store satisfies Storer.
var _ Storer = (*Store)(nil)

// Store holds the profileshared.BaseStore (pool + querier + txBound flag) and
// implements the username.Storer interface.
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

// CheckUsernameAvailable returns true when no user row with username = username
// exists in the database.
// The query uses SELECT EXISTS so pgx.ErrNoRows is never returned from this method.
func (s *Store) CheckUsernameAvailable(ctx context.Context, username string) (bool, error) {
	// CheckUsernameAvailable takes pgtype.Text; use ToText to convert.
	exists, err := s.Queries.CheckUsernameAvailable(ctx, s.ToText(username))
	if err != nil {
		return false, fmt.Errorf("store.CheckUsernameAvailable: query: %w", err)
	}
	// EXISTS returns true when the username IS taken; negate for "available".
	return !exists, nil
}

// UpdateUsernameTx atomically updates the user's username and writes an audit row.
//
// Guard ordering (from docs/prompts/username/0-design.md §5b):
//  1. Fetch current username with FOR UPDATE lock.
//  2. If currentUsername == newUsername → ErrSameUsername (no-op, no DB write).
//  3. SET username; map 23505 on idx_users_username → ErrUsernameTaken; rows==0 → ErrUserNotFound.
//  4. Write audit log with old and new username in metadata.
//  5. Commit.
func (s *Store) UpdateUsernameTx(ctx context.Context, in UpdateUsernameInput) error {
	h, err := s.BeginOrBind(ctx)
	if err != nil {
		// Unreachable: BeginOrBind with TxBound=true never calls Pool.Begin
		// and always returns nil error. No test can trigger this branch.
		return fmt.Errorf("store.UpdateUsernameTx: begin tx: %w", err)
	}

	// 1. Fetch the current username and lock the row.
	row, err := h.Q.GetUserForUsernameUpdate(ctx, s.ToPgtypeUUID(in.UserID))
	if err != nil {
		h.Rollback()
		if s.IsNoRows(err) {
			return authshared.ErrUserNotFound
		}
		return fmt.Errorf("store.UpdateUsernameTx: get user: %w", err)
	}

	// 2. Same-username guard — row.Username is pgtype.Text; .String is safe because
	// username IS NOT NULL for all authenticated users (set at registration or earlier).
	if row.Username.String == in.Username {
		h.Rollback()
		return ErrSameUsername
	}

	// 3. Update the username column.
	rowsAffected, err := h.Q.SetUsername(ctx, db.SetUsernameParams{
		Username: s.ToText(in.Username),
		UserID:   s.ToPgtypeUUID(in.UserID),
	})
	if err != nil {
		h.Rollback()
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrUsernameTaken
		}
		return fmt.Errorf("store.UpdateUsernameTx: set username: %w", err)
	}
	if rowsAffected == 0 {
		h.Rollback()
		return authshared.ErrUserNotFound
	}

	// 4. Audit log — record old and new username for a complete change trail.
	if err := h.Q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		UserID:    s.ToPgtypeUUID(in.UserID),
		EventType: string(audit.EventUsernameChanged),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(in.IPAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(in.UserAgent)),
		Metadata: s.MustJSON(map[string]string{
			"old_username": row.Username.String,
			"new_username": in.Username,
		}),
	}); err != nil {
		h.Rollback()
		return fmt.Errorf("store.UpdateUsernameTx: audit log: %w", err)
	}

	if err := h.Commit(); err != nil {
		// Unreachable via QuerierProxy: on the TxBound path Commit is a no-op
		// that always returns nil; on the non-TxBound path Commit wraps
		// pgx.Tx.Commit which the proxy cannot intercept.
		return fmt.Errorf("store.UpdateUsernameTx: commit: %w", err)
	}
	return nil
}
