package rbacshared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"

	"github.com/7-Dany/store/backend/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// maxUserAgentBytes is the maximum number of bytes stored in the user_agent column.
const maxUserAgentBytes = 512

// BaseStore holds the connection pool and the beginOrBind machinery.
// Every rbac feature's Store embeds BaseStore.
type BaseStore struct {
	Pool    *pgxpool.Pool
	Queries db.Querier
	TxBound bool
}

// NewBaseStore constructs a BaseStore backed by pool.
func NewBaseStore(pool *pgxpool.Pool) BaseStore {
	return BaseStore{
		Pool:    pool,
		Queries: db.New(pool),
	}
}

// WithQuerier returns a shallow copy of b whose Queries field is replaced by q
// and whose TxBound flag is set to true. Used in tests to scope all writes to a
// single rolled-back transaction.
func (b BaseStore) WithQuerier(q db.Querier) BaseStore {
	b.Queries = q
	b.TxBound = true
	return b
}

// TxHelpers bundles the querier and commit/rollback funcs produced by BeginOrBind.
// Rollback returns an error so callers can log failures explicitly.
type TxHelpers struct {
	Q        db.Querier
	Commit   func() error
	Rollback func() error
}

// BeginOrBind either opens a real transaction (production path) or binds to
// the caller-injected querier (test path). In the bound case Commit and Rollback
// are no-ops so the outer test transaction stays in control.
func (b BaseStore) BeginOrBind(ctx context.Context) (TxHelpers, error) {
	if b.TxBound {
		return TxHelpers{
			Q:        b.Queries,
			Commit:   func() error { return nil },
			Rollback: func() error { return nil },
		}, nil
	}
	tx, err := b.Pool.Begin(ctx)
	if err != nil {
		return TxHelpers{}, err
	}
	safeCtx := context.WithoutCancel(ctx)
	return TxHelpers{
		Q:      db.New(tx),
		Commit: func() error { return tx.Commit(ctx) },
		Rollback: func() error {
			if rErr := tx.Rollback(safeCtx); rErr != nil && !errors.Is(rErr, pgx.ErrTxClosed) {
				return rErr
			}
			return nil
		},
	}, nil
}

// ── Type-conversion helpers ───────────────────────────────────────────────────

// ToPgtypeUUID converts a raw [16]byte to pgtype.UUID.
func (b BaseStore) ToPgtypeUUID(v [16]byte) pgtype.UUID {
	return pgtype.UUID{Bytes: v, Valid: true}
}

// ToPgtypeUUIDNullable converts a raw [16]byte to pgtype.UUID.
// Returns a NULL UUID (Valid: false) if v is the zero value.
func (b BaseStore) ToPgtypeUUIDNullable(v [16]byte) pgtype.UUID {
	if v == [16]byte{} {
		return pgtype.UUID{Valid: false}
	}
	return pgtype.UUID{Bytes: v, Valid: true}
}

// UUIDToPgtypeUUID converts a google/uuid.UUID to pgtype.UUID.
func (b BaseStore) UUIDToPgtypeUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte(u), Valid: true}
}

// UUIDToBytes converts a google/uuid.UUID to a raw [16]byte.
func (b BaseStore) UUIDToBytes(u uuid.UUID) [16]byte {
	return [16]byte(u)
}

// ParseUUIDString parses a standard UUID string into a pgtype.UUID.
func (b BaseStore) ParseUUIDString(s string) (pgtype.UUID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("ParseUUIDString: %w", err)
	}
	return b.UUIDToPgtypeUUID(u), nil
}

// ToText wraps a string as a pgtype.Text. Valid is false when s is empty.
func (b BaseStore) ToText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

// MustJSON marshals v to JSON and panics on error.
func (b BaseStore) MustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("rbacshared.MustJSON: unexpected marshal error: %v", err))
	}
	return data
}

// IPToNullable parses ip as a netip.Addr and returns a pointer to it.
// Returns nil when ip is empty or cannot be parsed, mapping to SQL NULL.
func (b BaseStore) IPToNullable(ip string) *netip.Addr {
	if ip == "" {
		return nil
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return nil
	}
	return &addr
}

// TruncateUserAgent truncates ua to at most maxUserAgentBytes bytes.
func (b BaseStore) TruncateUserAgent(ua string) string {
	if len(ua) <= maxUserAgentBytes {
		return ua
	}
	return ua[:maxUserAgentBytes]
}

// IsNoRows reports whether err is a pgx no-rows sentinel.
func (b BaseStore) IsNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

// IsDuplicateEmail reports whether err is a Postgres unique-violation (23505)
// on idx_users_email_active.
func (b BaseStore) IsDuplicateEmail(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505" && pgErr.ConstraintName == "idx_users_email_active"
	}
	return false
}

// IsDuplicateUsername reports whether err is a Postgres unique-violation (23505)
// on idx_users_username_active.
func (b BaseStore) IsDuplicateUsername(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505" && pgErr.ConstraintName == "idx_users_username_active"
	}
	return false
}

// ── Standalone helpers ────────────────────────────────────────────────────────

// LogRollback calls tx.Rollback and logs any error that is not ErrTxClosed.
func LogRollback(ctx context.Context, tx interface{ Rollback(context.Context) error }, label string) {
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		slog.Error("store: rollback failed", "label", label, "error", err)
	}
}
