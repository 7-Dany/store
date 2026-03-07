package register

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
)

// Storer is the subset of the store that the service requires.
// *Store satisfies this interface; tests may supply a fake implementation.
type Storer interface {
	CreateUserTx(ctx context.Context, in CreateUserInput) (CreatedUser, error)
	WriteRegisterFailedAuditTx(ctx context.Context, userID [16]byte, ipAddress, userAgent string) error
}

// Service holds pure business logic for the register sub-package.
// It has no knowledge of HTTP, pgtype, pgxpool, or JWT signing.
type Service struct {
	store    Storer
	tokenTTL time.Duration
	// generateCodeHash produces a random OTP code and its bcrypt hash.
	// Defaults to authshared.GenerateCodeHash; overridable in tests via
	// SetGenerateCodeHashForTest (export_test.go).
	generateCodeHash func() (raw string, hash string, err error)
}

// NewService constructs a Service with the given store and optional OTP token TTL.
// tokenTTL is sourced from config.Config.OTPValidMinutes at startup.
// When omitted (e.g. in unit tests that do not exercise TTL behaviour), a
// default of 15 minutes is used.
func NewService(store Storer, tokenTTL ...time.Duration) *Service {
	ttl := 15 * time.Minute
	if len(tokenTTL) > 0 {
		ttl = tokenTTL[0]
	}
	return &Service{
		store:            store,
		tokenTTL:         ttl,
		generateCodeHash: authshared.GenerateCodeHash,
	}
}

// Register creates a new user account and returns the raw OTP code that the
// caller must deliver via email. The service never calls the mailer — that is
// the handler's responsibility.
//
// Timing invariant: HashPassword is called before CreateUserTx so that both the
// happy path and the duplicate-email path incur the same bcrypt cost.
func (s *Service) Register(ctx context.Context, in RegisterInput) (RegisterResult, error) {
	// 1. Hash the password.
	// Security: HashPassword is called first — equalises timing between the happy
	// path and the duplicate-email path so response latency cannot reveal whether
	// the email is already registered (ADR-006).
	passwordHash, err := authshared.HashPassword(in.Password)
	if err != nil {
		return RegisterResult{}, fmt.Errorf("register.Register: hash password: %w", err)
	}

	// 2. Generate a cryptographically random OTP and its stored bcrypt hash.
	raw, codeHash, err := s.generateCodeHash()
	if err != nil {
		return RegisterResult{}, fmt.Errorf("register.Register: generate code hash: %w", err)
	}

	// 3. Persist the user, verification token, and audit log in one transaction.
	created, err := s.store.CreateUserTx(ctx, CreateUserInput{
		DisplayName:  in.DisplayName,
		Email:        in.Email,
		PasswordHash: passwordHash,
		Username:     in.Username,
		CodeHash:     codeHash,
		TTL:          s.tokenTTL,
		IPAddress:    in.IPAddress,
		UserAgent:    in.UserAgent,
	})
	if err != nil {
		// ErrEmailTaken: write a register_failed audit row before propagating.
		// Use zero userID — no user row was committed at this point.
		if errors.Is(err, authshared.ErrEmailTaken) {
			// Security: detach from the request context so a client-timed disconnect
			// cannot abort the audit write and hide duplicate-registration attempts.
			if auditErr := s.store.WriteRegisterFailedAuditTx(context.WithoutCancel(ctx), [16]byte{}, in.IPAddress, in.UserAgent); auditErr != nil {
				slog.ErrorContext(ctx, "register.Register: write register_failed audit", "error", auditErr)
			}
		}
		// Propagate original error unchanged — bcrypt ran in step 1 so timing is
		// already equalised between this path and the success path.
		return RegisterResult{}, err
	}

	return RegisterResult{
		UserID:  created.UserID,
		Email:   created.Email,
		RawCode: raw,
	}, nil
}
