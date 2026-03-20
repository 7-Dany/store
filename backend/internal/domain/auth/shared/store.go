package authshared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
	"github.com/jackc/pgx/v5"
	// github.com/jackc/pgx/v5/pgconn is not in the §1.3 DB-package gate list but
	// is permitted here solely for *pgconn.PgError type inspection in IsDuplicateEmail.
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	// google/uuid is not in the §1.3 DB-package gate list but is permitted by
	// the broader §1.7 auth/shared allowed list. Used only for UUID parsing and
	// type-conversion helpers (ParseUUIDString, UUIDToPgtypeUUID, UUIDToBytes).
	"github.com/google/uuid"
)

// maxUserAgentBytes is the maximum number of bytes stored in the user_agent column.
const maxUserAgentBytes = 512

// BaseStore holds the connection pool and the beginOrBind machinery.
// Every feature's Store embeds BaseStore.
//
// Fields are exported so feature stores in sibling packages can access them
// directly when needed (e.g. s.Pool.Begin in IncrementAttemptsTx variants).
type BaseStore struct {
	Pool    *pgxpool.Pool
	Queries db.Querier
	TxBound bool
}

var log = telemetry.New("authshared")

// NewBaseStore constructs a BaseStore backed by pool.
func NewBaseStore(pool *pgxpool.Pool) BaseStore {
	return BaseStore{
		Pool:    pool,
		Queries: db.New(pool),
	}
}

// WithQuerier returns a shallow copy of b whose Queries field is replaced by q
// and whose TxBound flag is set to true. Tx methods in feature stores will use
// q directly without opening a new transaction, so the caller's transaction
// controls commit/rollback. Used in tests to scope all writes to a single
// rolled-back transaction.
func (b BaseStore) WithQuerier(q db.Querier) BaseStore {
	b.Queries = q
	b.TxBound = true
	return b
}

// TxHelpers bundles the querier and commit/rollback funcs produced by BeginOrBind.
type TxHelpers struct {
	Q        db.Querier
	Commit   func() error
	Rollback func()
}

// BeginOrBind either opens a real transaction (production path) or binds to
// the caller-injected querier (test path). In the bound case, Commit and
// Rollback are no-ops so the outer test transaction stays in control.
//
// Rollback uses context.WithoutCancel so a cancelled HTTP request context does
// not prevent the rollback from reaching PostgreSQL.
func (b BaseStore) BeginOrBind(ctx context.Context) (TxHelpers, error) {
	if b.TxBound {
		return TxHelpers{
			Q:        b.Queries,
			Commit:   func() error { return nil },
			Rollback: func() {},
		}, nil
	}
	tx, err := b.Pool.Begin(ctx)
	if err != nil {
		return TxHelpers{}, err
	}
	return TxHelpers{
		Q:      db.New(tx),
		Commit: func() error { return tx.Commit(ctx) },
		Rollback: func() {
			// Security: use WithoutCancel so a cancelled request context does not
			// prevent the rollback from reaching PostgreSQL.
			LogRollback(context.WithoutCancel(ctx), tx, "tx")
		},
	}, nil
}

// ── Type-conversion helpers ───────────────────────────────────────────────────
// These are methods on BaseStore so that feature stores access them through the
// embedded struct (s.ToPgtypeUUID, s.ToText, etc.) without importing the
// helper functions directly. They have no state dependency on BaseStore.

// ToPgtypeUUID converts a raw [16]byte to pgtype.UUID.
func (b BaseStore) ToPgtypeUUID(v [16]byte) pgtype.UUID {
	return pgtype.UUID{Bytes: v, Valid: true}
}

// ToPgtypeUUIDNullable converts a raw [16]byte to pgtype.UUID.
// Returns a NULL UUID (Valid: false) if v is the zero value.
func (b BaseStore) ToPgtypeUUIDNullable(v [16]byte) pgtype.UUID {
	// Check if all bytes are zero
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
		return pgtype.UUID{}, telemetry.Store("ParseUUIDString.parse", err)
	}
	return b.UUIDToPgtypeUUID(u), nil
}

// ToText wraps a string as a pgtype.Text. Valid is false when s is empty.
func (b BaseStore) ToText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

// MustJSON marshals v to JSON and panics on error.
// Use only for values guaranteed to be marshallable (e.g. map[string]string).
func (b BaseStore) MustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("authshared.MustJSON: unexpected marshal error: %v", err))
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
// on idx_users_email_active (users.email WHERE email IS NOT NULL AND deleted_at IS NULL).
func (b BaseStore) IsDuplicateEmail(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505" && pgErr.ConstraintName == "idx_users_email_active"
	}
	return false
}

// IsDuplicateUsername reports whether err is a Postgres unique-violation (23505)
// on idx_users_username_active (users.username WHERE username IS NOT NULL AND deleted_at IS NULL).
func (b BaseStore) IsDuplicateUsername(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505" && pgErr.ConstraintName == "idx_users_username_active"
	}
	return false
}

// ── Shared transactional methods ────────────────────────────────────────────────

// IncrementAttemptsTx records a failed OTP attempt and, if the threshold is
// reached, locks the account.
//
// Production path (TxBound == false): always opens a fresh transaction via
// b.Pool, never BeginOrBind, so attempt and lock rows are committed
// independently even when the caller's transaction rolls back. This prevents
// a client-disconnect from wiping the attempt counter and granting unlimited
// OTP guesses.
//
// Test path (TxBound == true): delegates to BeginOrBind so the injected
// QuerierProxy can intercept individual calls and the test-transaction user
// is visible. Commit/rollback are no-ops; the outer test transaction controls
// everything.
func (b BaseStore) IncrementAttemptsTx(ctx context.Context, in IncrementInput) error {
	if in.AttemptEvent == "" {
		return telemetry.Store("IncrementAttemptsTx.validate", errors.New("AttemptEvent must not be empty"))
	}

	// Security: detach from the request context so a client-timed disconnect
	// cannot abort the counter increment and grant unlimited OTP attempts.
	// (No-op on the test path because BeginOrBind ignores the context for
	// its no-op commit/rollback, but harmless.)
	safeCtx := context.WithoutCancel(ctx)

	var (
		q          db.Querier
		commitFn   func() error
		rollbackFn func(label string)
	)

	if b.TxBound {
		// Test path: reuse the injected querier so QuerierProxy intercepts
		// individual methods and the test-transaction rows are visible.
		h, err := b.BeginOrBind(safeCtx)
		// Unreachable: BeginOrBind with TxBound=true never calls Pool.Begin
		// and always returns nil error. No test can trigger this branch.
		if err != nil {
			return telemetry.Store("IncrementAttemptsTx.begin tx", err)
		}
		q = h.Q
		commitFn = h.Commit
		rollbackFn = func(_ string) { h.Rollback() }
	} else {
		// Production path: fresh, independent transaction.
		tx, err := b.Pool.Begin(safeCtx)
		if err != nil {
			return telemetry.Store("IncrementAttemptsTx.begin tx", err)
		}
		q = db.New(tx)
		commitFn = func() error { return tx.Commit(safeCtx) }
		rollbackFn = func(label string) { LogRollback(safeCtx, tx, label) }
	}

	tokenPgUUID := b.ToPgtypeUUID(in.TokenID)
	userPgUUID := b.ToPgtypeUUID(in.UserID)

	// 1. Increment attempts counter (DB guard prevents exceeding max_attempts).
	// pgx.ErrNoRows means the token was already at max_attempts; treat as
	// "already at ceiling" and fall through so the lock check still fires.
	newAttempts, err := q.IncrementVerificationAttempts(safeCtx, tokenPgUUID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			rollbackFn("IncrementAttemptsTx/increment")
			return telemetry.Store("IncrementAttemptsTx.increment attempts", err)
		}
		// Token already at ceiling; use MaxAttempts so the threshold check triggers.
		newAttempts = in.MaxAttempts
	}

	// 2. Always emit the attempt-failed audit row first — the attempt happened
	// before any lock, so chronological order matters for audit trail analysis.
	if err := q.InsertAuditLog(safeCtx, db.InsertAuditLogParams{
		UserID:    userPgUUID,
		EventType: string(in.AttemptEvent),
		Provider:  db.AuthProviderEmail,
		IpAddress: b.IPToNullable(in.IPAddress),
		UserAgent: b.ToText(b.TruncateUserAgent(in.UserAgent)),
		Metadata:  []byte("{}"),
	}); err != nil {
		rollbackFn("IncrementAttemptsTx/audit-attempt")
		return telemetry.Store("IncrementAttemptsTx.audit log", err)
	}

	// 3. Lock account and emit account_locked audit row if threshold is reached.
	// Use newAttempts (the DB-current value) instead of in.Attempts+1 to close the
	// TOCTOU window: a concurrent wrong-code submission can push the DB counter past
	// MaxAttempts without this caller's copy of in.Attempts reflecting that.
	if newAttempts >= in.MaxAttempts {
		if _, err := q.LockAccount(safeCtx, userPgUUID); err != nil {
			rollbackFn("IncrementAttemptsTx/lock-account")
			return telemetry.Store("IncrementAttemptsTx.lock account", err)
		}
		if err := q.InsertAuditLog(safeCtx, db.InsertAuditLogParams{
			UserID:    userPgUUID,
			EventType: string(audit.EventAccountLocked),
			Provider:  db.AuthProviderEmail,
			IpAddress: b.IPToNullable(in.IPAddress),
			UserAgent: b.ToText(b.TruncateUserAgent(in.UserAgent)),
			Metadata:  []byte("{}"),
		}); err != nil {
			rollbackFn("IncrementAttemptsTx/audit-locked")
			return telemetry.Store("IncrementAttemptsTx.audit log (account_locked)", err)
		}
	}

	// Unreachable via QuerierProxy: on the TxBound path commitFn is a no-op
	// that always returns nil; on the non-TxBound path commitFn wraps
	// pgx.Tx.Commit which the proxy cannot intercept.
	if err := commitFn(); err != nil {
		return telemetry.Store("IncrementAttemptsTx.commit", err)
	}

	return nil
}

// UpdatePasswordHashTx updates the user's password hash, revokes all active
// refresh tokens, ends all sessions, and writes a password_changed audit row —
// all in one transaction. This ensures a password change immediately invalidates
// every active session.
func (b BaseStore) UpdatePasswordHashTx(ctx context.Context, userID [16]byte, newHash, ipAddress, userAgent string) error {
	h, err := b.BeginOrBind(ctx)
	if err != nil {
		return telemetry.Store("UpdatePasswordHashTx.begin tx", err)
	}

	userPgUUID := b.ToPgtypeUUID(userID)

	// 1. Update the password hash.
	if err := h.Q.UpdatePasswordHash(ctx, db.UpdatePasswordHashParams{
		PasswordHash: b.ToText(newHash),
		UserID:       userPgUUID,
	}); err != nil {
		h.Rollback()
		return telemetry.Store("UpdatePasswordHashTx.update hash", err)
	}

	// 2. Revoke all active refresh tokens so no session survives the password change.
	if err := h.Q.RevokeAllUserRefreshTokens(ctx, db.RevokeAllUserRefreshTokensParams{
		UserID: userPgUUID,
		Reason: "password_changed",
	}); err != nil {
		h.Rollback()
		return telemetry.Store("UpdatePasswordHashTx.revoke refresh tokens", err)
	}

	// 3. End all open sessions.
	if err := h.Q.EndAllUserSessions(ctx, userPgUUID); err != nil {
		h.Rollback()
		return telemetry.Store("UpdatePasswordHashTx.end sessions", err)
	}

	// 4. Audit row.
	if err := h.Q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		UserID:    userPgUUID,
		EventType: string(audit.EventPasswordChanged),
		Provider:  db.AuthProviderEmail,
		IpAddress: b.IPToNullable(ipAddress),
		UserAgent: b.ToText(b.TruncateUserAgent(userAgent)),
		Metadata:  []byte("{}"),
	}); err != nil {
		h.Rollback()
		return telemetry.Store("UpdatePasswordHashTx.audit log", err)
	}

	// Unreachable via QuerierProxy: same structural reason as
	// IncrementAttemptsTx above — h.Commit wraps pgx.Tx.Commit which
	// the proxy cannot intercept.
	if err := h.Commit(); err != nil {
		return telemetry.Store("UpdatePasswordHashTx.commit", err)
	}
	return nil
}

// ── Standalone helpers ────────────────────────────────────────────────────────

// LogRollback calls tx.Rollback and logs any error that is not ErrTxClosed.
// ErrTxClosed is expected when PostgreSQL has already aborted the transaction.
func LogRollback(ctx context.Context, tx interface{ Rollback(context.Context) error }, label string) {
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		log.Warn(ctx, "rollback failed", "label", label, "error", err)
	}
}
