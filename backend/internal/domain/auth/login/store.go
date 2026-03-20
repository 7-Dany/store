package login

import (
	"context"
	"time"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
	"github.com/jackc/pgx/v5/pgxpool"
)

// compile-time check that *Store satisfies Storer.
var _ Storer = (*Store)(nil)

// Store holds a connection pool and a Querier for the login feature.
// When TxBound is true (set by WithQuerier), Tx methods use the injected
// querier directly instead of opening a new transaction — the caller owns
// the transaction boundary. This is used in tests to scope all writes to a
// single transaction that is rolled back at the end of each test.
type Store struct {
	authshared.BaseStore
}

// NewStore creates a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{BaseStore: authshared.NewBaseStore(pool)}
}

// WithQuerier returns a shallow copy of s whose Queries field is replaced by q
// and whose TxBound flag is set. Tx methods will use q directly without opening
// a new transaction, so the caller's transaction controls commit/rollback.
//
//	store := login.NewStore(testPool).WithQuerier(db.New(tx))
func (s *Store) WithQuerier(q db.Querier) *Store {
	c := *s
	c.BaseStore = s.BaseStore.WithQuerier(q)
	return &c
}

// GetUserForLogin fetches the minimal user data needed to authenticate a login.
// identifier may be an email address or a username.
// Returns ErrUserNotFound on no-rows so the service can run a dummy bcrypt
// compare without branching on the error type.
func (s *Store) GetUserForLogin(ctx context.Context, identifier string) (LoginUser, error) {
	row, err := s.Queries.GetUserForLogin(ctx, s.ToText(identifier))
	if err != nil {
		if s.IsNoRows(err) {
			return LoginUser{}, authshared.ErrUserNotFound
		}
		return LoginUser{}, telemetry.Store("GetUserForLogin.query", err)
	}
	var loginLockedUntil *time.Time
	if row.LoginLockedUntil.Valid {
		t := row.LoginLockedUntil.Time.UTC()
		loginLockedUntil = &t
	}
	return LoginUser{
		ID:               s.UUIDToBytes(row.ID),
		Email:            row.Email.String,
		Username:         row.Username.String,
		PasswordHash:     row.PasswordHash.String,
		IsActive:         row.IsActive,
		EmailVerified:    row.EmailVerified,
		IsLocked:         row.IsLocked,
		AdminLocked:      row.AdminLocked,
		LoginLockedUntil: loginLockedUntil,
	}, nil
}

// LoginTx runs the post-authentication persistence work inside a single
// transaction: creates a session row, issues a refresh token, stamps
// last_login_at, and writes the audit log.
//
// Steps:
//  1. Create a user session row tied to this authentication event.
//  2. Issue a root refresh token tied to the session.
//  3. Stamp last_login_at (inside TX so a rollback doesn't leave a stale timestamp).
//  4. Write the login audit log row.
//
// The bcrypt check is NOT performed here — it must happen before calling
// LoginTx so the transaction stays as short as possible (no slow crypto inside a TX).
func (s *Store) LoginTx(ctx context.Context, in LoginTxInput) (LoggedInSession, error) {
	h, err := s.BeginOrBind(ctx)
	// Unreachable: BeginOrBind with TxBound=true never calls Pool.Begin and always
	// returns nil. On the non-TxBound path Pool.Begin can fail, but that path
	// cannot be injected via QuerierProxy.
	if err != nil {
		return LoggedInSession{}, telemetry.Store("LoginTx.begin_tx", err)
	}

	userPgUUID := s.ToPgtypeUUID(in.UserID)

	// 1. Open a session row.
	// Design: there is no per-user session cap. Multi-device concurrent sessions
	// are intentionally unlimited — a user may be signed in on many devices
	// simultaneously. If a cap is ever required, add a query here to count and
	// optionally evict the oldest sessions before inserting.
	sessionRow, err := h.Q.CreateUserSession(ctx, db.CreateUserSessionParams{
		UserID:       userPgUUID,
		AuthProvider: db.AuthProviderEmail,
		IpAddress:    s.IPToNullable(in.IPAddress),
		UserAgent:    s.ToText(s.TruncateUserAgent(in.UserAgent)),
	})
	if err != nil {
		h.Rollback()
		return LoggedInSession{}, telemetry.Store("LoginTx.create_session", err)
	}

	sessionPgUUID := s.UUIDToPgtypeUUID(sessionRow.ID)

	// 2. Issue a root refresh token tied to the session.
	tokenRow, err := h.Q.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
		UserID:    userPgUUID,
		SessionID: sessionPgUUID,
	})
	if err != nil {
		h.Rollback()
		return LoggedInSession{}, telemetry.Store("LoginTx.create_token", err)
	}

	// 3. Stamp last_login_at. Intentionally inside the TX so a rollback
	// doesn't leave a stale timestamp on a failed login persistence step.
	if err := h.Q.UpdateLastLoginAt(ctx, userPgUUID); err != nil {
		h.Rollback()
		return LoggedInSession{}, telemetry.Store("LoginTx.update_last_login", err)
	}

	// 4. Audit log.
	if err := h.Q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		UserID:    userPgUUID,
		EventType: string(audit.EventLogin),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(in.IPAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(in.UserAgent)),
		Metadata:  []byte("{}"),
	}); err != nil {
		h.Rollback()
		return LoggedInSession{}, telemetry.Store("LoginTx.audit", err)
	}

	// Unreachable via QuerierProxy: on the TxBound path h.Commit is a no-op
	// that always returns nil; on the non-TxBound path it wraps pgx.Tx.Commit
	// which the proxy cannot intercept.
	if err := h.Commit(); err != nil {
		return LoggedInSession{}, telemetry.Store("LoginTx.commit", err)
	}

	return LoggedInSession{
		UserID:        in.UserID,
		SessionID:     s.UUIDToBytes(sessionRow.ID),
		RefreshJTI:    tokenRow.Jti.Bytes,
		FamilyID:      tokenRow.FamilyID.Bytes,
		RefreshExpiry: tokenRow.ExpiresAt.Time.UTC(),
	}, nil
}

// WriteLoginFailedAuditTx persists a login_failed event in the audit log for a
// known user. reason is stored as JSON metadata so analysts can distinguish
// between wrong_password, account_locked, email_not_verified, and account_inactive
// failures without scanning application logs.
//
// Uses BeginOrBind so it respects the TxBound flag.
// ctx must already be detached (context.WithoutCancel) by the caller; this
// method uses ctx directly for InsertAuditLog so a client disconnect cannot
// skip the write.
func (s *Store) WriteLoginFailedAuditTx(ctx context.Context, userID [16]byte, reason, ipAddress, userAgent string) error {
	h, err := s.BeginOrBind(ctx)
	// Unreachable: BeginOrBind with TxBound=true never calls Pool.Begin and always
	// returns nil. On the non-TxBound path Pool.Begin can fail, but that path
	// cannot be injected via QuerierProxy.
	if err != nil {
		return telemetry.Store("WriteLoginFailedAuditTx.begin_tx", err)
	}

	meta := s.MustJSON(map[string]string{"reason": reason})

	// ctx is already cancel-free; callers must pass context.WithoutCancel(ctx).
	// Security: ctx must already be detached (context.WithoutCancel) by the
	// caller so a client disconnect cannot abort the audit write (§3.6).
	if err := h.Q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		UserID:    s.ToPgtypeUUID(userID),
		EventType: string(audit.EventLoginFailed),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(ipAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(userAgent)),
		Metadata:  meta,
	}); err != nil {
		h.Rollback()
		return telemetry.Store("WriteLoginFailedAuditTx.audit", err)
	}

	// Unreachable via QuerierProxy: on the TxBound path (tests) h.Commit is a
	// no-op that always returns nil; on the non-TxBound path it wraps
	// pgx.Tx.Commit which the proxy cannot intercept.
	if err := h.Commit(); err != nil {
		return telemetry.Store("WriteLoginFailedAuditTx.commit", err)
	}
	return nil
}

// IncrementLoginFailuresTx atomically increments failed_login_attempts,
// applies the time-based lockout at threshold 10, and writes audit rows.
//
// Design note (ADR-003): this method always opens a fresh pool transaction
// via s.Pool.Begin and intentionally bypasses BeginOrBind / TxBound.
// The record must commit independently so it persists even when the caller's
// request context is cancelled or the outer transaction rolls back.
//
// Steps:
//  1. Increment counter; DB sets login_locked_until when threshold reached.
//  2. Write login_failed audit row (wrong_password).
//  3. If threshold just reached, write login_lockout audit row.
func (s *Store) IncrementLoginFailuresTx(ctx context.Context, userID [16]byte, ipAddress, userAgent string) error {
	// Security: detach from the request context so a client-timed disconnect
	// cannot abort the counter increment.
	// (already detached by caller on the service path; this second wrap is a
	//  belt-and-suspenders guard for direct store calls in production.)
	safeCtx := context.WithoutCancel(ctx)

	// Intentional pool.Begin — bypasses BeginOrBind / TxBound (ADR-003).
	tx, err := s.Pool.Begin(safeCtx)
	// Pool.Begin failure is live in production (pool exhaustion, DB unreachable)
	// but cannot be injected in tests because IncrementLoginFailuresTx bypasses
	// QuerierProxy; no test-isolation path exists.
	if err != nil {
		return telemetry.Store("IncrementLoginFailuresTx.begin_tx", err)
	}

	q := db.New(tx)
	userPgUUID := s.ToPgtypeUUID(userID)

	// 1. Increment counter; DB sets login_locked_until when threshold reached.
	// Unreachable via QuerierProxy: IncrementLoginFailuresTx always calls
	// s.Pool.Begin and wraps the transaction in db.New(tx), bypassing
	// s.Queries entirely. QuerierProxy intercepts db.Querier methods on
	// s.Queries but cannot intercept the fresh db.New(tx) querier.
	row, err := q.IncrementLoginFailures(safeCtx, userPgUUID)
	if err != nil {
		authshared.LogRollback(safeCtx, tx, "IncrementLoginFailuresTx/increment")
		return telemetry.Store("IncrementLoginFailuresTx.increment", err)
	}

	// 2. Audit row: login_failed / wrong_password.
	// Unreachable via QuerierProxy: same reason as step 1 — the fresh db.New(tx)
	// querier bypasses the injected proxy (see step 1 comment).
	if err := q.InsertAuditLog(safeCtx, db.InsertAuditLogParams{
		UserID:    userPgUUID,
		EventType: string(audit.EventLoginFailed),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(ipAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(userAgent)),
		Metadata:  s.MustJSON(map[string]string{"reason": "wrong_password"}),
	}); err != nil {
		authshared.LogRollback(safeCtx, tx, "IncrementLoginFailuresTx/audit-failed")
		return telemetry.Store("IncrementLoginFailuresTx.audit_failed", err)
	}

	// 3. If the threshold was just reached write a login_lockout audit row.
	if row.LoginLockedUntil.Valid {
		// Unreachable via QuerierProxy: same reason as steps 1 and 2.
		if err := q.InsertAuditLog(safeCtx, db.InsertAuditLogParams{
			UserID:    userPgUUID,
			EventType: string(audit.EventLoginLockout),
			Provider:  db.AuthProviderEmail,
			IpAddress: s.IPToNullable(ipAddress),
			UserAgent: s.ToText(s.TruncateUserAgent(userAgent)),
			Metadata:  s.MustJSON(map[string]string{"locked_until": row.LoginLockedUntil.Time.UTC().Format(time.RFC3339)}),
		}); err != nil {
			authshared.LogRollback(safeCtx, tx, "IncrementLoginFailuresTx/audit-lockout")
			return telemetry.Store("IncrementLoginFailuresTx.audit_lockout", err)
		}
	}

	// Unreachable via QuerierProxy: tx.Commit is called directly on the pgx.Tx
	// opened by Pool.Begin; QuerierProxy only intercepts db.Querier methods and
	// has no way to intercept Commit on a raw pgx.Tx.
	if err := tx.Commit(safeCtx); err != nil {
		return telemetry.Store("IncrementLoginFailuresTx.commit", err)
	}
	return nil
}

// ResetLoginFailuresTx clears failed_login_attempts and login_locked_until
// after a successful authentication. Uses BeginOrBind so it participates in
// the caller's transaction when TxBound (tests).
func (s *Store) ResetLoginFailuresTx(ctx context.Context, userID [16]byte) error {
	h, err := s.BeginOrBind(ctx)
	// Unreachable: BeginOrBind with TxBound=true never calls Pool.Begin and always
	// returns nil. On the non-TxBound path Pool.Begin can fail, but that path
	// cannot be injected via QuerierProxy.
	if err != nil {
		return telemetry.Store("ResetLoginFailuresTx.begin_tx", err)
	}

	if err := h.Q.ResetLoginFailures(ctx, s.ToPgtypeUUID(userID)); err != nil {
		h.Rollback()
		return telemetry.Store("ResetLoginFailuresTx.query", err)
	}

	// Unreachable via QuerierProxy: on the TxBound path h.Commit is a no-op
	// that always returns nil; on the non-TxBound path it wraps pgx.Tx.Commit
	// which the proxy cannot intercept.
	if err := h.Commit(); err != nil {
		return telemetry.Store("ResetLoginFailuresTx.commit", err)
	}
	return nil
}
