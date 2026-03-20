package register

import (
	"context"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
	"github.com/jackc/pgx/v5/pgxpool"
)

// compile-time check that *Store satisfies Storer.
var _ Storer = (*Store)(nil)

// Store holds the authshared.BaseStore (pool + querier + txBound flag) and
// implements the register.Storer interface.
type Store struct {
	authshared.BaseStore
}

// NewStore creates a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{BaseStore: authshared.NewBaseStore(pool)}
}

// WithQuerier returns a shallow copy of s whose underlying querier is replaced
// by q and whose TxBound flag is set. Used in tests to scope all writes to a
// single rolled-back transaction.
func (s *Store) WithQuerier(q db.Querier) *Store {
	c := *s
	c.BaseStore = s.BaseStore.WithQuerier(q)
	return &c
}

// CreateUserTx registers a new user inside a transaction. It executes three
// steps: (1) insert the user row, (2) issue an email verification token, and
// (3) write a register audit log entry. A rollback at any step leaves no
// partial state in the database.
//
// On duplicate-email it returns ErrEmailTaken before any rollback-visible
// partial state is committed.
func (s *Store) CreateUserTx(ctx context.Context, in CreateUserInput) (CreatedUser, error) {
	h, err := s.BeginOrBind(ctx)
	if err != nil {
		return CreatedUser{}, telemetry.Store("CreateUserTx.begin_tx", err)
	}
	// idempotent after Commit; covers panics and the commit-failure path.
	defer h.Rollback()

	// 1. Insert user row.
	userRow, err := h.Q.CreateUser(ctx, db.CreateUserParams{
		Email:        s.ToText(in.Email),
		DisplayName:  s.ToText(in.DisplayName),
		PasswordHash: s.ToText(in.PasswordHash),
		Username:     s.ToText(in.Username), // Valid=false when empty → stored as NULL
	})
	if err != nil {
		switch {
		case s.IsDuplicateEmail(err):
			return CreatedUser{}, authshared.ErrEmailTaken
		case s.IsDuplicateUsername(err):
			return CreatedUser{}, authshared.ErrUsernameTaken
		}
		return CreatedUser{}, telemetry.Store("CreateUserTx.create_user", err)
	}

	userPgUUID := s.UUIDToPgtypeUUID(userRow.ID)

	// 2. Insert email verification token.
	if _, err = h.Q.CreateEmailVerificationToken(ctx, db.CreateEmailVerificationTokenParams{
		UserID:     userPgUUID,
		Email:      in.Email,
		CodeHash:   s.ToText(in.CodeHash),
		TtlSeconds: in.TTL.Seconds(),
		IpAddress:  s.IPToNullable(in.IPAddress),
	}); err != nil {
		return CreatedUser{}, telemetry.Store("CreateUserTx.create_token", err)
	}

	// 3. Audit row.
	if err := h.Q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		UserID:    userPgUUID,
		EventType: string(audit.EventRegister),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(in.IPAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(in.UserAgent)),
		Metadata:  []byte("{}"),
	}); err != nil {
		return CreatedUser{}, telemetry.Store("CreateUserTx.audit", err)
	}

	if err := h.Commit(); err != nil {
		return CreatedUser{}, telemetry.Store("CreateUserTx.commit", err)
	}

	return CreatedUser{
		UserID: userRow.ID.String(),
		Email:  userRow.Email.String,
	}, nil
}

// WriteRegisterFailedAuditTx writes a single register_failed audit row.
// userID must be a zero [16]byte when no user was committed (e.g. ErrEmailTaken);
// this is the accepted convention for pre-creation audit rows.
func (s *Store) WriteRegisterFailedAuditTx(ctx context.Context, userID [16]byte, ipAddress, userAgent string) error {
	h, err := s.BeginOrBind(ctx)
	if err != nil {
		return telemetry.Store("WriteRegisterFailedAuditTx.begin_tx", err)
	}
	// idempotent after Commit; covers panics and the commit-failure path.
	defer h.Rollback()

	// 1. Audit row.
	if err := h.Q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		UserID:    s.ToPgtypeUUIDNullable(userID),
		EventType: string(audit.EventRegisterFailed),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(ipAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(userAgent)),
		Metadata:  []byte("{}"),
	}); err != nil {
		return telemetry.Store("WriteRegisterFailedAuditTx.audit", err)
	}

	if err := h.Commit(); err != nil {
		return telemetry.Store("WriteRegisterFailedAuditTx.commit", err)
	}
	return nil
}
